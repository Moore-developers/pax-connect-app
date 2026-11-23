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

func TestTrafficApplicationProjectionMergesActualExitsBeforePaging(t *testing.T) {
	ctx := context.Background()
	f := newRecencyFixture(t)
	f.now = controller.SetHistoryFixtureClock(f.c)
	var records []config.Observation
	for routeIndex, route := range []string{"route-a", "direct", "route-b"} {
		for i := 0; i < 3; i++ {
			at := f.now.Add(-time.Duration(20-i) * time.Second)
			direction := "proxy"
			if route == "direct" {
				direction = "direct"
			}
			fact := config.FlowFact{Key: fmt.Sprintf("merge/%d/%d", routeIndex, i), ApplicationID: "fixture.merge", Application: "Merged App", StartedAt: at, Domain: "example.test", RouteID: route, RouteName: route, Direction: direction, EstablishmentState: "ready", ResponseKnown: true, ResponseMS: uint64(i), Rule: "final", RoutingMatchedAt: at}
			f.seq++
			records = append(records, config.Observation{Session: "merge", Sequence: f.seq, At: at, Kind: "started", Flow: &fact, Upload: uint64(i + 1), Download: uint64(i + 2)})
		}
	}
	blockedAt := f.now.Add(-time.Second)
	blocked := config.FlowFact{Key: "merge/blocked", ApplicationID: "fixture.merge", Application: "Merged App", StartedAt: blockedAt, Domain: "blocked.test", RouteID: "blocked", RouteName: "blocked", Direction: "blocked", Failure: "rule_rejected", EstablishmentState: "failed", Rule: "domain=blocked.test", RoutingMatchedAt: blockedAt}
	f.seq++
	records = append(records, config.Observation{Session: "merge", Sequence: f.seq, At: blockedAt, Kind: "rejected", Flow: &blocked})
	other := config.FlowFact{Key: "merge/another", ApplicationID: "fixture.other", Application: "Merged App", StartedAt: f.now.Add(-time.Minute), Domain: "other.test", Direction: "direct"}
	f.seq++
	records = append(records, config.Observation{Session: "merge", Sequence: f.seq, At: other.StartedAt, Kind: "started", Flow: &other, Download: 2})
	if err := controller.FeedHistoryFixture(t, f.c, ctx, config.ObservationBatch{Session: "merge", HighWater: f.seq, Records: records}); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"hour", "day", "today"} {
		q := &pb.TrafficQuery{Dimension: "application", Window: window, GroupApplications: true, Limit: 1}
		page, err := f.c.QueryTraffic(ctx, q)
		if err != nil {
			t.Fatal(window, err)
		}
		if page.TotalRows != 2 || len(page.Items) != 1 || page.NextCursor == "" {
			t.Fatalf("%s expected two application identities and one paged row, got total=%d items=%d", window, page.TotalRows, len(page.Items))
		}
		row := page.Items[0]
		if row.ObjectId != "fixture.merge" || row.Connections != 9 || row.BlockedCount != 1 || row.FailedCount != 1 || row.Upload != 18 || row.Download != 27 {
			t.Fatalf("%s merged counters wrong: connections=%d blocked=%d failed=%d", window, row.Connections, row.BlockedCount, row.FailedCount)
		}
		if row.RouteId != "" || row.Direction != "mixed" || len(row.Exits) != 4 {
			t.Fatalf("%s merged exits wrong: route=%q direction=%q exits=%d", window, row.RouteId, row.Direction, len(row.Exits))
		}
		if row.LatestMatch.GetDirection() != "blocked" || len(row.Bases) != 4 {
			t.Fatalf("%s rule evidence lost: %v", window, row.Bases)
		}
		q.ReadToken, q.Cursor = page.ReadToken, page.NextCursor
		next, err := f.c.QueryTraffic(ctx, q)
		if err != nil || len(next.GetItems()) != 1 || next.Items[0].ObjectId != "fixture.other" || next.NextCursor != "" {
			t.Fatalf("%s pagination did not preserve distinct same-name apps: %v", window, err)
		}
		q.ReadToken, q.Cursor, q.Search = "", "", "route-a"
		found, err := f.c.QueryTraffic(ctx, q)
		if err != nil || len(found.GetItems()) != 1 || found.Items[0].Id != row.Id || found.Connections != 9 {
			t.Fatalf("%s search did not preserve whole-app totals: %v", window, err)
		}
		q.Search, q.Direction = "", "proxy"
		filtered, err := f.c.QueryTraffic(ctx, q)
		if err != nil || len(filtered.GetItems()) != 1 || filtered.Items[0].Id != row.Id || filtered.Connections != 6 || filtered.Items[0].BlockedCount != 0 {
			t.Fatalf("%s proxy filter did not scope app contributions: %v", window, err)
		}
		detail, err := f.c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
		if err != nil {
			t.Fatal(window, err)
		}
		if detail.RetainedConnectionCount != 10 || len(detail.Items) != 10 {
			t.Fatalf("%s merged detail lost members: retained=%d items=%d", window, detail.RetainedConnectionCount, len(detail.Items))
		}
		targets, err := f.c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id, GroupConnections: true})
		if err != nil || targets.GetTargetGroupTotal() != 2 || targets.GetFilteredConnectionCount() != 10 {
			t.Fatalf("%s merged target display lost members: %v", window, err)
		}
	}
}
