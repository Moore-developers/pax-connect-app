package singbox

import (
	"context"
	"encoding/json"
	"pax-connect/daemon/internal/config"
	"reflect"
	"testing"
)

func TestCoreCatalogProfileBuildsSelectableRoutes(t *testing.T) {
	a := testProfile()
	a.ID = "a"
	a.ResolvedConfigRevision = 1
	b := a
	b.ID = "b"
	b.ResolvedConfigRevision = 2
	raw, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{a, b}, a, testRules(t), "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Outbounds []struct {
			Type, Tag, Default string
			Outbounds          []string
		}
	}
	if err = json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range doc.Outbounds {
		if o.Type == "selector" && o.Tag == "proxy" {
			found = true
			if o.Default != config.RuntimeTag(a) || len(o.Outbounds) != 2 {
				t.Fatal("selector lost routes")
			}
		}
	}
	if !found {
		t.Fatal("missing runtime selector")
	}
}

func TestOfflinePolicyMatchesGeneratedCoreRules(t *testing.T) {
	p := testProfile()
	p.ID = "fixture"
	p.ResolvedConfigRevision = 1
	raw, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{p}, p, testRules(t), "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Route struct {
			Rules []json.RawMessage
			Final string
		}
	}
	if err = json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	offline := config.ConfiguredRoutingRules()
	if len(document.Route.Rules) != len(offline)-1 || document.Route.Final != config.DefaultRoutingOutbound {
		t.Fatal("offline policy diverged", len(offline), document.Route)
	}
	for i, rawRule := range document.Route.Rules {
		var fields map[string]any
		if err = json.Unmarshal(rawRule, &fields); err != nil {
			t.Fatal(err)
		}
		if _, leaked := fields["View"]; leaked {
			t.Fatal("presentation metadata entered core")
		}
		switch i {
		case 0:
			if fields["action"] != "sniff" {
				t.Fatal(fields)
			}
		case 1:
			if fields["action"] != "hijack-dns" {
				t.Fatal(fields)
			}
		case 2:
			if fields["ip_is_private"] != true || fields["outbound"] != "direct" {
				t.Fatal(fields)
			}
		case 3:
			if fields["outbound"] != "proxy" {
				t.Fatal(fields)
			}
		case 4, 5:
			if fields["outbound"] != "direct" {
				t.Fatal(fields)
			}
		}
	}
}

func TestCoreCatalogProfileRulesMatchCompiledRouting(t *testing.T) {
	p := testProfile()
	p.ID = "fixture"
	p.ResolvedConfigRevision = 1
	p.NetworkPrivacy.WebRTC = true
	library := config.NewRoutingLibrary()
	for i := range library.Packages {
		if library.Packages[i].Kind == "geo" {
			library.Packages[i].GeoContent = []config.RoutingEntry{
				{ID: "geosite", Method: "source", Target: "geosite", Enabled: true, Match: json.RawMessage(`{"domain":["example.cn"]}`)},
				{ID: "geoip", Method: "source", Target: "geoip", Enabled: true, Match: json.RawMessage(`{"ip_cidr":["203.0.113.0/24"]}`)},
			}
		}
	}
	p.Routing = &library
	raw, err := CoreCatalogProfile(context.Background(), []config.RouteConfig{p}, p, testRules(t), "127.0.0.1:17890")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Route struct{ Rules []map[string]any }
	}
	if err = json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	expectedLibrary := library
	expectedLibrary.WebRTC = true
	compiled, err := config.CompileRouting(expectedLibrary, p.RoutingApplications, map[string]string{"route:" + p.ID: config.RuntimeTag(p)}, true)
	if err != nil {
		t.Fatal(err)
	}
	var want []map[string]any
	data, _ := json.Marshal(compiled)
	if err = json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	gotRules, wantRules := normalizeSingletons(document.Route.Rules), normalizeSingletons(want)
	if !reflect.DeepEqual(gotRules, wantRules) {
		got, _ := json.MarshalIndent(gotRules, "", " ")
		exp, _ := json.MarshalIndent(wantRules, "", " ")
		t.Fatalf("route rules diverged from CompileRouting\ngot: %s\nwant: %s", got, exp)
	}
	for _, rule := range document.Route.Rules {
		if rule["action"] == "hijack-dns" {
			if _, merged := rule["ip_is_private"]; merged {
				t.Fatal("hijack-dns merged ip_is_private", rule)
			}
		}
		if rule["ip_is_private"] == true {
			if _, merged := rule["rule_set"]; merged {
				t.Fatal("private-network rule merged rule_set", rule)
			}
		}
	}
}

// normalizeSingletons folds one-element arrays to scalars: sing-box emits Listable
// values that way, which is the same JSON meaning.
func normalizeSingletons(v any) any {
	switch x := v.(type) {
	case []map[string]any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeSingletons(x[i])
		}
		return out
	case []any:
		if len(x) == 1 {
			if _, nested := x[0].(map[string]any); !nested {
				return normalizeSingletons(x[0])
			}
		}
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeSingletons(x[i])
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = normalizeSingletons(e)
		}
		return out
	}
	return v
}
