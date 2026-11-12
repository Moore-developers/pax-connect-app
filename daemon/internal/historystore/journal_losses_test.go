package historystore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalLossIntentReplayBeforeDestructionAndReplacementIsSafe(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	j := mustJournal(t, dir)
	if err := j.Append(ctx, baseHour, []Observation{obs("s", 1)}, Cursor{"s", 1}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	// Persist the real intent, then restore the original file to simulate a crash
	// before destruction. Replay must complete it and leave a durable loss.
	j.mu.Lock()
	d, err := j.loseLocked(ctx, baseHour, "corrupt")
	j.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(d.Quarantined, j.path(baseHour)); err != nil {
		t.Fatal(err)
	}
	restarted := mustJournal(t, dir)
	losses, err := restarted.PendingLosses(ctx)
	if err != nil || len(losses) != 1 {
		t.Fatal(losses, err)
	}
	if _, err = os.Stat(j.path(baseHour)); !os.IsNotExist(err) {
		t.Fatal("intent not resumed", err)
	}
	// A new healthy file at this hour is never quarantined by the old inode intent.
	if err = restarted.Append(ctx, baseHour, []Observation{obs("replacement", 1)}, Cursor{"replacement", 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.PendingLosses(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(j.path(baseHour)); err != nil {
		t.Fatal("replacement removed", err)
	}
}

func TestJournalLossAppendBeforeAckCrossHourReplaysOnce(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	j := mustJournal(t, dir)
	if err := j.Append(ctx, baseHour, []Observation{obs("s", 1)}, Cursor{"s", 1}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	writeGarbage(t, j.path(baseHour))
	if _, _, err := j.Cursor(ctx); err != nil {
		t.Fatal(err)
	}
	losses, err := j.PendingLosses(ctx)
	if err != nil || len(losses) != 1 {
		t.Fatal(losses, err)
	}
	loss := losses[0]
	row := Observation{Session: "loss:" + loss.ID, Seq: 1, AtMs: baseHour.Add(time.Hour).UnixMilli(), Kind: "gap"}
	// This append commits, but the process exits before acknowledging the intent.
	if err = j.Append(ctx, baseHour.Add(time.Hour), []Observation{row, obs("s", 2)}, Cursor{"s", 2}); err != nil {
		t.Fatal(err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := mustJournal(t, dir)
	if err = restarted.AppendLoss(ctx, baseHour.Add(2*time.Hour), loss.ID, row, Cursor{"s", 2}); err != nil {
		t.Fatal(err)
	}
	rows, err := restarted.ReadAfter(ctx, Cursor{Session: row.Session, Seq: 1}, 10)
	if err != nil || len(rows) != 1 || rows[0].Session != "s" || rows[0].Seq != 2 {
		t.Fatalf("duplicate recovery cursor skipped middle record: %v %v", rows, err)
	}
	losses, err = restarted.PendingLosses(ctx)
	if err != nil || len(losses) != 0 {
		t.Fatal("intent not acknowledged", losses, err)
	}
}

func TestJournalCapacityEvictionPersistsLossAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	j := mustJournal(t, dir)
	for i := 0; i < 3; i++ {
		if err := j.Append(ctx, baseHour.Add(time.Duration(i)*time.Hour), []Observation{obs("s", uint64(i+1))}, Cursor{"s", uint64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	evicted, err := j.EvictOver(1<<30, 1)
	if err != nil || len(evicted) != 2 {
		t.Fatal(evicted, err)
	}
	_ = j.Close()
	restarted := mustJournal(t, dir)
	losses, err := restarted.PendingLosses(ctx)
	if err != nil || len(losses) != 2 {
		t.Fatal(losses, err)
	}
	for _, loss := range losses {
		if loss.Reason != "evicted" {
			t.Fatal(loss)
		}
	}
}

func TestJournalLossManifestRejectsUnsafeOrInvalidState(t *testing.T) {
	dir := historyDir(t)
	j := mustJournal(t, dir)
	path := filepath.Join(dir, "journal", "losses.json")
	if err := os.WriteFile(path, []byte(`{"Version":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJournal(dir, uid()); err == nil {
		t.Fatal("invalid recovery state ignored")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte(`{"Version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := j.PendingLosses(context.Background()); err == nil {
		t.Fatal("symlink recovery state followed")
	}
}

func TestConsumedCapacityEvictionDoesNotInventLoss(t *testing.T) {
	ctx := context.Background()
	j := mustJournal(t, historyDir(t))
	for i := 0; i < 3; i++ {
		if err := j.Append(ctx, baseHour.Add(time.Duration(i)*time.Hour), []Observation{obs("s", uint64(i+1))}, Cursor{"s", uint64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := j.EvictOverConsumed(ctx, 1<<30, 1, []time.Time{baseHour, baseHour.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if losses, err := j.PendingLosses(ctx); err != nil || len(losses) != 0 {
		t.Fatal("consumed eviction invented gap", losses, err)
	}
}
