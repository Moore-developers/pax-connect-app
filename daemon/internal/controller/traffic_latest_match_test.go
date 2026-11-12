package controller_test

import (
	"context"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"testing"
	"time"
)

func TestTrafficLatestMatchKeepsOneExitAndAllMetrics(t *testing.T) {
	f := newRecencyFixture(t)
	at := f.now.Add(-time.Minute)
	old := config.FlowFact{Key: "latest/old", ApplicationID: "fixture.latest", Application: "Frozen App", Domain: "latest.test", RouteID: "direct", RouteName: "direct", Direction: "direct", StartedAt: at, RoutingMatchedAt: at.Add(time.Millisecond), Rule: "final", ResponseKnown: true, ResponseMS: 12}
	newer := old
	newer.Key, newer.Rule, newer.RuleSource = "latest/new", "application=fixture.latest", "Pax 包：优先规则"
	newer.StartedAt, newer.RoutingMatchedAt = at.Add(time.Second), at.Add(time.Second+time.Millisecond)
	records := []config.Observation{
		{Session: "latest", Sequence: 1, At: at, Kind: "started", Flow: &old, Upload: 10},
		{Session: "latest", Sequence: 2, At: at.Add(time.Second), Kind: "started", Flow: &newer, Upload: 20},
		{Session: "latest", Sequence: 3, At: at.Add(2 * time.Second), Kind: "sample", Flow: &old, Upload: 30},
	}
	if err := controller.FeedHistoryFixture(t, f.c, context.Background(), config.ObservationBatch{Session: "latest", HighWater: 3, Collecting: true, Records: records}); err != nil {
		t.Fatal(err)
	}
	for _, window := range []string{"minute", "hour", "today", "week"} {
		page, err := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "application", Window: window})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Upload != 60 || page.Connections != 2 {
			t.Fatalf("%s one exit/all totals: %v", window, page)
		}
		row := page.Items[0]
		if row.LatestMatch == nil || row.LatestMatch.Rule != newer.Rule || row.LatestMatch.Identity != newer.Key || !row.LatestMatch.At.AsTime().Equal(newer.RoutingMatchedAt) || row.LatestMatch.Application != "Frozen App" {
			t.Errorf("%s latest decision: %v", window, row.LatestMatch)
		}
		if len(row.Bases) != 2 {
			t.Errorf("%s lost full rule evidence", window)
		}
		detail, err := f.c.QueryTrafficDetail(context.Background(), &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: row.Id})
		if err != nil || len(detail.GetItems()) != 2 || len(detail.GetRecentResponses()) != 2 {
			t.Fatalf("%s detail/response membership: %v %v", window, detail, err)
		}
	}
}

func TestLatestRejectedMatchAndFrozenTokenKeepTheirOwnWindow(t *testing.T) {
	f := newRecencyFixture(t)
	at := f.now.Add(-20 * time.Second)
	fact := config.FlowFact{Key: "reject/first", ApplicationID: "fixture.reject", Application: "Frozen App", StartedAt: at, RoutingMatchedAt: at.Add(time.Millisecond), Direction: "blocked", Rule: "builtin=IPv6 流量", RuleSource: "Pax 包：Geo 基础包", Failure: "rule_rejected", EstablishmentState: "failed"}
	batch := config.ObservationBatch{Session: "reject", HighWater: 1, Records: []config.Observation{{Session: "reject", Sequence: 1, At: at, Kind: "rejected", Flow: &fact}}}
	if err := controller.FeedHistoryFixture(t, f.c, context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	q := &pb.TrafficQuery{Dimension: "application", Window: "today"}
	first, err := f.c.QueryTraffic(context.Background(), q)
	if err != nil || len(first.GetItems()) != 1 {
		t.Fatal(first, err)
	}
	row := first.Items[0]
	if row.Connections != 0 || row.FailedCount != 1 || row.LatestMatch == nil || row.LatestMatch.Identity != fact.Key {
		t.Fatal("rejection is a decision, not a started connection", row)
	}
	fact.Key = "reject/second"
	fact.RoutingMatchedAt = at.Add(time.Second)
	fact.StartedAt = at.Add(time.Second)
	batch.HighWater = 2
	batch.Records = []config.Observation{{Session: "reject", Sequence: 2, At: at.Add(time.Second), Kind: "rejected", Flow: &fact}}
	if err = controller.FeedHistoryFixture(t, f.c, context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	// Replay is ignored by the journal cursor; it cannot add another failed attempt.
	if err = controller.FeedHistoryFixture(t, f.c, context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	frozen, err := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: q.Dimension, Window: q.Window, ReadToken: first.ReadToken})
	if err != nil || frozen.Items[0].FailedCount != 1 || frozen.Items[0].LatestMatch.Identity != "reject/first" {
		t.Fatal("frozen decision changed", frozen, err)
	}
	latest, err := f.c.QueryTraffic(context.Background(), q)
	if err != nil || latest.Items[0].Id != row.Id || latest.Items[0].FailedCount != 2 || latest.Items[0].LatestMatch.Identity != fact.Key {
		t.Fatal("same row/latest decision or dedup lost", latest, err)
	}
}

func TestMinuteLegacyUnknownCannotYieldEarlierKnownDecision(t *testing.T) {
	f := newRecencyFixture(t)
	at := f.now.Add(-20 * time.Second)
	known := config.FlowFact{Key: "legacy-known", ApplicationID: "fixture.unknown", Application: "Frozen App", StartedAt: at, RoutingMatchedAt: at.Add(time.Second), RouteID: "direct", Direction: "direct", Rule: "final"}
	unknown := known
	unknown.Key = "legacy-unknown"
	unknown.StartedAt = at.Add(-time.Second)
	unknown.RoutingMatchedAt = time.Time{}
	batch := config.ObservationBatch{Session: "legacy-minute", HighWater: 2, Records: []config.Observation{{Session: "legacy-minute", Sequence: 1, At: at.Add(time.Second), Kind: "started", Flow: &known}, {Session: "legacy-minute", Sequence: 2, At: at.Add(2 * time.Second), Kind: "started", Flow: &unknown}}}
	if err := controller.FeedHistoryFixture(t, f.c, context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	page, err := f.c.QueryTraffic(context.Background(), &pb.TrafficQuery{Window: "minute", Dimension: "application"})
	if err != nil || len(page.GetItems()) != 1 || page.Items[0].LatestMatch != nil {
		t.Fatal("unknown decision time was replaced by an earlier known match", page, err)
	}
}
