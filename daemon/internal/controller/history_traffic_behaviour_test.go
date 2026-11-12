package controller

import (
	"context"
	"slices"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func coverageSet(t *testing.T, cov map[int64][]byte) []int {
	t.Helper()
	var out []int
	for bucket, bits := range cov {
		base := int(time.UnixMilli(bucket).UTC().Sub(time.Date(2026, 10, 26, 8, 10, 0, 0, time.UTC)) / time.Second)
		for s := range 300 {
			if bits[s/8]&(1<<uint(s%8)) != 0 {
				out = append(out, base+s)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestTrafficCoverageFromPolls(t *testing.T) {
	h := newTrafficHarness(t)
	base := time.Date(2026, 10, 26, 8, 10, 0, 0, time.UTC)
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }
	h.clock = at(60)
	for _, s := range []int{0, 5, 10} { // continuous: seconds 0..9
		h.appendPoll(at(s), "s", true)
	}
	h.appendPoll(at(20), "s", true)  // 10 s apart: nothing
	h.appendPoll(at(25), "s", false) // not collecting: nothing
	h.appendPoll(at(30), "s", true)  // the previous poll was not collecting: nothing
	h.appendPoll(at(35), "s", true)  // 30..34
	h.appendPoll(at(40), "t", true)  // another session: nothing
	h.appendPoll(at(45), "t", true)  // 40..44
	h.appendGap(at(47), journalGap{Reason: gapLost, Session: "t"})
	h.appendPoll(at(50), "t", true) // first poll after the gap: nothing
	h.appendPoll(at(55), "t", true) // 50..54
	h.settle()
	want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 30, 31, 32, 33, 34, 40, 41, 42, 43, 44, 50, 51, 52, 53, 54}
	if got := coverageSet(t, readCoverage(t, h.store.Traffic)); !slices.Equal(got, want) {
		t.Fatalf("coverage seconds %v want %v", got, want)
	}
	// The last poll survives a restart, so the next interval is not lost.
	h.restart()
	h.tr.now = func() time.Time { return h.clock }
	h.appendPoll(at(60), "t", true)
	h.settle()
	if got := coverageSet(t, readCoverage(t, h.store.Traffic)); !slices.Equal(got, append(want, 55, 56, 57, 58, 59)) {
		t.Fatalf("coverage after restart %v", got)
	}
	if trafficMeta(t, h.store.Traffic, trafficMetaCoverageFrom) == "" {
		t.Fatal("coverage start was not recorded")
	}
}

// markedFlow is the one-connection fixture of the counting tests.
func markedFlow(key string) *config.FlowFact {
	return trafficFlow(key, func(f *config.FlowFact) { f.ApplicationID, f.Application, f.Domain = "com.a.app", "A", "m.example.com" })
}

func TestTrafficFlowMarksCountOnceAcrossRestartAndReplay(t *testing.T) {
	h := newTrafficHarness(t)
	f := markedFlow("s/1/m")
	answered := withFact(f, func(c *config.FlowFact) { c.ResponseKnown, c.ResponseMS = true, 50 })
	h.append(trafficEpoch.Add(5*time.Second), true,
		h.obs(0, "started", f, 0, 0), h.obs(time.Second, "ready", answered, 10, 20), h.obs(2*time.Second, "sample", answered, 5, 5))
	h.settle()
	up, down, conns, _ := h.counters("application")
	if up != 15 || down != 25 || conns != 1 {
		t.Fatalf("after first commit: up %d down %d conns %d", up, down, conns)
	}
	if m := h.flowMarks()["s/1/m"]; !m.Started || !m.Responded || m.Failed || m.Closed {
		t.Fatalf("mark %+v", m)
	}
	// A restart resumes at the committed cursor: nothing is read twice.
	h.restart()
	h.append(trafficEpoch.Add(10*time.Second), true, h.obs(6*time.Second, "sample", answered, 1, 1))
	h.settle()
	up, down, conns, _ = h.counters("application")
	if up != 16 || down != 26 || conns != 1 {
		t.Fatalf("after restart: up %d down %d conns %d", up, down, conns)
	}
	// Even a full replay of the journal cannot count the one-shot events again: the marks hold.
	if err := h.store.Update(context.Background(), func(tx *historystore.TrafficTx) error {
		return tx.SetCursor(context.Background(), trafficCursorName, historystore.Cursor{})
	}); err != nil {
		t.Fatal(err)
	}
	h.restart()
	h.settle()
	_, _, conns, _ = h.counters("application")
	var latencies uint64
	for _, r := range h.rows("traffic_5m") {
		if r.Dimension == "application" && len(r.LatencyHist) > 0 {
			v, err := decodeTrafficRow(r)
			if err != nil {
				t.Fatal(err)
			}
			latencies += histogramCount(v.Latency)
		}
	}
	if conns != 1 || latencies != 1 {
		t.Fatalf("replay recounted: conns %d latencies %d", conns, latencies)
	}
}

func TestTrafficFlowMarkLifecycle(t *testing.T) {
	h := newTrafficHarness(t)
	open, done := markedFlow("s/1/open"), markedFlow("s/1/done")
	h.clock = trafficEpoch.Add(time.Hour)
	h.append(trafficEpoch, true,
		h.obs(0, "started", open, 1, 1), h.obs(0, "started", done, 1, 1), h.obs(time.Second, "closed", withClosed(done, trafficEpoch.Add(time.Second)), 0, 0))
	h.settle()
	if marks := h.flowMarks(); len(marks) != 2 || !marks["s/1/done"].Closed || marks["s/1/open"].Closed {
		t.Fatalf("marks %+v", marks)
	}
	other := markedFlow("s/1/other")
	h.append(trafficEpoch.Add(9*time.Minute), true, h.obs(9*time.Minute, "started", other, 0, 0))
	h.settle()
	if marks := h.flowMarks(); len(marks) != 3 {
		t.Fatalf("a closed mark must outlive 10 minutes only after the observation clock passes them: %+v", marks)
	}
	h.append(trafficEpoch.Add(11*time.Minute+time.Second), true, h.obs(11*time.Minute+time.Second, "sample", other, 1, 1))
	h.settle()
	marks := h.flowMarks()
	if _, ok := marks["s/1/done"]; ok || len(marks) != 2 {
		t.Fatalf("closed mark not removed 10 minutes after its last record: %+v", marks)
	}
	h.clock = trafficEpoch.Add(26 * time.Hour)
	h.append(trafficEpoch.Add(25*time.Hour), true, h.obs(25*time.Hour, "sample", other, 1, 1))
	h.settle()
	marks = h.flowMarks()
	if _, ok := marks["s/1/open"]; ok || len(marks) != 1 {
		t.Fatalf("never-closed mark not removed after 24 hours: %+v", marks)
	}
}

func TestTrafficRetentionAndCapacity(t *testing.T) {
	h := newTrafficHarness(t)
	f := markedFlow("s/1/r")
	h.clock = trafficEpoch.Add(10 * time.Minute)
	h.append(trafficEpoch, true, h.obs(0, "started", f, 4, 4))
	h.settle()
	if n := len(h.rows("recent_cuts")); n == 0 {
		t.Fatal("recent_cuts empty after the commit")
	}
	h.clock = trafficEpoch.Add(21 * time.Minute) // past the 20 minute window
	h.settle()
	if n := len(h.rows("recent_cuts")); n != 0 {
		t.Fatalf("recent_cuts kept %d rows past 20 minutes", n)
	}
	if n := len(h.rows("traffic_5m")); n == 0 {
		t.Fatal("traffic_5m must survive the recent window")
	}
	h.clock = trafficEpoch.Add(31 * 24 * time.Hour)
	h.settle()
	if n := len(h.rows("traffic_5m")); n != 0 {
		t.Fatalf("traffic_5m kept %d rows past 30 days", n)
	}
	// Capacity: past the cap the oldest buckets go and the flag is set, but writes are never refused.
	h.clock = trafficEpoch.Add(time.Minute)
	h.append(trafficEpoch.Add(time.Minute), true, h.obs(time.Minute, "sample", f, 9, 9))
	h.settle()
	h.tr.capBytes = 1
	h.settle()
	if n := len(h.rows("traffic_5m")); n != 0 {
		t.Fatalf("eviction left %d rows", n)
	}
	if trafficMeta(t, h.store.Traffic, historystore.MetaCapacityEvicted) != "1" || trafficMeta(t, h.store.Traffic, historystore.MetaPartial) != "1" {
		t.Fatal("capacity_evicted and partial must be set")
	}
	h.tr.capBytes = trafficCapacityBytes
	h.append(trafficEpoch.Add(2*time.Minute), true, h.obs(2*time.Minute, "sample", f, 3, 3))
	h.settle()
	if up, _, _, _ := h.counters("application"); up != 3 {
		t.Fatalf("a write after eviction was refused or lost: %d", up)
	}
}

func TestTrafficBacksOffKeepsCursorAndRecoversWithoutDoubleCounting(t *testing.T) {
	h := newTrafficHarness(t)
	f := markedFlow("s/1/b")
	h.append(trafficEpoch, true, h.obs(0, "started", f, 10, 10))
	var delays []time.Duration
	for range 4 {
		h.store.fail = 1 // the transaction fails; the marker write after it succeeds
		delays = append(delays, h.tr.step(context.Background()))
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if !slices.Equal(delays, want) {
		t.Fatalf("backoff %v want %v", delays, want)
	}
	if _, ok, err := h.store.Cursor(context.Background(), trafficCursorName); err != nil || ok {
		t.Fatalf("cursor advanced by a failed transaction: %v %v", ok, err)
	}
	if cursors := h.consumers.snapshot(); len(cursors) != 1 || !cursors[0].IsZero() {
		t.Fatalf("consumer cursor advanced after a failure: %+v", cursors)
	}
	if len(h.rows("traffic_5m")) != 0 || len(h.flowMarks()) != 0 {
		t.Fatal("a rolled-back transaction left rows or marks")
	}
	if trafficMeta(t, h.store.Traffic, historystore.MetaPartial) != "1" || trafficMeta(t, h.store.Traffic, historystore.MetaErrorCode) != trafficErrorStore {
		t.Fatal("failure must mark only traffic degraded")
	}
	h.settle()
	if up, down, conns, _ := h.counters("application"); up != 10 || down != 10 || conns != 1 {
		t.Fatalf("retry counted up %d down %d conns %d", up, down, conns)
	}
	if trafficMeta(t, h.store.Traffic, historystore.MetaErrorCode) != "" {
		t.Fatal("error code not cleared after recovery")
	}
	cursors := h.consumers.snapshot()
	if len(cursors) != 1 || cursors[0].IsZero() {
		t.Fatalf("consumer not advanced after commit: %+v", cursors)
	}
	if d := h.tr.step(context.Background()); d != trafficPollInterval {
		t.Fatalf("backoff did not reset: %v", d)
	}
}

func TestTrafficBatchesAtMostOneHundredTwentyEightRecords(t *testing.T) {
	h := newTrafficHarness(t)
	f := markedFlow("s/1/big")
	records := make([]config.Observation, 0, 2500)
	for range 2500 {
		records = append(records, h.obs(time.Second, "sample", f, 1, 0))
	}
	h.append(trafficEpoch, true, records...)
	before := h.store.updates
	if d := h.tr.step(context.Background()); d != 0 {
		t.Fatalf("first step of a 2500-record backlog returned %v, want an immediate continue", d)
	}
	if up, _, _, _ := h.counters("application"); up != 128 {
		t.Fatalf("first transaction committed %d records, want 128", up)
	}
	h.settle()
	if up, _, _, _ := h.counters("application"); up != 2500 || h.store.updates-before < 20 {
		t.Fatalf("up %d updates %d", up, h.store.updates-before)
	}
}

func TestTrafficJobGoroutineFlushAndStop(t *testing.T) {
	h := newTrafficHarness(t)
	f := markedFlow("s/1/g")
	h.append(trafficEpoch, true, h.obs(0, "started", f, 2, 2))
	h.tr.start(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.tr.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if up, _, conns, _ := h.counters("application"); up != 2 || conns != 1 {
		t.Fatalf("flush returned before the commit: up %d conns %d", up, conns)
	}
	h.append(trafficEpoch.Add(time.Second), true, h.obs(time.Second, "sample", f, 3, 3))
	if err := h.tr.stop(ctx); err != nil { // stop flushes first
		t.Fatal(err)
	}
	store, _, err := historystore.OpenTraffic(ctx, h.dir, uint32(osEuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	var up int64
	for _, r := range readTrafficTable(t, store, "traffic_5m") {
		if r.Dimension == "application" {
			up += r.UpBytes
		}
	}
	if up != 5 {
		t.Fatalf("stop did not commit the last batch: %d", up)
	}
	if len(h.consumers.snapshot()) != 0 {
		t.Fatal("stop must release the consumer slot")
	}
}

func TestLogJobIgnoresPollRowsButConsumesThem(t *testing.T) {
	h := newLogsHarness(t)
	poll := journalPoll{Session: "s", Collecting: true, AtMs: logsEpoch.UnixMilli()}.observation()
	if err := h.journal.Append(context.Background(), logsEpoch, []historystore.Observation{poll}, historystore.Cursor{}); err != nil {
		t.Fatal(err)
	}
	h.settle()
	summaries, events, _ := readLogState(t, h.logs)
	if len(summaries) != 0 || len(events) != 0 {
		t.Fatalf("a poll row produced log data: %d summaries %d events", len(summaries), len(events))
	}
	cursor, ok, err := h.logs.Cursor(context.Background(), logsCursorName)
	if err != nil || !ok || cursor.Session != poll.Session {
		t.Fatalf("poll row not consumed: %+v %v %v", cursor, ok, err)
	}
}

func TestTrafficFlushFailsWhileBackoffIsAtItsCap(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	h.append(trafficEpoch, true, h.obs(0, "started", markedFlow("s/1/cap"), 10, 10))
	h.store.fail = 1000
	var last time.Duration
	for range 8 {
		last = h.tr.step(ctx)
	}
	if last != trafficPollInterval {
		t.Fatalf("backoff did not reach the cap that equals the idle interval: %v", last)
	}
	err := h.tr.execute(ctx, trafficCommand{kind: trafficCommandFlush})
	if err == nil {
		t.Fatal("flush reported success while the journal could not be committed")
	}
	if _, ok, cursorErr := h.store.Cursor(ctx, trafficCursorName); cursorErr != nil || ok {
		t.Fatalf("cursor advanced by failed flush: %v %v", ok, cursorErr)
	}
	h.store.fail = 0
	if err = h.tr.execute(ctx, trafficCommand{kind: trafficCommandFlush}); err != nil {
		t.Fatalf("flush after recovery: %v", err)
	}
	if up, _, _, _ := h.counters("application"); up != 10 {
		t.Fatalf("recovered flush counted %d", up)
	}
}

func TestTrafficFlushFailsWhileDegradedMetadataCannotBeCleared(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	if err := h.store.Traffic.Update(ctx, func(tx *historystore.TrafficTx) error {
		return tx.SetMeta(ctx, historystore.MetaErrorCode, trafficErrorStore)
	}); err != nil {
		t.Fatal(err)
	}
	h.tr.down = true // the journal is empty: only the stale error marker is left to clear
	h.store.fail = 1000
	if err := h.tr.execute(ctx, trafficCommand{kind: trafficCommandFlush}); err == nil {
		t.Fatal("flush reported success while the degraded marker could not be cleared")
	}
	h.store.fail = 0
	if err := h.tr.execute(ctx, trafficCommand{kind: trafficCommandFlush}); err != nil {
		t.Fatalf("flush after the metadata write recovered: %v", err)
	}
	if trafficMeta(t, h.store.Traffic, historystore.MetaErrorCode) != "" || h.tr.down {
		t.Fatal("error marker not cleared after recovery")
	}
}
