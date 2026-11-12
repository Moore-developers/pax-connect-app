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

// Window connection starts come from the route's aggregates, while the open-flow
// count must keep following the flow's route after its identity was re-keyed from
// "no route yet" to the route id, and drop to zero when collection stops.
func TestRouteUsageWindowConnectionsAndOpenFlowsAfterRekey(t *testing.T) {
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
	seq := uint64(0)
	send := func(collecting bool, f config.FlowFact, kind string, d time.Duration, up, down uint64) {
		t.Helper()
		seq++
		b := config.ObservationBatch{Session: "usage", Collecting: collecting, Records: []config.Observation{{Session: "usage", Sequence: seq, At: at.Add(d), Kind: kind, Flow: &f, Upload: up, Download: down}}}
		if e := controller.FeedHistoryFixture(t, c, ctx, b); e != nil {
			t.Fatal(e)
		}
	}
	usage := func() *pb.RouteUsageResponse {
		t.Helper()
		v, e := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "pax-node-1", Window: "today"})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	first := config.FlowFact{Key: "usage/1", StartedAt: at, Domain: "api.example.com", Direction: "unknown", ApplicationID: "fixture.app", Application: "Fixture"}
	send(true, first, "sample", time.Second, 10, 90)
	first.Direction, first.RouteID, first.RouteName = "proxy", "pax-node-1", "美国线路"
	send(true, first, "started", 2*time.Second, 0, 0)
	second := config.FlowFact{Key: "usage/2", StartedAt: at.Add(3 * time.Second), Domain: "b.example.com", Direction: "proxy", RouteID: "pax-node-1", RouteName: "美国线路", ApplicationID: "fixture.app", Application: "Fixture"}
	send(true, second, "started", 3*time.Second, 0, 0)
	send(true, second, "sample", 4*time.Second, 5, 5)
	got := usage()
	var buckets uint64
	for _, item := range got.Items {
		buckets += item.Connections
	}
	if got.Connections != 2 || got.WindowConnections != 2 || buckets != 2 {
		t.Fatalf("open=%d window=%d buckets=%d", got.Connections, got.WindowConnections, buckets)
	}
	// The first sample was committed before the route was known. It stays
	// unattributed; later starts and bytes belong to the known route.
	if got.Upload != 5 || got.Download != 5 {
		t.Fatalf("committed bytes migrated during rekey: %d/%d", got.Upload, got.Download)
	}
	unknown, err := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "unattributed", Window: "today"})
	if err != nil || unknown.Upload != 10 || unknown.Download != 90 || unknown.WindowConnections != 0 {
		t.Fatalf("earlier attribution changed: %v %v", unknown, err)
	}
	closedAt := at.Add(5 * time.Second)
	second.ClosedAt = &closedAt
	send(true, second, "closed", 5*time.Second, 0, 0)
	if got = usage(); got.Connections != 1 || got.WindowConnections != 2 {
		t.Fatalf("after close open=%d window=%d", got.Connections, got.WindowConnections)
	}
	// Collection stopped: no open-flow claim, but started connections stay in the window.
	send(false, first, "sample", 6*time.Second, 0, 0)
	if got = usage(); got.Connections != 0 || got.WindowConnections != 2 {
		t.Fatalf("idle open=%d window=%d", got.Connections, got.WindowConnections)
	}
}
