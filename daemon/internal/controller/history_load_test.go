package controller_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/controller"
	"runtime"
	"sort"
	"testing"
	"time"
)

// Tests the public ingestion/query seam with the actual private database, not its internals.
func TestHistoryBurstRemainsQueryableAndBounded(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	c := controller.New("test", "test")
	if err = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseHistory(ctx) }()
	now := time.Now().UTC().Truncate(5 * time.Second).Add(-5 * time.Second)
	sequence := uint64(0)
	started := time.Now()
	for block := 0; block < 20; block++ {
		batch := config.ObservationBatch{Session: "burst", Collecting: true}
		for i := 0; i < 100; i++ {
			f := config.FlowFact{Key: fmt.Sprintf("burst/1/%d", block*100+i), StartedAt: now.Add(-10 * time.Second), Direction: "direct", RouteID: "direct", Domain: "load.example.test"}
			for _, kind := range []string{"started", "sample"} {
				sequence++
				o := config.Observation{Session: "burst", Sequence: sequence, At: now.Add(-time.Second), Kind: kind, Flow: &f}
				if kind == "sample" {
					o.Upload = 10
					o.Download = 100
				}
				batch.Records = append(batch.Records, o)
			}
		}
		batch.HighWater = sequence
		if err = controller.FeedHistoryFixture(t, c, ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	queryStart := time.Now()
	page, err := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "minute"})
	elapsed := time.Since(queryStart)
	if err != nil || page.Connections != 2000 || page.Upload != 20000 || page.Download != 200000 {
		t.Fatalf("burst query: %v %v", page, err)
	}
	t.Logf("2000 live flows / 4000 observations: ingest=%s query=%s", time.Since(started), elapsed)
	if elapsed > 2*time.Second {
		t.Fatal("query exceeded request budget", elapsed)
	}
}

// Opt-in real-time soak matches V6; ordinary make test remains short and offline.
func TestHistorySustainedLoad(t *testing.T) {
	if os.Getenv("PAX_HISTORY_SOAK") != "1" {
		t.Skip("opt-in 10-minute real-time history soak")
	}
	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0700)
	c := controller.New("soak", "soak")
	if err = controller.ConfigureHistoryFixture(t, c, ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseHistory(ctx) }()
	sequence := uint64(0)
	active := make([]config.FlowFact, 0, 2000)
	queries := []time.Duration{}
	started := time.Now()
	for second := 0; second < 600; second++ {
		tick := started.Add(time.Duration(second) * time.Second)
		if d := time.Until(tick); d > 0 {
			time.Sleep(d)
		}
		now := time.Now().UTC()
		records := make([]config.Observation, 0, 2400)
		emit := func(kind string, f config.FlowFact, upload, download uint64) {
			sequence++
			copy := f
			records = append(records, config.Observation{Session: "soak", Sequence: sequence, At: now, Kind: kind, Flow: &copy, Upload: upload, Download: download})
		}
		for i := 0; i < 100; i++ {
			key := second*100 + i
			f := config.FlowFact{Key: fmt.Sprintf("soak/1/%d", key), StartedAt: now, Direction: "direct", RouteID: "direct", Domain: "soak.example.test"}
			if len(active) < 2000 {
				active = append(active, f)
			} else {
				slot := key % 2000
				old := active[slot]
				old.ClosedAt = &now
				emit("closed", old, 0, 0)
				active[slot] = f
			}
			emit("started", f, 0, 0)
		}
		for _, f := range active {
			emit("sample", f, 10, 100)
		}
		for len(records) > 0 {
			end := min(256, len(records))
			if err = controller.FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "soak", HighWater: sequence, Collecting: true, Records: records[:end]}); err != nil {
				t.Fatal(err)
			}
			records = records[end:]
		}
		if second%10 == 0 && second > 0 {
			qtime := time.Now()
			page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "domain", Window: "minute"})
			queries = append(queries, time.Since(qtime))
			if e != nil || page.Connections < 100 {
				t.Fatalf("soak query %d: %v", second, e)
			}
		}
	}
	sort.Slice(queries, func(i, j int) bool { return queries[i] < queries[j] })
	p95 := queries[len(queries)*95/100]
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	info, err := os.Stat(filepath.Join(dir, "history/traffic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("600 seconds, 100 new flows/s, 2000 active: query p95=%s heap=%d MiB database=%d MiB", p95, mem.HeapAlloc>>20, info.Size()>>20)
	if p95 > 200*time.Millisecond {
		t.Error("query p95 exceeded 200ms", p95)
	}
}
