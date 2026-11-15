//go:build integration

package controller

import (
	"context"
	"os"
	"testing"
	"time"

	"pax-connect/daemon/internal/historystore"
)

// Normalize the legacy ~512MiB diagnostic fixture, not current 2GiB acceptance.
// Use the real retention path. No rows
// or indexes are edited directly; every deletion updates its production groups.
func TestColumnNormalizeCapacityFixture(t *testing.T) {
	dir := os.Getenv("PAX_COLUMN_NORMALIZE_DEST")
	if dir == "" {
		t.Skip("explicit private fixture required")
	}
	ctx := context.Background()
	store, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	store.ConfigureColumnProjector(projectColumnBatch)
	before, err := store.LiveBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before <= 505<<20 || before > 550<<20 {
		t.Fatalf("fixture is not an expected overshoot: %d", before)
	}
	removed, err := store.EvictOldest(ctx, 493<<20, historystore.Limits{Rows: 2000, Duration: 2 * time.Second})
	if err != nil || !removed {
		t.Fatalf("normal retention failed: removed=%v err=%v", removed, err)
	}
	if _, err = store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := store.LiveBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var events, connections, groups int
	if err = store.Reader().QueryRowContext(ctx, "SELECT (SELECT count(*) FROM col_events),(SELECT count(*) FROM col_connections),(SELECT count(*) FROM col_groups)").Scan(&events, &connections, &groups); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_NEAR_CAP_NORMALIZED before_bytes=%d final_bytes=%d final_mib=%.3f events=%d connections=%d groups=%d retention=production oldest_first=true", before, after, float64(after)/(1<<20), events, connections, groups)
	if after < 480<<20 || after > 505<<20 {
		t.Fatalf("normal retention did not leave a near-cap fixture: %.3fMiB", float64(after)/(1<<20))
	}
}
