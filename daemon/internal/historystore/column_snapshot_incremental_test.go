package historystore

import (
	"context"
	"testing"

	"pax-connect/daemon/internal/config"
)

// Acceptance: one incremental RPC's frozen head and its touched group evidence
// cannot mix a concurrent append into an older high-water response.
func TestColumnSnapshotIncrementalHeadExcludesConcurrentAppend(t *testing.T) {
	c, v := snapshotFixture(t)
	ctx := context.Background()
	s, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		_, err := tx.PutEvent(ctx, config.HistoryEvent{ID: "appended", Sequence: 2, At: baseHour, Flow: &v.Fact})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.EventsAfter(ctx, 0, int64(s.Meta().HeadSequence), 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "event" || rows[0].Sequence != 1 {
		t.Fatalf("incremental mixed heads: %+v", rows)
	}
	if err = s.WithRead(ctx, func(q ColumnQuery) error {
		var total int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM snap_col_events WHERE flow_present=1").Scan(&total); err != nil {
			return err
		}
		if total != 1 {
			t.Fatalf("group included concurrent append %d", total)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
