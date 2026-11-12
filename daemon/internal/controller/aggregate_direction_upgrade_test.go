package controller_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
)

// A flow may first be seen (bytes, identity) before routing has chosen its route.
// Already committed bytes stay in their original attribution; later bytes use the observed route.
func TestAggregateFollowsRouteResolvedAfterFirstObservation(t *testing.T) {
	ctx := context.Background()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	c := controller.New("test", "test")
	if e = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := controller.CloseHistoryFixture(c, ctx); e != nil {
			t.Error(e)
		}
	}()
	at := time.Now().Add(-20 * time.Second)
	f := config.FlowFact{Key: "route/1/a", StartedAt: at, Domain: "api.example.test", Direction: "unknown", ApplicationID: "fixture.app", Application: "Fixture"}
	send := func(seq uint64, kind string, d time.Duration, up, down uint64) {
		t.Helper()
		b := config.ObservationBatch{Session: "route", Collecting: true, Records: []config.Observation{{Session: "route", Sequence: seq, At: at.Add(d), Kind: kind, Flow: &f, Upload: up, Download: down}}}
		if e := controller.FeedHistoryFixture(t, c, ctx, b); e != nil {
			t.Fatal(e)
		}
	}
	send(1, "sample", time.Second, 10, 90)
	f.Direction, f.RouteID, f.RouteName = "proxy", "pax-node-1", "美国线路"
	send(2, "started", 2*time.Second, 0, 0)
	send(3, "sample", 3*time.Second, 100, 900)
	for _, dimension := range []string{"application", "domain"} {
		page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: dimension, Window: "today"})
		if e != nil || len(page.GetItems()) != 2 {
			t.Fatalf("%s rows=%d err=%v", dimension, len(page.GetItems()), e)
		}
		var unknown, known *pb.TrafficRow
		for _, row := range page.Items {
			if row.RouteId == "pax-node-1" {
				known = row
			} else {
				unknown = row
			}
		}
		if known == nil || unknown == nil || known.ObjectId != unknown.ObjectId || known.Id == unknown.Id || page.Upload != 110 || page.Download != 990 || known.Direction != "proxy" || known.RouteName != "美国线路" || known.Upload != 100 || known.Download != 900 || unknown.Upload != 10 || unknown.Download != 90 {
			t.Fatalf("%s committed attribution changed: %v", dimension, page.Items)
		}
	}
	// The route detail's usage chart is keyed by route id, so the upgraded route
	// dimension must carry that id rather than the empty id of the first sighting.
	usage, e := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "pax-node-1", Window: "today"})
	if e != nil || usage.Upload != 100 || usage.Download != 900 {
		t.Fatalf("route usage up=%d down=%d err=%v", usage.GetUpload(), usage.GetDownload(), e)
	}
}
