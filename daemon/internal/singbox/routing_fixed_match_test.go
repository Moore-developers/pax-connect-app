package singbox

import (
	"context"
	"net/netip"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	"github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	"pax-connect/daemon/internal/config"
)

func TestUnavailableFixedRouteReportsRejectionFromTunPreMatch(t *testing.T) {
	ctx := context.Background()
	library := config.NewRoutingLibrary()
	library.Packages[0].Rules = []config.RoutingEntry{
		{ID: "a", Method: "ip", Target: "203.0.113.1", Destination: "route:a", Enabled: true},
	}
	library.Packages[1].Enabled = false
	library.RouteNames = map[string]string{"route:a": "A"}
	rules, labels, err := config.CompileRoutingEvidence(library, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"log":          map[string]any{"disabled": true},
		"route":        map[string]any{"rules": rules},
		"experimental": map[string]any{"cache_file": map[string]any{"enabled": true, "path": filepath.Join(t.TempDir(), "cache.db")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := NewCoreRuntime(ctx)
	defer r.Close()
	r.SetRoutingSources(labels)
	if err = r.Apply(string(raw)); err != nil {
		t.Fatal(err)
	}
	router := r.current().Instance().Box().Router()
	stun := []byte{0, 1, 0, 0, 0x21, 0x12, 0xa4, 0x42, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	verdict := adapter.JudgeFlow(router, "tun-in", "tun", syscall.IPPROTO_UDP,
		netip.MustParseAddrPort("172.29.231.1:40001"), netip.MustParseAddrPort("203.0.113.1:3478"), stun)
	if verdict.Action != tun.ActionReject {
		t.Fatalf("tun verdict = %v, want reject", verdict.Action)
	}
	observed := r.ReadObservations(ctx, "", 0, 10)
	if len(observed.Records) != 2 || observed.Records[0].Kind != "rejected" || observed.Records[1].Kind != "closed" {
		t.Fatalf("tun did not report a terminal rejection: %+v", observed.Records)
	}
	if flow := observed.Records[0].Flow; flow == nil || flow.RouteID != "" || config.LogStatus(*flow) != "指定线路 A 不可用" {
		t.Fatalf("wrong tun rejection: %+v", flow)
	}
}

func TestUnavailableFixedRouteWinsOnlyAtItsOriginalMatch(t *testing.T) {
	library := config.NewRoutingLibrary()
	library.WebRTC = true
	library.RouteNames = map[string]string{"route:claude": "A"}
	library.Packages[0].Rules = []config.RoutingEntry{
		{ID: "direct", Method: "domain", Target: "api.anthropic.com", Destination: "direct", Enabled: true},
		{ID: "missing-priority", Method: "domain", Target: "priority.example", Destination: "route:missing", Enabled: true},
		{ID: "block", Method: "domain", Target: "blocked.claude.ai", Destination: "block", Enabled: true},
		{ID: "default", Method: "domain", Target: "follow.claude.ai", Destination: "default", Enabled: true},
		{ID: "other", Method: "domain_suffix", Target: "claude.ai", Destination: "route:other", Enabled: true},
	}
	library.Packages[1].Enabled = false
	subscription := config.RoutingPackage{
		ID: "subscription", Name: "Subscription", Kind: "subscription",
		Source: "https://rules.example/pack.json", Destination: "route:unloaded", Enabled: true,
		Rules: []config.RoutingEntry{
			{
				ID: "source", Method: "source", Target: "订阅域名和端口", Destination: "global", Enabled: true,
				Match: []byte(`{"type":"logical","mode":"and","rules":[{"domain":["sub.anthropic.com"]},{"port":3478}]}`),
			},
		},
	}
	claude := config.NewClaudeRoutingPackage()
	claude.Enabled, claude.Destination = true, "route:claude"
	library.Packages = slices.Insert(library.Packages, library.BuiltInStart(), subscription, claude)
	compiled, labels, err := config.CompileRoutingEvidence(library, nil, map[string]string{"route:other": "fixed"}, true)
	if err != nil || len(compiled) != len(labels) {
		t.Fatal(compiled, labels, err)
	}
	type match struct {
		rule     adapter.Rule
		evidence config.RoutingMatchEvidence
	}
	var matchers []match
	for i, rule := range compiled {
		raw, err := json.Marshal(rule)
		if err != nil {
			t.Fatal(err)
		}
		var opts option.Rule
		if err = json.Unmarshal(raw, &opts); err != nil {
			t.Fatal(err)
		}
		native, err := R.NewRule(context.Background(), log.NewNOPFactory().Logger(), opts, false)
		if err != nil {
			t.Fatalf("rule %d: %v", i, err)
		}
		t.Cleanup(func() { _ = native.Close() })
		switch native.Action().(type) {
		case *R.RuleActionRoute, *R.RuleActionReject:
			evidence, _ := config.RecordedRoutingEvidence(labels[i])
			matchers = append(matchers, match{native, evidence})
		}
	}

	for _, tc := range []struct{ domain, protocol, action, unavailable string }{
		{"api.anthropic.com", "tls", "direct", ""},
		{"api.anthropic.com", "stun", "proxy", ""},
		{"priority.example", "stun", "reject", "missing"},
		{"blocked.claude.ai", "tls", "reject", ""},
		{"blocked.claude.ai", "stun", "proxy", ""},
		{"other.claude.ai", "tls", "fixed", ""},
		{"other.claude.ai", "stun", "fixed", ""},
		{"follow.claude.ai", "stun", "reject", "A"},
		{"sub.anthropic.com", "stun", "reject", "unloaded"},
		{"web.anthropic.com", "tls", "reject", "A"},
		{"web.anthropic.com", "stun", "reject", "A"},
		{"unrelated.example", "stun", "proxy", ""},
		{"unrelated.example", "tls", "", ""},
	} {
		t.Run(tc.domain+"/"+tc.protocol, func(t *testing.T) {
			metadata := adapter.InboundContext{
				Domain: tc.domain, Protocol: tc.protocol,
				Destination: M.Socksaddr{Addr: netip.MustParseAddr("1.1.1.1"), Port: 3478},
			}
			var action, unavailable string
			for _, m := range matchers {
				metadata.ResetRuleCache()
				if !m.rule.Match(&metadata) {
					continue
				}
				unavailable = m.evidence.UnavailableRoute
				switch a := m.rule.Action().(type) {
				case *R.RuleActionRoute:
					action = a.Outbound
				case *R.RuleActionReject:
					action = "reject"
					if unavailable != "" && !a.NoDrop {
						t.Fatal("unavailable route rejection can become a silent drop")
					}
				}
				break
			}
			if action != tc.action || unavailable != tc.unavailable {
				t.Fatalf("got (%q, %q), want (%q, %q)", action, unavailable, tc.action, tc.unavailable)
			}
		})
	}
}
