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
	if _, ok := dnsDomainMatch(mixed); ok {
		t.Fatal("IP predicate silently dropped")
	}
}
