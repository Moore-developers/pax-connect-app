package singbox

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/route/rule"
	"pax-connect/daemon/internal/config"
)

func adblockFixture(tb testing.TB) ([]byte, []config.RoutingEntry) {
	tb.Helper()
	raw, err := os.ReadFile("../../../rulesets/adblock-lite.srs")
	if err != nil {
		tb.Fatal(err)
	}
	entries, _, err := DecodeRoutingSource(raw)
	if err != nil || len(entries) != 1 {
		tb.Fatal("bundled ad source", len(entries), err)
	}
	return raw, entries
}
func adblockMatcher(tb testing.TB) *rule.DomainItem {
	tb.Helper()
	_, entries := adblockFixture(tb)
	var predicates struct {
		Domain []string `json:"domain"`
		Suffix []string `json:"domain_suffix"`
	}
	if err := json.Unmarshal(entries[0].Match, &predicates); err != nil {
		tb.Fatal(err)
	}
	item, err := rule.NewDomainItem(predicates.Domain, predicates.Suffix)
	if err != nil {
		tb.Fatal(err)
	}
	return item
}
func TestAdblockBundledMatcherAndDNSPrecedence(t *testing.T) {
	item := adblockMatcher(t)
	for _, host := range []string{"doubleclick.net", "sub.doubleclick.net", "googlesyndication.com", "cnzz.com"} {
		if !item.Match(&adapter.InboundContext{Domain: host}) {
			t.Fatal("ad missing", host)
		}
	}
	for _, host := range []string{"www.baidu.com", "www.taobao.com", "api.anthropic.com", "api.openai.com", "statsig.anthropic.com", "doubleclick.net.example.org"} {
		if item.Match(&adapter.InboundContext{Domain: host}) {
			t.Fatal("false positive", host)
		}
	}
	_, entries := adblockFixture(t)
	l := config.NewRoutingLibrary()
	l.Packages = append(l.Packages[:1], config.RoutingPackage{ID: config.AdblockPackageID, Name: "广告拦截", Kind: "adblock", Source: config.AdblockSource, Destination: "block", Rules: entries}, l.Packages[1])
	dns, err := config.CompileDNSRouting(l, nil, nil, "balanced")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range dns {
		if r["action"] == "reject" {
			t.Fatal("disabled adblock active")
		}
	}
	l.Packages[1].Enabled = true
	l.Packages[0].Rules = []config.RoutingEntry{{ID: "exception", Method: "domain", Target: "doubleclick.net", Destination: "direct", Enabled: true}}
	dns, err = config.CompileDNSRouting(l, nil, nil, "balanced")
	if err != nil || len(dns) < 2 || dns[0]["server"] != "dns-domestic" || dns[1]["action"] != "reject" {
		t.Fatal("DNS exception/adblock order", dns, err)
	}
	traffic, _, err := config.CompileRoutingEvidence(l, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	exception, block := -1, -1
	for i, r := range traffic {
		if r["outbound"] == "direct" && r["domain"] != nil {
			exception = i
		}
		if r["action"] == "reject" {
			block = i
		}
	}
	if exception < 0 || block <= exception {
		t.Fatal("traffic exception order", traffic)
	}
}

// Matcher-only cost, not an end-to-end request latency or process RSS claim.
func BenchmarkAdblockLookup(b *testing.B) {
	item := adblockMatcher(b)
	for name, host := range map[string]string{"miss": "api.anthropic.com", "hit": "doubleclick.net", "subdomain": "ad.doubleclick.net"} {
		b.Run(name, func(b *testing.B) {
			metadata := &adapter.InboundContext{Domain: host}
			b.ReportAllocs()
			for b.Loop() {
				_ = item.Match(metadata)
			}
		})
	}
}
func BenchmarkAdblockDecode(b *testing.B) {
	raw, _ := adblockFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := DecodeRoutingSource(raw); err != nil {
			b.Fatal(err)
		}
	}
}
