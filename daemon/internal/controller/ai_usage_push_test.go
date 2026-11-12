package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "pax-connect/daemon/gen/proto/paxv1"
	"pax-connect/daemon/internal/aiusage"
)

// aiUsageRig is a controller wired to a real collector over a temp home, with one Watch
// subscriber queue standing in for a stream.
type aiUsageRig struct {
	c     *Controller
	store *aiusage.Store
	log   string
	queue chan *pb.WatchEvent
}

func TestAIUsageNativeRenamePushDoesNotAdvanceAccounting(t *testing.T) {
	r := newAIUsageRig(t, false)
	ctx := context.Background()
	if err := os.WriteFile(r.log, []byte("{\"type\":\"ai-title\",\"sessionId\":\"empty\",\"aiTitle\":\"Original\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	var first *pb.AIUsageState
	for first == nil {
		select {
		case event := <-r.queue:
			if state := event.GetAiUsage(); state != nil && state.CatalogReady {
				first = state
			}
		case <-deadline:
			t.Fatal("catalog first snapshot not pushed")
		}
	}
	if first.Revision != 0 || first.CatalogRevision == 0 {
		t.Fatal(first)
	}
	if err := os.WriteFile(r.log, []byte("{\"type\":\"custom-title\",\"sessionId\":\"empty\",\"customTitle\":\"Renamed\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.store.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	deadline = time.After(3 * time.Second)
	for {
		select {
		case event := <-r.queue:
			state := event.GetAiUsage()
			if state != nil && state.CatalogRevision > first.CatalogRevision {
				if state.Revision != 0 || state.Generation != 0 {
					t.Fatal("native rename advanced accounting", state)
				}
				page, err := NewAIUsageServer(r.store, nil).ListAISessions(ctx, &pb.AISessionCatalogQuery{Query: "Renamed"})
				if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Renamed" {
					t.Fatal(page, err)
				}
				activity := page.Items[0].LastActivityMs
				inside, err := NewAIUsageServer(r.store, nil).ListAISessions(ctx, &pb.AISessionCatalogQuery{FromMs: activity, ToMs: activity + 1})
				if err != nil || inside.Total != 1 || len(inside.Items) != 1 {
					t.Fatal("catalog date lower boundary", inside, err)
				}
				outside, err := NewAIUsageServer(r.store, nil).ListAISessions(ctx, &pb.AISessionCatalogQuery{FromMs: activity + 1, ToMs: activity + 2, Query: "Renamed"})
				if err != nil || outside.Total != 0 || len(outside.Items) != 0 {
					t.Fatal("catalog date filter ignored by RPC", outside, err)
				}
				return
			}
		case <-deadline:
			t.Fatal("rename-only catalog revision not pushed")
		}
	}
}

func newAIUsageRig(t *testing.T, configure bool) *aiUsageRig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(home, ".claude", "projects", "p")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	store, e := aiusage.Open(ctx, aiusage.Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
	if configure {
		if e = store.Configure(ctx); e != nil {
			t.Fatal(e)
		}
	}
	c := New("test", "fp")
	c.ConfigureAIUsage(ctx, store)
	queue := make(chan *pb.WatchEvent, 16)
	c.mu.Lock()
	c.subscribers[queue] = make(chan struct{})
	c.mu.Unlock()
	return &aiUsageRig{c: c, store: store, log: filepath.Join(root, "session.jsonl"), queue: queue}
}

func (r *aiUsageRig) writeSample(t *testing.T) {
	t.Helper()
	sample, e := os.ReadFile("../../../tests/ai-usage/claude-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(r.log, sample, 0600); e != nil {
		t.Fatal(e)
	}
}

func (r *aiUsageRig) next(t *testing.T, within time.Duration) *pb.AIUsageState {
	t.Helper()
	select {
	case ev := <-r.queue:
		if state := ev.GetAiUsage(); state != nil {
			return state
		}
		t.Fatalf("unexpected event %T", ev.Payload)
	case <-time.After(within):
	}
	return nil
}

// settle consumes the pushes of the first (backfill) round until the queue is quiet.
func (r *aiUsageRig) settle(t *testing.T) {
	t.Helper()
	for r.next(t, 1300*time.Millisecond) != nil {
	}
}

func TestAIUsageForegroundIsAnyAIOrDashboardPageOnALiveWatch(t *testing.T) {
	c := New("test", "fp")
	show := func(views ...string) {
		t.Helper()
		if _, e := c.SetViewVisibility(context.Background(), &pb.ViewVisibility{Views: views}); e != nil {
			t.Fatal(e)
		}
	}
	show("ai_overview")
	if c.aiUsageForeground() {
		t.Fatal("no Watch subscriber means everything is hidden")
	}
	queue := make(chan *pb.WatchEvent)
	c.mu.Lock()
	c.subscribers[queue] = make(chan struct{})
	c.mu.Unlock()
	for _, v := range []string{"ai_overview", "ai_logs", "dashboard"} {
		if show(v); !c.aiUsageForeground() {
			t.Fatalf("%s must keep the collector in the foreground", v)
		}
	}
	for _, v := range [][]string{nil, {"traffic", "logs", "ai_profiles"}} {
		if show(v...); c.aiUsageForeground() {
			t.Fatalf("%v must leave it in the background", v)
		}
	}
	show("ai_logs")
	c.mu.Lock()
	delete(c.subscribers, queue)
	c.mu.Unlock()
	if c.aiUsageForeground() {
		t.Fatal("the last subscriber leaving means everything is hidden")
	}
}

func TestOpeningAnAIPageWakesTheSlowCollectorWithoutAnyQueryLease(t *testing.T) {
	r := newAIUsageRig(t, true)
	r.settle(t) // first round done; the background cadence waits two minutes
	r.writeSample(t)
	// Querying never extends a lease: the collector stays on its slow cadence.
	server := NewAIUsageServer(r.store, nil)
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, e := server.QueryAIUsageBuckets(context.Background(), &pb.AIUsageQuery{FromMs: now.Add(-24 * time.Hour).UnixMilli(), ToMs: now.UnixMilli(), Granularity: "hour"}); e != nil {
		t.Fatal(e)
	}
	time.Sleep(100 * time.Millisecond)
	if r.store.State().Revision != 0 {
		t.Fatal("a query must not speed the collector up")
	}
	if _, e := r.c.SetViewVisibility(context.Background(), &pb.ViewVisibility{Views: []string{"ai_overview"}}); e != nil {
		t.Fatal(e)
	}
	state := r.next(t, 3*time.Second)
	if state == nil || state.Revision != 1 {
		t.Fatalf("a newly visible AI page must scan and push at once: %+v", state)
	}
}

func (r *aiUsageRig) appendRequest(t *testing.T, id string) {
	t.Helper()
	line := `{"type":"assistant","sessionId":"s","timestamp":"2025-01-01T10:00:00Z","requestId":"r-` + id + `","message":{"id":"` + id + `","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":10,"output_tokens":5}}}` + "\n"
	f, e := os.OpenFile(r.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = f.Close() }()
	if _, e = f.WriteString(line); e != nil {
		t.Fatal(e)
	}
	if e = r.store.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestAIUsagePushCoalescesToOnePerSecondAndOnlyOnChange(t *testing.T) {
	r := newAIUsageRig(t, false)
	if state := r.next(t, 150*time.Millisecond); state != nil {
		t.Fatalf("no change, no push: %+v", state)
	}
	if e := r.store.Scan(context.Background()); e != nil { // the backfill round
		t.Fatal(e)
	}
	r.settle(t)
	r.appendRequest(t, "a")
	first := r.next(t, time.Second)
	if first == nil || first.Revision != 1 {
		t.Fatalf("insert must push at once: %+v", first)
	}
	started := time.Now()
	r.appendRequest(t, "b")
	r.appendRequest(t, "c")
	if state := r.next(t, 500*time.Millisecond); state != nil {
		t.Fatalf("a second push inside the window: %+v", state)
	}
	merged := r.next(t, 2*time.Second)
	if merged == nil || merged.Revision != 3 {
		t.Fatalf("changes inside the window merge into the latest value: %+v", merged)
	}
	if gap := time.Since(started); gap < 900*time.Millisecond {
		t.Fatalf("pushes must be at least a second apart: %v", gap)
	}
	if e := r.store.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	if state := r.next(t, 1200*time.Millisecond); state != nil {
		t.Fatalf("an empty scan pushes nothing: %+v", state)
	}
}

func TestAIUsageFirstFrameCarriesTheCurrentState(t *testing.T) {
	r := newAIUsageRig(t, false)
	r.writeSample(t)
	if e := r.store.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	r.c.mu.Lock()
	snapshot := r.c.snapshot()
	r.c.mu.Unlock()
	if got := snapshot.GetAiUsage(); got == nil || got.Revision != 1 || got.Generation != 0 {
		t.Fatalf("snapshot ai_usage: %+v", got)
	}
	if New("test", "fp").snapshot().GetAiUsage() != nil {
		t.Fatal("without a collector the snapshot carries no AI usage")
	}
}

// A subscriber uses the last accepted Watch sequence as its cursor. AI changes must
// occupy their own place in that same stream, between snapshots from other domains.
func TestAIUsagePushAdvancesTheSharedWatchCursor(t *testing.T) {
	r := newAIUsageRig(t, false)
	ctx := context.Background()
	if err := r.store.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	r.settle(t)

	r.c.PublishSnapshot(&pb.Snapshot{})
	previous := <-r.queue
	if previous.GetSnapshot() == nil {
		t.Fatalf("expected preceding snapshot, got %T", previous.Payload)
	}
	r.appendRequest(t, "watch-cursor")
	var event *pb.WatchEvent
	select {
	case event = <-r.queue:
	case <-time.After(3 * time.Second):
		t.Fatal("AI change did not reach Watch")
	}
	if event.GetAiUsage().GetRevision() != 1 {
		t.Fatalf("expected the new AI revision, got %v", event)
	}
	if event.Seq != previous.Seq+1 || event.SnapshotVersion != event.Seq {
		t.Errorf("AI event must advance the shared cursor: previous=%d event=%d snapshot=%d", previous.Seq, event.Seq, event.SnapshotVersion)
	}
	current, err := r.c.GetSnapshot(ctx, &pb.GetSnapshotRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if current.SnapshotVersion != previous.Seq+1 || current.GetAiUsage().GetRevision() != 1 {
		t.Errorf("snapshot must include the published AI state and cursor: %v", current)
	}

	r.c.PublishSnapshot(&pb.Snapshot{})
	following := <-r.queue
	if following.GetSnapshot() == nil || following.Seq != previous.Seq+2 || following.Seq != event.Seq+1 || following.SnapshotVersion != following.Seq {
		t.Errorf("following snapshot must continue without duplicate or skipped sequence: previous=%d AI=%d following=%v", previous.Seq, event.Seq, following)
	}
}
