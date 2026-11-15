package historystore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// Acceptance: a mutable group's ordering/count/level and the entire connection
// pagination stay at one version during multiple commits, then old versions
// disappear once their last shared token closes.
func TestColumnSnapshotPagesAndSharedVersions(t *testing.T) {
	ctx := context.Background()
	c := mustColumnsImportTarget(t, historyDir(t))
	if err := c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Update(ctx, func(tx *ColumnsTx) error {
		for i := 0; i < 300; i++ {
			key := fmt.Sprintf("f%03d", i)
			s := config.LogConnectionSummary{Fact: config.FlowFact{Key: key, StartedAt: baseHour.Add(time.Duration(i) * time.Second)}, Metrics: config.ConnectionMetrics{Upload: uint64(i)}}
			if err := tx.PutConnection(ctx, s); err != nil {
				return err
			}
		}
		return tx.PutGroup(ctx, ColumnGroup{View: "activity", Key: "group", FirstAt: baseHour, LastAt: baseHour, Count: 1, Level: 1})
	}); err != nil {
		t.Fatal(err)
	}
	first, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readPage := func(s *ColumnSnapshot, offset int) string {
		t.Helper()
		var result strings.Builder
		err := s.WithRead(ctx, func(q ColumnQuery) error {
			rows, e := q.QueryContext(ctx, "SELECT c.flow_key,c.upload FROM snap_col_connections c ORDER BY c.started_ns DESC,c.flow_key LIMIT 37 OFFSET ?", offset)
			if e != nil {
				return e
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var key string
				var upload int64
				if e = rows.Scan(&key, &upload); e != nil {
					return e
				}
				fmt.Fprintf(&result, "%s:%d,", key, upload)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.String()
	}
	var before []string
	for offset := 0; offset < 300; offset += 37 {
		before = append(before, readPage(first, offset))
	}
	for round := 0; round < 3; round++ {
		if err = c.Update(ctx, func(tx *ColumnsTx) error {
			for i := 0; i < 50; i++ {
				key := fmt.Sprintf("f%03d", i)
				s := config.LogConnectionSummary{Fact: config.FlowFact{Key: key, StartedAt: baseHour.Add(time.Hour)}, Metrics: config.ConnectionMetrics{Upload: 999}}
				if e := tx.PutConnection(ctx, s); e != nil {
					return e
				}
			}
			return tx.PutGroup(ctx, ColumnGroup{View: "activity", Key: "group", FirstAt: baseHour, LastAt: baseHour.Add(time.Hour), Count: uint64(round + 2), Level: 3})
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i, offset := 0, 0; offset < 300; i, offset = i+1, offset+37 {
		if got := readPage(first, offset); got != before[i] {
			t.Fatalf("changed first page %d", i)
		}
		if got := readPage(second, offset); got != before[i] {
			t.Fatalf("changed shared page %d", i)
		}
	}
	if err = first.WithRead(ctx, func(q ColumnQuery) error {
		var count, level int
		if e := q.QueryRowContext(ctx, "SELECT event_count,level FROM snap_col_groups WHERE group_key='group'").Scan(&count, &level); e != nil {
			return e
		}
		if count != 1 || level != 1 {
			return fmt.Errorf("group changed count=%d level=%d", count, level)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained int
	if err = c.snapshots.cache.QueryRow("SELECT count(*) FROM v_col_connections").Scan(&retained); err != nil || retained != 50 {
		t.Fatalf("shared old rows=%d err=%v", retained, err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if !second.Valid() {
		t.Fatal("closing one reference revoked another")
	}
	if err = second.Close(); err != nil {
		t.Fatal(err)
	}
	if err = c.snapshots.cache.QueryRow("SELECT count(*) FROM v_col_connections").Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("released versions=%d %v", retained, err)
	}
}
