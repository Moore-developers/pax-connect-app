package controller

import (
	"context"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// recordingTraffic remembers the limits and result of every Prune pass.
type recordingTraffic struct {
	trafficStore
	limits []historystore.Limits
	pruned []historystore.Pruned
}

func (r *recordingTraffic) Prune(ctx context.Context, c historystore.TrafficCutoffs, l historystore.Limits) (historystore.Pruned, error) {
	p, err := r.trafficStore.Prune(ctx, c, l)
	r.limits, r.pruned = append(r.limits, l), append(r.pruned, p)
	return p, err
}

func expiredRows(t *testing.T, h *trafficHarness) int {
	t.Helper()
	total := 0
	for _, table := range []string{"traffic_5m", "coverage_5m", "recent_cuts"} {
		var n int
		if err := h.store.Reader().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	return total
}

func TestTrafficMaintenancePassesAreBoundedToTwoThousandRowsAndOneHundredMilliseconds(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	f := markedFlow("s/1/old")
	records := make([]config.Observation, 0, 1500)
	for i := range 1500 { // one record per 5-minute bucket: far more expired rows than one pass may delete
		records = append(records, h.obs(time.Duration(i)*5*time.Minute, "sample", f, 1, 1))
	}
	h.append(trafficEpoch.Add(time.Minute), true, records...)
	h.settle()
	before := expiredRows(t, h)
	if before <= trafficMaintainRows {
		t.Fatalf("fixture holds %d rows, need more than one pass can delete", before)
	}
	h.clock = h.clock.Add(trafficRetention + 1500*5*time.Minute + time.Hour) // everything is expired
	rec := &recordingTraffic{trafficStore: h.tr.store}
	h.tr.store = rec
	passes := 0
	for ; passes < 100; passes++ {
		if d := h.tr.maintain(ctx); d != 0 {
			t.Fatalf("maintain backed off by %v", d)
		}
		if !rec.pruned[len(rec.pruned)-1].More {
			passes++
			break
		}
	}
	// Aggregates are pruned first and alone (their deletion moves the generation); once they are
	// done a pass adds a second call for the 5-second tail and the flow marks.
	if len(rec.limits) < passes || passes < 2 {
		t.Fatalf("%d prune calls over %d passes: the expired rows must need several bounded passes", len(rec.limits), passes)
	}
	for i, l := range rec.limits {
		if l.Rows != 2000 || l.Duration != 100*time.Millisecond {
			t.Fatalf("pass %d limits %+v, want 2000 rows and 100ms", i, l)
		}
		if rec.pruned[i].Deleted > 2000 {
			t.Fatalf("pass %d deleted %d rows, over the 2000 limit", i, rec.pruned[i].Deleted)
		}
	}
	if !rec.pruned[0].More || rec.pruned[0].Deleted == 0 {
		t.Fatalf("first pass %+v: it must delete a bounded part and report more to do", rec.pruned[0])
	}
	if left := expiredRows(t, h); left != 0 {
		t.Fatalf("%d expired rows left after %d passes", left, passes)
	}
}
