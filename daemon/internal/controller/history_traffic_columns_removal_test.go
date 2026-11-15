package controller

import (
	"context"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Acceptance: a retained legacy summary's read correction depends on its
// retained last-event evidence. Removing only that event clears the derived
// correction, while an opened immutable detail can still use its old version.
func TestColumnTrafficRemovalDropsExpiredCorrection(t *testing.T) {
	c, _ := newTrafficRPCFixture(t)
	ctx := context.Background()
	job, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute)
	eventAt := at.Add(-time.Hour)
	f := config.FlowFact{Key: "expire/rejection", StartedAt: at, ApplicationID: "app", Application: "App", Domain: "example.test", Direction: "unknown", Failure: "rule_rejected"}
	s := config.LogConnectionSummary{Fact: f, LastEventID: "old-rejection", LastLogAt: at, Metrics: config.ConnectionMetrics{AsOf: at}}
	old := f
	old.Direction, old.RouteID, old.RouteName = "blocked", "r", "R"
	e := config.HistoryEvent{ID: s.LastEventID, At: eventAt, Kind: "rejected", Flow: &old}
	if err = job.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		if _, _, err := tx.AppendEvent(ctx, e); err != nil {
			return err
		}
		return tx.PutSummary(ctx, s)
	}); err != nil {
		t.Fatal(err)
	}
	columns := job.logs.(interface{ Columns() *historystore.Columns }).Columns()
	snapshot, err := columns.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	filter := historystore.ColumnTrafficFilter{ConnectionKey: f.Key, Until: time.Now()}
	before, err := snapshot.TrafficConnections(ctx, filter, 0, 1)
	if err != nil || len(before.Items) != 1 || before.Items[0].Fact.Direction != "blocked" {
		t.Fatalf("initial correction missing: %+v %v", before, err)
	}
	if _, err = job.logs.Prune(ctx, eventAt.Add(time.Second), historystore.Limits{Rows: 2000, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	fresh, err := columns.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	after, err := fresh.TrafficConnections(ctx, filter, 0, 1)
	if err != nil || len(after.Items) != 1 || after.Items[0].Fact.Direction != "unknown" {
		t.Fatalf("deleted event still authorizes correction: %+v %v", after, err)
	}
	frozen, err := snapshot.TrafficConnections(ctx, filter, 0, 1)
	if err != nil || len(frozen.Items) != 1 || frozen.Items[0].Fact.Direction != "blocked" {
		t.Fatalf("old detail lost retained evidence: %+v %v", frozen, err)
	}
}
