package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Installed applications on either side of a classification page boundary
// remain visible in the application view and excluded from the system view.
func TestColumnLogClassificationAcrossIdentityPages(t *testing.T) {
	ctx := context.Background()
	c := openColumnProjectionTest(t)
	at := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	var batch historystore.ColumnImportBatch
	var apps []config.RoutingApplication
	for i := 0; i < 130; i++ {
		id := fmt.Sprintf("test.app.%03d", i)
		flow := config.FlowFact{Key: fmt.Sprintf("flow/%03d", i), ApplicationID: id, Application: id, Process: "/Applications/" + id, Domain: "example.test", StartedAt: at, Direction: "direct"}
		event := config.HistoryEvent{Sequence: uint64(i + 1), ID: fmt.Sprintf("event/%03d", i), At: at, Kind: "started", Level: "info", Source: "sing-box", Flow: &flow}
		batch.Events = append(batch.Events, event)
		batch.Connections = append(batch.Connections, config.LogConnectionSummary{Fact: flow, LastEventID: event.ID, LastLogAt: at, LastSequence: event.Sequence})
		if i == 1 || i == 129 {
			apps = append(apps, config.RoutingApplication{ID: id, Name: id, Path: flow.Process, Bundle: true})
		}
	}
	if err := c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		for _, summary := range batch.Connections {
			if err := tx.PutConnection(ctx, summary); err != nil {
				return err
			}
		}
		for _, event := range batch.Events {
			if _, err := tx.PutEvent(ctx, event); err != nil {
				return err
			}
		}
		return projectColumnBatch(ctx, tx, batch)
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	w := historystore.ColumnLogWindow{View: "activity", Collection: "collecting", From: at.Add(-time.Minute), Until: at.Add(time.Minute), Cutoff: at.Add(-time.Hour), Apps: apps}
	w.Classify = func(id, path string) (config.RoutingApplication, bool) { return userApplicationMatch(id, path, apps) }
	err = snapshot.WithRead(ctx, func(q historystore.ColumnQuery) error {
		selection, e := historystore.PrepareColumnLogs(ctx, q, w, func(ctx context.Context, s *historystore.ColumnLogSelection, key string) (historystore.ColumnGroup, error) {
			return projectColumnGroupStream(ctx, columnSelectionStream{s}, "activity", key, w.Until, w.Collection)
		})
		if e != nil {
			return e
		}
		for _, check := range []struct {
			filter historystore.ColumnLogFilter
			want   uint64
		}{{historystore.ColumnLogFilter{Scope: "access", Dimension: "application", IncludeDNS: true}, 2}, {historystore.ColumnLogFilter{Scope: "system", Dimension: "system", IncludeDNS: true}, 128}} {
			got, e := selection.Count(ctx, check.filter)
			if e != nil || got != check.want {
				return fmt.Errorf("scope %s: got %d want %d: %v", check.filter.Scope, got, check.want, e)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
