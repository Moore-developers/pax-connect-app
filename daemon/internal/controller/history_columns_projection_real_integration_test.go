//go:build integration

package controller

import (
	"context"
	"os"
	"testing"
	"time"

	"pax-connect/daemon/internal/historystore"
)

// TestColumnProjectionReal explicitly converts an already-imported private copy.
// Ordinary tests never open the user's databases or start the daemon/App.
func TestColumnProjectionReal(t *testing.T) {
	dir := os.Getenv("PAX_COLUMNS_PROJECTION_DEST")
	if dir == "" {
		t.Skip("PAX_COLUMNS_PROJECTION_DEST is not set")
	}
	ctx := context.Background()
	c, err := historystore.OpenColumns(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	start := time.Now()
	if err = rebuildColumnProjections(ctx, c); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_PROJECTION_REBUILD elapsed=%s", time.Since(start))
	checkColumnProjectionOracle(t, c)
	start = time.Now()
	if err = rebuildColumnProjections(ctx, c); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_PROJECTION_REPEAT elapsed=%s", time.Since(start))
}

func TestColumnProjectionRealMembers(t *testing.T) {
	dir := os.Getenv("PAX_COLUMNS_PROJECTION_DEST")
	if dir == "" {
		t.Skip("PAX_COLUMNS_PROJECTION_DEST is not set")
	}
	c, err := historystore.OpenColumns(context.Background(), dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	checkColumnMemberOracle(t, c)
}
