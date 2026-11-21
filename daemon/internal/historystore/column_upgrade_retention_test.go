package historystore

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// A failed import keeps frozen old evidence while time retention continues
// to remove only expired new-column facts. Collection remains usable.
func TestColumnUpgradeFailureRetentionKeepsOldEvidence(t *testing.T) {
	l, _ := openUpgradeFixture(t)
	ctx := context.Background()
	if _, err := l.PrepareUpgrade(ctx, uid()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.UpgradeBatch(ctx, func(context.Context, *ColumnsTx, ColumnImportBatch) error {
		return errors.New("injected import failure")
	}); err == nil {
		t.Fatal("import did not fail")
	}
	oldEvents := count(t, l.Reader(), "SELECT count(*) FROM events")
	oldConnections := count(t, l.Reader(), "SELECT count(*) FROM connections")
	if err := l.Update(ctx, func(tx *LogsTx) error {
		for _, event := range []config.HistoryEvent{
			{ID: "expired", At: baseHour},
			{ID: "retained", At: baseHour.Add(2 * time.Hour)},
		} {
			if _, _, err := tx.AppendEvent(ctx, event); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if pruned, err := l.Prune(ctx, baseHour.Add(time.Hour), Limits{Rows: 128, Duration: time.Second}); err != nil || pruned.More || pruned.Deleted != 1 {
		t.Fatal("failed upgrade blocked retention", pruned, err)
	}
	if count(t, l.Reader(), "SELECT count(*) FROM events") != oldEvents || count(t, l.Reader(), "SELECT count(*) FROM connections") != oldConnections {
		t.Fatal("retention changed frozen upgrade evidence")
	}
	if _, found, err := l.Columns().Event(ctx, "retained"); err != nil || !found {
		t.Fatal("retention removed current evidence", found, err)
	}
	if state, _ := l.UpgradeState(ctx); state != "failed" {
		t.Fatal("retention hid conversion failure", state)
	}
	if err := l.Update(ctx, func(tx *LogsTx) error {
		_, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "after-retention", At: baseHour.Add(2 * time.Hour)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// A sparse tail crosses the former 2GiB file-size budget without allocating
// that amount of fixture data. SQLite's committed facts remain unchanged.
func TestColumnUpgradeContinuesBeyondFormerFileSizeBudget(t *testing.T) {
	l, path := openUpgradeFixture(t)
	if err := os.Truncate(path, (2<<30)+1); err != nil {
		t.Fatal(err)
	}
	completeUpgrade(t, l)
	ctx := context.Background()
	if err := l.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 37 {
		t.Fatal("oversized-file upgrade changed events", n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_connections"); n != 3 {
		t.Fatal("oversized-file upgrade changed summaries", n)
	}
}
