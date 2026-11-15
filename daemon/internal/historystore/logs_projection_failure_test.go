package historystore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// Acceptance: the first failed derived transaction rolls back fully. Once the
// owner marks that side failed, replay and retention continue on facts while
// every query remains explicitly unavailable, including after process restart.
func TestColumnProjectionFailureIsolation(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	l := mustLogs(t, dir)
	calls := 0
	injected := errors.New("injected derived failure")
	l.SetColumnProjector(func(context.Context, *ColumnsTx, ColumnImportBatch) error { calls++; return injected })
	write := func(tx *LogsTx) error {
		if _, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: "fact", At: time.UnixMilli(1)}); err != nil {
			return err
		}
		return tx.SetCursor(ctx, "logs", Cursor{Session: "s", Seq: 1})
	}
	if err := l.Update(ctx, write); !errors.Is(err, ErrColumnProjection) || !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 0 {
		t.Fatal("failed derived transaction committed fact", n)
	}
	if _, ok, err := l.Cursor(ctx, "logs"); err != nil || ok {
		t.Fatal("failed derived transaction advanced cursor", ok, err)
	}
	if err := l.DisableColumnProjection(ctx, "history_store_failed"); err != nil {
		t.Fatal(err)
	}
	if err := l.Update(ctx, write); err != nil {
		t.Fatal("facts-only replay failed", err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 1 {
		t.Fatal(n)
	}
	if cursor, ok, err := l.Cursor(ctx, "logs"); err != nil || !ok || cursor.Seq != 1 {
		t.Fatal(cursor, ok, err)
	}
	if calls != 1 {
		t.Fatal("disabled projection retried", calls)
	}
	if err := l.Ready(ctx); err == nil || err.Error() != "history_store_failed" {
		t.Fatal(err)
	}
	meta, err := l.SnapshotMeta(ctx)
	if err != nil || !meta.Partial || meta.ErrorCode != "history_store_failed" {
		t.Fatal(meta, err)
	}
	if p, err := l.Prune(ctx, time.UnixMilli(2), Limits{Rows: 128, Duration: time.Second}); err != nil || p.Deleted != 1 {
		t.Fatal(p, err)
	}
	if calls != 1 {
		t.Fatal("retention retried failed projection", calls)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := OpenLogs(ctx, filepath.Dir(filepath.Join(dir, logsFile)), uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	reopened.SetColumnProjector(func(context.Context, *ColumnsTx, ColumnImportBatch) error { calls++; return injected })
	if err = reopened.Update(ctx, func(tx *LogsTx) error {
		_, _, e := tx.AppendEvent(ctx, config.HistoryEvent{ID: "after-restart", At: time.UnixMilli(3)})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("restart retried failed projection", calls)
	}
	if err = reopened.Ready(ctx); err == nil {
		t.Fatal("restart hid query failure")
	}
}

func TestColumnProjectionFailurePreventsUpgradeCutover(t *testing.T) {
	l, _ := openUpgradeFixture(t)
	ctx := context.Background()
	if _, err := l.PrepareUpgrade(ctx, uid()); err != nil {
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
	if err := l.DisableColumnProjection(ctx, "history_store_failed"); err != nil {
		t.Fatal(err)
	}
	if err := l.UpgradeFinalize(ctx); !errors.Is(err, ErrColumnProjection) {
		t.Fatal(err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN('connections','events')"); n != 2 {
		t.Fatal("failed projections deleted old facts", n)
	}
}
