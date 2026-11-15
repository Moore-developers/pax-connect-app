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

// A failed import keeps its old evidence, while active column facts still obey
// their own capacity cap and accept further writes.
func TestColumnUpgradeFailureCapacityKeepsOldEvidence(t *testing.T) {
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
	before, err := l.CapacityBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if physicalLive, e := l.LiveBytes(ctx); e != nil || physicalLive <= before {
		t.Fatalf("frozen evidence counted as active capacity: all=%d active=%d err=%v", physicalLive, before, e)
	}
	write := func(id string) error {
		return l.Update(ctx, func(tx *LogsTx) error {
			_, _, e := tx.AppendEvent(ctx, config.HistoryEvent{ID: id, At: baseHour, Raw: []string{strings.Repeat("x", 8192)}})
			return e
		})
	}
	for i := 0; i < 20; i++ {
		if err = write(fmt.Sprintf("new/%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if removed, e := l.EvictOldest(ctx, before+32768, Limits{Rows: 128, Duration: time.Second}); e != nil || !removed {
		t.Fatalf("failed upgrade blocked eviction: removed=%v err=%v", removed, e)
	}
	if size, e := l.CapacityBytes(ctx); e != nil || size > before+32768 {
		t.Fatalf("active bytes above cap: %d %v", size, e)
	}
	if count(t, l.Reader(), "SELECT count(*) FROM events") != oldEvents || count(t, l.Reader(), "SELECT count(*) FROM connections") != oldConnections {
		t.Fatal("capacity eviction changed frozen upgrade evidence")
	}
	if err = write("after-capacity"); err != nil {
		t.Fatal(err)
	}
	if state, _ := l.UpgradeState(ctx); state != "failed" {
		t.Fatal("capacity eviction hid conversion failure", state)
	}
}
