package controller

import (
	"context"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"testing"
	"time"
)

func TestTrafficCommittedCutoffExcludesLaterObservationInSameFiveSecondBucket(t *testing.T) {
	h := newTrafficHarness(t)
	f := trafficFlow("s/cutoff", func(*config.FlowFact) {})
	epoch := time.Now().Truncate(5 * time.Minute).Add(-5 * time.Minute)
	first, last := h.obs(11*time.Second, "started", f, 10, 90), h.obs(13*time.Second, "sample", f, 100, 900)
	first.At, last.At = epoch.Add(11*time.Second), epoch.Add(13*time.Second)
	h.append(h.clock, true, first, last)
	h.settle()
	c := New("test", "test")
	c.traffic = h.tr
	snap, e := c.trafficSnapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = snap.read.Close() }()
	rows, e := snap.buckets(context.Background(), epoch, epoch.Add(12*time.Second), []string{"route"})
	if e != nil {
		t.Fatal(e)
	}
	var up, down uint64
	for _, v := range rows {
		up += v.Upload
		down += v.Download
	}
	if up != 10 || down != 90 {
		t.Fatalf("cutoff leaked future sample: %d/%d", up, down)
	}
}

func TestTrafficReadMatchesPendingAndCommittedTotals(t *testing.T) {
	h := newTrafficHarness(t)
	live := newLiveState()
	h.tr.live = func() *liveState { return live }
	c := New("test", "test")
	c.traffic = h.tr
	c.liveView.Store(live)
	f := trafficFlow("s/read", func(*config.FlowFact) {})
	records := []config.Observation{h.obs(time.Second, "started", f, 10, 90), h.obs(2*time.Second, "sample", f, 100, 900)}
	live.apply(liveBatch(records...), h.clock)
	h.append(h.clock, true, records...)
	read := func() (uint64, uint64) {
		snap, e := c.trafficSnapshot(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = snap.read.Close() }()
		rows, e := snap.buckets(context.Background(), trafficEpoch, h.clock, []string{"route"})
		if e != nil {
			t.Fatal(e)
		}
		var up, down uint64
		for _, r := range rows {
			up += r.Upload
			down += r.Download
		}
		return up, down
	}
	for i := range 2 {
		up, down := read()
		if up != 110 || down != 990 {
			t.Fatalf("phase%d totals %d/%d", i, up, down)
		}
		h.settle()
	}
}

func TestTrafficReaderIsNotBlockedByUncommittedWrite(t *testing.T) {
	h := newTrafficHarness(t)
	c := New("test", "test")
	c.traffic = h.tr
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- h.store.Update(context.Background(), func(tx *historystore.TrafficTx) error {
			if e := tx.SetMeta(context.Background(), "uncommitted", "1"); e != nil {
				return e
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snap, e := c.trafficSnapshot(ctx)
	if e == nil {
		_, ok, err := snap.read.Meta(ctx, "uncommitted")
		_ = snap.read.Close()
		if ok || err != nil {
			t.Errorf("reader saw uncommitted metadata: %v %v", ok, err)
		}
	} else {
		t.Error(e)
	}
	close(release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestTrafficCompactedPendingIsCountedOnceAcrossPartialCommits(t *testing.T) {
	h := newTrafficHarness(t)
	live := newLiveState()
	h.tr.live = func() *liveState { return live }
	f := trafficFlow("s/compact", func(*config.FlowFact) {})
	var records []config.Observation
	for range 5001 {
		records = append(records, h.obs(time.Second, "sample", f, 1, 2))
	}
	live.apply(liveBatch(records...), h.clock)
	live.mu.Lock()
	live.pending.fold(4500)
	live.mu.Unlock()
	h.append(h.clock, true, records...)
	for step := 0; step < 4; step++ {
		up, down, _, _ := h.counters("route")
		pending := live.Pending()
		if uint64(up)+pending.Upload != 5001 || uint64(down)+pending.Download != 10002 {
			t.Fatalf("step%d committed=%d/%d pending=%d/%d", step, up, down, pending.Upload, pending.Download)
		}
		h.tr.step(context.Background())
	}
}

func TestTodayConnectionsKeepRetainedLongActiveConnectionWithOneHourLogs(t *testing.T) {
	c, dir := newTrafficRPCFixture(t)
	ctx := context.Background()
	midnight, _, _ := calendarWindow("today", time.Now())
	if time.Since(midnight) < 2*time.Hour {
		midnight = time.Now().Add(-3 * time.Hour)
	}
	f := config.FlowFact{Key: "retained/long", StartedAt: midnight.Add(time.Minute), Domain: "long.example.test", Direction: "direct", RouteID: "direct"}
	at := time.Now().Add(-time.Second)
	if err := FeedHistoryFixture(t, c, ctx, config.ObservationBatch{Session: "retained", Records: []config.Observation{{Session: "retained", Sequence: 1, At: at, Kind: "started", Flow: &f}}}); err != nil {
		t.Fatal(err)
	}
	job, _ := c.logJob()
	if err := job.logs.Update(ctx, func(tx *historystore.LogsTx) error { return tx.SetMeta(ctx, historystore.MetaRetentionHours, "1") }); err != nil {
		t.Fatal(err)
	}
	r := &historyRead{from: midnight, at: time.Now()}
	if err := c.freezeTrafficConnections(ctx, r, ""); err != nil || len(r.aggregateFlows) != 1 {
		t.Fatalf("retained active connection hidden: %d %v dir=%s", len(r.aggregateFlows), err, dir)
	}
}
