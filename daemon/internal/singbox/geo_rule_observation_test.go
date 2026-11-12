//go:build integration

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"pax-connect/daemon/internal/config"
)

// Reuse the generated rules and their labels through the actual runtime and
// observation bridge, but run only an isolated loopback listener, never TUN.
func TestGeneratedGeoRuleRetainsActualMatchThroughObservation(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	library := config.NewRoutingLibrary()
	library.WebRTC = true
	library.Packages[1].GeoContent = []config.RoutingEntry{
		{ID: "google", Method: "source", Target: "geosite-google", Enabled: true, Match: []byte(`{"domain":["proxy.fixture.test"]}`)},
		{ID: "site", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain":["localhost"]}`)},
		{ID: "ip", Method: "source", Target: "geoip-cn", Enabled: true, Match: []byte(`{"ip_cidr":["93.184.216.0/24"]}`)},
	}
	p := testProfile()
	p.NetworkPrivacy.WebRTC = true
	p.Routing = &library
	raw, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{p}, p, testRules(t), "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	var generated map[string]any
	if err = json.Unmarshal([]byte(raw), &generated); err != nil {
		t.Fatal(err)
	}
	_, labels, err := config.CompileRoutingEvidence(library, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	port := freeLoopbackPort(t)
	profile := map[string]any{
		"log":          map[string]any{"level": "error"},
		"experimental": map[string]any{"cache_file": map[string]any{"path": filepath.Join(t.TempDir(), "cache.db")}},
		"inbounds":     []any{map[string]any{"type": "mixed", "tag": "fixture", "listen": "127.0.0.1", "listen_port": port}},
		"outbounds":    []any{map[string]any{"type": "direct", "tag": "direct"}, map[string]any{"type": "direct", "tag": "proxy"}},
		"dns":          map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
		"route":        map[string]any{"final": "direct", "default_domain_resolver": "local", "rules": generated["route"].(map[string]any)["rules"]},
	}
	canonical, _ := json.Marshal(profile)
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	r.SetRoutingSources(labels)
	if err = r.Apply(string(canonical)); err != nil {
		t.Fatal(err)
	}
	proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	_, targetPort, _ := net.SplitHostPort(target.Listener.Addr().String())
	response, err := (&http.Client{Transport: transport}).Get("http://localhost:" + targetPort)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	_ = r.Clear()
	for _, record := range r.ReadObservations(context.Background(), "", 0, 256).Records {
		if record.Flow != nil && record.Kind == "response" {
			if record.Flow.RuleSource != "Pax 包：geosite-cn" || record.Flow.Rule != "source=中国域名 / IP 规则" {
				t.Fatalf("observed rule differs from actual match: %s/%s", record.Flow.Rule, record.Flow.RuleSource)
			}
			return
		}
	}
	t.Fatal("missing actual connection response evidence")
}
