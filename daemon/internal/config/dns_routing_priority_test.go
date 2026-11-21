package config

import "testing"

func TestDNSUncertainUserPriorityPreservesKnownPrefixAndDefaultsRemainder(t *testing.T) {
	for _, mode := range []string{"balanced", "strict"} {
		for _, method := range []string{"ip", "cidr"} {
			for _, destination := range []string{"global", "direct", "block", "route:selected"} {
				l := NewRoutingLibrary()
				target := map[string]string{"ip": "1.1.1.1", "cidr": "1.1.1.0/24"}[method]
				l.Packages[0].Rules = []RoutingEntry{
					{ID: "known", Method: "domain", Target: "known.example", Enabled: true, Destination: "direct"},
					{ID: "unknown", Method: method, Target: target, Enabled: true, Destination: destination},
					{ID: "later-block", Method: "domain", Target: "later.example", Enabled: true, Destination: "block"},
				}
				rules, err := CompileDNSRouting(l, nil, map[string]string{"route:selected": "dns-selected"}, mode)
				if err != nil {
					t.Fatal(err)
				}
				wantPrefix := "dns-domestic"
				if mode == "strict" {
					wantPrefix = "dns-remote"
				}
				if len(rules) != 2 || rules[0]["server"] != wantPrefix || rules[1]["server"] != "dns-remote" || len(rules[1]) != 1 {
					t.Fatalf("%s/%s/%s lower rule overrode undecidable priority: %v", mode, method, destination, rules)
				}
			}
		}
	}
}
