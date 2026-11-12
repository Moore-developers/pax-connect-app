package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Exercise the real query/model/SQLite removal path, including a second page that
// was not sent to the App before the database records disappeared.
func TestTrafficDetailSnapshotSurvivesLogRemoval(t *testing.T) {
	for _, scenario := range []struct{ window, removal string }{
		{"day", "retention"}, {"day", "clear"}, {"day", "capacity"},
		{"today", "retention"}, {"today", "clear"}, {"today", "capacity"},
	} {
		t.Run(scenario.window+"/"+scenario.removal, func(t *testing.T) {
			ctx := context.Background()
			c, _ := newTrafficRPCFixture(t)
			at := time.Now().Add(-time.Minute)
			var records []config.Observation
			for i := 0; i < 30; i++ {
				fact := config.FlowFact{Key: fmt.Sprintf("snapshot/%02d", i), StartedAt: at.Add(time.Duration(i) * time.Millisecond), ApplicationID: "fixture.app", Application: "Fixture", Domain: "example.test", Direction: "direct", ResponseKnown: true, ResponseMS: uint64(i)}
				records = append(records, config.Observation{Session: "snapshot", Sequence: uint64(i + 1), Kind: "started", At: fact.StartedAt, Flow: &fact, Upload: 1, Download: 2})
			}
			if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "snapshot", HighWater: 30, Records: records}); err != nil {
				t.Fatal(err)
			}
			page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: scenario.window})
			if err != nil || len(page.GetItems()) != 1 {
				t.Fatal("traffic fixture", page, err)
			}
			query := &pb.TrafficDetailRequest{RowId: page.Items[0].Id, ReadToken: page.ReadToken, Limit: 20}
			first, err := c.QueryTrafficDetail(ctx, query)
			if err != nil || len(first.GetItems()) != 20 || first.NextCursor == "" {
				t.Fatal("opening detail", first, err)
			}
			query.ReadToken, query.Limit = first.ReadToken, 200
			before, err := c.QueryTrafficDetail(ctx, query)
			if err != nil || len(before.GetItems()) != 30 {
				t.Fatal("full frozen detail", before, err)
			}
			l := stopWriterGoroutine(t, c)
			switch scenario.removal {
			case "retention":
				_, err = l.logs.Prune(ctx, time.Now(), historystore.Limits{Rows: 2000, Duration: time.Second})
			case "clear":
				err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.ClearLogTables(ctx) })
			case "capacity":
				_, err = l.logs.EvictOldest(ctx, 1, historystore.Limits{Rows: 2000, Duration: time.Second})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: scenario.window, ReadToken: page.ReadToken}); err == nil {
				t.Fatal("list token survived log removal")
			}
			after, err := c.QueryTrafficDetail(ctx, query)
			if err != nil || !proto.Equal(before, after) {
				t.Fatal("opened detail changed after log removal", err)
			}
			query.Cursor, query.Limit = first.NextCursor, 20
			next, err := c.QueryTrafficDetail(ctx, query)
			if err != nil || len(next.GetItems()) != 10 || next.NextCursor != "" {
				t.Fatal("frozen second page unavailable", next, err)
			}
			if !proto.Equal(next.Summary, first.Summary) || !proto.Equal(next.AsOf, first.AsOf) {
				t.Fatal("pagination replaced the opening snapshot")
			}
			c.readTokens.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
			if _, err = c.QueryTrafficDetail(ctx, query); err == nil {
				t.Fatal("detail snapshot exceeded its lifetime")
			}
		})
	}
}
