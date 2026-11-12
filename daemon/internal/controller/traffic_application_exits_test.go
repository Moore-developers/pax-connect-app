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

func TestTrafficRowsMergeRulesBeforePagingAndSamples(t *testing.T) {
	f := newRecencyFixture(t)
	var records []config.Observation
	for routeIndex, route := range []string{"route-a", "direct", "route-b"} {
		for packIndex, rule := range []string{"final", "ip_is_private"} {
			for i := 0; i < 75; i++ {
				at := f.now.Add(-time.Duration(90-i) * time.Second)
				fact := config.FlowFact{Key: fmt.Sprintf("split/%d/%d/%02d", routeIndex, packIndex, i), ApplicationID: "fixture.split", Application: "Same name", StartedAt: at, Domain: "example.test", RouteID: route, RouteName: route, Direction: "proxy", Rule: rule, ResponseKnown: true, ResponseMS: uint64(i)}
				if route == "direct" {
					fact.Direction = "direct"
				}
				f.seq++
				records = append(records, config.Observation{Session: "split", Sequence: f.seq, At: at, Kind: "started", Flow: &fact, Upload: uint64(i + 1), Download: uint64(i+1) * 2})
			}
		}
	}
	other := config.FlowFact{Key: "other", ApplicationID: "fixture.other", Application: "Same name", StartedAt: f.now.Add(-time.Second), Domain: "other.test", RouteID: "direct", Direction: "direct"}
	f.seq++
	records = append(records, config.Observation{Session: "split", Sequence: f.seq, At: other.StartedAt, Kind: "started", Flow: &other, Upload: 1, Download: 2})
	if err := controller.FeedHistoryFixture(t, f.c, context.Background(), config.ObservationBatch{Session: "split", Collecting: true, HighWater: f.seq, Records: records}); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"hour", "day", "today", "week", "month"} {
		q := &pb.TrafficQuery{Dimension: "application", Window: window, Limit: 2}
		page, err := f.c.QueryTraffic(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if page.TotalRows != 4 || page.Connections != 451 || page.Upload != 17101 || page.Download != 34202 {
			t.Fatalf("%s split totals wrong", window)
		}
		firstToken := page.ReadToken
		rows := append([]*pb.TrafficRow(nil), page.Items...)
		seenRows := map[string]bool{}
		for page.NextCursor != "" {
			q.ReadToken, q.Cursor = firstToken, page.NextCursor
			page, err = f.c.QueryTraffic(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			rows = append(rows, page.Items...)
		}
		for _, row := range rows {
			if seenRows[row.Id] {
				t.Fatal("row duplicated across pages")
			}
			seenRows[row.Id] = true
			if row.ObjectId == "fixture.other" {
				continue
			}
			if row.Connections != 150 || row.Upload != 5700 || row.Download != 11400 || len(row.Exits) != 1 || row.RouteId == "" || row.Direction == "mixed" {
				t.Fatalf("%s split row lost metrics: %s/%s", window, row.RouteId, row.Basis)
			}
			if len(row.Bases) != 2 {
				t.Fatal("merged row lost original rule evidence")
			}
			if len(row.RecentResponses) != 20 || row.RecentResponses[0].Milliseconds != 65 || row.RecentResponses[19].Milliseconds != 74 {
				t.Fatal("row latest20 lost")
			}
			detail, err := f.c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{ReadToken: firstToken, RowId: row.Id})
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.Items) != 20 || detail.NextCursor == "" || len(detail.RecentResponses) != 60 || detail.RecentResponses[0].Milliseconds != 45 {
				t.Fatal("row detail paging/tail60 lost")
			}
			seen := map[string]bool{}
			current := detail
			var last time.Time
			for {
				for _, item := range current.Items {
					if seen[item.Key] || item.RouteId != row.RouteId || !last.IsZero() && item.StartedAt.AsTime().After(last) {
						t.Fatal("detail crosses path or duplicates/reorders")
					}
					seen[item.Key] = true
					last = item.StartedAt.AsTime()
				}
				if current.NextCursor == "" {
					break
				}
				current, err = f.c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{ReadToken: firstToken, RowId: row.Id, Cursor: current.NextCursor})
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(seen) != 150 {
				t.Fatal("split detail incomplete")
			}
		}
		filtered, err := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: window, Direction: "proxy", Search: "route-a"})
		if err != nil || len(filtered.GetItems()) != 1 || filtered.Connections != 150 {
			t.Fatalf("%s search crossed exit groups: %v", window, err)
		}
	}
}
