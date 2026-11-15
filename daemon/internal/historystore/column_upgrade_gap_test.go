package historystore

import (
	"context"
	"path/filepath"
	"testing"

	"pax-connect/daemon/internal/config"
)

// A gap committed before import must also affect old open summaries seeded by
// new observations and summaries imported later, including across restart.
func TestColumnUpgradeGapSurvivesPendingImport(t *testing.T) {
	l, path := openUpgradeFixture(t)
	ctx := context.Background()
	if err := l.Update(ctx, func(tx *LogsTx) error { return tx.MarkImportGap(ctx) }); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	l, _, err := OpenLogs(ctx, filepath.Dir(path), uid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if err = l.Update(ctx, func(tx *LogsTx) error {
		v, ok, e := tx.Summary(ctx, "flow-1")
		if e != nil || !ok {
			t.Fatalf("missing legacy seed: %v %v", ok, e)
		}
		assertImportGap(t, v)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	completeUpgrade(t, l)
	v, ok, err := l.columns.Connection(ctx, "flow-1")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	assertImportGap(t, v)
}

func assertImportGap(t *testing.T, v config.LogConnectionSummary) {
	t.Helper()
	if !v.Fact.StateUnknown || v.Metrics.BytesState != "partial" || v.Metrics.Reason != "observation_gap" || v.Fact.ObservedUntil == nil || !v.Fact.ObservedUntil.Equal(v.Metrics.AsOf) {
		t.Fatalf("import lost gap: %+v", v)
	}
}
