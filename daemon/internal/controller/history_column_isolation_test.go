package controller

import (
	"context"
	"errors"
	"testing"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Acceptance: the owner replays a rolled-back batch into fact storage after
// explicitly stopping a broken projection. Its cursor and cleanup continue;
// queries report failure rather than reading an incomplete derived directory.
func TestColumnProjectionFailureKeepsOwnerMoving(t *testing.T) {
	h := newLogsHarness(t)
	ctx := context.Background()
	calls := 0
	h.logs.SetColumnProjector(func(context.Context, *historystore.ColumnsTx, historystore.ColumnImportBatch) error {
		calls++
		return errors.New("injected")
	})
	f := config.FlowFact{Key: "isolation/1", StartedAt: h.clock, Domain: "example.test"}
	h.appendRows([]config.Observation{{Session: "isolation", Sequence: 1, At: h.clock, Kind: "started", Flow: &f}})
	if delay := h.l.step(ctx); delay != 0 {
		t.Fatalf("projection failure stopped replay: %v", delay)
	}
	if h.l.down {
		t.Fatal("query projection degraded the fact owner")
	}
	h.settle()
	cursor, ok, err := h.logs.Cursor(ctx, logsCursorName)
	if err != nil || !ok || cursor.Seq != 1 {
		t.Fatalf("cursor did not advance %+v %v %v", cursor, ok, err)
	}
	row, found, err := h.logs.Columns().Connection(ctx, f.Key)
	if err != nil || !found || row.Fact.Key != f.Key {
		t.Fatalf("facts did not advance %+v %v", row, err)
	}
	if calls != 1 {
		t.Fatalf("failing projection was repeatedly invoked: %d", calls)
	}
	if _, err = h.l.columnStore(ctx); err == nil || err.Error() != "history_store_failed" {
		t.Fatalf("incomplete projection queryable: %v", err)
	}
}
