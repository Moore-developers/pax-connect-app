package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
)

func TestGeoUpdateThreeSourcesAtomicAndBrowser(t *testing.T) {
	c, repo := routingController(t)
	c.routing.decode = func(raw []byte) ([]config.RoutingEntry, string, error) {
		if string(raw) == "bad" {
			return nil, "", errors.New("invalid")
		}
		return []config.RoutingEntry{{ID: "geo", Method: "source", Target: "geo", Enabled: true, Destination: "direct", Match: []byte(`{"domain_suffix":["example.cn","second.cn"],"domain_regex":["^service[0-9]+\\.cn$"]}`)}}, "v-new", nil
	}
	fetch := &geoFixtureFetcher{}
	c.ConfigureSubscriptions(fetch)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"geosite-google", "geosite-cn", "geoip-cn"} {
		if err = os.WriteFile(filepath.Join(dir, tag+".srs"), []byte(tag), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c.routing.decode = func(raw []byte) ([]config.RoutingEntry, string, error) {
		var match json.RawMessage
		if string(raw) == "geosite-google" {
			match = []byte(`{"domain":["safebrowsing.googleapis.com"]}`)
		} else if string(raw) == "geosite-cn" {
			match = []byte(`{"domain_suffix":["example.cn","second.cn"],"domain_regex":["^service[0-9]+\\.cn$"]}`)
		} else if string(raw) == "geoip-cn" {
			match = []byte(`{"ip_cidr":["192.0.2.0/24"]}`)
		} else {
			return nil, "", config.ErrRoutingLibrary
		}
		return []config.RoutingEntry{{ID: "geo", Method: "source", Enabled: true, Destination: "direct", Match: match}}, string(raw) + "-v2", nil
	}
	if err := c.ConfigureGeoRules(dir); err != nil {
		t.Fatal(err)
	}
	baseline, err := c.QueryGeoRules(context.Background(), &pb.GeoRulesQuery{Limit: 100})
	if err != nil || baseline.Total != 5 {
		t.Fatal(baseline, err)
	}
	q := &pb.RoutingMutationRequest{ExpectedRevision: 1, Action: "update_geo", PackageId: "geo"}
	fetch.failSecond = true
	if _, err := c.MutateRoutingLibrary(context.Background(), q); err == nil {
		t.Fatal("partial download committed")
	}
	if repo.value.Revision != 1 || len(repo.value.Packages[1].GeoContent) != 0 {
		t.Fatal("old rules lost")
	}
	fetch.failSecond = false
	result, err := c.MutateRoutingLibrary(context.Background(), q)
	if err != nil || result.Packages[1].UpdatedUnix == 0 || len(repo.value.Packages[1].GeoContent) != 3 {
		t.Fatal(result, err)
	}
	page, err := c.QueryGeoRules(context.Background(), &pb.GeoRulesQuery{Search: "example", Limit: 1})
	if err != nil || page.Total != 1 || page.Rules[0].Destination != "geosite-cn" {
		t.Fatal(page, err)
	}
	compiled, _, err := config.CompileRoutingEvidence(repo.value, nil, nil, true)
	if err != nil || len(compiled) != 6 {
		t.Fatal(compiled, err)
	}
	if _, ok := compiled[4]["domain_suffix"]; !ok {
		t.Fatal("updated Geo not in runtime input", compiled)
	}
}

func TestGoogleGeoExtendsOldCacheWithoutReplacingCN(t *testing.T) {
	c, repo := routingController(t)
	old := config.RoutingEntry{ID: "saved-cn", Method: "source", Target: "geosite-cn", Enabled: true, Match: []byte(`{"domain":["saved.example.cn"]}`)}
	c.routing.library.Packages[1].GeoContent = []config.RoutingEntry{old}
	c.routing.library.Packages[1].Version = "saved-cn / saved-ip"
	c.routing.googleBaselineVersion = "google-pinned"
	c.routing.geoBaseline = []config.RoutingEntry{{ID: "google-pinned", Method: "source", Target: "geosite-google", Enabled: true, Match: []byte(`{"domain":["safebrowsing.googleapis.com"]}`)}}
	page, err := c.QueryGeoRules(context.Background(), &pb.GeoRulesQuery{Search: "geosite-google", Limit: 100})
	if err != nil || page.Total != 1 || page.Rules[0].Target != "safebrowsing.googleapis.com" {
		t.Fatal("missing old-cache Google fallback", page, err)
	}
	snapshot := c.routingLibrarySnapshot("")
	geo := snapshot.Packages[1]
	if len(geo.Rules) != 4 || geo.Rules[1].Id != "geosite-google" || geo.Rules[1].Destination != "global" {
		t.Fatal("Geo package hides mixed proxy/direct rules", geo)
	}
	if geo.Version != "saved-cn / saved-ip / Google 内置 google-pinned" {
		t.Fatal("cached version was relabeled", geo.Version)
	}
	if len(c.routing.library.Packages[1].GeoContent) != 1 || c.routing.library.Packages[1].GeoContent[0].ID != old.ID || repo.value.Revision != 1 {
		t.Fatal("reading Google fallback modified saved cache")
	}
}

type geoFixtureFetcher struct{ failSecond bool }

func (f *geoFixtureFetcher) FetchSubscription(_ context.Context, address string) ([]byte, error) {
	switch address {
	case "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-google.srs":
		return []byte("geosite-google"), nil
	case "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs":
		return []byte("geosite-cn"), nil
	case "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs":
		if f.failSecond {
			return nil, errors.New("offline")
		}
		return []byte("geoip-cn"), nil
	default:
		return nil, errors.New("unreviewed source")
	}
}
