//go:build darwin && integration && with_clash_api

package singbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/daemon"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/protocol/socks"
	"github.com/sagernet/sing/protocol/socks/socks5"
	"pax-connect/daemon/internal/config"
)

// These peers keep existing streams and UDP associations alive when fail is set.
// Only new handshakes fail, so a lost old stream proves active retirement.
type breakerPeer struct {
	address string
	fail    atomic.Bool
	tcp     atomic.Int64
	udp     atomic.Int64
	port    uint16
}

func breakerSOCKSPeer(t *testing.T, businessPort uint16) *breakerPeer {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp4", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	p := &breakerPeer{address: listener.Addr().String(), port: businessPort}
	var workers sync.WaitGroup
	var connections sync.Map
	workers.Add(2)
	go func() {
		defer workers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Store(conn, true)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer connections.Delete(conn)
				defer conn.Close()
				if p.fail.Load() {
					return
				}
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(conn)
				if _, err := socks5.ReadAuthRequest(reader); err != nil {
					return
				}
				if socks5.WriteAuthResponse(conn, socks5.AuthResponse{Method: socks5.AuthTypeNotRequired}) != nil {
					return
				}
				request, err := socks5.ReadRequest(reader)
				if err != nil {
					return
				}
				if request.Command == socks5.CommandUDPAssociate {
					if socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeSuccess, Bind: M.ParseSocksaddr(p.address)}) == nil {
						_, _ = io.Copy(io.Discard, reader)
					}
					return
				}
				if request.Command != socks5.CommandConnect {
					return
				}
				target := request.Destination
				if target.Port == p.port {
					p.tcp.Add(1)
				}
				peer, err := net.DialTimeout("tcp4", breakerPhysicalTarget(target), time.Second)
				if err != nil {
					return
				}
				defer peer.Close()
				if socks5.WriteResponse(conn, socks5.Response{ReplyCode: socks5.ReplyCodeSuccess, Bind: M.ParseSocksaddr(p.address)}) != nil {
					return
				}
				done := make(chan struct{})
				go func() { _, _ = io.Copy(peer, reader); _ = peer.Close(); close(done) }()
				_, _ = io.Copy(conn, peer)
				_ = conn.Close()
				<-done
			}()
		}
	}()
	go func() {
		defer workers.Done()
		buffer := make([]byte, 4096)
		for {
			n, source, err := packet.ReadFrom(buffer)
			if err != nil {
				return
			}
			if n < 4 {
				continue
			}
			reader := bytes.NewReader(buffer[3:n])
			target, err := M.SocksaddrSerializer.ReadAddrPort(reader)
			if err != nil {
				continue
			}
			prefix := n - reader.Len()
			if target.Port == p.port {
				p.udp.Add(1)
			}
			peer, err := net.DialTimeout("udp4", breakerPhysicalTarget(target), time.Second)
			if err != nil {
				continue
			}
			_ = peer.SetDeadline(time.Now().Add(time.Second))
			_, _ = peer.Write(buffer[prefix:n])
			reply := make([]byte, 4096)
			size, err := peer.Read(reply)
			_ = peer.Close()
			if err == nil {
				_, _ = packet.WriteTo(append(append([]byte(nil), buffer[:prefix]...), reply[:size]...), source)
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		_ = packet.Close()
		connections.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
		workers.Wait()
	})
	return p
}

func breakerPhysicalTarget(target M.Socksaddr) string {
	if strings.HasSuffix(target.Fqdn, ".fixture.test") {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(int(target.Port)))
	}
	return target.String()
}

type breakerEcho struct {
	address string
	tcp     atomic.Int64
	udp     atomic.Int64
}

func breakerEchoPeer(t *testing.T) *breakerEcho {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp4", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	e := &breakerEcho{address: listener.Addr().String()}
	var workers sync.WaitGroup
	var connections sync.Map
	workers.Add(2)
	go func() {
		defer workers.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			e.tcp.Add(1)
			connections.Store(conn, true)
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer connections.Delete(conn)
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	go func() {
		defer workers.Done()
		buffer := make([]byte, 4096)
		for {
			n, source, err := packet.ReadFrom(buffer)
			if err != nil {
				return
			}
			e.udp.Add(1)
			_, _ = packet.WriteTo(buffer[:n], source)
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		_ = packet.Close()
		connections.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
		workers.Wait()
	})
	return e
}

func breakerRoute(t *testing.T, id string, peer *breakerPeer) config.RouteConfig {
	t.Helper()
	host, port, _ := net.SplitHostPort(peer.address)
	raw := fmt.Sprintf(`{"type":"socks","server":%q,"server_port":%s}`, host, port)
	route, err := config.RouteFromDocument([]byte(raw), id, strings.ToUpper(id))
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func breakerRuntimeProfile(t *testing.T, a, b config.RouteConfig, port int, selectedA bool, resolver *httptest.Server) (string, []string) {
	t.Helper()
	library := config.NewRoutingLibrary()
	library.Packages[1].Enabled = false
	library.RouteNames = map[string]string{"route:a": "A"}
	library.Packages[0].Rules = []config.RoutingEntry{
		{ID: "protected", Method: "domain_suffix", Target: "claude.fixture.test", Destination: "route:a", Enabled: true},
		{ID: "direct", Method: "domain", Target: "direct.fixture.test", Destination: "direct", Enabled: true},
	}
	selected := b
	if selectedA {
		selected = a
	}
	selected.Routing = &library
	profile, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{a, b}, selected, testRules(t), fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	profile, err = ValidateCoreOptions(context.Background(), []byte(profile), t.TempDir(), port)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(profile), &document); err != nil {
		t.Fatal(err)
	}
	document["inbounds"] = []any{map[string]any{"type": "mixed", "tag": "mixed", "listen": "127.0.0.1", "listen_port": port}}
	route := document["route"].(map[string]any)
	delete(route, "auto_detect_interface")
	for _, value := range route["rule_set"].([]any) {
		entry := value.(map[string]any)
		entry["path"] = filepath.Join(testRules(t).Directory, filepath.Base(entry["path"].(string)))
	}
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: resolver.Certificate().Raw}))
	for _, value := range document["dns"].(map[string]any)["servers"].([]any) {
		server := value.(map[string]any)
		if server["type"] == "https" {
			server["server"] = "127.0.0.1"
			server["server_port"] = resolver.Listener.Addr().(*net.TCPAddr).Port
			server["tls"] = map[string]any{"enabled": true, "certificate": []string{cert}}
		}
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	_, labels, err := config.CompileRoutingEvidence(library, nil, map[string]string{"route:a": config.RuntimeTag(a), "route:b": config.RuntimeTag(b)}, true)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), labels
}

func breakerUDPFlow(t *testing.T, port int, destination M.Socksaddr) net.PacketConn {
	t.Helper()
	client := socks.NewClient(N.SystemDialer, M.ParseSocksaddr(fmt.Sprintf("127.0.0.1:%d", port)), socks.Version5, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	packet, err := client.ListenPacket(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = packet.Close() })
	return packet
}

func breakerTCPReply(t *testing.T, conn net.Conn, marker string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(conn, marker+"\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil || line != marker+"\n" {
		t.Fatalf("TCP response %q, %v; expected %q", line, err, marker)
	}
}

func breakerUDPReply(t *testing.T, packet net.PacketConn, target M.Socksaddr, marker string, allowed bool) {
	t.Helper()
	_ = packet.SetDeadline(time.Now().Add(250 * time.Millisecond))
	_, err := packet.WriteTo([]byte(marker), target)
	if err == nil {
		buffer := make([]byte, 256)
		n, _, readErr := packet.ReadFrom(buffer)
		err = readErr
		if allowed && (err != nil || string(buffer[:n]) != marker) {
			t.Fatalf("UDP response %q, %v; expected %q", buffer[:n], err, marker)
		}
	}
	if allowed && err != nil || !allowed && err == nil {
		t.Fatalf("UDP allowed=%v: %v", allowed, err)
	}
}

func breakerMaintainUntil(t *testing.T, runtime *CoreRuntime, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.Maintain(context.Background())
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("runtime maintenance did not confirm expected protection state")
}

func TestFixedBreakerRuntimeAcceptance(t *testing.T) {
	for _, chain := range []bool{false, true} {
		name := "single"
		if chain {
			name = "two_hop"
		}
		t.Run(name, func(t *testing.T) { verifyFixedBreakerRuntime(t, chain) })
	}
}

func verifyFixedBreakerRuntime(t *testing.T, chain bool) {
	t.Helper()
	echo := breakerEchoPeer(t)
	_, echoPort, _ := net.SplitHostPort(echo.address)
	numericPort, _ := strconv.Atoi(echoPort)
	aPeer, bPeer := breakerSOCKSPeer(t, uint16(numericPort)), breakerSOCKSPeer(t, uint16(numericPort))
	a, b := breakerRoute(t, "a", aPeer), breakerRoute(t, "b", bPeer)
	faults := []*breakerPeer{aPeer}
	if chain {
		frontPeer := breakerSOCKSPeer(t, uint16(numericPort))
		front := breakerRoute(t, "front", frontPeer)
		a.Front = &front
		faults = append([]*breakerPeer{frontPeer}, faults...)
	}
	var firstBad atomic.Bool
	var targetRequests atomic.Int64
	health := func(first bool) *httptest.Server {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			targetRequests.Add(1)
			if first && firstBad.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(server.Close)
		return server
	}
	first, second := health(true), health(false)
	resolver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		var question, answer dns.Msg
		if question.Unpack(body) != nil || len(question.Question) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answer.SetReply(&question)
		if q := question.Question[0]; q.Qtype == dns.TypeA {
			answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60}, A: net.IPv4(127, 0, 0, 1)}}
		}
		w.Header().Set("Content-Type", "application/dns-message")
		wire, _ := answer.Pack()
		_, _ = w.Write(wire)
	}))
	t.Cleanup(resolver.Close)
	port := freeLoopbackPort(t)
	profile, labels := breakerRuntimeProfile(t, a, b, port, !chain, resolver)
	runtime := NewCoreRuntime(context.Background())
	t.Cleanup(runtime.Close)
	t.Cleanup(func() { _ = runtime.Clear() })
	runtime.SetRoutingSources(labels)
	if err := runtime.Apply(profile); err != nil {
		t.Fatal(err)
	}
	if capture := os.Getenv("PAX_FIXED_BREAKER_OBSERVATIONS"); !chain && capture != "" {
		defer func() {
			batch := runtime.ReadObservations(context.Background(), "", 0, 256)
			for len(batch.Records) > 0 && batch.Records[len(batch.Records)-1].Sequence < batch.HighWater {
				page := runtime.ReadObservations(context.Background(), batch.Session, batch.Records[len(batch.Records)-1].Sequence, 256)
				if len(page.Records) == 0 {
					break
				}
				batch.Records = append(batch.Records, page.Records...)
			}
			raw, err := json.MarshalIndent(batch, "", "  ")
			if err == nil {
				err = os.WriteFile(capture, raw, 0600)
			}
			if err != nil {
				t.Error("capturing runtime observations:", err)
			}
		}()
	}
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(first.Certificate())
	rootCAs.AddCert(second.Certificate())
	runtime.fixedHealth.targets = []string{first.URL, second.URL}
	runtime.fixedHealth.tls = &tls.Config{RootCAs: rootCAs, MinVersion: tls.VersionTLS12}
	runtime.fixedHealth.interval = 20 * time.Millisecond
	tag := config.RuntimeTag(a)
	protected := M.ParseSocksaddr("claude.fixture.test:" + echoPort)
	global := M.ParseSocksaddr("global.fixture.test:" + echoPort)
	direct := M.ParseSocksaddr("direct.fixture.test:" + echoPort)
	protectedTCP := socksFlow(t, port, protected.String())
	protectedUDP := breakerUDPFlow(t, port, protected)
	breakerTCPReply(t, protectedTCP, "healthy-fixed-A")
	breakerUDPReply(t, protectedUDP, protected, "healthy-fixed-A-udp", true)
	if !chain {
		oldGlobal := socksFlow(t, port, global.String())
		breakerTCPReply(t, oldGlobal, "global-before-switch")
		if selected, _, err := runtime.SelectRoute(context.Background(), tag, config.RuntimeTag(b)); err != nil || selected != config.RuntimeTag(b) {
			t.Fatalf("global A to B: %s %v", selected, err)
		}
		breakerTCPReply(t, protectedTCP, "fixed-A-survives-global-switch")
		breakerUDPReply(t, protectedUDP, protected, "fixed-A-udp-survives-global-switch", true)
		_ = oldGlobal.SetReadDeadline(time.Now().Add(time.Second))
		var buffer [1]byte
		if _, err := oldGlobal.Read(buffer[:]); err == nil {
			t.Fatal("old global A stream survived the switch")
		}
		t.Log("global A→B: fixed A TCP/UDP survived, old selector flow closed")
	}
	globalTCP, directTCP := socksFlow(t, port, global.String()), socksFlow(t, port, direct.String())
	globalUDP, directUDP := breakerUDPFlow(t, port, global), breakerUDPFlow(t, port, direct)
	breakerTCPReply(t, globalTCP, "healthy-global-B")
	breakerTCPReply(t, directTCP, "healthy-direct")
	breakerUDPReply(t, globalUDP, global, "healthy-global-B-udp", true)
	breakerUDPReply(t, directUDP, direct, "healthy-direct-udp", true)
	firstBad.Store(true)
	baseline := targetRequests.Load()
	breakerMaintainUntil(t, runtime, func() bool { return targetRequests.Load() >= baseline+8 })
	if runtime.fixed.blocked[tag] {
		t.Fatal("single target outage tripped the fixed route")
	}
	breakerTCPReply(t, protectedTCP, "single-target-outage-retains-A")
	firstBad.Store(false)
	t.Log("one failing health target: A remained allowed across repeated rounds")

	for hop, faulty := range faults {
		faulty.fail.Store(true)
		breakerTCPReply(t, protectedTCP, "old-A-still-live-before-confirmation")
		breakerUDPReply(t, protectedUDP, protected, "old-A-udp-live-before-confirmation", true)
		breakerMaintainUntil(t, runtime, func() bool { return runtime.fixed.blocked[tag] })
		breakerMaintainUntil(t, runtime, func() bool {
			snapshot := &connectionSnapshot{ctx: context.Background()}
			_ = runtime.current().SubscribeConnections(&daemon.SubscribeConnectionsRequest{}, snapshot)
			if snapshot.events == nil {
				return false
			}
			for _, event := range snapshot.events.Events {
				if c := event.Connection; c != nil && c.ClosedAt == 0 && c.Outbound == tag {
					return false
				}
			}
			return true
		})
		_ = protectedTCP.SetReadDeadline(time.Now().Add(time.Second))
		var buffer [1]byte
		if _, err := protectedTCP.Read(buffer[:]); err == nil {
			t.Fatal("confirmed failure did not close established fixed A stream")
		}
		breakerUDPReply(t, protectedUDP, protected, "closed-fixed-A-udp", false)
		runtime.SetRoutingSources(labels)
		if err := runtime.UpdateRouting(context.Background(), profile); err != nil {
			t.Fatal("hot rules update while fixed A is blocked:", err)
		}
		if !runtime.fixed.blocked[tag] {
			t.Fatal("hot rules update cleared an unconfirmed fixed route fault")
		}
		sequence := runtime.ReadObservations(context.Background(), "", 0, 1).HighWater
		before := [4]int64{bPeer.tcp.Load(), bPeer.udp.Load(), echo.tcp.Load(), echo.udp.Load()}
		client := socks.NewClient(N.SystemDialer, M.ParseSocksaddr(fmt.Sprintf("127.0.0.1:%d", port)), socks.Version5, "", "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		connection, err := client.DialContext(ctx, "tcp", protected)
		if err == nil {
			// The mixed SOCKS listener may acknowledge before routing/dialing.
			// Require an actual payload exchange to distinguish that early ACK.
			_ = connection.SetDeadline(time.Now().Add(time.Second))
			_, err = io.WriteString(connection, "blocked-new-fixed-A-tcp\n")
			if err == nil {
				var response [64]byte
				_, err = connection.Read(response[:])
			}
			_ = connection.Close()
		}
		cancel()
		if err == nil {
			t.Fatal("confirmed unavailable A admitted new fixed TCP")
		}
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatal("blocked fixed TCP timed out without confirming closure")
		}
		blockedUDP := breakerUDPFlow(t, port, protected)
		breakerUDPReply(t, blockedUDP, protected, "blocked-new-fixed-A-udp", false)
		after := [4]int64{bPeer.tcp.Load(), bPeer.udp.Load(), echo.tcp.Load(), echo.udp.Load()}
		if before != after {
			t.Fatalf("blocked traffic reached B/direct/target: before=%v after=%v", before, after)
		}
		rejected := map[string]bool{}
		for _, event := range runtime.ReadObservations(context.Background(), "", sequence, 256).Records {
			if fact := event.Flow; event.Kind == "rejected" && fact != nil && fact.Domain == protected.Fqdn {
				if fact.Direction != "blocked" || config.UnavailableRoutingMessage(*fact) != "指定线路 A 不可用" {
					t.Fatalf("rejection lost its fixed route explanation: %+v", fact)
				}
				rejected[fact.Transport] = true
			}
		}
		if !rejected["tcp"] || !rejected["udp"] {
			t.Fatalf("missing fixed route rejection facts: %v", rejected)
		}
		breakerTCPReply(t, globalTCP, "global-B-survives-A-failure")
		breakerTCPReply(t, directTCP, "direct-survives-A-failure")
		breakerUDPReply(t, globalUDP, global, "global-B-udp-survives-A-failure", true)
		breakerUDPReply(t, directUDP, direct, "direct-udp-survives-A-failure", true)
		t.Logf("hop %d confirmed failure: old A TCP/UDP closed; new TCP/UDP rejected; B/target counters unchanged %v", hop, after)
		faulty.fail.Store(false)
		breakerMaintainUntil(t, runtime, func() bool { return !runtime.fixed.blocked[tag] })
		protectedTCP = socksFlow(t, port, protected.String())
		protectedUDP = breakerUDPFlow(t, port, protected)
		breakerTCPReply(t, protectedTCP, "recovered-fixed-A")
		breakerUDPReply(t, protectedUDP, protected, "recovered-fixed-A-udp", true)
		t.Logf("hop %d recovery: two A-only health rounds reopened original fixed A TCP/UDP", hop)
	}
	if runtime.SelectedTag() != config.RuntimeTag(b) {
		t.Fatal("breaker changed the user's selected global route")
	}
}
