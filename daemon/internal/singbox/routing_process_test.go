package singbox

import (
	"context"
	"slices"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing/common/json"
	"pax-connect/daemon/internal/config"
)

func TestClaudePackageRoutesCoreAndSharedDomains(t *testing.T) {
	library := config.NewRoutingLibrary()
	pack := config.NewClaudeRoutingPackage()
	pack.Destination = "route:fixed"
	library.Packages = slices.Insert(library.Packages, library.BuiltInStart(), pack)
	library.Packages[2].Enabled = false
	library.Packages[0].Rules = []config.RoutingEntry{
		{ID: "override", Method: "domain", Target: "www.google.com", Destination: "direct", Enabled: true},
	}
	routes := map[string]string{"route:fixed": "fixed"}
	compiled, err := config.CompileRouting(library, nil, routes, true)
	if err != nil || len(compiled) != 3 {
		t.Fatal("disabled Claude changed routing", compiled, err)
	}
	library.Packages[1].Enabled = true
	compiled, err = config.CompileRouting(library, nil, routes, true)
	if err != nil {
		t.Fatal(err)
	}
	type match struct {
		rule     adapter.Rule
		outbound string
	}
	var matchers []match
	for _, rule := range compiled {
		outbound, ok := rule["outbound"].(string)
		if !ok {
			continue
		}
		raw, err := json.Marshal(rule)
		if err != nil {
			t.Fatal(err)
		}
		var options option.Rule
		if err := json.Unmarshal(raw, &options); err != nil {
			t.Fatal(err)
		}
		matcher, err := R.NewRule(context.Background(), log.NewNOPFactory().Logger(), options, false)
		if err != nil {
			t.Fatal(err)
		}
		matchers = append(matchers, match{matcher, outbound})
	}
	for host, want := range map[string]string{
		"api.anthropic.com": "fixed", "accounts.google.com": "fixed", "mail.gmail.com": "fixed",
		"checkout.stripe.com": "fixed", "r.stripe.com": "fixed", "inquiry.withpersona.com": "fixed",
		"fcmtoken.googleapis.com": "fixed", "nexus-websocket-a.intercom.io": "fixed",
		"www.google.com": "direct", "notstripe.com": "", "google.com.example.net": "",
	} {
		got := ""
		for _, matcher := range matchers {
			if matcher.rule.Match(&adapter.InboundContext{Domain: host}) {
				got = matcher.outbound
				break
			}
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", host, got, want)
		}
	}
	dns, err := config.CompileDNSRouting(library, nil, map[string]string{"route:fixed": "dns-fixed"}, "balanced")
	if err != nil || !slices.ContainsFunc(dns, func(rule map[string]any) bool {
		domains, _ := rule["domain_suffix"].([]string)
		return slices.Contains(domains, "withpersona.com") && rule["server"] == "dns-fixed"
	}) {
		t.Fatal("Claude DNS did not inherit the selected route", dns, err)
	}
}

func TestRoutingProcessMatchesOnlyExactExecutable(t *testing.T) {
	const target = "/Applications/Example.app/Contents/Helpers/worker"
	library := config.NewRoutingLibrary()
	library.Packages[0].Rules = []config.RoutingEntry{
		{ID: "process", Method: "process", Target: target, Destination: "block", Enabled: true},
	}
	rules, labels, err := config.CompileRoutingEvidence(library, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, label := range labels {
		evidence, ok := config.RecordedRoutingEvidence(label)
		if !ok || evidence.Method != "process" || evidence.Target != target {
			continue
		}
		raw, err := json.Marshal(rules[i])
		if err != nil {
			t.Fatal(err)
		}
		var options option.Rule
		if err = json.Unmarshal(raw, &options); err != nil {
			t.Fatal(err)
		}
		matcher, err := R.NewRule(context.Background(), log.NewNOPFactory().Logger(), options, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{
			target, "/opt/another/worker", "/Applications/Example.app/Contents/MacOS/Example", "",
		} {
			metadata := adapter.InboundContext{ProcessInfo: &adapter.ConnectionOwner{ProcessPath: path}}
			if matched := matcher.Match(&metadata); matched != (path == target) {
				t.Fatalf("Match(%q) = %v", path, matched)
			}
		}
		return
	}
	t.Fatal("missing compiled process rule")
}
