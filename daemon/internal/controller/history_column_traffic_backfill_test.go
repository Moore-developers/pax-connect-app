package controller

import (
	"context"
	"fmt"
	"os"
	"testing"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Acceptance: a library with current facts but a missing narrow traffic index
// remains explicitly building while the same owner fills bounded batches. No
// v3 import or all-facts startup model is needed for this derived-only upgrade.
func TestColumnOwnerBackfillsTrafficEvidenceBeforeReady(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, err := historystore.OpenColumns(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 2; batch++ {
		if err = c.Update(ctx, func(tx *historystore.ColumnsTx) error {
			for i := 0; i < 128; i++ {
				f := config.FlowFact{Key: fmt.Sprintf("backfill/%d/%d", batch, i), StartedAt: logsEpoch, Domain: "example.test", Direction: "direct"}
				if e := tx.PutConnection(ctx, config.LogConnectionSummary{Fact: f}); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	store, _, err := historystore.OpenLogs(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err = store.Ready(ctx); err == nil || err.Error() != "history_building" {
		t.Fatalf("partial traffic index queryable %v", err)
	}
	u := columnUpgradeJob{store: store, owner: uint32(os.Geteuid())}
	for i := 0; i < 5 && !u.done; i++ {
		if err = u.step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if !u.done {
		t.Fatal("bounded derived upgrade did not finish")
	}
	if err = store.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	var total int
	if err = store.Reader().QueryRowContext(ctx, "SELECT count(*) FROM col_traffic").Scan(&total); err != nil || total != 256 {
		t.Fatalf("traffic rows %d %v", total, err)
	}
}
