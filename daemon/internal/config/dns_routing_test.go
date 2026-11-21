package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDNSRoutingUsesActiveDomainRulesAndMode(t *testing.T) {
	l := NewRoutingLibrary()
	l.Packages[0].Rules = []RoutingEntry{
		{ID: "one", Method: "domain_suffix", Target: "one.example", Destination: "route:one", Enabled: true},
		{ID: "deny", Method: "domain", Target: "deny.example", Destination: "block", Enabled: true},
	}
	tags := map[string]string{"route:one": "dns-route-one"}
	for _, mode := range []string{"balanced", "strict"} {
		rules, err := CompileDNSRouting(l, nil, tags, mode)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(rules)
		s := string(raw)
		if !strings.Contains(s, "dns-route-one") || !strings.Contains(s, `"action":"reject"`) {
			t.Fatal(s)
		}
		if strings.Contains(s, "dns-domestic") != (mode == "balanced") {
			t.Fatal(mode, s)
		}
	}
	l.Packages[1].Enabled = false
	rules, err := CompileDNSRouting(l, nil, tags, "balanced")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rules)
	if strings.Contains(string(raw), "dns-domestic") {
		t.Fatal("disabled Geo still resolves direct", string(raw))
	}
}

func TestDNSRoutingRejectsQueriesForUnloadedFixedOutbound(t *testing.T) {
	l := NewRoutingLibrary()
	l.WebRTC = true
	l.Packages[0].Rules = []RoutingEntry{
		{ID: "missing", Method: "domain", Target: "missing.example", Destination: "route:missing", Enabled: true},
		{ID: "available", Method: "domain", Target: "available.example", Destination: "route:available", Enabled: true},
		{ID: "deny", Method: "domain", Target: "deny.example", Destination: "block", Enabled: true},
		{ID: "direct", Method: "domain_suffix", Target: "example.test", Destination: "direct", Enabled: true},
	}
	rules, err := CompileDNSRouting(l, nil, map[string]string{"route:available": "dns-available"}, "balanced")
	if err != nil || len(rules) < 4 {
		t.Fatal(rules, err)
	}
	for i, want := range []struct{ field, value string }{
		{"action", "reject"}, {"server", "dns-available"}, {"action", "reject"}, {"server", "dns-domestic"},
	} {
		if rules[i][want.field] != want.value {
			t.Fatalf("unexpected DNS rule %d: %v", i, rules[i])
		}
	}
	if rules[0]["no_drop"] != true || rules[0]["server"] != nil {
		t.Fatal("missing fixed route must refuse DNS without choosing another resolver", rules[0])
	}
}

func TestDNSRoutingPreservesFollowDefaultAndRejectsUnknownPredicates(t *testing.T) {
	l := NewRoutingLibrary()
	l.Packages[0].Rules = []RoutingEntry{
		{ID: "skip", Method: "domain", Target: "skip.example", Destination: "default", Enabled: true},
		{ID: "direct", Method: "domain_suffix", Target: "group.example", Destination: "direct", Enabled: true},
	}
	rules, err := CompileDNSRouting(l, nil, nil, "balanced")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rules[0])
	if !strings.Contains(string(raw), `"invert":true`) || !strings.Contains(string(raw), "skip.example") {
		t.Fatal(string(raw))
	}
	mixed := map[string]any{"type": "logical", "mode": "and", "rules": []any{map[string]any{"domain": []string{"secret.example"}}, map[string]any{"ip_cidr": []string{"192.0.2.0/24"}}}}
	if match, _ := dnsQueryMatch(mixed); match != nil {
		t.Fatal("IP predicate silently dropped")
	}
}
