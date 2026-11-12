//go:build history_audit

package controller

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// TestHistoryStorageLoadAudit measures source-backed queries on the §11-3 load.
// This is a synthetic load benchmark, not evidence of 24 hours of actual use.
func TestHistoryStorageLoadAudit(t *testing.T) {
	c := buildLogLoadFixtureWindow(t, 23*time.Hour+50*time.Minute)
	ctx := context.Background()
	job := c.trafficJob()
	// Shared journal contains all 45,000 real pipeline fixture events.
	if e := job.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	seedRepresentativeTraffic(t, job)
	cases := []struct {
		name   string
		budget time.Duration
		query  func() error
	}{
		{"logs_default_30m", 200 * time.Millisecond, func() error {
			_, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", From: timestamppb.New(time.Now().Add(-30 * time.Minute))})
			return e
		}},
		{"traffic_detail_today", 200 * time.Millisecond, func() error {
			page, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: "application", Window: "today"})
			if e != nil {
				return e
			}
			if len(page.Items) == 0 {
				return fmt.Errorf("empty traffic fixture")
			}
			_, e = c.QueryTrafficDetail(ctx, &pb.TrafficDetailRequest{ReadToken: page.ReadToken, RowId: page.Items[0].Id})
			return e
		}},
		{"route_month", 500 * time.Millisecond, func() error {
			_, e := c.QueryRouteUsage(ctx, &pb.RouteHistoryRequest{RouteId: "direct", Window: "month"})
			return e
		}},
	}
	// Include the widest domain/IP scans, not only the cheapest application view.
	for _, window := range []string{"today", "week", "month"} {
		for _, dimension := range []string{"application", "domain", "ip", "system", "network"} {
			budget := 500 * time.Millisecond
			if window == "today" {
				budget = 200 * time.Millisecond
			}
			cases = append(cases, struct {
				name   string
				budget time.Duration
				query  func() error
			}{
				"traffic_" + window + "_" + dimension, budget, func() error {
					_, e := c.QueryTraffic(ctx, &pb.TrafficQuery{Dimension: dimension, Window: window})
					return e
				},
			})
		}
	}
	cases = append(cases, struct {
		name   string
		budget time.Duration
		query  func() error
	}{"logs_24h", 0, func() error {
		_, e := c.QueryLogs(ctx, &pb.LogQuery{View: "activity", From: timestamppb.New(time.Now().Add(-24 * time.Hour))})
		return e
	}})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var timings []time.Duration
			for range 100 {
				started := time.Now()
				if e := test.query(); e != nil {
					t.Fatalf("%s: %v", test.name, e)
				}
				timings = append(timings, time.Since(started))
			}
			sort.Slice(timings, func(i, j int) bool { return timings[i] < timings[j] })
			p95 := timings[(len(timings)*95+99)/100-1]
			t.Logf("%s p95=%s samples=%d budget=%s", test.name, p95, len(timings), test.budget)
			if test.budget > 0 && p95 > test.budget {
				t.Errorf("%s p95 exceeds %s: %s", test.name, test.budget, p95)
			}
		})
	}
	var rows int
	if e := c.logs.logs.(*historystore.Logs).Reader().QueryRow("SELECT count(*) FROM connections").Scan(&rows); e != nil || rows != 15000 {
		t.Fatal(fmt.Sprint("load summary count=", rows, " error=", e))
	}
}

type timedTrafficAudit struct {
	*historystore.Traffic
	durations []time.Duration
}

func (s *timedTrafficAudit) Update(ctx context.Context, fn func(*historystore.TrafficTx) error) error {
	started := time.Now()
	err := s.Traffic.Update(ctx, fn)
	s.durations = append(s.durations, time.Since(started))
	return err
}

func TestTrafficTransactionBudgetAudit(t *testing.T) {
	h := newTrafficHarness(t)
	store := &timedTrafficAudit{Traffic: h.store.Traffic}
	h.tr.store = store
	var records []config.Observation
	for i := 0; i < 2000; i++ {
		f := trafficFlow(fmt.Sprintf("s/budget/%d", i), func(f *config.FlowFact) {
			f.ApplicationID = "budget.app"
			f.Application = "Budget"
			f.Domain = fmt.Sprintf("host-%d.example.test", i)
			f.IP = fmt.Sprintf("192.0.2.%d", i%250+1)
		})
		records = append(records, h.obs(time.Second, "started", f, 10, 100), h.obs(2*time.Second, "sample", f, 10, 100))
	}
	h.append(h.clock, true, records...)
	h.settle()
	var maximum time.Duration
	for _, d := range store.durations {
		maximum = max(maximum, d)
	}
	t.Logf("traffic SQL transaction max=%s n=%d batch_limit=%d margin=%s recent_cuts=enabled", maximum, len(store.durations), trafficBatchRecords, 100*time.Millisecond-maximum)
	if maximum > 100*time.Millisecond {
		t.Fatal("transaction exceeded 100ms", maximum)
	}
}
