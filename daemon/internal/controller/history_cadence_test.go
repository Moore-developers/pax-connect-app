package controller

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/historystore"
)

// The protocol rules R2, R3, R4, R8 and R9 for the history domains (protocol.md section 4.2): idle
// polls never move a version, deletion moves the generation, restarts never go back.

func (h *trafficHarness) appendRows(at time.Time, rows ...historystore.Observation) {
	h.t.Helper()
	if err := h.journal.Append(context.Background(), at, rows, historystore.Cursor{Session: "s", Seq: h.seq}); err != nil {
		h.t.Fatal(err)
	}
}

func TestTrafficSpanPollMarksTheWholeRange(t *testing.T) {
	h := newTrafficHarness(t)
	base := time.Date(2026, 10, 26, 8, 10, 0, 0, time.UTC)
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }
	h.clock = at(600)
	span := func(from, to int, collecting bool) historystore.Observation {
		return journalPoll{Session: "s", Collecting: collecting, FromMs: at(from).UnixMilli(), AtMs: at(to).UnixMilli()}.observation()
	}
	h.appendRows(at(130), span(10, 130, true))
	h.appendRows(at(200), span(150, 200, false)) // not collecting: proves nothing
	h.appendRows(at(260), span(250, 250, true))  // empty range: proves nothing
	h.settle()
	var want []int
	for s := 10; s < 130; s++ {
		want = append(want, s)
	}
	if got := coverageSet(t, readCoverage(t, h.store.Traffic)); !slices.Equal(got, want) {
		t.Fatalf("coverage seconds %v want %v", got, want)
	}
}

func TestTrafficRevisionMovesOnlyForStoredObservations(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	signals := 0
	h.tr.changed = func() { signals++ }
	revision := func() string { return trafficMeta(t, h.store.Traffic, "traffic_revision") }
	at := h.clock

	// Poll rows of both forms and a gap marker are bookkeeping: no version, no signal.
	h.appendPoll(at, "s", true)
	h.appendRows(at, journalPoll{Session: "s", Collecting: true, FromMs: at.Add(-time.Minute).UnixMilli(), AtMs: at.UnixMilli()}.observation())
	h.appendGap(at, journalGap{Reason: gapLost, Session: "s"})
	h.settle()
	if got := revision(); got != "" && got != "0" {
		t.Fatalf("poll and gap rows moved traffic_revision to %q", got)
	}
	signals = 0
	for range 3 { // idle passes
		h.tr.step(ctx)
	}
	if signals != 0 || h.tr.state.revision != 0 {
		t.Fatalf("idle passes: %d publisher signals, revision %d", signals, h.tr.state.revision)
	}

	// A stored observation moves it once, in the same transaction as the counters.
	h.append(at, true, h.obs(time.Second, "started", markedFlow("s/1/rev"), 10, 10))
	h.settle()
	if got := revision(); got != "1" || h.tr.state.revision != 1 || signals == 0 {
		t.Fatalf("observation: revision %q state %d signals %d", got, h.tr.state.revision, signals)
	}
	h.appendPoll(at.Add(5*time.Second), "s", true)
	h.settle()
	if got := revision(); got != "1" {
		t.Fatalf("a later poll moved traffic_revision to %q", got)
	}

	// It never goes back across a restart.
	h.restart()
	if h.tr.state.revision != 1 {
		t.Fatalf("revision after restart %d", h.tr.state.revision)
	}
}

func TestTrafficDeletionMovesGenerationAndRevisionOnceAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	h := newTrafficHarness(t)
	f := markedFlow("s/1/old")
	h.append(trafficEpoch.Add(time.Minute), true,
		h.obs(0, "sample", f, 1, 1), h.obs(10*time.Minute, "sample", f, 1, 1), h.obs(20*time.Minute, "sample", f, 1, 1))
	h.settle()
	if h.tr.state.deletion != 0 || h.tr.state.revision != 1 {
		t.Fatalf("before deletion: generation %d revision %d", h.tr.state.deletion, h.tr.state.revision)
	}
	routine := h.tr.state.deletion
	// Routine compaction of the 5-second tail is not a deletion of stored traffic.
	h.clock = h.clock.Add(time.Hour)
	h.tr.step(ctx)
	if h.tr.state.deletion != routine {
		t.Fatalf("tail pruning moved the generation to %d", h.tr.state.deletion)
	}

	h.clock = h.clock.Add(trafficRetention + 24*time.Hour) // the aggregates are past retention
	h.tr.step(ctx)
	if h.tr.state.deletion != 1 || h.tr.state.revision != 2 {
		t.Fatalf("expiry: generation %d revision %d, want 1 and 2", h.tr.state.deletion, h.tr.state.revision)
	}
	if m := h.tr.meta.Load(); m == nil || m.TrafficDeletionGeneration != 1 || m.TrafficRevision != 2 {
		t.Fatalf("published meta %+v", m)
	}
	if got := trafficMeta(t, h.store.Traffic, trafficMetaDeletion); got != "1" {
		t.Fatalf("stored generation %q", got)
	}
	h.tr.step(ctx) // nothing left to delete
	if h.tr.state.deletion != 1 || h.tr.state.revision != 2 {
		t.Fatalf("an empty pass moved them: generation %d revision %d", h.tr.state.deletion, h.tr.state.revision)
	}

	h.restart()
	if h.tr.state.deletion != 1 || h.tr.state.revision != 2 {
		t.Fatalf("after restart: generation %d revision %d", h.tr.state.deletion, h.tr.state.revision)
	}
}

func TestLogMetaSignalsThePublisherOnlyWhenItChanged(t *testing.T) {
	c := New("test", "test")
	drain := func() bool {
		select {
		case <-c.logsChanged:
			return true
		default:
			return false
		}
	}
	m := historystore.LogsMeta{LogsRevision: 1, SettingsRevision: 1}
	c.acceptLogMeta(m)
	if !drain() {
		t.Fatal("first meta must signal")
	}
	c.acceptLogMeta(m)
	if drain() {
		t.Fatal("identical meta signalled the publisher")
	}
	m.LogsRevision = 2
	c.acceptLogMeta(m)
	if !drain() {
		t.Fatal("a new logs revision must signal")
	}
}

func TestHistoryStatusChangesPushADeltaWithoutMovingAnyRevision(t *testing.T) {
	ctx := context.Background()
	c := New("test", "test")
	c.history = &historyOwner{}
	c.logState.Store(&historystore.LogsMeta{LogsRevision: 7, SettingsRevision: 3, RetentionHours: 12})
	c.publishHistory(ctx)
	before, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	c.logState.Store(&historystore.LogsMeta{LogsRevision: 7, SettingsRevision: 3, RetentionHours: 12, Partial: true, ErrorCode: "history_store_failed"})
	c.publishHistory(ctx)
	after, err := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !after.History.Partial || after.History.ErrorCode != "history_store_failed" || after.SnapshotVersion <= before.SnapshotVersion {
		t.Fatalf("status change was not pushed: %v (version %d -> %d)", after.History, before.SnapshotVersion, after.SnapshotVersion)
	}
	if after.History.LogsRevision != before.History.LogsRevision || after.History.TrafficRevision != before.History.TrafficRevision || after.History.RouteHistoryRevision != before.History.RouteHistoryRevision {
		t.Fatalf("a status change moved a revision: %v -> %v", before.History, after.History)
	}
}

func TestHistoryPublishCarriesTheRouteHistoryRevision(t *testing.T) {
	ctx := context.Background()
	c := New("test", "test")
	c.history = &historyOwner{}
	c.data = &routeData{}
	c.data.revision.Store(5)
	c.publishHistory(ctx)
	first, _ := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if first.History.RouteHistoryRevision != 5 {
		t.Fatalf("route_history_revision %d, want 5", first.History.RouteHistoryRevision)
	}
	c.data.revision.Store(6)
	c.publishHistory(ctx)
	second, _ := c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if second.History.RouteHistoryRevision != 6 || second.SnapshotVersion <= first.SnapshotVersion {
		t.Fatalf("revision change was not pushed: %d at version %d -> %d", second.History.RouteHistoryRevision, first.SnapshotVersion, second.SnapshotVersion)
	}
}

func TestAutoLatencyEveryNewPointMovesRouteHistoryRevisionAndWakesThePublisher(t *testing.T) {
	f := newAutoFixture(t)
	drain := func() bool {
		select {
		case <-f.c.logsChanged:
			return true
		default:
			return false
		}
	}
	drain()
	last := f.c.routeHistoryRevision()
	for i := range 3 {
		if v := f.round(t); v != "recorded" {
			t.Fatalf("round %d: %q", i, v)
		}
		now := f.c.routeHistoryRevision()
		if now != last+1 {
			t.Fatalf("round %d: revision %d -> %d, want one step", i, last, now)
		}
		if !drain() {
			t.Fatalf("round %d did not wake the history publisher", i)
		}
		last = now
	}
	// A restart reads the revision back from the file.
	loaded, err := f.c.data.historyStore.Load()
	if err != nil || loaded.Revision < last {
		t.Fatalf("persisted revision %d < %d (%v)", loaded.Revision, last, err)
	}
}

func TestRouteHistoryTrimOnReadMovesRevisionAndIsPersisted(t *testing.T) {
	f := newAutoFixture(t)
	if v := f.round(t); v != "recorded" {
		t.Fatal(v)
	}
	before := f.c.routeHistoryRevision()
	// Age the point past the 7-day window and let a read trim it.
	if err := f.c.query(context.Background(), func(context.Context) {
		f.c.data.history.Items[0].At = time.Now().Add(-8 * 24 * time.Hour)
	}); err != nil {
		t.Fatal(err)
	}
	if items := f.history(t, "latency_auto"); len(items) != 0 {
		t.Fatalf("expired point still served: %d", len(items))
	}
	after := f.c.routeHistoryRevision()
	if after <= before {
		t.Fatalf("trim did not move the revision: %d -> %d", before, after)
	}
	loaded, err := f.c.data.historyStore.Load()
	if err != nil || len(loaded.Items) != 0 || loaded.Revision < after {
		t.Fatalf("trim not persisted with its revision: %+v (%v)", loaded, err)
	}
	f.history(t, "latency_auto") // an untouched history never moves
	if again := f.c.routeHistoryRevision(); again != after {
		t.Fatalf("an idle read moved the revision %d -> %d", after, again)
	}
}

func TestStoredCaptureRowsReachTheLogJobWithoutWaitingForItsFallbackTimer(t *testing.T) {
	ctx := context.Background()
	dir := newLogsTestDir(t)
	c, _ := newLogsTestController(t, dir)
	core := newFakeCore("S1")
	core.add(testEpoch, "started", flow("a"), 0, 0)
	c.ConfigureCoreSession(pagerSession{core})
	if err := c.ConfigureHistoryIngest(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseHistoryIngest(context.Background()) }()
	l, err := c.logJob()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second) // the log job's own fallback is 60 s
	for {
		cursor, ok, err := l.logs.Cursor(ctx, logsCursorName)
		if err != nil {
			t.Fatal(err)
		}
		if ok && cursor.Session != "" { // the first stored page, possibly ending in its span poll row
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("log job did not consume the stored row in time: %+v %v", cursor, ok)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
