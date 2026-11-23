package controller

import (
	"context"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Acceptance: an unavailable log read side cannot erase traffic.db totals or
// silently report zero response samples as ready, including hour/day consumers.
func TestColumnTrafficBuildingIsolatesLogEvidence(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "building/one", StartedAt: at, ApplicationID: "building.app", Application: "Building", Domain: "example.test", RouteID: "direct", Direction: "direct", ResponseKnown: true, ResponseMS: 0}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "building", Records: []config.Observation{{Session: "building", Sequence: 1, Kind: "started", At: at, Flow: &f, Upload: 12, Download: 34}}}); err != nil {
		t.Fatal(err)
	}
	job, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"building", "failed"} {
		if err = job.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.SetMeta(ctx, "column_upgrade_state", state) }); err != nil {
			t.Fatal(err)
		}
		for _, window := range []string{"hour", "day", "today", "week", "month"} {
			p, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Window: window, Dimension: "application"})
			if err != nil {
				t.Fatal(state, window, err)
			}
			if p.Upload != 12 || p.Download != 34 || p.Connections != 1 || len(p.Items) != 1 {
				t.Fatalf("%s/%s totals erased: %v", state, window, p)
			}
			wantCode := "history_store_failed"
			if state == "building" {
				wantCode = "history_converting"
			}
			if p.Items[0].RecentResponsesState != "failed" || p.Items[0].RecentResponsesError != wantCode || p.History.ErrorCode != wantCode {
				t.Fatalf("%s/%s evidence hid failure: %v", state, window, p)
			}
		}
	}
}

// Old capacity eviction alone is no longer a warning about retained response samples.
func TestTrafficResponseCoverageIgnoresPastCapacityEviction(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	f := config.FlowFact{Key: "coverage/one", StartedAt: at, ApplicationID: "coverage.app", Application: "Coverage", Domain: "example.test", RouteID: "direct", Direction: "direct", ResponseKnown: true, ResponseMS: 23}
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "coverage", Records: []config.Observation{{Session: "coverage", Sequence: 1, Kind: "started", At: at, Flow: &f, Upload: 12, Download: 34}}}); err != nil {
		t.Fatal(err)
	}
	job, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	if err := job.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		return tx.SetMeta(ctx, historystore.MetaCapacityEvicted, "1")
	}); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		for _, window := range []string{"hour", "week"} {
			page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Window: window, Dimension: "application"})
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Items) != 1 {
				t.Fatalf("%s: got %d rows, want 1", window, len(page.Items))
			}
			row := page.Items[0]
			if row.RecentResponsesState != want || len(row.RecentResponses) != 1 || row.RecentResponses[0].Milliseconds != 23 {
				t.Errorf("%s: response state=%q samples=%v, want %q and one 23ms response", window, row.RecentResponsesState, row.RecentResponses, want)
			}
		}
	}
	check("ready")
	if err := job.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		return tx.SetMeta(ctx, historystore.MetaPartial, "1")
	}); err != nil {
		t.Fatal(err)
	}
	check("partial")
}
