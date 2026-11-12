package singbox

import (
	"context"
	"encoding/json"
	"github.com/sagernet/sing-box/option"
	boxjson "github.com/sagernet/sing/common/json"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	R "github.com/sagernet/sing-box/route/rule"
	M "github.com/sagernet/sing/common/metadata"

	"pax-connect/daemon/internal/config"
)

func TestCatalogRuleConditionsExactlyMatchCompiledEvidence(t *testing.T) {
	for _, webRTC := range []bool{false, true} {
		p := testProfile()
		p.ID = "fixture"
		p.NetworkPrivacy.WebRTC = webRTC
		library := config.NewRoutingLibrary()
		library.WebRTC = webRTC
		library.Packages[1].GeoContent = []config.RoutingEntry{
			{ID: "google", Method: "source", Target: "geosite-google", Enabled: true, Match: []byte(`{"domain":["proxy.fixture.test"]}`)},
			{ID: "site", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain_suffix":["fixture.test","service.fixture.test"]}`)},
			{ID: "ip", Method: "source", Target: "geoip-cn", Enabled: true, Match: []byte(`{"ip_cidr":["93.184.216.0/24"]}`)},
		}
		p.Routing = &library
		raw, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{p}, p, testRules(t), "127.0.0.1:17890")
		if err != nil {
			t.Fatal(err)
		}
		var document struct {
			Route struct {
				Rules []map[string]any `json:"rules"`
			} `json:"route"`
		}
		if err = json.Unmarshal([]byte(raw), &document); err != nil {
			t.Fatal(err)
		}
		compiled, _, err := config.CompileRoutingEvidence(library, nil, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		// Canonical JSON removes []string/[]any and integer representation differences.
		data, _ := json.Marshal(compiled)
		var typed []option.Rule
		if err = boxjson.UnmarshalContext(context.Background(), data, &typed); err != nil {
			t.Fatal(err)
		}
		data, err = boxjson.MarshalContext(context.Background(), typed)
		if err != nil {
			t.Fatal(err)
		}
		var expected []map[string]any
		if err = json.Unmarshal(data, &expected); err != nil {
			t.Fatal(err)
		}
		if len(expected) != len(document.Route.Rules) {
			t.Fatal("rule count differs")
		}
		for i, rule := range document.Route.Rules {
			if !reflect.DeepEqual(rule, expected[i]) {
				t.Errorf("WebRTC=%v rule[%d] conditions differ: compiled=%v actual=%v", webRTC, i, expected[i], rule)
			}
		}
	}
}

// Exercise the rules that leave the actual profile builder, not only the
// compiler's pristine maps. This catches stale fields retained during decoding.
func TestCatalogPublicGeoAndPrivateMatchesHaveDistinctEvidence(t *testing.T) {
	for _, webRTC := range []bool{false, true} {
		p := testProfile()
		p.NetworkPrivacy.WebRTC = webRTC
		library := config.NewRoutingLibrary()
		library.WebRTC = webRTC
		library.Packages[1].GeoContent = []config.RoutingEntry{
			{ID: "google", Method: "source", Target: "geosite-google", Enabled: true, Match: []byte(`{"domain":["proxy.fixture.test"]}`)},
			{ID: "site", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain_suffix":["fixture.cn"]}`)},
			{ID: "ip", Method: "source", Target: "geoip-cn", Enabled: true, Match: []byte(`{"ip_cidr":["93.184.216.0/24"]}`)},
		}
		p.Routing = &library
		raw, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{p}, p, testRules(t), "127.0.0.1:17890")
		if err != nil {
			t.Fatal(err)
		}
		ctx := runtimeContext(context.Background())
		var options struct {
			Route struct {
				Rules []option.Rule `json:"rules"`
			} `json:"route"`
		}
		if err = boxjson.UnmarshalContext(ctx, []byte(raw), &options); err != nil {
			t.Fatal(err)
		}
		_, labels, err := config.CompileRoutingEvidence(library, nil, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, sample := range []struct{ domain, ip, pack, target string }{
			{"public.fixture.cn", "1.1.1.1", "geosite-cn", "中国域名 / IP 规则"},
			{"", "93.184.216.34", "geoip-cn", "中国域名 / IP 规则"},
			{"", "127.0.0.1", "Geo 基础包", "局域网与私有网络地址"},
			{"", "192.168.1.2", "Geo 基础包", "局域网与私有网络地址"},
		} {
			matched := false
			for i, candidate := range options.Route.Rules {
				matcher, e := R.NewRule(ctx, log.NewNOPFactory().Logger(), candidate, false)
				if e != nil {
					t.Fatal(e)
				}
				metadata := adapter.InboundContext{Network: "tcp", Domain: sample.domain, Destination: M.ParseSocksaddrHostPort(sample.ip, 443)}
				hit := matcher.Action().Type() == C.RuleActionTypeRoute && matcher.Match(&metadata)
				_ = matcher.Close()
				if !hit {
					continue
				}
				evidence, ok := config.RecordedRoutingEvidence(labels[i])
				if !ok || evidence.Package != sample.pack || evidence.Target != sample.target {
					t.Fatalf("WebRTC=%v %s/%s matched wrong evidence: %+v", webRTC, sample.domain, sample.ip, evidence)
				}
				matched = true
				break
			}
			if !matched {
				t.Fatalf("WebRTC=%v missing match for %+v", webRTC, sample)
			}
		}
	}
}
