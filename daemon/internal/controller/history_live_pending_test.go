package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func liveBatch(records ...config.Observation) config.ObservationBatch {
	return config.ObservationBatch{Session: "s", Collecting: true, Records: records}
}

func TestLiveKeepsPerFlowWindowAndClosedFlowsForAMinute(t *testing.T) {
	h := newTrafficHarness(t)
	live := newLiveState()
	a, b := markedFlow("s/1/a"), markedFlow("s/1/b")
	now := trafficEpoch.Add(30 * time.Second)
	live.apply(liveBatch(
		h.obs(0, "started", a, 10, 100),
		h.obs(7*time.Second, "sample", a, 20, 200),
		h.obs(8*time.Second, "started", b, 1, 1),
		h.obs(12*time.Second, "closed", withClosed(b, trafficEpoch.Add(12*time.Second)), 0, 0)), now)
	flows := live.Flows(now)
	if len(flows) != 2 || flows[0].Fact.Key != "s/1/a" || flows[0].Closed || !flows[1].Closed {
		t.Fatalf("flows %+v", flows)
	}
	var up, down uint64
	for _, p := range flows[0].Window {
		up, down = up+p.Upload, down+p.Download
	}
	if up != 30 || down != 300 || flows[0].Upload != 30 {
		t.Fatalf("window totals %d/%d, flow %d", up, down, flows[0].Upload)
	}
	// 10 s of records land in 5 s points: the 0 s and 7 s samples sit in different points.
	nonzero := 0
	for _, p := range flows[0].Window {
		if p != (livePoint{}) {
			nonzero++
		}
	}
	if nonzero != 2 {
		t.Fatalf("window %v", flows[0].Window)
	}
	// Snapshots are copies: later records do not change them.
	live.apply(liveBatch(h.obs(20*time.Second, "sample", a, 99, 99)), now.Add(time.Second))
	if flows[0].Upload != 30 {
		t.Fatal("snapshot changed after the fact")
	}
	// A closed flow idle for more than 60 s is dropped; the active one stays and its old points age out.
	late := trafficEpoch.Add(100 * time.Second)
	live.apply(liveBatch(), late)
	flows = live.Flows(late)
	if len(flows) != 1 || flows[0].Fact.Key != "s/1/a" {
		t.Fatalf("closed flow not dropped: %+v", flows)
	}
	for _, p := range flows[0].Window {
		if p != (livePoint{}) {
			t.Fatalf("points older than 60 s must be gone: %v", flows[0].Window)
		}
	}
	if live.Snapshot(late).Active != 1 {
		t.Fatal("the active count must not include closed flows")
	}
}

func TestLiveFlowViewsShareNoPointersWithTheirSource(t *testing.T) {
	h := newTrafficHarness(t)
	live := newLiveState()
	closedAt, until := trafficEpoch.Add(3*time.Second), trafficEpoch.Add(4*time.Second)
	f := withFact(markedFlow("s/1/p"), func(c *config.FlowFact) {
		c.ClosedAt, c.ObservedUntil = &closedAt, &until
		c.DNS = &config.DNSFact{QueryType: "A", Result: "ok", Answers: []string{"1.1.1.1", "2.2.2.2"}}
	})
	now := trafficEpoch.Add(10 * time.Second)
	live.apply(liveBatch(h.obs(3*time.Second, "closed", f, 1, 1)), now)
	view := live.Flows(now)[0].Fact
	// The source (and, through it, the state) changes after the view was taken.
	closedAt, until = closedAt.Add(time.Hour), until.Add(time.Hour)
	f.DNS.Answers[0], f.DNS.Result = "9.9.9.9", "failed"
	for _, flow := range live.flows {
		flow.Fact.DNS.Answers[1] = "8.8.8.8"
	}
	for _, flow := range live.closed {
		flow.Fact.DNS.Answers[1] = "8.8.8.8"
	}
	if !view.ClosedAt.Equal(trafficEpoch.Add(3*time.Second)) || !view.ObservedUntil.Equal(trafficEpoch.Add(4*time.Second)) {
		t.Fatalf("view times follow the source: %v %v", view.ClosedAt, view.ObservedUntil)
	}
	if view.DNS.Result != "ok" || view.DNS.Answers[0] != "1.1.1.1" || view.DNS.Answers[1] != "2.2.2.2" {
		t.Fatalf("view DNS follows the source: %+v", view.DNS)
	}
}

func TestLivePendingDeltaMatchesTrafficJobAndDropsAfterCommit(t *testing.T) {
	h := newTrafficHarness(t)
	live := newLiveState()
	h.tr.live = func() *liveState { return live }
	f := markedFlow("s/1/p")
	answered := withFact(f, func(c *config.FlowFact) { c.ResponseKnown, c.ResponseMS = true, 40 })
	direct := trafficFlow("s/1/d", func(c *config.FlowFact) { c.Direction, c.RouteID, c.RouteName = "direct", "direct", "直连" })
	lost := trafficFlow("s/1/n", func(c *config.FlowFact) { c.Direction, c.RouteID, c.RouteName = "unknown", "", "" })
	dns := trafficFlow("s/1/dns", func(c *config.FlowFact) { c.Purpose, c.Direction, c.RouteID = "dns", "direct", "direct" })
	records := []config.Observation{
		h.obs(0, "started", f, 10, 100), h.obs(time.Second, "ready", answered, 5, 50), h.obs(2*time.Second, "sample", answered, 1, 1),
		h.obs(3*time.Second, "started", direct, 7, 70), h.obs(4*time.Second, "started", lost, 3, 30),
		h.obs(5*time.Second, "started", dns, 9, 9), h.obs(6*time.Second, "closed", withClosed(answered, trafficEpoch.Add(6*time.Second)), 0, 0),
	}
	live.apply(liveBatch(records...), trafficEpoch.Add(10*time.Second))
	h.append(trafficEpoch.Add(10*time.Second), true, records...)
	want := trafficPending{Upload: 16 + 7 + 3, Download: 151 + 70 + 30, Proxied: 167, Direct: 77, Unclassified: 33, Connections: 3}
	if got := live.Pending(); got != want {
		t.Fatalf("pending %+v want %+v", got, want)
	}
	// The job counts the same bytes and starts for the same flows: route dimension sums equal the pending totals.
	h.settle()
	var up, down, conns int64
	for _, r := range h.rows("traffic_5m") {
		if r.Dimension == "route" {
			up, down, conns = up+r.UpBytes, down+r.DownBytes, conns+r.NewConns
		}
	}
	if uint64(up) != want.Upload || uint64(down) != want.Download || uint64(conns) != want.Connections {
		t.Fatalf("job committed %d/%d/%d, pending said %+v", up, down, conns, want)
	}
	if got := live.Pending(); got != (trafficPending{}) {
		t.Fatalf("pending not dropped after the commit: %+v", got)
	}
	// Records after the commit stay pending until the next one.
	more := h.obs(20*time.Second, "sample", answered, 2, 2)
	live.apply(liveBatch(more), trafficEpoch.Add(21*time.Second))
	if got := live.Pending(); got.Upload != 2 || got.Connections != 0 {
		t.Fatalf("pending %+v", got)
	}
}

func TestLivePendingFoldsOldEntriesAndRestoresFromJournal(t *testing.T) {
	h := newTrafficHarness(t)
	f := markedFlow("s/1/o")
	old := h.obs(0, "started", f, 4, 4)
	fresh := h.obs(40*time.Minute, "sample", f, 6, 6)
	live := newLiveState()
	live.apply(liveBatch(old, fresh), trafficEpoch.Add(41*time.Minute))
	got := live.Pending()
	if !got.Partial || got.Upload != 10 || got.Connections != 1 || len(live.pending.entries) != 1 {
		t.Fatalf("pending %+v entries %d: entries older than 30 minutes fold into totals and report partial", got, len(live.pending.entries))
	}
	live.commitTraffic(historystore.Cursor{Session: "s", Seq: old.Sequence})
	if got = live.Pending(); got.Partial || got.Upload != 6 {
		t.Fatalf("after committing past the folded part: %+v", got)
	}

	// Restart: the journal holds records the new view never saw; they come back through replayPending.
	h.append(trafficEpoch.Add(time.Minute), true, old, fresh)
	restored := newLiveState()
	if err := h.tr.replayPending(context.Background(), restored); err != nil {
		t.Fatal(err)
	}
	if got = restored.Pending(); got.Upload != 10 || got.Connections != 1 {
		t.Fatalf("restored pending %+v", got)
	}
	// A record the view applies itself after the restore is not counted twice, and the restored mark
	// keeps a started flow from counting its start again.
	restored.apply(liveBatch(h.obs(41*time.Minute, "sample", f, 1, 1)), trafficEpoch.Add(42*time.Minute))
	if got = restored.Pending(); got.Upload != 11 || got.Connections != 1 {
		t.Fatalf("after a new record: %+v", got)
	}
	h.tr.live = func() *liveState { return restored }
	h.settle()
	if got = restored.Pending(); got.Upload != 1 {
		t.Fatalf("commit past the journal rows must leave only the newer record: %+v", got)
	}
}

func TestLivePendingEntryCountIsCappedAndFoldedIntoPartialTotals(t *testing.T) {
	h := newTrafficHarness(t)
	f := markedFlow("s/1/n")
	live := newLiveState()
	now := trafficEpoch.Add(time.Second)
	total := pendingMaxEntries + 123
	records := make([]config.Observation, 0, total)
	for range total {
		records = append(records, h.obs(0, "sample", f, 1, 2))
	}
	live.apply(liveBatch(records...), now)
	if n := len(live.pending.entries); n > pendingMaxEntries || n == 0 {
		t.Fatalf("entries %d, want at most %d", n, pendingMaxEntries)
	}
	got := live.Pending()
	if !got.Partial || got.Upload != uint64(total) || got.Download != uint64(2*total) {
		t.Fatalf("folded entries must stay in the totals and report partial: %+v", got)
	}
	// Committing past the folded part drops it again.
	live.commitTraffic(historystore.Cursor{Session: "s", Seq: records[total-1].Sequence})
	if got = live.Pending(); got != (trafficPending{}) || len(live.pending.entries) != 0 {
		t.Fatalf("pending after commit: %+v", got)
	}
}

func TestIngestEmptyReadsWriteNothingAndAnIdleSpanIsFlushedOncePerMinute(t *testing.T) {
	h := newIngestHarness(t)
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	h.settle()
	start := h.clock
	appends := len(h.journal.appends)
	for i := 1; i < 12; i++ { // 5 s .. 55 s of empty reads
		h.clock = start.Add(time.Duration(i) * 5 * time.Second)
		h.in.step(context.Background())
	}
	if len(h.journal.appends) != appends || len(h.pollRows()) != 0 {
		t.Fatalf("empty reads wrote to the journal: appends %d -> %d, polls %d", appends, len(h.journal.appends), len(h.pollRows()))
	}
	h.clock = start.Add(ingestCoverageFlush)
	h.in.step(context.Background())
	polls := h.pollRows()
	if len(polls) != 1 || len(h.journal.appends) != appends+1 {
		t.Fatalf("flush wrote %d polls in %d appends", len(polls), len(h.journal.appends)-appends)
	}
	if p := polls[0]; !p.Collecting || p.Session != "S1" || p.FromMs != start.UnixMilli() || p.AtMs != h.clock.UnixMilli() {
		t.Fatalf("span row %+v, want [%d, %d)", p, start.UnixMilli(), h.clock.UnixMilli())
	}
	// The next span starts where the flushed one ended.
	h.clock = h.clock.Add(5 * time.Second)
	h.in.step(context.Background())
	if len(h.journal.appends) != appends+1 {
		t.Fatal("a read right after a flush wrote again")
	}
}

func TestIngestSpanEndsOnCollectingFlipSessionChangeAndLongPause(t *testing.T) {
	h := newIngestHarness(t)
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	h.settle()
	at := h.clock
	step := func(d time.Duration) {
		at = at.Add(d)
		h.clock = at
		h.in.step(context.Background())
	}
	first := at
	step(5 * time.Second)
	step(5 * time.Second)
	h.core.collecting = false // flip: the collecting span [first, first+10 s) is proven now
	step(5 * time.Second)
	polls := h.pollRows()
	if len(polls) != 1 || polls[0].FromMs != first.UnixMilli() || polls[0].AtMs != first.Add(10*time.Second).UnixMilli() || !polls[0].Collecting {
		t.Fatalf("flip did not prove the ended span: %+v", polls)
	}
	h.core.collecting = true
	step(5 * time.Second) // new collecting span starts here
	spanStart := at
	step(5 * time.Second)
	h.core.err = context.DeadlineExceeded
	step(20 * time.Second) // failed read; the pause also exceeds the allowed spacing
	h.core.err = nil
	step(5 * time.Second) // first read after the pause: the old span ends at its last read
	polls = h.pollRows()
	last := polls[len(polls)-1]
	if len(polls) != 2 || last.FromMs != spanStart.UnixMilli() || last.AtMs != spanStart.Add(5*time.Second).UnixMilli() {
		t.Fatalf("a pause did not end the span at its last read: %+v", polls)
	}
}

func TestIngestWakesTheLogJobOnlyForStoredRows(t *testing.T) {
	h := newIngestHarness(t)
	woken := 0
	h.in.onRows = func() { woken++ }
	h.settle()
	if woken != 0 {
		t.Fatalf("seed without rows woke the log job %d times", woken)
	}
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	h.clock = h.clock.Add(5 * time.Second)
	h.settle()
	if woken != 1 {
		t.Fatalf("a stored page woke the log job %d times, want 1", woken)
	}
	h.clock = h.clock.Add(5 * time.Second)
	h.in.step(context.Background())
	if woken != 1 {
		t.Fatalf("an empty read woke the log job (%d)", woken)
	}
}

func (h *ingestHarness) pollRows() []journalPoll {
	h.t.Helper()
	var out []journalPoll
	for _, r := range h.allRows() {
		if r.Kind == journalPollKind {
			out = append(out, decodePoll(h.t, r))
		}
	}
	return out
}

func decodePoll(t *testing.T, row historystore.Observation) journalPoll {
	t.Helper()
	var p journalPoll
	if err := json.Unmarshal(row.Body, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRestorePendingRebasesLiveRecordsOnCommittedCounters(t *testing.T) {
	h := newTrafficHarness(t)
	f := trafficFlow("s/rebase", func(f *config.FlowFact) { f.ResponseKnown = true; f.ResponseMS = 123 })
	first := h.obs(time.Second, "started", f, 10, 90)
	h.append(h.clock, true, first)
	h.settle()
	live := newLiveState()
	sample := h.obs(2*time.Second, "sample", f, 100, 900)
	live.apply(liveBatch(sample), h.clock)
	if !live.pending.entries[0].count.Response {
		t.Fatal("fixture did not exercise unseeded response")
	}
	h.append(h.clock, true, sample)
	if e := h.tr.replayPending(context.Background(), live); e != nil {
		t.Fatal(e)
	}
	live.mu.RLock()
	defer live.mu.RUnlock()
	if len(live.pending.entries) != 1 || live.pending.entries[0].count.Response || live.pending.entries[0].up != 100 {
		t.Fatalf("restore duplicated committed response: %+v", live.pending.entries)
	}
}
