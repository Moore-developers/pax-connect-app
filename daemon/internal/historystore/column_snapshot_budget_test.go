package historystore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func writeSnapshotRawEvents(t *testing.T, l *Logs, rows, bytes int) {
	t.Helper()
	ctx := context.Background()
	if err := l.Update(ctx, func(tx *LogsTx) error {
		for i := 0; i < rows; i++ {
			id := fmt.Sprintf("raw-%03d", i)
			e := config.HistoryEvent{ID: id, At: baseHour, Raw: []string{strings.Repeat("x", bytes)}}
			if _, _, err := tx.AppendEvent(ctx, e); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: deleting a retained database larger than the version budget
// never captures the whole database first. Overflow stops row capture in the
// same transaction and explicitly expires the affected token after commit.
func TestColumnSnapshotClearBoundsCaptureBeforeCommit(t *testing.T) {
	ctx := context.Background()
	l := mustLogs(t, historyDir(t))
	writeSnapshotRawEvents(t, l, 64, 64<<10)
	c := l.Columns()
	old, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.snapshots.budget = 512 << 10
	if err = l.Update(ctx, func(tx *LogsTx) error {
		if e := tx.ClearLogTables(ctx); e != nil {
			return e
		}
		var charge, captured int64
		var active, overflow int
		if e := tx.tx.QueryRowContext(ctx, "SELECT charge,active,overflow FROM pax_versions.v_control").Scan(&charge, &active, &overflow); e != nil {
			return e
		}
		if e := tx.tx.QueryRowContext(ctx, "SELECT coalesce(sum(length(CAST(value AS BLOB))),0) FROM pax_versions.v_col_raw").Scan(&captured); e != nil {
			return e
		}
		if charge > c.snapshots.budget || captured > c.snapshots.budget || active != 0 || overflow != 1 {
			t.Fatalf("unbounded precommit capture charge=%d payload=%d active=%d overflow=%d", charge, captured, active, overflow)
		}
		t.Logf("SNAPSHOT_PRECOMMIT_CLEAR source_payload=%d captured_payload=%d charged=%d budget=%d", 64*64<<10, captured, charge, c.snapshots.budget)
		return nil
	}); err != nil {
		t.Fatal("version overflow prevented clearing facts", err)
	}
	if old.Valid() {
		t.Fatal("overflowed token stayed valid")
	}
	if _, _, err = old.Event(ctx, "raw-000"); !errors.Is(err, errColumnCursor) {
		t.Fatal(err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 0 {
		t.Fatal("clear did not commit", n)
	}
	if err = l.Ready(ctx); err != nil {
		t.Fatal("ordinary budget eviction marked query failed", err)
	}
	latest, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = latest.Close()
}

// Acceptance: one old payload larger than the entire budget is rejected before
// it is inserted into the cache; the product fact mutation still commits.
func TestColumnSnapshotSingleLargeRowSkipsCapture(t *testing.T) {
	ctx := context.Background()
	l := mustLogs(t, historyDir(t))
	writeSnapshotRawEvents(t, l, 1, 2<<20)
	c := l.Columns()
	old, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.snapshots.budget = 512 << 10
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		if _, e := tx.tx.ExecContext(ctx, "DELETE FROM col_raw"); e != nil {
			return e
		}
		var n int
		if e := tx.tx.QueryRowContext(ctx, "SELECT count(*) FROM pax_versions.v_col_raw").Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			t.Fatal("oversized row was inserted before eviction")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if old.Valid() || count(t, l.Reader(), "SELECT count(*) FROM col_raw") != 0 {
		t.Fatal("large-row overflow did not expire token and commit deletion")
	}
}

// Acceptance: a cache handle lost after the fact callback cannot report a
// committed transaction as failed. Its cursor advances, snapshots are revoked,
// and subsequent facts bypass the broken cache and projector.
func TestColumnSnapshotPostCommitFailureKeepsFactsCommitted(t *testing.T) {
	ctx := context.Background()
	l := mustLogs(t, historyDir(t))
	c := l.Columns()
	old, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	l.SetColumnProjector(func(context.Context, *ColumnsTx, ColumnImportBatch) error { calls++; return nil })
	if err = l.Update(ctx, func(tx *LogsTx) error {
		if _, _, e := tx.AppendEvent(ctx, config.HistoryEvent{ID: "committed", At: time.UnixMilli(baseHour.UnixMilli())}); e != nil {
			return e
		}
		if e := tx.SetCursor(ctx, "logs", Cursor{Session: "s", Seq: 10}); e != nil {
			return e
		}
		// The writer's attached connection keeps the in-memory transaction
		// valid, while postcommit housekeeping loses its separate handle.
		return c.snapshots.cache.Close()
	}); err != nil {
		t.Fatal("committed facts were reported as rolled back", err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 1 {
		t.Fatal(n)
	}
	if cursor, ok, e := l.Cursor(ctx, "logs"); e != nil || !ok || cursor.Seq != 10 {
		t.Fatal(cursor, ok, e)
	}
	if old.Valid() || !c.snapshots.failed.Load() {
		t.Fatal("broken cache did not revoke snapshots")
	}
	if err = l.Ready(ctx); err == nil || err.Error() != "history_store_failed" {
		t.Fatal("cache failure hidden from product queries", err)
	}
	if _, err = c.Snapshot(ctx); err == nil || err.Error() != "history_store_failed" {
		t.Fatal("created snapshot over a failed cache", err)
	}
	if err = l.Update(ctx, func(tx *LogsTx) error {
		_, _, e := tx.AppendEvent(ctx, config.HistoryEvent{ID: "next-fact", At: time.UnixMilli(baseHour.UnixMilli())})
		return e
	}); err != nil {
		t.Fatal("broken cache stopped collection", err)
	}
	if calls != 1 {
		t.Fatal("broken query projection kept running", calls)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 2 {
		t.Fatal(n)
	}
}
