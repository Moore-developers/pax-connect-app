package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
)

func TestYesterdayAndRetainedDetailCountHaveIndependentRanges(t *testing.T) {
	f := newRecencyFixture(t).pinCalendar()
	local := f.now.In(time.Local)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
	var records []config.Observation
	// All records remain inside the actual last24h. Yesterday has 5, today has 25;
	// the same path's detail pool has 30 while either day's aggregate stays fixed.
	for i := 0; i < 30; i++ {
		at := midnight.Add(time.Duration(i-5) * time.Minute)
		fact := config.FlowFact{Key: fmt.Sprintf("yesterday/%02d", i), ApplicationID: "fixture.app", Application: "Fixture", Domain: "example.test", RouteID: "direct", Direction: "direct", Rule: "final", StartedAt: at, ResponseKnown: true, ResponseMS: uint64(i)}
		f.seq++
		records = append(records, config.Observation{Session: "yesterday", Sequence: f.seq, At: at, Kind: "started", Flow: &fact, Upload: 1, Download: 2})
	}
	if err := controller.FeedHistoryFixture(t, f.c, context.Background(), config.ObservationBatch{Session: "yesterday", Collecting: true, HighWater: f.seq, Records: records}); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"yesterday", "today", "week"} {
		page, err := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: window})
		if err != nil || len(page.GetItems()) != 1 {
			t.Fatalf("%s query %v", window, err)
		}
		want := uint64(30)
		if window == "yesterday" {
			want = 5
		}
		if window == "today" {
			want = 25
		}
		if page.Connections != want {
			t.Fatalf("%s total %d want%d", window, page.Connections, want)
		}
		if window == "yesterday" && (!page.AsOf.AsTime().Equal(midnight) || !page.From.AsTime().Equal(midnight.AddDate(0, 0, -1))) {
			t.Fatal("yesterday is not the previous local calendar day")
		}
		detail, err := f.c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{RowId: page.Items[0].Id, ReadToken: page.ReadToken})
		if err != nil {
			t.Fatalf("%s freshly created historical token expired: %v", window, err)
		}
		if detail.Summary.Connections != want || detail.RetainedConnectionCount != 30 || len(detail.Items) != 20 || detail.NextCursor == "" {
			t.Fatalf("%s retained count mixed aggregate/page count", window)
		}
		next, err := f.c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{RowId: page.Items[0].Id, ReadToken: page.ReadToken, Cursor: detail.NextCursor})
		if err != nil || len(next.GetItems()) != 10 || next.RetainedConnectionCount != 30 {
			t.Fatalf("%s second page count changed: %v", window, err)
		}
		if window == "yesterday" && len(detail.RecentResponses) != 5 {
			t.Fatal("yesterday curve contains today's responses")
		}
	}
}
