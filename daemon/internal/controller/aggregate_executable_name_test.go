package controller

import (
	"context"

	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Aggregates persist a display name; buckets saved when the name was the bare
// version directory must still show the executable name, with the ID unchanged.
func TestAggregateRenamesStoredVersionDirectoryExecutable(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c, _ := routingController(t)
	if err = ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := CloseHistoryFixture(c, ctx); e != nil {
			t.Error(e)
		}
	}()
	path := "/Users/test/.local/share/claude/versions/2.1.301"
	at := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "fixture/1/a", StartedAt: at, ApplicationID: "process:" + path, Application: "2.1.301", Process: path, Domain: "example.test", RouteID: "direct", Direction: "direct", Rule: "final"}
	if err = FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "fixture", Collecting: true, Records: []config.Observation{{Session: "fixture", Sequence: 1, At: at, Kind: "started", Flow: &f}}}); err != nil {
		t.Fatal(err)
	}
	// Emulate buckets written by a build that named the tool after its path basename.
	rows := readTrafficTable(t, c.trafficJob().store.(*historystore.Traffic), "traffic_5m")
	err = c.trafficJob().store.Update(ctx, func(tx *historystore.TrafficTx) error {
		for _, r := range rows {
			r.Name = "2.1.301"
			if e := tx.PutTraffic5m(ctx, r); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "week"})
	if err != nil || len(page.GetItems()) != 1 {
		t.Fatalf("application rows: %v %v", page.GetItems(), err)
	}
	row := page.Items[0]
	if row.Name != "Claude Code" || row.SourceLabel != "CLI" || row.ObjectId != "process:"+path {
		t.Fatalf("name=%q label=%q object=%q", row.Name, row.SourceLabel, row.ObjectId)
	}
}
