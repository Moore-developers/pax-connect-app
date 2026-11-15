//go:build integration

package controller

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"pax-connect/daemon/internal/historystore"
)

// Acceptance: a real exit during a group transaction preserves its previous
// committed checkpoint, then the normal backfill resumes to identical rows.
func TestColumnProjectionRealResume(t *testing.T) {
	dir := os.Getenv("PAX_COLUMNS_PROJECTION_RESUME_DEST")
	if dir == "" {
		t.Skip("PAX_COLUMNS_PROJECTION_RESUME_DEST is not set")
	}
	ctx := context.Background()
	c, err := historystore.OpenColumns(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	if err = c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	if cursor, er := c.ProjectionProgress(ctx, "groups/activity"); er != nil || cursor != "" {
		t.Fatalf("resume fixture already has group progress %q: %v", cursor, er)
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker := exec.CommandContext(ctx, executable, "-test.run=^TestColumnProjectionCrashWorker$", "-test.v", "-test.timeout=20m")
	worker.Env = append(os.Environ(), "PAX_COLUMNS_PROJECTION_CRASH_WORKER=1")
	output, err := worker.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 91 {
		t.Fatalf("crash worker err=%v output=%s", err, output)
	}
	t.Logf("COLUMN_PROJECTION_CRASH exit=91 output=%s", output)
	c, err = historystore.OpenColumns(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	keys, err := c.GroupKeys(ctx, "activity", "", 128)
	if err != nil || len(keys) != 128 {
		t.Fatal(len(keys), err)
	}
	if cursor, er := c.ProjectionProgress(ctx, "groups/activity"); er != nil || cursor != keys[63] {
		t.Fatalf("uncommitted checkpoint survived: %q %v", cursor, er)
	}
	if _, ok, er := c.Group(ctx, "activity", keys[63]); er != nil || !ok {
		t.Fatal("committed group missing", ok, er)
	}
	if _, ok, er := c.Group(ctx, "activity", keys[64]); er != nil || ok {
		t.Fatal("uncommitted group survived", ok, er)
	}
	start := time.Now()
	if err = rebuildColumnProjections(ctx, c); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_PROJECTION_RESUME elapsed=%s committed_groups_before_resume=64", time.Since(start))
	checkColumnProjectionOracle(t, c)
	start = time.Now()
	if err = rebuildColumnProjections(ctx, c); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_PROJECTION_REPEAT elapsed=%s", time.Since(start))
}

func TestColumnProjectionCrashWorker(t *testing.T) {
	if os.Getenv("PAX_COLUMNS_PROJECTION_CRASH_WORKER") != "1" {
		t.Skip("child process only")
	}
	ctx := context.Background()
	c, err := historystore.OpenColumns(ctx, os.Getenv("PAX_COLUMNS_PROJECTION_RESUME_DEST"), uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	if err = rebuildColumnProjectionInputs(ctx, c); err != nil {
		t.Fatal(err)
	}
	keys, err := c.GroupKeys(ctx, "activity", "", 128)
	if err != nil || len(keys) != 128 {
		t.Fatal(len(keys), err)
	}
	groups := make([]historystore.ColumnGroup, len(keys))
	for i, key := range keys {
		groups[i], err = projectColumnGroup(ctx, c, "activity", key)
		if err != nil {
			t.Fatal(err)
		}
	}
	write := func(tx *historystore.ColumnsTx, start, end int) error {
		for _, g := range groups[start:end] {
			if er := tx.PutGroup(ctx, g); er != nil {
				return er
			}
		}
		return tx.SetProjectionProgress(ctx, "groups/activity", keys[end-1])
	}
	if err = c.Update(ctx, func(tx *historystore.ColumnsTx) error { return write(tx, 0, 64) }); err != nil {
		t.Fatal(err)
	}
	if err = c.Update(ctx, func(tx *historystore.ColumnsTx) error {
		if er := write(tx, 64, 128); er != nil {
			return er
		}
		t.Log("committed_groups=64 uncommitted_groups=64 uncommitted_checkpoint=true")
		os.Exit(91)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
