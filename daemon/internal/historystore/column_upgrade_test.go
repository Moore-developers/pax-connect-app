package historystore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func openUpgradeFixture(t *testing.T) (*Logs, string) {
	t.Helper()
	path := columnImportFixture(t)
	db := rawDB(t, path)
	var oldBody []byte
	if err := db.QueryRow("SELECT body FROM connections WHERE flow_key='flow-1'").Scan(&oldBody); err != nil {
		t.Fatal(err)
	}
	var old config.LogConnectionSummary
	if err := json.Unmarshal(oldBody, &old); err != nil {
		t.Fatal(err)
	}
	old.Metrics.Upload, old.Metrics.Download = 321, 765
	oldBody, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE connections SET body=? WHERE flow_key='flow-1'", oldBody); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE sqlite_sequence SET seq=100 WHERE name='events'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	logs, _, err := OpenLogs(context.Background(), filepath.Dir(path), uid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logs.Close() })
	return logs, path
}

func completeUpgrade(t *testing.T, l *Logs) {
	t.Helper()
	ctx := context.Background()
	needed, err := l.PrepareUpgrade(ctx, uid())
	if err != nil || !needed {
		t.Fatal(needed, err)
	}
	for {
		done, err := l.UpgradeBatch(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if err = l.UpgradeFinalize(ctx); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: importing old summaries cannot overwrite observations written
// after upgrade starts, and deleted legacy events do not rewind the sequence.
func TestColumnUpgradeKeepsNewWritesAndHighWater(t *testing.T) {
	l, path := openUpgradeFixture(t)
	ctx := context.Background()
	if err := l.Ready(ctx); err == nil || err.Error() != "history_converting" {
		t.Fatal(err)
	}
	summary := config.LogConnectionSummary{Fact: config.FlowFact{Key: "flow-1", Application: "new", StartedAt: baseHour}, LastEventID: "new", LastLogAt: baseHour.Add(time.Minute), LastSequence: 101}
	var err error
	if err = l.Update(ctx, func(tx *LogsTx) error {
		if inserted, seq, e := tx.AppendEvent(ctx, config.HistoryEvent{ID: "e-001"}); e != nil || inserted || seq != 0 {
			t.Fatal("legacy replay consumed sequence", inserted, seq, e)
		}
		next, e := tx.NextEventSequence(ctx)
		if e != nil || next != 101 {
			t.Fatalf("head rewound: %d %v", next, e)
		}
		if _, seq, e := tx.AppendEvent(ctx, config.HistoryEvent{ID: "new", At: time.UnixMilli(baseHour.UnixMilli())}); e != nil || seq != 101 {
			t.Fatal(seq, e)
		}
		return tx.PutSummary(ctx, summary)
	}); err != nil {
		t.Fatal(err)
	}
	completeUpgrade(t, l)
	if err = l.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	got, ok, err := l.columns.Connection(ctx, "flow-1")
	if err != nil || !ok || got.Fact.Application != "new" || got.LastSequence != 101 {
		t.Fatal(got, ok, err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 38 {
		t.Fatal(n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN('events','connections')"); n != 0 {
		t.Fatal("legacy tables remain", n)
	}
	backup := filepath.Join(filepath.Dir(path), columnUpgradeBackup)
	if info, err := os.Stat(backup); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if err = l.Update(ctx, func(tx *LogsTx) error {
		if err := tx.ClearLogTables(ctx); err != nil {
			return err
		}
		seq, e := tx.NextEventSequence(ctx)
		if seq != 102 || e != nil {
			t.Fatal(seq, e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: a process may reopen between committed import batches. Its
// protected backup is reused, counters and sequence remain durable.
func TestColumnUpgradeReopensBetweenBatches(t *testing.T) {
	l, path := openUpgradeFixture(t)
	ctx := context.Background()
	if _, err := l.PrepareUpgrade(ctx, uid()); err != nil {
		t.Fatal(err)
	}
	if done, err := l.UpgradeBatch(ctx, nil); err != nil || done {
		t.Fatal(done, err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := OpenLogs(ctx, filepath.Dir(path), uid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	completeUpgrade(t, reopened)
	meta, err := reopened.SnapshotMeta(ctx)
	if err != nil || meta.HeadSequence != 100 {
		t.Fatal(meta, err)
	}
	if n := count(t, reopened.Reader(), "SELECT count(*) FROM col_connections"); n != 3 {
		t.Fatal(n)
	}
}

// Acceptance: a failed migration keeps original tables and backup, stops
// automatic import retry, and does not prevent new-column collection writes.
func TestColumnUpgradeFailureKeepsEvidenceAndWriter(t *testing.T) {
	l, path := openUpgradeFixture(t)
	ctx := context.Background()
	if _, err := l.PrepareUpgrade(ctx, uid()); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("projection rejected")
	if _, err := l.UpgradeBatch(ctx, func(context.Context, *ColumnsTx, ColumnImportBatch) error { return injected }); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if state, err := l.UpgradeState(ctx); err != nil || state != "failed" {
		t.Fatal(state, err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM connections"); n != 3 {
		t.Fatal(n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), columnUpgradeBackup)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.PrepareUpgrade(ctx, uid()); err == nil {
		t.Fatal("failed migration restarted")
	}
	if err := l.Update(ctx, func(tx *LogsTx) error {
		_, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "still-writing", At: time.UnixMilli(baseHour.UnixMilli())})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// Acceptance: a connection not reached by the import cursor is seeded from
// the old summary before the production fold applies a new observation.
func TestColumnUpgradeWriterSeedsUnimportedSummary(t *testing.T) {
	l, _ := openUpgradeFixture(t)
	ctx := context.Background()
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_connections"); n != 0 {
		t.Fatal(n)
	}
	if err := l.Update(ctx, func(tx *LogsTx) error {
		previous, ok, err := tx.Summary(ctx, "flow-1")
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		if previous.Fact.Domain != "example.test" || previous.LastEventID != "e-001" || previous.Metrics.Upload != 321 || previous.Metrics.Download != 765 {
			t.Fatalf("old identity was lost: %+v", previous)
		}
		previous.Metrics.Upload = 987
		previous.LastSequence = 101
		return tx.PutSummary(ctx, previous)
	}); err != nil {
		t.Fatal(err)
	}
	completeUpgrade(t, l)
	got, ok, err := l.columns.Connection(ctx, "flow-1")
	if err != nil || !ok || got.Fact.Domain != "example.test" || got.LastEventID != "e-001" || got.Metrics.Upload != 987 || got.Metrics.Download != 765 {
		t.Fatal(got, ok, err)
	}
}

func TestColumnUpgradeClearCannotResurrectOldFacts(t *testing.T) {
	l, _ := openUpgradeFixture(t)
	ctx := context.Background()
	if _, err := l.PrepareUpgrade(ctx, uid()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.UpgradeBatch(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := l.Update(ctx, func(tx *LogsTx) error { return tx.ClearLogTables(ctx) }); err != nil {
		t.Fatal(err)
	}
	if done, err := l.UpgradeBatch(ctx, nil); err != nil || !done {
		t.Fatal(done, err)
	}
	if err := l.UpgradeFinalize(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 0 {
		t.Fatal(n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_connections"); n != 0 {
		t.Fatal(n)
	}
	if err := l.Ready(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestColumnUpgradeRetentionDoesNotRestoreRemovedPrefix(t *testing.T) {
	l, _ := openUpgradeFixture(t)
	ctx := context.Background()
	if _, err := l.PrepareUpgrade(ctx, uid()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.UpgradeBatch(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// Connections have been imported, events have not. Retention removes the
	// prefix and advances its durable fence before the event phase catches up.
	if _, err := l.Prune(ctx, baseHour.Add(time.Second), Limits{Rows: 128, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	for {
		done, err := l.UpgradeBatch(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
	}
	if err := l.UpgradeFinalize(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 0 {
		t.Fatal(n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_connections"); n != 0 {
		t.Fatal(n)
	}
}

func TestColumnUpgradeBackupFailureDoesNotStopCollector(t *testing.T) {
	source := columnImportFixture(t)
	other := columnImportFixture(t)
	db := rawDB(t, other)
	if _, err := db.Exec("UPDATE meta SET value='different' WHERE key='journal_generation'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(source), columnUpgradeBackup), bytes, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l, _, err := OpenLogs(ctx, filepath.Dir(source), uid())
	if err != nil {
		t.Fatal("query-side upgrade failure stopped opening collector", err)
	}
	defer func() { _ = l.Close() }()
	if err = l.Ready(ctx); err == nil || err.Error() != "history_store_failed" {
		t.Fatal(err)
	}
	if err = l.Update(ctx, func(tx *LogsTx) error {
		old, ok, e := tx.Summary(ctx, "flow-1")
		if e != nil || !ok {
			t.Fatal(ok, e)
		}
		if e = tx.PutSummary(ctx, old); e != nil {
			return e
		}
		_, _, e = tx.AppendEvent(ctx, config.HistoryEvent{ID: "collector-kept-running", At: time.UnixMilli(baseHour.UnixMilli())})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM events"); n != 37 {
		t.Fatal("old facts changed", n)
	}
}
