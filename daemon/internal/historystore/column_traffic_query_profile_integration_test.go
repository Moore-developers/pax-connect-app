//go:build integration

package historystore

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestColumnTrafficQueryProfile(t *testing.T) {
	dir := os.Getenv("PAX_COLUMN_TRAFFIC_LOGS")
	if dir == "" {
		t.Skip("explicit private converted source required")
	}
	ctx := context.Background()
	c, err := OpenColumns(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err = c.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	ready, err := c.TrafficProjectionReady(ctx)
	if err != nil || !ready {
		t.Fatal("run controller traffic backfill before profiling", err)
	}
	snapshot, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var ns int64
	if err = c.Reader().QueryRowContext(ctx, "SELECT max(at_ns) FROM col_events").Scan(&ns); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, ns).Add(time.Second)
	f := ColumnTrafficFilter{Dimension: "dns", Object: "dns", Direction: "all", Until: at, From: at.Add(-24 * time.Hour)}
	err = snapshot.WithRead(ctx, func(q ColumnQuery) error {
		cte, args := columnTrafficScope(f, false)
		query := cte + " SELECT flow_key,start_ns,read_latency_known,read_latency,dns_group_id FROM traffic_selected"
		rows, err := q.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err = rows.Scan(&id, &parent, &notused, &detail); err != nil {
				_ = rows.Close()
				return err
			}
			t.Logf("TRAFFIC_SQL_PLAN %s", detail)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		start := time.Now()
		var count int
		if err = q.QueryRowContext(ctx, cte+"SELECT count(*) FROM traffic_selected", args...).Scan(&count); err != nil {
			return err
		}
		t.Logf("TRAFFIC_SQL_COUNT rows=%d ms=%.3f", count, float64(time.Since(start).Microseconds())/1000)
		start = time.Now()
		_, err = q.ExecContext(ctx, "CREATE TEMP TABLE profile_dns AS "+query, args...)
		if err != nil {
			return err
		}
		t.Logf("TRAFFIC_SQL_DIRECTORY ms=%.3f", float64(time.Since(start).Microseconds())/1000)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = snapshot.TrafficPeriodGroups(ctx, ColumnTrafficFilter{Dimension: "system", Direction: "all", Until: at, From: at.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TRAFFIC_PERIOD_SYSTEM ms=%.3f", float64(time.Since(start).Microseconds())/1000)
	start = time.Now()
	groups, err := snapshot.TrafficPeriodGroups(ctx, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TRAFFIC_PERIOD_DNS groups=%d ms=%.3f", len(groups), float64(time.Since(start).Microseconds())/1000)
}
