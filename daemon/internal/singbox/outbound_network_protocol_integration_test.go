//go:build protocolintegration

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
	"github.com/sagernet/sing/protocol/socks/socks5"
)

func TestLocalAnyTLSTCPOnlySelectorAndRule(t *testing.T) {
	ctx := context.Background()
	serverPort := networkProtocolPort(t)
	cert, key := localCertificate(t)
	startNetworkProtocolServer(t, map[string]any{
		"type": "anytls", "listen": "127.0.0.1", "listen_port": serverPort,
		"users":          []map[string]string{{"name": "fixture", "password": "fixture-password"}},
		"tls":            map[string]any{"enabled": true, "certificate": cert, "key": key},
		"padding_scheme": []string{"stop=3", "0=30-30", "1=70-70", "2=80-80"},
	})
	outbounds := []map[string]any{
		{"type": "selector", "tag": "proxy", "outbounds": []string{"limited", "full"}, "default": "limited"},
		{"type": "anytls", "tag": "limited", "server": "127.0.0.1", "server_port": serverPort, "password": "fixture-password", "tls": map[string]any{"enabled": true, "server_name": "localhost", "insecure": true}, "network": "tcp"},
		{"type": "anytls", "tag": "full", "server": "127.0.0.1", "server_port": serverPort, "password": "fixture-password", "tls": map[string]any{"enabled": true, "server_name": "localhost", "insecure": true}},
	}
	defaultDestination := networkProtocolEcho(t)
	ruleDestination := networkProtocolEcho(t)
	runtime, proxyPort := startNetworkProtocolCore(t, outbounds, "proxy", []map[string]any{
		{"port": ruleDestination.Port, "outbound": "limited"},
	})
	assertNetworkProtocolTCP(t, proxyPort)
	checkNetworkProtocolUDP(t, proxyPort, defaultDestination, false)
	if selected, _, err := runtime.SelectRoute(ctx, "limited", "full"); err != nil || selected != "full" {
		t.Fatalf("switch to UDP-capable outbound: selected=%s, err=%v", selected, err)
	}
	checkNetworkProtocolUDP(t, proxyPort, defaultDestination, true)
	checkNetworkProtocolUDP(t, proxyPort, ruleDestination, false)
	if selected, _, err := runtime.SelectRoute(ctx, "full", "limited"); err != nil || selected != "limited" {
		t.Fatalf("switch to TCP-only outbound: selected=%s, err=%v", selected, err)
	}
	checkNetworkProtocolUDP(t, proxyPort, defaultDestination, false)
}

func TestLocalTCPOnlyCannotCarryDetourUDP(t *testing.T) {
	for _, protocol := range []string{"shadowsocks", "vless"} {
		t.Run(protocol, func(t *testing.T) {
			frontPort := networkProtocolPort(t)
			finalPort := networkProtocolPort(t)
			frontIn := map[string]any{"type": protocol, "listen": "127.0.0.1", "listen_port": frontPort}
			front := map[string]any{"type": protocol, "tag": "front", "server": "127.0.0.1", "server_port": frontPort, "network": "tcp"}
			switch protocol {
			case "shadowsocks":
				frontIn["method"], frontIn["password"] = "aes-128-gcm", "fixture-front"
				front["method"], front["password"] = "aes-128-gcm", "fixture-front"
			case "vless":
				const uuid = "11111111-1111-1111-1111-111111111111"
				frontIn["users"] = []map[string]string{{"uuid": uuid}}
				front["uuid"] = uuid
				cert, key := localCertificate(t)
				frontIn["tls"] = map[string]any{"enabled": true, "certificate": cert, "key": key}
				front["tls"] = map[string]any{"enabled": true, "server_name": "localhost", "insecure": true}
				transport := map[string]any{"type": "ws", "path": "/fixture"}
				frontIn["transport"], front["transport"] = transport, transport
			}
			startNetworkProtocolServer(t, frontIn)
			startNetworkProtocolServer(t, map[string]any{
				"type": "shadowsocks", "listen": "127.0.0.1", "listen_port": finalPort,
				"method": "aes-128-gcm", "password": "fixture-final",
			})
			final := map[string]any{
				"type": "shadowsocks", "tag": "final", "server": "127.0.0.1", "server_port": finalPort,
				"method": "aes-128-gcm", "password": "fixture-final", "detour": "front",
			}
			destination := networkProtocolEcho(t)
			runtime, proxyPort := startNetworkProtocolCore(t, []map[string]any{front, final}, "final", nil)
			assertNetworkProtocolTCP(t, proxyPort)
			outbound, ok := runtime.current().Instance().Box().Outbound().Outbound("final")
			if !ok {
				t.Fatal("detoured outbound missing")
			}
			for _, connect := range []bool{false, true} {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				var tunnel io.Closer
				var err error
				if connect {
					tunnel, err = outbound.DialContext(ctx, N.NetworkUDP, destination)
				} else {
					tunnel, err = outbound.ListenPacket(ctx, destination)
				}
				cancel()
				if err == nil {
					_ = tunnel.Close()
					t.Fatalf("TCP-only front admitted UDP (connect=%t)", connect)
				}
				if !strings.Contains(err.Error(), "unknown network: udp") {
					t.Fatalf("expected front restriction (connect=%t): %v", connect, err)
				}
			}
			// The same chain forwards UDP when the front explicitly allows it.
			delete(front, "network")
			_, allowedPort := startNetworkProtocolCore(t, []map[string]any{front, final}, "final", nil)
			checkNetworkProtocolUDP(t, allowedPort, destination, true)
		})
	}
}

func networkProtocolPort(t *testing.T) int {
	t.Helper()
	lease, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	return lease.Addr().(*net.TCPAddr).Port
}

func startNetworkProtocolServer(t *testing.T, inbound map[string]any) {
	t.Helper()
	ctx := runtimeContext(context.Background())
	raw, err := json.Marshal(map[string]any{
		"log":      map[string]any{"disabled": true},
		"inbounds": []map[string]any{inbound},
	})
	if err != nil {
		t.Fatal(err)
	}
	var options option.Options
	if err = options.UnmarshalJSONContext(ctx, raw); err != nil {
		t.Fatal(err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	if err = instance.Start(); err != nil {
		t.Fatal(err)
	}
}

func startNetworkProtocolCore(t *testing.T, outbounds []map[string]any, final string, rules []map[string]any) (*CoreRuntime, int) {
	t.Helper()
	port := networkProtocolPort(t)
	raw, err := json.Marshal(map[string]any{
		"inbounds":  []map[string]any{{"type": "mixed", "listen": "127.0.0.1", "listen_port": port}},
		"outbounds": outbounds,
		"route":     map[string]any{"final": final, "rules": rules},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	validated, err := ValidateCoreOptions(ctx, raw, t.TempDir(), port)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewCoreRuntime(ctx)
	t.Cleanup(func() { _ = runtime.Clear(); runtime.Close() })
	if err = runtime.Apply(validated); err != nil {
		t.Fatal(err)
	}
	return runtime, port
}

func assertNetworkProtocolTCP(t *testing.T, proxyPort int) {
	t.Helper()
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	client := socks.NewClient(N.SystemDialer, M.ParseSocksaddr(fmt.Sprintf("127.0.0.1:%d", proxyPort)), socks.Version5, "", "")
	transport := target.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return client.DialContext(ctx, network, M.ParseSocksaddr(addr))
	}
	response, err := (&http.Client{Transport: transport, Timeout: 3 * time.Second}).Get(target.URL)
	if err != nil {
		t.Fatalf("TCP forwarding: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("TCP forwarding status: %s", response.Status)
	}
}

func networkProtocolEcho(t *testing.T) M.Socksaddr {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		buffer := make([]byte, 128)
		for {
			n, peer, err := conn.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = conn.WriteTo(buffer[:n], peer)
		}
	}()
	t.Cleanup(func() { _ = conn.Close(); wg.Wait() })
	return M.SocksaddrFromNet(conn.LocalAddr())
}

func checkNetworkProtocolUDP(t *testing.T, proxyPort int, destination M.Socksaddr, allowed bool) {
	t.Helper()
	control, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if err = control.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := socks.ClientHandshake5(control, socks5.CommandUDPAssociate, M.ParseSocksaddr("127.0.0.1:0"), "", "")
	if err != nil {
		t.Fatal(err)
	}
	relay := response.Bind.UDPAddr()
	relay.IP = net.ParseIP("127.0.0.1")
	conn, err := net.DialUDP("udp", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	timeout := 400 * time.Millisecond
	if allowed {
		timeout = 3 * time.Second
	}
	if err = conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatal(err)
	}
	packet := socks.NewAssociatePacketConn(conn, M.Socksaddr{}, control)
	if _, err = packet.WriteTo([]byte("echo"), destination.UDPAddr()); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 128)
	n, _, err := packet.ReadFrom(data)
	if allowed {
		if err != nil || string(data[:n]) != "echo" {
			t.Fatalf("expected UDP echo: got %q, err=%v", data[:n], err)
		}
	} else if err == nil {
		t.Fatalf("TCP-only outbound forwarded UDP: got %q", data[:n])
	}
}
