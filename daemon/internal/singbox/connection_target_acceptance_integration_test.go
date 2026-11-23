//go:build darwin && integration && with_clash_api

package singbox

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
	"pax-connect/daemon/internal/config"
)

// Explicit acceptance of a rule saved through the running App to a disposable
// library. Every peer is loopback; the installed daemon and system routes are untouched.
func TestConnectionTargetSavedRuleRuntimeAcceptance(t *testing.T) {
	path, domain := os.Getenv("PAX_CONNECTION_RULE_LIBRARY"), os.Getenv("PAX_CONNECTION_RULE_TARGET")
	if path == "" || domain == "" {
		t.Skip("set a disposable App-saved library and exact target for runtime acceptance")
	}
	library, exists, err := config.NewRoutingLibraryStore(path, uint32(os.Geteuid())).Load()
	if err != nil || !exists {
		t.Fatal("saved library unavailable", err)
	}
	var saved *config.RoutingEntry
	for _, p := range library.Packages {
		if p.Kind == "priority" {
			for _, r := range p.Rules {
				if r.Method == "domain" && r.Target == domain {
					if saved != nil {
						t.Fatal("duplicate exact target rules")
					}
					copy := r
					saved = &copy
				}
			}
		}
	}
	if saved == nil || !saved.Enabled {
		t.Fatal("enabled exact target rule missing")
	}
	if saved.Destination != "block" && !strings.HasPrefix(saved.Destination, "route:") {
		t.Fatal("acceptance needs block or a selected route")
	}
	echo := breakerEchoPeer(t)
	_, businessPort, _ := net.SplitHostPort(echo.address)
	var portNumber uint16
	if _, err = fmt.Sscan(businessPort, &portNumber); err != nil {
		t.Fatal(err)
	}
	aPeer, bPeer := breakerSOCKSPeer(t, portNumber), breakerSOCKSPeer(t, portNumber)
	aPeer.loopbackDomain, bPeer.loopbackDomain = domain, domain
	id := strings.TrimPrefix(saved.Destination, "route:")
	if saved.Destination == "block" {
		id = "acceptance-a"
	}
	a, b := breakerRoute(t, id, aPeer), breakerRoute(t, "acceptance-b", bPeer)
	isolated := config.NewRoutingLibrary()
	isolated.Packages[0].Rules = []config.RoutingEntry{*saved}
	isolated.Packages[1].Enabled = false
	b.Routing = &isolated
	port := freeLoopbackPort(t)
	profile, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{a, b}, b, testRules(t), fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	profile, err = ValidateCoreOptions(context.Background(), []byte(profile), t.TempDir(), port)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal([]byte(profile), &document); err != nil {
		t.Fatal(err)
	}
	document["inbounds"] = []any{map[string]any{"type": "mixed", "tag": "mixed", "listen": "127.0.0.1", "listen_port": port}}
	route := document["route"].(map[string]any)
	delete(route, "auto_detect_interface")
	for _, value := range route["rule_set"].([]any) {
		entry := value.(map[string]any)
		entry["path"] = filepath.Join(testRules(t).Directory, filepath.Base(entry["path"].(string)))
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewCoreRuntime(context.Background())
	t.Cleanup(runtime.Close)
	t.Cleanup(func() { _ = runtime.Clear() })
	health := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(health.Close)
	roots := x509.NewCertPool()
	roots.AddCert(health.Certificate())
	runtime.fixedHealth.targets = []string{health.URL}
	runtime.fixedHealth.tls = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	runtime.fixedHealth.interval = time.Hour
	if err = runtime.Apply(string(raw)); err != nil {
		t.Fatal(err)
	}
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", port), nil, &net.Dialer{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	conn, dialErr := dialer.Dial("tcp", net.JoinHostPort(domain, businessPort))
	if saved.Destination == "block" {
		if conn != nil {
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = io.WriteString(conn, "blocked\n")
			var response [16]byte
			if _, err = conn.Read(response[:]); err == nil {
				t.Fatal("blocked connection reached business peer")
			}
		}
		if aPeer.tcp.Load() != 0 || bPeer.tcp.Load() != 0 || echo.tcp.Load() != 0 {
			t.Fatal("blocked target escaped to an outbound")
		}
		t.Log("App-saved exact target block: new TCP refused; both outbounds and business peer received zero connections")
		return
	}
	if dialErr != nil {
		t.Fatal(dialErr)
	}
	defer conn.Close()
	breakerTCPReply(t, conn, "App-saved-specific-route")
	if aPeer.tcp.Load() < 1 || bPeer.tcp.Load() != 0 || echo.tcp.Load() < 1 {
		t.Fatal("saved target did not use its selected outbound")
	}
	t.Log("App-saved exact target route: payload echoed through selected loopback SOCKS outbound; default outbound received zero connections")
}
