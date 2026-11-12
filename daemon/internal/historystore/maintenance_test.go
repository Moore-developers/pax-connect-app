package historystore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func fillConnections(t *testing.T, l *Logs, from, to int, pad string) {
	t.Helper()
	ctx := context.Background()
	err := l.Update(ctx, func(tx *LogsTx) error {
		for i := from; i < to; i++ {
			c := LogConnection{FlowKey: fmt.Sprintf("k%06d", i), StartedMs: int64(i), LastActivityMs: int64(i), Domain: pad, Body: []byte(pad)}
			if err := tx.PutConnection(ctx, c); err != nil {
				return err
			}
			if _, _, err := tx.PutEvent(ctx, LogEvent{EventID: fmt.Sprintf("s:%d", i), FlowKey: c.FlowKey, AtMs: int64(i), Kind: "start", Body: []byte(pad)}); err != nil {
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
	if n := count(t, l.Reader(), "SELECT count(*) FROM connections WHERE last_activity_ms < 500"); n != 0 {
		t.Fatalf("old rows left: %d", n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM connections"); n != 500 {
		t.Fatalf("newer rows touched: %d", n)
	}
	if n := count(t, l.Reader(), "SELECT count(*) FROM events"); n != 500 {
		t.Fatalf("newer events touched: %d", n)
	}
}

func TestEvictOldestShrinksLiveSizeBelowCapAndReports(t *testing.T) {
	l := mustLogs(t, historyDir(t))
	ctx := context.Background()
	limits := Limits{Rows: 500, Duration: time.Second}
	fillConnections(t, l, 0, 4000, strings.Repeat("d", 200))
	live, err := l.LiveBytes(ctx)
	if err != nil || live < 1<<20 {
		t.Fatalf("live size %d %v", live, err)
	}
	if evicted, err := l.EvictOldest(ctx, live*2, limits); err != nil || evicted {
		t.Fatalf("under the cap: evicted=%v %v", evicted, err)
	}
	if v, ok, _ := l.Meta(ctx, "capacity_evicted"); !ok || v != "0" {
		t.Fatal("capacity_evicted set without eviction")
	}
	capBytes := live / 2
	evicted, err := l.EvictOldest(ctx, capBytes, limits)
	if err != nil || !evicted {
		t.Fatalf("evicted=%v %v", evicted, err)
	}
	if after, _ := l.LiveBytes(ctx); after > capBytes {
		t.Fatalf("live %d above cap %d", after, capBytes)
	}
	if v, ok, _ := l.Meta(ctx, "capacity_evicted"); !ok || v != "1" {
		t.Fatal("capacity_evicted not recorded")
	}
	var oldest, newest int64
	if err = l.Reader().QueryRow("SELECT min(last_activity_ms), max(last_activity_ms) FROM connections").Scan(&oldest, &newest); err != nil {
		t.Fatal(err)
	}
	if oldest == 0 || newest != 3999 {
		t.Fatalf("eviction must remove oldest first: min %d max %d", oldest, newest)
	}
	// Writes are still accepted after eviction, and vacuum plus checkpoint run on the idle writer.
	fillConnections(t, l, 5000, 5010, "")
	if err = l.IncrementalVacuum(ctx, 1<<20); err != nil {
		t.Fatal(err)
	}
	if truncated, err := l.Checkpoint(ctx); err != nil || !truncated {
		t.Fatalf("checkpoint: %v %v", truncated, err)
	}
}

func TestTrafficEvictAndPruneByTable(t *testing.T) {
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
	limits := Limits{Rows: 400, Duration: time.Second}
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
	live, _ := tr.LiveBytes(ctx)
	evicted, err := tr.EvictOldest(ctx, live/2, limits)
	if err != nil || !evicted {
		t.Fatalf("evicted=%v %v", evicted, err)
	}
	if after, _ := tr.LiveBytes(ctx); after > live/2 {
		t.Fatalf("live %d above cap %d", after, live/2)
	}
	if n := count(t, tr.Reader(), "SELECT count(*) FROM coverage_5m WHERE bucket_ms < (SELECT min(bucket_ms) FROM traffic_5m)"); n != 0 {
		t.Fatal("coverage older than the aggregate frontier survived eviction")
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
