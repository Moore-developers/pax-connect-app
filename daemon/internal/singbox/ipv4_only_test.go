//go:build with_quic

package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/miekg/dns"
	"github.com/sagernet/sing-box/adapter"
	"path/filepath"
	"testing"

	"pax-connect/daemon/internal/config"
)

func TestIPv4OnlyCoreAndMeasurementPolicy(t *testing.T) {
	p := config.WithIPv4Only(testProfile(), true)
	p.Front = nil
	raw, err := CoreProfile(context.Background(), p, testRules(t), "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	dns := doc["dns"].(map[string]any)
	if dns["strategy"] != "ipv4_only" {
		t.Fatal("not ipv4-only", dns["strategy"])
	}
	rules := dns["rules"].([]any)
	if len(rules) == 0 || rules[0].(map[string]any)["action"] != "predefined" {
		t.Fatal("AAAA was not stopped")
	}
	rejected := false
	for _, rule := range doc["route"].(map[string]any)["rules"].([]any) {
		r := rule.(map[string]any)
		if r["ip_version"] == float64(6) && r["action"] == "reject" {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("IPv6 traffic not rejected")
	}
	if _, err = ValidateCoreOptions(context.Background(), []byte(raw), t.TempDir(), 17890); err != nil {
		t.Fatal("generated policy rejected by root whitelist", err)
	}
	for _, tun := range []bool{true, false} {
		if _, err = buildOptions(runtimeContext(context.Background()), p, testRules(t), tun, false); err != nil {
			t.Fatal("shared builder", tun, err)
		}
	}
	p.Server = "2001:db8::1"
	if _, err = CoreProfile(context.Background(), p, testRules(t), "127.0.0.1:17890"); !errors.Is(err, config.ErrIPv6Unavailable) {
		t.Fatal("IPv6-only node accepted", err)
	}
}

func TestIPv4OnlyTwoHopResolvers(t *testing.T) {
	p := config.WithIPv4Only(testProfile(), true)
	front := p
	front.ID = "front"
	p.Front = &front
	out, _, err := plannedOutbounds(p, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	for _, hop := range out {
		if hop["domain_resolver"].(map[string]any)["strategy"] != "ipv4_only" {
			t.Fatal("hop resolver not frozen")
		}
	}
}

func TestIPv4OnlyDNSReturnsNoAAAAWithoutUpstream(t *testing.T) {
	r := NewCoreRuntime(context.Background())
	defer r.Close()
	p := config.WithIPv4Only(testProfile(), true)
	policy, err := dnsPolicy(p, false)
	if err != nil {
		t.Fatal(err)
	}
	// This native DNS rule test must not construct a proxy or contact real resolvers.
	policy["servers"] = []any{map[string]any{"type": "udp", "tag": "dns-remote", "server": "127.0.0.1", "server_port": 1}}
	policy["rules"] = append(policy["rules"].([]any), map[string]any{"domain": []string{"fixture.test"}, "query_type": []string{"A"}, "action": "predefined", "answer": []string{"fixture.test. 60 IN A 192.0.2.1"}})
	doc := map[string]any{"log": map[string]any{"disabled": true}, "outbounds": []any{map[string]any{"type": "direct", "tag": "proxy"}}, "route": map[string]any{"final": "proxy"}, "dns": policy, "experimental": map[string]any{"cache_file": map[string]any{"enabled": true, "path": filepath.Join(t.TempDir(), "cache.db")}}}
	raw, _ := json.Marshal(doc)
	if err = r.Apply(string(raw)); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []uint16{dns.TypeA, dns.TypeAAAA} {
		q := new(dns.Msg)
		q.SetQuestion("fixture.test.", kind)
		answer, err := r.current().Instance().Box().DNSRouter().Exchange(context.Background(), q, adapter.DNSQueryOptions{})
		if err != nil || answer == nil || answer.Rcode != dns.RcodeSuccess {
			t.Fatal(answer, err)
		}
		if kind == dns.TypeA && len(answer.Answer) != 1 {
			t.Fatal("A response missing", answer)
		}
		if kind == dns.TypeAAAA && len(answer.Answer) != 0 {
			t.Fatal("AAAA escaped IPv4-only policy", answer)
		}
	}
}
