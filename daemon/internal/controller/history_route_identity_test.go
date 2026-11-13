package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
)

func TestHistoryRouteSurvivesSavedEditAndRetirement(t *testing.T) {
	c := New("test", "test")
	c.data = &routeData{tags: map[string]string{}}
	old := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "route", Name: "Original"}, ResolvedConfigRevision: 3}
	c.rememberHistoryRoute(old)
	c.current.Routes = []*pb.NodeProfile{{Id: "route", Name: "Edited", ConfigRevision: 4}}
	c.rememberHistoryRoute(config.RouteConfig{NodeProfile: config.NodeProfile{ID: "other", Name: "Other"}, ResolvedConfigRevision: 1})
	// The old version is no longer selected or present in the saved directory.
	c.current.Routes = nil
	f := config.FlowFact{RouteID: config.RuntimeTag(old), RouteName: config.RuntimeTag(old), Direction: "proxy"}
	c.mapHistoryRoute(&f)
	if f.RouteID != "route" || f.RouteName != "Original" {
		t.Fatalf("lost running identity: %+v", f)
	}
	// Reprocessing a frozen fact, or renaming the same config, must not rewrite it.
	old.Name = "Renamed"
	c.rememberHistoryRoute(old)
	c.mapHistoryRoute(&f)
	if f.RouteName != "Original" {
		t.Fatal("rewrote frozen name")
	}
}

func TestIngestFreezesRouteBeforeJournalAndDoesNotMutateCorePage(t *testing.T) {
	c := New("test", "test")
	c.data = &routeData{tags: map[string]string{}}
	p := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "route", Name: "Original"}, ResolvedConfigRevision: 3}
	c.rememberHistoryRoute(p)
	core := newFakeCore("session")
	flow := config.FlowFact{Key: "session/1", RouteID: config.RuntimeTag(p), RouteName: config.RuntimeTag(p), Direction: "proxy"}
	core.add(time.Now(), "started", &flow, 0, 0)
	in := newHistoryIngest(nil, core)
	in.route = c.mapHistoryRoute
	in.seeding = false
	batch, err := in.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.current.Routes = nil
	c.data = &routeData{tags: map[string]string{}} // a restart has no old in-memory registry
	plan := in.plan(batch)
	if len(plan.rows) != 1 {
		t.Fatalf("rows=%d", len(plan.rows))
	}
	var got config.Observation
	if err = json.Unmarshal(plan.rows[0].Body, &got); err != nil {
		t.Fatal(err)
	}
	c.mapHistoryRoute(got.Flow)
	if got.Flow.RouteID != "route" || got.Flow.RouteName != "Original" {
		t.Fatalf("journal did not freeze identity: %+v", got.Flow)
	}
	if flow.RouteID != config.RuntimeTag(p) {
		t.Fatal("mutated shared core page")
	}
}

// The production journal processors must preserve identity even after the
// runtime index and saved catalog are gone (including daemon restart/replay).
func TestHistoryRouteFrozenJournalFeedsBothStores(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	c.data = &routeData{tags: map[string]string{}}
	p := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "route", Name: "Original"}, ResolvedConfigRevision: 3}
	c.rememberHistoryRoute(p)
	at := time.Now().Add(-20 * time.Second)
	flow := config.FlowFact{Key: "frozen/1", ApplicationID: "fixture.app", Application: "Fixture", Domain: "example.test", StartedAt: at, RouteID: config.RuntimeTag(p), RouteName: config.RuntimeTag(p), Direction: "proxy", Rule: "final", RoutingMatchedAt: at, ResponseKnown: true, ResponseMS: 12, EstablishmentState: "response_ready"}
	core := newFakeCore("frozen")
	core.add(at, "started", &flow, 100, 200)
	in := newHistoryIngest(nil, core)
	in.seeding = false
	in.route = c.mapHistoryRoute
	batch, err := in.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.data = &routeData{tags: map[string]string{}}
	c.current.Routes = nil
	if err = FeedHistoryFixture(t, c, context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"minute", "today", "week"} {
		page, e := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: window})
		if e != nil {
			t.Fatal(e)
		}
		if len(page.Items) != 1 || page.Items[0].RouteId != "route" || page.Items[0].RouteName != "Original" || page.Upload != 100 || page.Download != 200 || page.Connections != 1 {
			t.Fatalf("%s lost identity/totals: %v", window, page)
		}
	}
	logs, e := c.logJob()
	if e != nil {
		t.Fatal(e)
	}
	var got config.LogConnectionSummary
	rows, err := logs.logs.LogConnections(context.Background(), []string{flow.Key})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(rows[flow.Key].Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Fact.RouteID != "route" || got.Fact.RouteName != "Original" {
		t.Fatalf("log identity changed: %+v", got.Fact)
	}
}

func TestHistoryRouteDoesNotGuessNameFromSavedCatalog(t *testing.T) {
	c := New("test", "test")
	p := config.RouteConfig{NodeProfile: config.NodeProfile{ID: "route", Name: "Saved rename"}, ResolvedConfigRevision: 3}
	c.current.Routes = []*pb.NodeProfile{{Id: p.ID, Name: p.Name, ConfigRevision: p.ResolvedConfigRevision}}
	f := config.FlowFact{RouteID: config.RuntimeTag(p), RouteName: config.RuntimeTag(p), Direction: "proxy"}
	c.mapHistoryRoute(&f)
	if f.RouteName != config.RuntimeTag(p) || f.RouteID != config.RuntimeTag(p) {
		t.Fatal("guessed historical identity from current saved directory")
	}
}
