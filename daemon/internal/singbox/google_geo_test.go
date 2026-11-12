package singbox

import (
	"context"
	"encoding/json"
	"net/netip"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	R "github.com/sagernet/sing-box/route/rule"
	boxjson "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/service"
	"pax-connect/daemon/internal/config"
)

type googleGeoRouter struct {
	adapter.Router
	sets map[string]adapter.RuleSet
}

func (r *googleGeoRouter) RuleSet(tag string) (adapter.RuleSet, bool) {
	s, ok := r.sets[tag]
	return s, ok
}

func TestGoogleGeoOverlapUsesProxyWithLegacyAndDownloadedCaches(t *testing.T) {
	for _, cache := range []string{"bundle", "legacy-cn-cache", "three-source-cache", "user-override", "disabled"} {
		t.Run(cache, func(t *testing.T) {
			library := config.NewRoutingLibrary()
			overlap := []string{"clientservices.googleapis.com", "safebrowsing.googleapis.com", "update.googleapis.com", "beacons.gcp.gvt2.com"}
			if cache != "bundle" {
				data, _ := json.Marshal(map[string]any{"domain": append(overlap, "service.fixture.cn")})
				library.Packages[1].GeoContent = []config.RoutingEntry{{ID: "cn", Method: "source", Target: "geosite-cn", Match: data, Enabled: true}}
			}
			if cache == "three-source-cache" {
				data, _ := json.Marshal(map[string]any{"domain": overlap})
				// The downloaded CN entry is intentionally first, proving compiler precedence.
				library.Packages[1].GeoContent = append(library.Packages[1].GeoContent, config.RoutingEntry{ID: "google", Method: "source", Target: "geosite-google", Match: data, Enabled: true})
			}
			if cache == "user-override" {
				library.Packages[0].Rules = []config.RoutingEntry{{ID: "override", Method: "domain_suffix", Target: "googleapis.com", Destination: "direct", Enabled: true}}
			}
			if cache == "disabled" {
				library.Packages[1].Enabled = false
			}
			p := testProfile()
			p.Routing = &library
			raw, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{p}, p, testRules(t), "127.0.0.1:17890")
			if err != nil {
				t.Fatal(err)
			}
			ctx := runtimeContext(context.Background())
			owner := &googleGeoRouter{sets: map[string]adapter.RuleSet{}}
			ctx = service.ContextWith[adapter.Router](ctx, owner)
			var opts option.Options
			if err = opts.UnmarshalJSONContext(ctx, []byte(raw)); err != nil {
				t.Fatal(err)
			}
			for _, set := range opts.Route.RuleSet {
				loaded, e := R.NewLocalRuleSet(ctx, log.NewNOPFactory().Logger(), set.Tag[0], set)
				if e != nil {
					t.Fatal(e)
				}
				owner.sets[set.Tag[0]] = loaded
				defer func() { _ = loaded.Close() }()
			}
			var matchers []adapter.Rule
			for _, r := range opts.Route.Rules {
				m, e := R.NewRule(ctx, log.NewNOPFactory().Logger(), r, false)
				if e != nil {
					t.Fatal(e)
				}
				if e = m.Start(); e != nil {
					t.Fatal(e)
				}
				matchers = append(matchers, m)
				defer func() { _ = m.Close() }()
			}
			decide := func(domain string) string {
				metadata := adapter.InboundContext{Domain: domain, Network: "tcp", Destination: M.Socksaddr{Addr: netip.MustParseAddr("1.1.1.1"), Port: 443}}
				for _, m := range matchers {
					metadata.ResetRuleCache()
					a, ok := m.Action().(*R.RuleActionRoute)
					if ok && m.Match(&metadata) {
						return a.Outbound
					}
				}
				return opts.Route.Final
			}
			for _, domain := range overlap {
				want := "proxy"
				if cache == "user-override" && domain != "beacons.gcp.gvt2.com" {
					want = "direct"
				}
				if got := decide(domain); got != want {
					t.Fatalf("%s %s got%s want%s", cache, domain, got, want)
				}
			}
			if cache == "legacy-cn-cache" || cache == "three-source-cache" {
				if decide("service.fixture.cn") != "direct" {
					t.Fatal("non-Google CN target changed")
				}
			}
			dnsRules, err := config.CompileDNSRouting(library, nil, nil, "balanced")
			if err != nil {
				t.Fatal(err)
			}
			for _, domain := range overlap {
				server := "dns-remote"
				for _, rule := range dnsRules {
					data, _ := json.Marshal(rule)
					var o option.DNSRule
					if e := boxjson.UnmarshalContext(ctx, data, &o); e != nil {
						t.Fatal(e)
					}
					matcher, e := R.NewDNSRule(ctx, log.NewNOPFactory().Logger(), o, false, false)
					if e != nil {
						t.Fatal(e)
					}
					if e = matcher.Start(); e != nil {
						t.Fatal(e)
					}
					metadata := adapter.InboundContext{Destination: M.Socksaddr{Fqdn: domain}}
					matched := matcher.Match(&metadata)
					_ = matcher.Close()
					if matched {
						server = o.DefaultOptions.RouteOptions.Server
						break
					}
				}
				want := "dns-remote"
				if cache == "user-override" && domain != "beacons.gcp.gvt2.com" {
					want = "dns-domestic"
				}
				if server != want {
					t.Fatalf("%s DNS %s got%s want%s", cache, domain, server, want)
				}
			}
		})
	}
}
