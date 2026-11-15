//go:build integration

package controller

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

type trafficColumnReadOnlyStore struct {
	logsStore
	col  *historystore.Columns
	meta historystore.LogsMeta
}

func (s trafficColumnReadOnlyStore) Columns() *historystore.Columns { return s.col }
func (s trafficColumnReadOnlyStore) SnapshotMeta(context.Context) (historystore.LogsMeta, error) {
	return s.meta, nil
}

func realColumnTrafficController(t *testing.T) *Controller {
	t.Helper()
	dir, trafficDir := os.Getenv("PAX_COLUMN_TRAFFIC_LOGS"), os.Getenv("PAX_COLUMN_TRAFFIC_BUCKETS")
	if dir == "" || trafficDir == "" {
		t.Skip("explicit converted private logs directory and private traffic directory required")
	}
	ctx := context.Background()
	columns, err := historystore.OpenColumns(ctx, dir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = columns.Close() })
	if err = columns.EnsureProjections(ctx); err != nil {
		t.Fatal(err)
	}
	for {
		done, e := advanceColumnTrafficIndex(ctx, columns)
		if e != nil {
			t.Fatal(e)
		}
		if done {
			break
		}
	}
	snapshot, err := columns.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	meta := snapshot.Meta()
	_ = snapshot.Close()
	traffic, _, err := historystore.OpenTraffic(ctx, trafficDir, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = traffic.Close() })
	var ns int64
	if err = columns.Reader().QueryRowContext(ctx, "SELECT max(at_ns) FROM col_events").Scan(&ns); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, ns).Add(time.Second)
	store := trafficColumnReadOnlyStore{col: columns, meta: meta}
	current := New("column-real", "column-real")
	current.logs = &historyLogs{logs: store, reads: current.readTokens}
	current.traffic = &historyTraffic{store: traffic}
	current.history = &historyOwner{clock: func() time.Time { return at }}
	current.readTokens.observeLogGenerations(meta.LogDeletionGeneration, meta.SummaryGeneration, meta.JournalGeneration, meta.LogRemovalBeforeMs)
	return current
}

func TestColumnTrafficRealRPCPerf(t *testing.T) {
	c := realColumnTrafficController(t)
	ctx := context.Background()
	query, detailQuery := c.QueryTraffic, c.QueryTrafficDetail
	transport := "controller"
	if os.Getenv("PAX_COLUMN_PERF_UNIX") == "1" {
		client := columnPerformanceClient(t, c)
		query = func(ctx context.Context, q *pb.TrafficQuery) (*pb.TrafficPage, error) {
			return client.QueryTraffic(ctx, q)
		}
		detailQuery = func(ctx context.Context, q *pb.TrafficDetailRequest) (*pb.TrafficDetail, error) {
			return client.QueryTrafficDetail(ctx, q)
		}
		transport = "unix-grpc"
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak uint64
	for _, window := range []string{"today", "day", "hour"} {
		for _, dim := range []string{"application", "domain", "system"} {
			if wanted := os.Getenv("PAX_COLUMN_TRAFFIC_CASE"); wanted != "" && !strings.Contains(window+"/"+dim, wanted) {
				continue
			}
			var list, detail, dns []time.Duration
			for i := 0; i < 20; i++ {
				start := time.Now()
				p, err := query(ctx, &pb.TrafficQuery{Window: window, Dimension: dim, Direction: "all", Limit: 100})
				if err != nil {
					t.Fatal(window, dim, err)
				}
				list = append(list, time.Since(start))
				if len(p.Items) > 0 {
					start = time.Now()
					_, err = detailQuery(ctx, &pb.TrafficDetailRequest{ReadToken: p.ReadToken, RowId: p.Items[0].Id, Limit: 20})
					if err != nil {
						t.Fatal(err)
					}
					detail = append(detail, time.Since(start))
				}
				if p.DnsSummary != nil {
					start = time.Now()
					_, err = detailQuery(ctx, &pb.TrafficDetailRequest{ReadToken: p.ReadToken, RowId: p.DnsSummary.Id, Limit: 20})
					if err != nil {
						t.Fatal(window, dim, "dns", err)
					}
					dns = append(dns, time.Since(start))
				}
				var sample runtime.MemStats
				runtime.ReadMemStats(&sample)
				peak = max(peak, sample.HeapAlloc)
			}
			percent := func(values []time.Duration) string {
				if len(values) == 0 {
					return "none"
				}
				sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
				p95 := values[(len(values)*95+99)/100-1]
				if p95 > 200*time.Millisecond {
					t.Errorf("%s/%s full RPC P95 exceeds 200ms: %s", window, dim, p95)
				}
				return fmt.Sprintf("n=%d p50_ms=%.3f p95_ms=%.3f", len(values), float64(values[len(values)/2].Microseconds())/1000, float64(values[(len(values)*95+99)/100-1].Microseconds())/1000)
			}
			t.Logf("COLUMN_TRAFFIC_REAL_PERF transport=%s window=%s dim=%s list=%s detail=%s dns=%s", transport, window, dim, percent(list), percent(detail), percent(dns))
		}
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("COLUMN_TRAFFIC_REAL_HEAP before=%d after=%d sampled_peak=%d delta_resident=%d", before.HeapAlloc, after.HeapAlloc, peak, int64(after.HeapAlloc)-int64(before.HeapAlloc))
}
