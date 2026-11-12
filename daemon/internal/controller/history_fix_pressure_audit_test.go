//go:build history_audit

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// A five-minute 100-starts/s burst with distinct subdomains deliberately stresses
// accumulated search evidence. It is synthetic, not measured user distribution.
func TestHistoryFixAccumulatedSearchTransactionBudget(t *testing.T) {
	h := newTrafficHarness(t)
	store := &timedTrafficAudit{Traffic: h.store.Traffic}
	h.tr.store = store
	epoch := trafficEpoch.Truncate(5 * time.Minute)
	for batch := 0; batch < (30000+trafficBatchRecords-1)/trafficBatchRecords; batch++ {
		records := make([]config.Observation, 0, trafficBatchRecords)
		for i := 0; i < trafficBatchRecords && batch*trafficBatchRecords+i < 30000; i++ {
			n := batch*trafficBatchRecords + i
			f := trafficFlow(fmt.Sprintf("pressure/%d", n), func(f *config.FlowFact) {
				f.ApplicationID = "pressure.app"
				f.Application = "Pressure"
				f.Domain = fmt.Sprintf("host-%d.example.test", n)
				f.IP = "192.0.2.1"
			})
			o := h.obs(0, "started", f, 1, 10)
			o.At = epoch.Add(time.Duration(n) * 10 * time.Millisecond)
			f.StartedAt = o.At
			records = append(records, o)
		}
		h.append(h.clock, true, records...)
		h.settle()
	}
	maximum := time.Duration(0)
	for _, d := range store.durations {
		maximum = max(maximum, d)
	}
	t.Logf("30000 unique subdomains / five minutes; batch=%d transactions=%d max=%s margin=%s", trafficBatchRecords, len(store.durations), maximum, 100*time.Millisecond-maximum)
	if maximum > 100*time.Millisecond {
		t.Fatalf("search accumulation exceeded transaction budget: %s", maximum)
	}
	var count int
	if err := h.store.Reader().QueryRow("SELECT count(*) FROM search_evidence").Scan(&count); err != nil || count == 0 {
		t.Fatal(count, err)
	}
	up, down, connections, _ := h.counters("application")
	if up != 30000 || down != 300000 || connections != 30000 {
		t.Fatalf("pressure lost totals: %d/%d/%d", up, down, connections)
	}
	for _, resolution := range []int{0, 1} {
		var terms int
		if err := h.store.Reader().QueryRow("SELECT count(*) FROM search_evidence WHERE resolution=? AND dimension='domain'", resolution).Scan(&terms); err != nil || terms != 30000 {
			t.Fatalf("pressure lost exact search evidence resolution=%d count=%d error=%v", resolution, terms, err)
		}
	}

}

func TestHistoryFixIdentityRefreshRemainsBoundedDuringArrivals(t *testing.T) {
	h := newTrafficHarness(t)
	f := trafficFlow("identity/load", func(f *config.FlowFact) { f.ApplicationID = "a.app"; f.Application = "A" })
	records := make([]config.Observation, 0, 2*trafficBatchRecords)
	for range 2 * trafficBatchRecords {
		records = append(records, h.obs(time.Second, "sample", f, 1, 0))
	}
	h.append(h.clock, true, records...)
	if d := h.tr.step(context.Background()); d != 0 {
		t.Fatal(d)
	}
	for i := 0; i < 10; i++ {
		known := *f
		known.Application = fmt.Sprintf("Known %d", i)
		h.append(h.clock, true, h.obs(2*time.Second, "sample", &known, 1, 0))
		h.tr.step(context.Background())
	}
	h.settle()
	up, _, _, _ := h.counters("application")
	if up != int64(2*trafficBatchRecords+10) {
		t.Fatal("arrival scan duplicated/lost bytes", up)
	}
}
