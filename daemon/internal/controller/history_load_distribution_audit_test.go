//go:build history_audit

package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// Distribution measured read-only from a COPY of history.db.migrated-20261027 at
// e9d5c7c: application 858 rows/17 objects, domain 2018/116, ip 1498/305,
// route 651/5, system 247/5, unknown 181/1 over four observed dates.
// The synthetic fixture preserves those per-day densities and object cardinalities,
// with anonymous names, five routes and real aggregate payloads rather than padding.
func seedRepresentativeTraffic(t *testing.T, job *historyTraffic) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	distribution := []struct {
		dimension     string
		rows, objects int
		kind          string
	}{
		{"application", 214, 17, "app"}, {"domain", 504, 116, "app"}, {"ip", 374, 305, "app"},
		{"route", 162, 5, "app"}, {"system", 61, 5, "system"}, {"unknown", 45, 1, "unknown"},
	}
	var payloadBytes int
	counts := map[string]int{}
	for day := 29; day >= 0; day-- {
		start := today.AddDate(0, 0, -day)
		if err := job.store.Update(ctx, func(tx *historystore.TrafficTx) error {
			for _, d := range distribution {
				for i := 0; i < d.rows; i++ {
					at := start.Add(time.Duration(i) * 24 * time.Hour / time.Duration(d.rows)).Truncate(5 * time.Minute)
					if !at.Before(now.Add(-5 * time.Minute)) {
						continue
					}
					route := fmt.Sprintf("load.route.%d", i%5)
					direction := "proxy"
					if i%5 == 0 {
						route = "direct"
						direction = "direct"
					}
					object := fmt.Sprintf("load.%s.%03d", d.dimension, i%d.objects)
					switch d.dimension {
					case "route":
						object = route
					case "application":
						if i%d.objects == 0 {
							object = "load.fixture"
						}
					case "domain":
						if i%d.objects == 0 {
							object = "example.test"
						}
					case "ip":
						object = auditIP(i % d.objects)
					}
					identity := aggregateIdentity{Dimension: d.dimension, Object: object, Kind: d.kind, Name: object, Route: route, RouteName: route, Direction: direction, Search: fmt.Sprintf("load.%s.%03d host-%d.example.test 192.0.2.%d /Applications/Load-%d.app/Contents/MacOS/Load domain=load.example.test user route=%s", d.dimension, i%d.objects, i%116, i%250+1, i%17, route)}
					v := aggregateBucket{At: at, Identity: identity, Upload: 12000, Download: 240000, Connections: 3, Latest: 80, Maximum: 120, LatestAt: at, Latency: map[int]uint64{latencyBin(80): 2, latencyBin(120): 1}, Bases: flowAggregateBases(config.FlowFact{Rule: "domain=load.example.test", RuleSource: "user", RouteID: route, RouteName: route, Direction: direction}, 3)}
					raw, e := json.Marshal(v)
					if e != nil {
						return e
					}
					payloadBytes += len(raw)
					key := historystore.TrafficKey{BucketMs: at.UnixMilli(), Dimension: d.dimension, Object: object, Route: route, Direction: direction}
					existing, ok, e := tx.Traffic5m(ctx, key)
					if e != nil {
						return e
					}
					if ok {
						old, e := decodeTrafficRow(existing)
						if e != nil {
							return e
						}
						mergeAggregate(&v, old)
					}
					row, e := encodeTrafficRow(key, v)
					if e != nil {
						return e
					}
					if e = tx.PutTraffic5m(ctx, row); e != nil {
						return e
					}
					counts[d.dimension]++
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reader := job.store.(interface{ Reader() *sql.DB }).Reader()
	actualRows, err := reader.QueryContext(ctx, "SELECT dimension,count(*),count(DISTINCT object) FROM traffic_5m GROUP BY dimension")
	if err != nil {
		t.Fatal(err)
	}
	actualObjects := map[string]int{}
	for actualRows.Next() {
		var dimension string
		var n, objects int
		if err = actualRows.Scan(&dimension, &n, &objects); err != nil {
			t.Fatal(err)
		}
		actualObjects[dimension] = objects
		t.Logf("combined traffic dimension=%s SQL_rows=%d SQL_distinct_objects=%d", dimension, n, objects)
	}
	if err = actualRows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = actualRows.Close()
	for _, d := range distribution {
		if actualObjects[d.dimension] != d.objects {
			t.Fatalf("%s object distribution=%d want %d", d.dimension, actualObjects[d.dimension], d.objects)
		}
	}

	live, e := job.store.LiveBytes(ctx)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("representative traffic rows=%v anonymous_aggregate_json_bytes=%d mean_bytes_per_day=%d SQLite_live_bytes=%d", counts, payloadBytes, payloadBytes/30, live)
	if payloadBytes < 25<<20 {
		t.Fatalf("traffic fixture below representative 30-day size: %d", payloadBytes)
	}
}

// TestPendingFiftyThousandSnapshotAudit exercises the real traffic snapshot seam,
// including the pinned WAL, pending attribution and application identification.
func TestPendingFiftyThousandSnapshotAudit(t *testing.T) {
	h := newTrafficHarness(t)
	c := New("audit", "audit")
	c.traffic = h.tr
	live := newLiveState()
	live.seeded = true
	now := time.Now()
	batch := config.ObservationBatch{Session: "pending-load", Collecting: true}
	for i := 0; i < pendingMaxEntries; i++ {
		f := config.FlowFact{Key: fmt.Sprintf("pending/%d", i), StartedAt: now, ApplicationID: "load.app", Application: "Load", Process: "/usr/bin/curl", Domain: fmt.Sprintf("host-%d.example.test", i%305), RouteID: "direct", Direction: "direct"}
		batch.Records = append(batch.Records, config.Observation{Session: batch.Session, Sequence: uint64(i + 1), At: now.Add(-time.Second), Kind: "sample", Flow: &f, Upload: 10, Download: 100})
	}
	live.apply(batch, now)
	c.liveView.Store(live)
	h.tr.live = func() *liveState { return live }
	if len(live.pending.entries) != pendingMaxEntries {
		t.Fatalf("pending backlog=%d", len(live.pending.entries))
	}
	routeCalls := 0
	h.tr.route = func(*config.FlowFact) { routeCalls++ }
	var firstRouteCalls int
	var timings []time.Duration
	for sample := range 100 {
		started := time.Now()
		snapshot, e := c.trafficSnapshot(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if sample == 0 {
			firstRouteCalls = routeCalls
		} else if sample == 1 && routeCalls != firstRouteCalls {
			t.Errorf("unchanged pending tail reran attribution: first=%d after_second=%d", firstRouteCalls, routeCalls)
		}
		if len(snapshot.entries) != pendingMaxEntries {
			t.Fatalf("snapshot lost pending records: %d", len(snapshot.entries))
		}
		if e = snapshot.read.Close(); e != nil {
			t.Fatal(e)
		}
		timings = append(timings, time.Since(started))
	}
	t.Logf("pending_50000_snapshot first=%s attribution_calls_first=%d total=%d", timings[0], firstRouteCalls, routeCalls)
	reportAuditTimings(t, "pending_50000_snapshot", timings, 200*time.Millisecond)
}

func reportAuditTimings(t *testing.T, name string, timings []time.Duration, budget time.Duration) {
	t.Helper()
	sort.Slice(timings, func(i, j int) bool { return timings[i] < timings[j] })
	p95 := timings[(len(timings)*95+99)/100-1]
	t.Logf("%s p95=%s samples=%d budget=%s max=%s", name, p95, len(timings), budget, timings[len(timings)-1])
	if budget > 0 && p95 > budget {
		t.Errorf("%s p95 exceeds %s: %s", name, budget, p95)
	}
}

// TestRepresentativeMonthIPQueryAudit is the minimal C1 capacity loop: month reads
// do not load logs, so the 45,000 log-event setup is deliberately absent here.
func TestRepresentativeMonthIPQueryAudit(t *testing.T) {
	h := newTrafficHarness(t)
	c := New("audit", "audit")
	c.traffic = h.tr
	seedRepresentativeTraffic(t, h.tr)
	seedTodayIPConnections(t, h.tr)
	var up, down, connections int64
	if err := h.store.Reader().QueryRow("SELECT sum(up_bytes),sum(down_bytes),sum(new_conns) FROM traffic_5m WHERE dimension='ip'").Scan(&up, &down, &connections); err != nil {
		t.Fatal(err)
	}
	page, err := c.QueryTraffic(context.Background(), &pb.TrafficQuery{Dimension: "ip", Window: "month"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) == 0 {
		t.Fatal("representative month IP list empty")
	}
	if page.Upload != uint64(up) || page.Download != uint64(down) || page.Connections != uint64(connections) {
		t.Fatalf("month IP truncated totals: got %d/%d/%d want %d/%d/%d", page.Upload, page.Download, page.Connections, up, down, connections)
	}
}

// Only the IP contribution of the 15,000-connection daily fixture is needed to
// reproduce the month acquisition limit; log events and other dimensions cannot
// affect an IP query and are omitted from this minimized loop.
func seedTodayIPConnections(t *testing.T, job *historyTraffic) {
	t.Helper()
	ctx := context.Background()
	base := time.Now().Truncate(5 * time.Minute).Add(-5 * time.Minute)
	for start := 0; start < logLoadConnections; start += 1000 {
		if err := job.store.Update(ctx, func(tx *historystore.TrafficTx) error {
			for i := start; i < min(start+1000, logLoadConnections); i++ {
				at := base.Add(-time.Duration(i) * (23*time.Hour + 50*time.Minute) / logLoadConnections).Truncate(5 * time.Minute)
				f := config.FlowFact{ApplicationID: "load.fixture", Application: "受控负载", Domain: fmt.Sprintf("target-%d.example.test", i), IP: fmt.Sprintf("192.0.2.%d", i%250+1), RouteID: "direct", RouteName: "直连", Direction: "direct"}
				for _, identity := range aggregateIdentities(f) {
					if identity.Dimension != "ip" {
						continue
					}
					v := aggregateBucket{At: at, Identity: identity, Upload: 12000, Download: 240000, Connections: 1, Bases: flowAggregateBases(f, 1)}
					key := historystore.TrafficKey{BucketMs: at.UnixMilli(), Dimension: "ip", Object: f.IP, Route: "direct", Direction: "direct"}
					existing, ok, err := tx.Traffic5m(ctx, key)
					if err != nil {
						return err
					}
					if ok {
						old, err := decodeTrafficRow(existing)
						if err != nil {
							return err
						}
						mergeAggregate(&v, old)
					}
					row, err := encodeTrafficRow(key, v)
					if err != nil {
						return err
					}
					if err = tx.PutTraffic5m(ctx, row); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// The daily fixture's 250 documentation IPs are a subset of these 305 objects.
func auditIP(index int) string {
	if index < 250 {
		return fmt.Sprintf("192.0.2.%d", index+1)
	}
	return fmt.Sprintf("198.51.100.%d", index-250+1)
}
