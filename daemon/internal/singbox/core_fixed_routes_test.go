package singbox

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
)

func TestFixedRouteFaultRejectsOnlyItsOriginalBusinessAndDNSMatch(t *testing.T) {
	profile := `{
		"route":{"final":"proxy","rules":[
			{"domain":"public.claude.ai","outbound":"direct"},
			{"type":"logical","mode":"and","rules":[{"domain_suffix":"claude.ai"},{"port":443}],"outbound":"A"},
			{"domain_suffix":"other.example","outbound":"B"},
			{"domain_suffix":"example","outbound":"proxy"}
		]},
		"dns":{"rules":[
			{"domain":"public.claude.ai","server":"dns-domestic"},
			{"domain_suffix":"claude.ai","server":"dns-route-A"},
			{"domain_suffix":"other.example","server":"dns-route-B"},
			{"server":"dns-remote"}
		]}
	}`
	f := &fixedRouteProtection{blocked: map[string]bool{"A": true}}
	blocked, err := f.applyProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := fixedRoutingDocument(blocked)
	var matchers []adapter.Rule
	for _, value := range fixedRoutingRules(doc, "route") {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var options option.Rule
		if err = options.UnmarshalJSONContext(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		rule, err := R.NewRule(context.Background(), log.NewNOPFactory().Logger(), options, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rule.Close() })
		matchers = append(matchers, rule)
	}
	for _, test := range []struct{ domain, want string }{
		{"public.claude.ai", "direct"},
		{"api.claude.ai", "reject"},
		{"api.other.example", "B"},
		{"unrelated.example", "proxy"},
	} {
		t.Run(test.domain, func(t *testing.T) {
			metadata := adapter.InboundContext{Domain: test.domain}
			metadata.Destination.Port = 443
			var got string
			for _, rule := range matchers {
				metadata.ResetRuleCache()
				if !rule.Match(&metadata) {
					continue
				}
				switch action := rule.Action().(type) {
				case *R.RuleActionRoute:
					got = action.Outbound
				case *R.RuleActionReject:
					if !action.NoDrop {
						t.Fatal("repeated attempts could become a silent drop")
					}
					got = "reject"
				}
				break
			}
			if got != test.want {
				t.Fatalf("route = %q, want %q", got, test.want)
			}
		})
	}
	dnsRules := fixedRoutingRules(doc, "dns")
	if dnsRules[0]["server"] != "dns-domestic" || dnsRules[1]["action"] != "reject" || dnsRules[1]["no_drop"] != true || dnsRules[1]["server"] != nil || dnsRules[2]["server"] != "dns-route-B" || dnsRules[3]["server"] != "dns-remote" {
		t.Fatalf("DNS escaped fault scope: %+v", dnsRules)
	}
	delete(f.blocked, "A")
	recovered, err := f.applyProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	var original, restored any
	_ = json.Unmarshal([]byte(profile), &original)
	_ = json.Unmarshal([]byte(recovered), &restored)
	if !reflect.DeepEqual(original, restored) {
		t.Fatal("recovery lost the original policy")
	}
}

func TestFixedRouteNativeOwnershipKeepsSelectorAndClosedConnectionsOut(t *testing.T) {
	for _, test := range []struct {
		name       string
		connection *daemon.Connection
		fixed      bool
		retired    bool
	}{
		{"fixed-a", &daemon.Connection{Outbound: "A", ChainList: []string{"A"}}, true, false},
		{"selector-a", &daemon.Connection{Outbound: "A", ChainList: []string{"A", "proxy"}}, false, true},
		{"healthy-fixed-b", &daemon.Connection{Outbound: "B", ChainList: []string{"B"}}, false, false},
		{"direct", &daemon.Connection{Outbound: "direct", ChainList: []string{"direct"}}, false, false},
		{"closed-fixed-a", &daemon.Connection{Outbound: "A", ChainList: []string{"A"}, ClosedAt: 1}, false, false},
		{"closed-selector-a", &daemon.Connection{Outbound: "A", ChainList: []string{"A", "proxy"}, ClosedAt: 1}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := fixedConnectionUses(test.connection, "A"); got != test.fixed {
				t.Fatalf("fixed fault ownership = %v, want %v", got, test.fixed)
			}
			runtime := &CoreRuntime{retired: map[string]bool{"A": true}}
			if got := runtime.retiredSelectorConnection(test.connection); got != test.retired {
				t.Fatalf("selector retirement ownership = %v, want %v", got, test.retired)
			}
		})
	}
}

func TestFixedRouteFaultLabelsKeepIdentityAndRestoreOriginalIntent(t *testing.T) {
	profile := `{"route":{"rules":[{"domain":"a.example","outbound":"A"},{"domain":"b.example","outbound":"B"}]}}`
	sources := []string{
		`{"Package":"Claude","Method":"domain","Target":"a.example","FixedRouteName":"线路 A","FixedRouteID":"route:a"}`,
		`{"Package":"Other","Method":"domain","Target":"b.example","FixedRouteName":"线路 B","FixedRouteID":"route:b"}`,
	}
	f := &fixedRouteProtection{blocked: map[string]bool{"A": true}}
	labels := f.applySources(profile, sources)
	var evidence map[string]any
	if json.Unmarshal([]byte(labels[0]), &evidence) != nil || evidence["UnavailableRoute"] != "线路 A" || evidence["FixedRouteID"] != "route:a" || labels[1] != sources[1] {
		t.Fatalf("fault label lost its recorded scope: %v", labels)
	}
	delete(f.blocked, "A")
	if restored := f.applySources(profile, sources); !reflect.DeepEqual(restored, sources) {
		t.Fatalf("recovery retained fault labels: %v", restored)
	}
}
