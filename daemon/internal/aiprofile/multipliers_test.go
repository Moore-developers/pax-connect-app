package aiprofile

import (
	"context"
	"database/sql"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMultiplierInputIsRangeAndThreeDecimalChecked(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m, e := New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = m.Close() }()
	for i, c := range []struct {
		name   string
		mult   float64
		models map[string]float64
		ok     bool
	}{
		{"zero", 0, nil, true},
		{"three decimals", 0.265, nil, true},
		{"whole", 1, nil, true},
		{"max", 100, nil, true},
		{"four decimals", 0.1234, nil, false},
		{"float noise", 0.1 + 0.2, nil, true}, // 0.30000000000000004 is 0.3
		{"over", 100.001, nil, false},
		{"negative", -0.5, nil, false},
		{"nan", math.NaN(), nil, false},
		{"inf", math.Inf(1), nil, false},
		{"model ok", 1, map[string]float64{"gpt-6.1-sol": 0.265, "claude-opus-5-5": 0}, true},
		{"model four decimals", 1, map[string]float64{"x": 0.2655}, false},
		{"model over", 1, map[string]float64{"x": 101}, false},
		{"model negative", 1, map[string]float64{"x": -1}, false},
		{"model blank id", 1, map[string]float64{"": 1}, false},
		{"empty map is no map", 1, map[string]float64{}, true},
	} {
		id := "4eb87612-9a70-49d3-88e0-a54d4024526" + string(rune('0'+i%10))
		if i >= 10 {
			id = "5eb87612-9a70-49d3-88e0-a54d4024526" + string(rune('0'+i%10))
		}
		w, e := m.Mutate(context.Background(), Mutation{Action: "save", OperationID: "op-" + c.name, Profile: Profile{ID: id, Name: c.name, BaseURL: "https://api.example.test", Multiplier: c.mult, ModelMultipliers: c.models}, Key: "k"})
		if c.ok != (e == nil) {
			t.Fatalf("%s: accepted=%v err=%v", c.name, e == nil, e)
		}
		if c.ok {
			for _, p := range w.Profiles {
				if p.ID == id && (len(c.models) == 0) != (p.ModelMultipliers == nil) {
					t.Fatalf("%s: an empty map must be stored as none: %v", c.name, p.ModelMultipliers)
				}
			}
		}
	}
}

func TestImportRejectsMoreThanThreeDecimals(t *testing.T) {
	m, _ := claudeFixture(t)
	w, e := m.Query(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Mutate(context.Background(), Mutation{Action: "import", CLI: Claude, InspectionID: w.Bindings[1].InspectionID, OperationID: "import", Profile: Profile{Name: "Original", Multiplier: 0.12345}})
	if e != Invalid {
		t.Fatalf("%v", e)
	}
}

func TestTimelineYieldsPerModelMultipliersAndTheyFollowSnapshots(t *testing.T) {
	tl := newTimeline([]Event{
		{Key: "a", CLI: Claude, At: 100e9, Kind: "apply", Attribution: Attribution{ProfileID: "p1", Multiplier: 1, Models: map[string]float64{"m": 3}}},
		{Key: "m1", At: 150e9, Kind: "metadata", Attribution: Attribution{ProfileID: "p1", Multiplier: 2, Models: map[string]float64{"m": .265}}},
		{Key: "m2", At: 180e9, Kind: "metadata", Attribution: Attribution{ProfileID: "p1", Multiplier: 2}},
	})
	at := func(s int64) Attribution { return tl.Resolve(Claude, time.Unix(s, 0)) }
	for _, c := range []struct {
		at         int64
		m, other   float64
		modelCount int
	}{{120, 3, 1, 1}, {160, .265, 2, 1}, {200, 2, 2, 0}} {
		a := at(c.at)
		if a.ForModel("m") != c.m || a.ForModel("other") != c.other || len(a.Models) != c.modelCount {
			t.Fatalf("at %d: %+v", c.at, a)
		}
	}
}

func TestModelMultipliersPersistInEventsAndSurviveRestart(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	p := w.Profiles[0]
	ctx := context.Background()
	p.Multiplier = 2
	p.ModelMultipliers = map[string]float64{"claude-opus-5-5": 0.265}
	if _, e := m.Mutate(ctx, Mutation{Action: "save", OperationID: "models", ExpectedRevision: p.Revision, Profile: p}); e != nil {
		t.Fatal(e)
	}
	plan, e := m.Plan(ctx, PlanRequest{CLI: Claude, ProfileID: p.ID, Revision: p.Revision + 1, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(ctx, ApplyRequest{PlanID: plan.ID, OperationID: "apply-models"}); e != nil {
		t.Fatal(e)
	}
	check := func(m *Manager) {
		t.Helper()
		tl, e := m.Timeline(ctx)
		if e != nil {
			t.Fatal(e)
		}
		a := tl.Resolve(Claude, time.Now())
		if a.ProfileID != p.ID || a.ForModel("claude-opus-5-5") != 0.265 || a.ForModel("claude-sonnet-5-5") != 2 {
			t.Fatalf("%+v", a)
		}
	}
	check(m)
	var raw string
	if e = m.events.QueryRowContext(ctx, "SELECT model_multipliers FROM profile_events WHERE kind='apply' ORDER BY at DESC LIMIT 1").Scan(&raw); e != nil || raw != `{"claude-opus-5-5":0.265}` {
		t.Fatalf("apply event snapshot %q %v", raw, e)
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
	if mult, models, found, e := reopened.Pricing(ctx, p.ID); e != nil || !found || mult != 2 || models["claude-opus-5-5"] != 0.265 {
		t.Fatalf("pricing %v %v %v %v", mult, models, found, e)
	}
	if _, _, found, _ := reopened.Pricing(ctx, "missing"); found {
		t.Fatal("unknown profile")
	}
}

func TestEventStoreMigratesFromVersionOneInPlace(t *testing.T) {
	m, home := claudeFixture(t)
	w := importClaude(t, m)
	ctx := context.Background()
	plan, e := m.Plan(ctx, PlanRequest{CLI: Claude, ProfileID: w.Profiles[0].ID, Revision: w.Profiles[0].Revision, InspectionID: w.Bindings[1].InspectionID})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Apply(ctx, ApplyRequest{PlanID: plan.ID, OperationID: "apply-v1"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(home, "state", "profile_events.db")
	for _, dropColumn := range []bool{true, false} { // false: a crash after ALTER, before the version bump
		db, e := sql.Open("sqlite", path)
		if e != nil {
			t.Fatal(e)
		}
		stmt := "PRAGMA user_version=1"
		if dropColumn {
			stmt = "ALTER TABLE profile_events DROP COLUMN model_multipliers; " + stmt
		}
		if _, e = db.Exec(stmt); e != nil {
			t.Fatal(e)
		}
		_ = db.Close()
		m, e = New(Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid())})
		if e != nil {
			t.Fatalf("drop=%v: %v", dropColumn, e)
		}
		tl, e := m.Timeline(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if a := tl.Resolve(Claude, time.Now()); a.ProfileID == "" || a.Models != nil || a.Multiplier != 1 {
			t.Fatalf("an old event reads as no overrides: %+v", a)
		}
		var v int
		if e = m.events.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); e != nil || v != 3 {
			t.Fatalf("version %d %v", v, e)
		}
		if e = m.Close(); e != nil {
			t.Fatal(e)
		}
	}
}
