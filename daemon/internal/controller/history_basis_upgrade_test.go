package controller

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"testing"
	"time"
)

func TestAggregateRuleUpgradeRecoversObservedEvidenceWithoutChangingTotals(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-10 * time.Minute)
	f := config.FlowFact{Key: "fixture/1/a", StartedAt: at, ApplicationID: "fixture.app", Application: "Fixture", Domain: "example.test", RouteID: "route", RouteName: "旧线路", Direction: "proxy", ResponseKnown: true, ResponseMS: 321}
	if e := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "fixture", Collecting: true, Records: []config.Observation{{Session: "fixture", Sequence: 1, At: at, Kind: "started", Flow: &f, Download: 123}}}); e != nil {
		t.Fatal(e)
	}
	// Evidence arriving after a committed start cannot backfill that historical counter.
	f.Rule, f.RuleSource = "final", "内核规则"
	later := config.ObservationBatch{Session: "fixture", Collecting: true, Records: []config.Observation{{Session: "fixture", Sequence: 2, At: at.Add(time.Second), Kind: "phase", Flow: &f}}}
	for range 2 {
		if e := FeedHistoryFixture(t, c, ctx, later); e != nil {
			t.Fatal(e)
		}
		page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "week"})
		if e != nil || page.GetDownload() != 123 || len(page.GetItems()) != 1 {
			t.Fatalf("late evidence changed metrics: %v", e)
		}
		row := page.Items[0]
		if row.Connections != 1 || row.ValidLatencyCount != 1 || row.LatestLatencyMs != 321 {
			t.Fatalf("late evidence changed counters/latency: %v", row)
		}
		for _, basis := range row.Bases {
			if basis.Rule == "final" && basis.Connections != 0 {
				t.Fatalf("late evidence moved committed start: %v", basis)
			}
		}
		detail, e := c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
		if e != nil || detail.Summary.Download != 123 {
			t.Fatalf("detail lost committed metrics: %v", e)
		}
	}
}
