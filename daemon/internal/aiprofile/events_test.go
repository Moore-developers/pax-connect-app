package aiprofile

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ev(key string, cli CLI, at int64, kind, id, name string, multiplier float64) Event {
	return Event{Key: key, CLI: cli, At: at, Kind: kind, Attribution: Attribution{ProfileID: id, ProfileName: name, Multiplier: multiplier}}
}

func TestTimelineResolvesTheProfileAtTheRequestTime(t *testing.T) {
	sec := func(s int64) time.Time { return time.Unix(s, 0) }
	tl := newTimeline([]Event{
		ev("a1", Claude, 100e9, "apply", "p1", "One", 1),
		ev("m1", "", 150e9, "metadata", "p1", "One", 2),
		ev("a2", Claude, 200e9, "apply", "p2", "Two", .5),
		ev("c1", Codex, 120e9, "apply", "p1", "One", 1),
		ev("x", Claude, 50e9, "external", "", "未归属", 1),
	})
	for _, c := range []struct {
		cli    CLI
		at     int64
		id     string
		factor float64
	}{
		{Claude, 10, "p1", 1},   // before the earliest switch: the earliest profile
		{Claude, 100, "p1", 1},  // exactly at the switch
		{Claude, 149, "p1", 1},  // before the multiplier change
		{Claude, 150, "p1", 2},  // the change takes effect
		{Claude, 199, "p1", 2},  // still the old profile
		{Claude, 200, "p2", .5}, // the next switch brings its own multiplier
		{Claude, 9999, "p2", .5},
		{Codex, 1, "p1", 1},
		{Codex, 9999, "p1", 2}, // the multiplier of a profile applies to every CLI using it
	} {
		got := tl.Resolve(c.cli, sec(c.at))
		if got.ProfileID != c.id || got.Multiplier != c.factor {
			t.Fatalf("%s at %d: %+v", c.cli, c.at, got)
		}
	}
	if got := tl.Resolve(DSH, sec(150)); got.ProfileID != "" || got.Multiplier != 1 {
		t.Fatalf("DSH has no profile: %+v", got)
	}
	if got := newTimeline(nil).Resolve(Claude, sec(150)); got.ProfileID != "" || got.ProfileName != "" || got.Multiplier != 1 {
		t.Fatalf("no events means an empty profile: %+v", got)
	}
}

func TestAppliedProfileAndPriceChangeEnterTheTimelineAndSurviveRestart(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	ctx := context.Background()
	plan, e := m.Plan(ctx, PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(ctx, ApplyRequest{PlanID: plan.ID, OperationID: "apply-events"}); e != nil {
		t.Fatal(e)
	}
	applied := time.Now()
	p.Multiplier = 2
	if _, e = m.Mutate(ctx, Mutation{Action: "save", OperationID: "price-events", ExpectedRevision: p.Revision, Profile: p}); e != nil {
		t.Fatal(e)
	}
	check := func(m *Manager) {
		tl, e := m.Timeline(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if a := tl.Resolve(Claude, applied); a.ProfileID != p.ID || a.Multiplier != 1 {
			t.Fatalf("at apply time: %+v", a)
		}
		if a := tl.Resolve(Claude, time.Now()); a.ProfileID != p.ID || a.Multiplier != 2 {
			t.Fatalf("after the price change: %+v", a)
		}
		if a := tl.Resolve(Claude, applied.Add(-time.Hour)); a.ProfileID != p.ID {
			t.Fatalf("before the first switch uses the earliest profile: %+v", a)
		}
	}
	check(m)
	// A foreign writer changing the CLI file no longer creates an ownership event.
	if e = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://external.test","ANTHROPIC_AUTH_TOKEN":"redacted"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Query(ctx); e != nil {
		t.Fatal(e)
	}
	var external int
	if e = m.events.QueryRowContext(ctx, "SELECT count(*) FROM profile_events WHERE kind='external'").Scan(&external); e != nil || external != 0 {
		t.Fatalf("external events: %d %v", external, e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close() }()
	check(reopened)
}

func TestFailedApplyDoesNotInventHistoricalOwnership(t *testing.T) {
	m, _ := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	p.BaseURL = "https://next.test"
	ctx := context.Background()
	w, e := m.Mutate(ctx, Mutation{Action: "save", OperationID: "edit-before-fail", ExpectedRevision: p.Revision, Profile: p})
	if e != nil {
		t.Fatal(e)
	}
	plan, e := m.Plan(ctx, PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: w.Profiles[0].Revision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	m.renameHook = func(int) error { return os.ErrPermission }
	if _, e = m.Apply(ctx, ApplyRequest{PlanID: plan.ID, OperationID: "fail-events"}); e == nil {
		t.Fatal("failed apply accepted")
	}
	tl, e := m.Timeline(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if a := tl.Resolve(Claude, time.Now()); a.ProfileID != "" || a.Multiplier != 1 {
		t.Fatalf("failed application became ownership: %+v", a)
	}
}

func TestHistorySeedsAreExactIdempotentAndSortFirst(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	ctx := context.Background()
	plan, e := m.Plan(ctx, PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(ctx, ApplyRequest{PlanID: plan.ID, OperationID: "apply-seeded"}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Query(ctx); e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	h, e := OpenHistory(filepath.Join(home, "state"), uint32(os.Geteuid()), false)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = h.Close() }()
	if _, e = h.PlanSeeds(ctx, []Seed{{Claude, "Original ", time.Now()}}); e == nil {
		t.Fatal("a name that is not exact was accepted")
	}
	old := time.Now().Add(-48 * time.Hour)
	seeds := []Seed{{Claude, "Original", old}, {Codex, "Original", old}}
	planned, e := h.PlanSeeds(ctx, seeds)
	if e != nil || len(planned) != 2 {
		t.Fatalf("plan: %+v %v", planned, e)
	}
	// Planning alone writes nothing but still yields a usable timeline.
	tl, e := h.Timeline(ctx, planned)
	if e != nil {
		t.Fatal(e)
	}
	if a := tl.Resolve(Codex, time.Now()); a.ProfileID != p.ID {
		t.Fatalf("seeded codex: %+v", a)
	}
	again, e := h.PlanSeeds(ctx, seeds)
	if e != nil || len(again) != 2 {
		t.Fatalf("planning must not write: %+v %v", again, e)
	}
	for range 2 {
		planned, e = h.PlanSeeds(ctx, seeds)
		if e != nil {
			t.Fatal(e)
		}
		if e = h.Write(ctx, planned); e != nil {
			t.Fatal(e)
		}
	}
	if left, e := h.PlanSeeds(ctx, seeds); e != nil || len(left) != 0 {
		t.Fatalf("seeds repeat: %+v %v", left, e)
	}
	var n int
	if e = h.m.events.QueryRowContext(ctx, "SELECT count(*) FROM profile_events WHERE event_key LIKE 'seed:%'").Scan(&n); e != nil || n != 2 {
		t.Fatalf("seed rows: %d %v", n, e)
	}
	tl, e = h.Timeline(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if a := tl.Resolve(Claude, old.Add(-time.Hour)); a.ProfileID != p.ID {
		t.Fatalf("before the seed: %+v", a)
	}
	// A seed later than a real switch still sorts before it, so the real switch wins.
	late := Seed{Claude, "Original", time.Now().Add(time.Hour)}
	if e = h.m.events.QueryRowContext(ctx, "DELETE FROM profile_events WHERE event_key='seed:claude' RETURNING 1").Scan(&n); e != nil {
		t.Fatal(e)
	}
	planned, e = h.PlanSeeds(ctx, []Seed{late})
	if e != nil || len(planned) != 1 {
		t.Fatal(planned, e)
	}
	var first int64
	if e = h.m.events.QueryRowContext(ctx, "SELECT min(at) FROM profile_events WHERE cli='claude' AND kind='apply'").Scan(&first); e != nil || planned[0].At >= first {
		t.Fatalf("seed %d must precede %d", planned[0].At, first)
	}
}

func TestHistoryRefusesWhileAnotherInstanceHoldsTheStore(t *testing.T) {
	m, home := claudeFixture(t)
	if _, e := OpenHistory(filepath.Join(home, "state"), uint32(os.Geteuid()), false); e == nil {
		t.Fatal("opened while the daemon's manager holds the lock")
	}
	_ = m
}

func TestUsageOnlyDSHRejectsWritesWithoutReadingConfiguration(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), UsageOnlyDSH: true})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	ctx := context.Background()
	w, e := m.Query(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if w.Bindings[2].State != "read_only" {
		t.Fatalf("DSH not read-only: %+v", w.Bindings)
	}
	if _, e = m.Mutate(ctx, Mutation{Action: "import", CLI: DSH, OperationID: "forbidden"}); e != Unsupported {
		t.Fatal("DSH import accepted")
	}
	if _, e = m.Plan(ctx, PlanRequest{CLI: DSH}); e != Unsupported {
		t.Fatal("DSH plan accepted")
	}
	if _, e = os.Stat(filepath.Join(home, ".dsh")); !os.IsNotExist(e) {
		t.Fatal("DSH config was created")
	}
}
