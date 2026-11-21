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

func fillConnections(t *testing.T, l *Logs, from, to int, pad string) {
	t.Helper()
	ctx := context.Background()
	err := l.Update(ctx, func(tx *LogsTx) error {
		for i := from; i < to; i++ {
			summary := config.LogConnectionSummary{Fact: config.FlowFact{Key: fmt.Sprintf("k%06d", i), Domain: pad, StartedAt: time.UnixMilli(int64(i))}, LastLogAt: time.UnixMilli(int64(i))}
			if err := tx.PutSummary(ctx, summary); err != nil {
				return err
			}
			if _, _, err := tx.AppendEvent(ctx, config.HistoryEvent{ID: fmt.Sprintf("s:%d", i), At: time.UnixMilli(int64(i)), Kind: "start", Flow: &summary.Fact}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPruneRespectsRowAndTimeLimits(t *testing.T) {
	l := mustLogs(t, historyDir(t))
	ctx := context.Background()
	fillConnections(t, l, 0, 1000, "")
	cutoff := time.UnixMilli(500)
	if _, err := l.Prune(ctx, cutoff, Limits{}); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("zero limits: %v", err)
	}
	if res, err := l.Prune(ctx, cutoff, Limits{Rows: 1000, Duration: time.Nanosecond}); err != nil || res.Deleted != 0 || !res.More {
		t.Fatalf("exhausted time budget must delete nothing: %+v %v", res, err)
	}
	passes, total := 0, 0
	for {
		res, err := l.Prune(ctx, cutoff, Limits{Rows: 120, Duration: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if res.Deleted > 120 {
			t.Fatalf("pass exceeded row limit: %d", res.Deleted)
		}
		passes++
		total += res.Deleted
		if !res.More {
			break
		}
	}
	// 500 connections plus 500 events, 120 rows per pass.
	if total != 1000 || passes != 9 {
		t.Fatalf("deleted %d in %d passes", total, passes)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_connections WHERE last_activity_ms < 500"); n != 0 {
		t.Fatalf("old rows left: %d", n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_connections"); n != 500 {
		t.Fatalf("newer rows touched: %d", n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 500 {
		t.Fatalf("newer events touched: %d", n)
	}
	// Returning expired pages to the filesystem must preserve the retained facts.
	if err := l.IncrementalVacuum(ctx, 1<<20); err != nil {
		t.Fatal(err)
	}
	if truncated, err := l.Checkpoint(ctx); err != nil || !truncated {
		t.Fatalf("checkpoint: %v %v", truncated, err)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_connections"); n != 500 {
		t.Fatalf("reclaim changed retained connections: %d", n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM col_events"); n != 500 {
		t.Fatalf("reclaim changed retained events: %d", n)
	}
}

func TestTrafficPruneByTable(t *testing.T) {
	tr := mustTraffic(t, historyDir(t))
	ctx := context.Background()
	err := tr.Update(ctx, func(tx *TrafficTx) error {
		for i := 0; i < 3000; i++ {
			row := TrafficRow{TrafficKey: TrafficKey{BucketMs: int64(i) * 300000, Dimension: "app", Object: fmt.Sprintf("o%d", i%7), Route: "r", Direction: "out"},
				Name: strings.Repeat("n", 100), UpBytes: 1, LatencyHist: make([]byte, 64)}
			if err := tx.PutTraffic5m(ctx, row); err != nil {
				return err
			}
			if err := tx.PutCoverage(ctx, Coverage{BucketMs: row.BucketMs, Bits: make([]byte, 38)}); err != nil {
				return err
			}
			if i < 100 {
				row.BucketMs = int64(i) * 5000
				if err := tx.PutRecent5s(ctx, row); err != nil {
					return err
				}
				if err := tx.PutFlowMark(ctx, FlowMark{FlowKey: fmt.Sprintf("c%d", i), Closed: i%2 == 0, LastAtMs: int64(i)}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.Prune(ctx, TrafficCutoffs{Recent: time.UnixMilli(250000), ClosedMarks: time.UnixMilli(50)}, Limits{Rows: 10000, Duration: time.Second})
	if err != nil || res.More {
		t.Fatalf("prune: %+v %v", res, err)
	}
	if n := count(t, tr.Reader(), "SELECT count(*) FROM recent_5s"); n != 50 {
		t.Fatalf("recent_5s: %d", n)
	}
	// Closed marks below 50 go (even i: 25), never-closed ones stay until their own cutoff.
	if n := count(t, tr.Reader(), "SELECT count(*) FROM flow_marks WHERE closed=0"); n != 50 {
		t.Fatalf("open marks: %d", n)
	}
	if n := count(t, tr.Reader(), "SELECT count(*) FROM flow_marks"); n != 75 {
		t.Fatalf("marks: %d", n)
	}
	if n := count(t, tr.Reader(), "SELECT count(*) FROM traffic_5m"); n != 3000 {
		t.Fatalf("aggregates touched without cutoff: %d", n)
	}
	if _, err = tr.Prune(ctx, TrafficCutoffs{Aggregates: time.UnixMilli(1000 * 300000)}, Limits{Rows: 100000, Duration: time.Second}); err != nil {
		t.Fatal(err)
	}
	if n := count(t, tr.Reader(), "SELECT count(*) FROM traffic_5m"); n != 2000 {
		t.Fatalf("aggregates after prune: %d", n)
	}
	if n := count(t, tr.Reader(), "SELECT count(*) FROM coverage_5m"); n != 2000 {
		t.Fatalf("coverage after prune: %d", n)
	}
}

func TestLogRemovalBoundPersistsWithoutAdvancingOnEmptyPrune(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	l := mustLogs(t, dir)
	fillConnections(t, l, 1, 10, "")
	limits := Limits{Rows: 2000, Duration: time.Second}
	if _, err := l.Prune(ctx, time.UnixMilli(5), limits); err != nil {
		t.Fatal(err)
	}
	meta, err := l.SnapshotMeta(ctx)
	if err != nil || meta.LogRemovalBeforeMs != 5 {
		t.Fatal(meta, err)
	}
	// No matching rows: do not invent a wider removal, even if a later cutoff is requested.
	if _, err = l.Prune(ctx, time.UnixMilli(3), limits); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := mustLogs(t, dir)
	meta, err = reopened.SnapshotMeta(ctx)
	if err != nil || meta.LogRemovalBeforeMs != 5 {
		t.Fatal("removal scope did not survive reopen", meta, err)
	}
}
