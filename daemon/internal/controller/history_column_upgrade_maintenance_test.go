package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Conversion failure must not bypass the owner's ordinary retention/vacuum
// work. This is separate from the storage-level frozen-evidence check.
func TestColumnUpgradeFailedOwnerMaintainsRetention(t *testing.T) {
	h := newLogsHarness(t)
	ctx := context.Background()
	if err := h.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		if _, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "expired-after-failure", At: h.clock.Add(-25 * time.Hour)}); err != nil {
			return err
		}
		if _, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "new-after-failure", At: h.clock, Raw: []string{strings.Repeat("x", 32768)}}); err != nil {
			return err
		}
		return tx.SetMeta(ctx, "column_upgrade_state", "failed")
	}); err != nil {
		t.Fatal(err)
	}
	if delay := h.l.maintain(ctx); delay != 0 || h.l.down {
		t.Fatalf("maintenance failed: %v down=%v", delay, h.l.down)
	}
	var events int
	if err := h.logs.Reader().QueryRowContext(ctx, "SELECT count(*) FROM col_events").Scan(&events); err != nil || events != 1 {
		t.Fatalf("failed conversion changed retained data: %d %v", events, err)
	}
	if _, found, err := h.logs.Columns().Event(ctx, "new-after-failure"); err != nil || !found {
		t.Fatalf("recent event lost: found=%v err=%v", found, err)
	}
	if h.l.lastVacuum.IsZero() {
		t.Fatal("failed conversion bypassed idle vacuum/checkpoint")
	}
}

// The production owner, without an extra test-side vacuum, finishes reclaim
// for an already upgraded database. A restarted job resumes the saved phase.
func TestColumnUpgradeOwnerReclaimsReadyDatabase(t *testing.T) {
	h := newLogsHarness(t)
	ctx := context.Background()
	if err := h.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		if _, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "temporary-evidence", At: h.clock.Add(-48 * time.Hour), Raw: []string{strings.Repeat("x", 1<<20)}}); err != nil {
			return err
		}
		_, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "kept-evidence", At: h.clock.Add(time.Minute), Raw: []string{"second", "first"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.logs.Prune(ctx, h.clock, historystore.Limits{Rows: 128, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err := h.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		if err := tx.SetMeta(ctx, "column_upgrade_state", "ready"); err != nil {
			return err
		}
		return tx.SetMeta(ctx, "column_upgrade_phase", "complete")
	}); err != nil {
		t.Fatal(err)
	}
	var free int
	if err := h.logs.Reader().QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil || free <= logsVacuumPages {
		t.Fatalf("fixture has no reclaim backlog: %d %v", free, err)
	}
	h.l.columnUpgrade = &columnUpgradeJob{store: h.logs, owner: 0}
	for pass := 0; pass < 1000 && !h.l.columnUpgrade.done; pass++ {
		h.l.advanceColumnUpgrade(ctx)
		if h.l.columnUpgrade.failed {
			t.Fatal("owner reclaim failed")
		}
		if pass == 0 {
			// Construct a fresh job to exercise persisted-phase resumption.
			h.l.columnUpgrade = &columnUpgradeJob{store: h.logs, owner: 0}
		}
	}
	if !h.l.columnUpgrade.done {
		t.Fatal("owner did not finish reclaim")
	}
	if err := h.logs.Reader().QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil || free != 0 {
		t.Fatalf("owner left free pages: %d %v", free, err)
	}
	if phase, _, err := h.logs.Meta(ctx, "column_upgrade_phase"); err != nil || phase != "reclaimed" {
		t.Fatal("reclaim phase not committed", phase, err)
	}
	event, found, err := h.logs.Columns().Event(ctx, "kept-evidence")
	if err != nil || !found || len(event.Raw) != 2 || event.Raw[0] != "second" || event.Raw[1] != "first" {
		t.Fatal("reclaim changed live evidence", event, found, err)
	}
}
