//go:build integration

package singbox

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	"pax-connect/daemon/internal/config"
)

// The production compiler feeds the actual DNS router. Both DNS peers are
// isolated loopback servers; counters prove which resolver received the query.
func TestDNSUserPriorityUsesProxyPoolInActualRuntimeAndHotUpdate(t *testing.T) {
	start := func() (*dns.Server, int, *atomic.Int64) {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		calls := new(atomic.Int64)
		srv := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
			calls.Add(1)
			answer := new(dns.Msg)
			answer.SetReply(q)
			answer.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: q.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0}, A: net.IPv4(1, 1, 1, 1)}}
			_ = w.WriteMsg(answer)
		})}
		go func() { _ = srv.ActivateAndServe() }()
		t.Cleanup(func() { _ = srv.Shutdown() })
		return srv, conn.LocalAddr().(*net.UDPAddr).Port, calls
	}
	_, remotePort, remote := start()
	_, domesticPort, domestic := start()
	l := config.NewRoutingLibrary()
	l.Packages[1].GeoContent = []config.RoutingEntry{{ID: "google", Method: "source", Target: "geosite-google", Enabled: true, Match: []byte(`{"domain":["proxy.fixture.test"]}`)}, {ID: "geo", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain":["fixture.example"]}`)}}
	l.Packages[0].Rules = []config.RoutingEntry{{ID: "app", Method: "application", Target: "app.fixture", Destination: "global", Enabled: true}}
	apps := []config.RoutingApplication{{ID: "app.fixture", Path: "/Applications/Fixture.app", Bundle: true}}
	doc := map[string]any{
		"log": map[string]any{"disabled": true}, "experimental": map[string]any{"cache_file": map[string]any{"path": filepath.Join(t.TempDir(), "cache.db")}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}}, "route": map[string]any{"final": "direct"},
		"dns": map[string]any{"servers": []any{map[string]any{"type": "udp", "tag": "dns-remote", "server": "127.0.0.1", "server_port": remotePort}, map[string]any{"type": "udp", "tag": "dns-domestic", "server": "127.0.0.1", "server_port": domesticPort}}, "final": "dns-remote", "disable_cache": true},
	}
	encode := func() string {
		rules, err := config.CompileDNSRouting(l, apps, nil, "balanced")
		if err != nil {
			t.Fatal(err)
		}
		doc["dns"].(map[string]any)["rules"] = rules
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	if err := r.Apply(encode()); err != nil {
		t.Fatal(err)
	}
	query := func() {
		q := new(dns.Msg)
		q.SetQuestion("fixture.example.", dns.TypeA)
		answer, err := r.current().Instance().Box().DNSRouter().Exchange(context.Background(), q, adapter.DNSQueryOptions{})
		if err != nil || answer.Rcode != dns.RcodeSuccess {
			t.Fatal("DNS exchange failed", err)
		}
	}
	query()
	if remote.Load() != 1 || domestic.Load() != 0 {
		t.Fatal("higher app proxy priority leaked to lower domestic DNS", remote.Load(), domestic.Load())
	}
	// Disable the user rule: normal balanced Geo DNS remains domestic.
	l.Packages[0].Rules[0].Enabled = false
	if err := r.UpdateRouting(context.Background(), encode()); err != nil {
		t.Fatal(err)
	}
	query()
	if remote.Load() != 1 || domestic.Load() != 1 {
		t.Fatal("disabled app rule prevented Geo DNS", remote.Load(), domestic.Load())
	}
	// Hot-enable must use the identical conservative compiler rule.
	l.Packages[0].Rules[0].Enabled = true
	if err := r.UpdateRouting(context.Background(), encode()); err != nil {
		t.Fatal(err)
	}
	query()
	if remote.Load() != 2 || domestic.Load() != 1 {
		t.Fatal("hot update lost app DNS priority", remote.Load(), domestic.Load())
	}
}
