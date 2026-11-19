//go:build protocolintegration && with_quic

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/option"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/importers"
)

func TestStructuredImportForwardsHY2AndShadowsocks(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/through-proxy" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	port := startStructuredImportProxy(t, "hysteria2")
	hy2YAML := fmt.Sprintf(`proxies:
  - {type: hysteria2, name: HY2, server: 127.0.0.1, port: %d, password: fixture, sni: localhost, skip-cert-verify: true, obfs: salamander, obfs-password: fixture-obfs}
`, port)
	hy2JSON := fmt.Sprintf(`{"outbounds":[{"type":"hysteria2","tag":"HY2","server":"127.0.0.1","server_port":%d,"password":"fixture","tls":{"enabled":true,"server_name":"localhost","insecure":true},"obfs":{"type":"salamander","password":"fixture-obfs"}}]}`, port)

	port = startStructuredImportProxy(t, "shadowsocks")
	ssYAML := fmt.Sprintf(`proxies:
  - {type: ss, name: SS, server: 127.0.0.1, port: %d, cipher: aes-128-gcm, password: fixture, udp: false}
`, port)
	ssJSON := fmt.Sprintf(`{"outbounds":[{"type":"shadowsocks","tag":"SS","server":"127.0.0.1","server_port":%d,"method":"aes-128-gcm","password":"fixture","network":"tcp"}]}`, port)

	runtime := NewMeasurements(nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	for _, fixture := range []struct {
		name, protocol, source string
	}{
		{"hy2/salamander/yaml", "hysteria2", hy2YAML},
		{"hy2/salamander/json", "hysteria2", hy2JSON},
		{"ss/aes-128-gcm/yaml", "shadowsocks", ssYAML},
		{"ss/aes-128-gcm/json", "shadowsocks", ssJSON},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			batch, err := importers.ParseSubscription([]byte(fixture.source))
			if err != nil || len(batch.Items) != 1 {
				t.Fatalf("import returned %d nodes, error %v", len(batch.Items), err)
			}
			item := batch.Items[0]
			if item.Route == nil || item.Route.ProtocolName != fixture.protocol {
				t.Fatalf("expected %s node, item error %q", fixture.protocol, item.Error)
			}
			profile := *item.Route
			profile.TLSResolved = config.SourceTLSPolicy(profile)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := ValidateImportedRouteDocument(ctx, profile.Document); err != nil {
				t.Fatal(err)
			}
			err = runtime.WithRouteNetwork(ctx, profile, func(dial func(context.Context, string, string) (net.Conn, error), _ func(context.Context, string) ([]netip.Addr, error)) error {
				transport := target.Client().Transport.(*http.Transport).Clone()
				transport.DialContext = dial
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport}
				request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL+"/through-proxy", nil)
				if err != nil {
					return err
				}
				response, err := client.Do(request)
				if err != nil {
					return err
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusNoContent {
					return fmt.Errorf("HTTPS destination returned %d", response.StatusCode)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func startStructuredImportProxy(t *testing.T, protocol string) int {
	t.Helper()
	inbound := map[string]any{"type": protocol, "tag": "server", "listen": "127.0.0.1"}
	var port int
	if protocol == "hysteria2" {
		packet, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port = packet.LocalAddr().(*net.UDPAddr).Port
		cert, key := localCertificate(t)
		inbound["tls"] = map[string]any{"enabled": true, "certificate": []string{cert}, "key": []string{key}}
		inbound["users"] = []any{map[string]any{"name": "fixture", "password": "fixture"}}
		inbound["obfs"] = map[string]any{"type": "salamander", "password": "fixture-obfs"}
		if err = packet.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port = listener.Addr().(*net.TCPAddr).Port
		inbound["method"], inbound["password"], inbound["network"] = "aes-128-gcm", "fixture", "tcp"
		if err = listener.Close(); err != nil {
			t.Fatal(err)
		}
	}
	inbound["listen_port"] = port
	raw, err := json.Marshal(map[string]any{
		"log":       map[string]any{"disabled": true},
		"inbounds":  []any{inbound},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := runtimeContext(context.Background())
	var options option.Options
	if err = options.UnmarshalJSONContext(ctx, raw); err != nil {
		t.Fatal(err)
	}
	server, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	return port
}
