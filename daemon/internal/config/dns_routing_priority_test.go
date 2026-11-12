package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplicationProxyDNSCannotFallThroughToLowerGeo(t *testing.T) {
	l := NewRoutingLibrary()
	l.Packages[0].Rules = []RoutingEntry{{ID: "proxy-app", Method: "application", Target: "app.chat", Enabled: true, Destination: "global"}}
	l.Packages[1].GeoContent = []RoutingEntry{{ID: "cn", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain":["update.googleapis.com"]}`)}}
	apps := []RoutingApplication{{ID: "app.chat", Path: "/Applications/Chat.app", Bundle: true}}
	rules, e := CompileDNSRouting(l, apps, nil, "balanced")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(rules)
	if strings.Contains(string(raw), "dns-domestic") {
		t.Fatalf("higher-priority app proxy is undecidable in DNS projection, yet lower Geo selects direct domestic DNS: %s", raw)
	}
}

func TestDNSUncertainUserPriorityPreservesKnownPrefixAndDefaultsRemainder(t *testing.T) {
	for _, mode := range []string{"balanced", "strict"} {
		for _, method := range []string{"application", "ip", "cidr"} {
			for _, destination := range []string{"global", "direct", "block", "route:selected"} {
				l := NewRoutingLibrary()
				target := map[string]string{"application": "app.chat", "ip": "1.1.1.1", "cidr": "1.1.1.0/24"}[method]
				l.Packages[0].Rules = []RoutingEntry{
					{ID: "known", Method: "domain", Target: "known.example", Enabled: true, Destination: "direct"},
					{ID: "unknown", Method: method, Target: target, Enabled: true, Destination: destination},
					{ID: "later-block", Method: "domain", Target: "later.example", Enabled: true, Destination: "block"},
				}
				apps := []RoutingApplication{{ID: "app.chat", Path: "/Applications/Chat.app", Bundle: true}}
				rules, err := CompileDNSRouting(l, apps, map[string]string{"route:selected": "dns-selected"}, mode)
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

func TestDNSUncertainCustomPackageAndFollowDefaultUseProxyFallback(t *testing.T) {
	l := NewRoutingLibrary()
	l.Packages[0].Rules = []RoutingEntry{
		{ID: "skip-app", Method: "application", Target: "app.chat", Enabled: true, Destination: "default"},
		{ID: "later", Method: "domain", Target: "later.example", Enabled: true, Destination: "direct"},
	}
	apps := []RoutingApplication{{ID: "app.chat", Path: "/Applications/Chat.app", Bundle: true}}
	rules, err := CompileDNSRouting(l, apps, nil, "balanced")
	if err != nil || len(rules) != 1 || rules[0]["server"] != "dns-remote" || len(rules[0]) != 1 {
		t.Fatal("follow-default's process exclusion was dropped", rules, err)
	}
	l.Packages[0].Rules = nil
	l.Packages = append(l.Packages[:1], append([]RoutingPackage{{ID: "custom", Name: "Custom", Kind: "local", Enabled: true, Destination: "global", Rules: []RoutingEntry{{ID: "app", Method: "application", Target: "app.chat", Enabled: true, Destination: "global"}}}}, l.Packages[1:]...)...)
	rules, err = CompileDNSRouting(l, apps, nil, "balanced")
	if err != nil || len(rules) != 1 || rules[0]["server"] != "dns-remote" {
		t.Fatal("custom application's priority was dropped", rules, err)
	}
	l.Packages[1].Enabled = false
	rules, err = CompileDNSRouting(l, apps, nil, "balanced")
	raw, _ := json.Marshal(rules)
	if err != nil || !strings.Contains(string(raw), "dns-domestic") {
		t.Fatal("disabled custom rule blocked normal Geo DNS", rules, err)
	}
}
