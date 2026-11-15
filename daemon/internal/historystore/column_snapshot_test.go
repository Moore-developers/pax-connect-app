package historystore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

func snapshotFixture(t *testing.T) (*Columns, config.LogConnectionSummary) {
	t.Helper()
	c := mustColumnsImportTarget(t, historyDir(t))
	ctx := context.Background()
	if err := c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	v := config.LogConnectionSummary{Fact: config.FlowFact{Key: "s/flow", Application: "before", Domain: "example.test", StartedAt: baseHour, DNS: &config.DNSFact{Answers: []string{"before", "second"}}}, Metrics: config.ConnectionMetrics{Upload: 1, AsOf: baseHour}, LastEventID: "event"}
	if err := c.Update(ctx, func(tx *ColumnsTx) error {
		if err := tx.PutConnection(ctx, v); err != nil {
			return err
		}
		if _, err := tx.PutEvent(ctx, config.HistoryEvent{ID: "event", Sequence: 1, At: baseHour, Flow: &v.Fact, Raw: []string{"raw"}}); err != nil {
			return err
		}
		return tx.PutMembers(ctx, v.Fact.Key, []ColumnMember{{Dimension: "application", Object: "before", Name: "Before", StartedAt: baseHour}})
	}); err != nil {
		t.Fatal(err)
	}
	return c, v
}

// Acceptance: index movement, byte-only updates and appended rows cannot alter
// an older token; rollback must not publish either cache rows or a new revision.
func TestColumnSnapshotMutationAndRollback(t *testing.T) {
	c, v := snapshotFixture(t)
	ctx := context.Background()
	old, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	want := v
	v.Fact.Application = "after"
	v.Fact.StartedAt = v.Fact.StartedAt.Add(time.Hour)
	v.Fact.DNS = &config.DNSFact{Answers: []string{"after"}}
	v.Metrics.Upload = 999
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		if e := tx.PutConnection(ctx, v); e != nil {
			return e
		}
		if e := tx.PutMembers(ctx, v.Fact.Key, []ColumnMember{{Dimension: "application", Object: "after", Name: "After", StartedAt: v.Fact.StartedAt}}); e != nil {
			return e
		}
		_, e := tx.PutEvent(ctx, config.HistoryEvent{ID: "new", Sequence: 2, At: baseHour})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := old.Connection(ctx, v.Fact.Key)
	if err != nil || !ok || !reflect.DeepEqual(got, want) {
		rows, e := c.snapshots.cache.Query("SELECT flow_key,_snap_from,_snap_until FROM v_col_connections")
		if e == nil {
			for rows.Next() {
				var key string
				var a, b int
				if e = rows.Scan(&key, &a, &b); e == nil {
					t.Logf("old key=%s from=%d until=%d", key, a, b)
				}
			}
			_ = rows.Close()
		}
		t.Logf("rev=%d valid=%v", old.revision, old.Valid())
		t.Fatalf("old changed: %+v %v %v", got, ok, err)
	}
	if _, ok, err = old.Event(ctx, "new"); err != nil || ok {
		t.Fatalf("new event leaked %v %v", ok, err)
	}
	err = old.WithRead(ctx, func(q ColumnQuery) error {
		var id string
		return q.QueryRowContext(ctx, "SELECT o.object_key FROM snap_col_members m JOIN snap_col_objects o ON o.id=m.object_id WHERE m.flow_key=?", v.Fact.Key).Scan(&idCheck{want: "before", got: &id})
	})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = latest.Close() }()
	got, ok, err = latest.Connection(ctx, v.Fact.Key)
	if err != nil || !ok || !reflect.DeepEqual(got, v) {
		t.Fatalf("new missing %v %v", ok, err)
	}
	rev := latest.revision
	failure := errors.New("abort")
	err = c.Update(ctx, func(tx *ColumnsTx) error {
		v.Metrics.Upload = 12345
		if e := tx.PutConnection(ctx, v); e != nil {
			return e
		}
		return failure
	})
	if !errors.Is(err, failure) || c.snapshots.revision != rev {
		t.Fatalf("rollback published %v", err)
	}
	got, _, err = latest.Connection(ctx, v.Fact.Key)
	if err != nil || got.Metrics.Upload != 999 {
		t.Fatalf("rollback value %d %v", got.Metrics.Upload, err)
	}
}

// A connection may reuse read views for multiple tokens at the same revision.
// Alternating old and current tokens across writes must still return the value
// each token froze, including after another token releases its reference.
func TestColumnSnapshotAlternatingTokensAcrossCommits(t *testing.T) {
	c, original := snapshotFixture(t)
	ctx := context.Background()
	first, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	check := func(s *ColumnSnapshot, want config.LogConnectionSummary) {
		t.Helper()
		got, ok, e := s.Connection(ctx, original.Fact.Key)
		if e != nil || !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("snapshot value = %+v, found=%v, err=%v; want %+v", got, ok, e, want)
		}
	}
	check(first, original)
	check(second, original)
	updated := original
	updated.Fact.Application = "after"
	updated.Fact.DNS = &config.DNSFact{Answers: []string{"new", "ordered"}}
	if err = c.Update(ctx, func(tx *ColumnsTx) error { return tx.PutConnection(ctx, updated) }); err != nil {
		t.Fatal(err)
	}
	current, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = current.Close() }()
	check(first, original)
	check(current, updated)
	check(second, original)
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	check(second, original)
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		_, e := tx.tx.ExecContext(ctx, "DELETE FROM col_connections WHERE flow_key=?", original.Fact.Key)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	check(current, updated)
	check(second, original)
	latest, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = latest.Close() }()
	if _, ok, e := latest.Connection(ctx, original.Fact.Key); e != nil || ok {
		t.Fatalf("deleted connection remains visible: found=%v err=%v", ok, e)
	}
}

type idCheck struct {
	want string
	got  *string
}

func (v *idCheck) Scan(value any) error {
	s, ok := value.(string)
	if !ok || s != v.want {
		return fmt.Errorf("index moved: %v", value)
	}
	*v.got = s
	return nil
}

// Acceptance: an independent detail retains deleted facts and ordered children,
// while an idle token never blocks a real writer WAL checkpoint.
func TestColumnSnapshotDeletionAndCheckpoint(t *testing.T) {
	c, v := snapshotFixture(t)
	ctx := context.Background()
	old, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	if err = c.Update(ctx, func(tx *ColumnsTx) error {
		for _, table := range []string{"col_connections", "col_events", "col_members", "col_answers", "col_raw", "col_identities"} {
			if _, e := tx.tx.ExecContext(ctx, "DELETE FROM "+table); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := old.Connection(ctx, v.Fact.Key)
	if err != nil || !ok || !reflect.DeepEqual(got, v) {
		t.Fatalf("deleted connection lost %v %v", ok, err)
	}
	event, ok, err := old.Event(ctx, "event")
	if err != nil || !ok || !reflect.DeepEqual(event.Raw, []string{"raw"}) {
		t.Fatalf("deleted evidence lost %+v %v %v", event, ok, err)
	}
	var busy, log, checkpoint int
	if err = c.w.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpoint); err != nil || busy != 0 || log != 0 {
		t.Fatalf("checkpoint blocked: %d %d %d %v", busy, log, checkpoint, err)
	}
}

// Acceptance: charging by live SQLite pages evicts old references explicitly;
// released and expired references no longer retain old evidence.
func TestColumnSnapshotBudgetAndExpiry(t *testing.T) {
	c, v := snapshotFixture(t)
	ctx := context.Background()
	v.Fact.DNS = &config.DNSFact{Answers: []string{string(make([]byte, 2000000))}}
	if err := c.Update(ctx, func(tx *ColumnsTx) error { return tx.PutConnection(ctx, v) }); err != nil {
		t.Fatal(err)
	}
	old, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.snapshots.budget = 512 << 10
	v.Fact.DNS = &config.DNSFact{Answers: []string{"small"}}
	if err = c.Update(ctx, func(tx *ColumnsTx) error { return tx.PutConnection(ctx, v) }); err != nil {
		t.Fatal(err)
	}
	if old.Valid() {
		t.Fatal("over-budget token retained")
	}
	if _, _, err = old.Connection(ctx, v.Fact.Key); !errors.Is(err, errColumnCursor) {
		t.Fatalf("wrong eviction error %v", err)
	}
	c.snapshots.budget = columnVersionBudget
	next, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next.last = time.Now().Add(-121 * time.Second)
	if err = next.WithRead(ctx, func(ColumnQuery) error { return nil }); !errors.Is(err, errColumnCursor) {
		t.Fatal(err)
	}
	if err = next.Close(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = c.snapshots.cache.QueryRow("SELECT count(*) FROM v_current").Scan(&n); err != nil || n != 0 {
		t.Fatalf("current catalog retained %d %v", n, err)
	}
}
