package aiusage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	modelA = "claude-sonnet-4-5-20250929" // 1M in + 1M out = 18 USD at 1x
	modelB = "claude-haiku-4-5-20251001"  // 1M in + 1M out = 6 USD at 1x
)

var repriceNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func lineAt(id, model string, at time.Time) string {
	return `{"type":"assistant","sessionId":"s","requestId":"r-` + id + `","timestamp":"` + at.UTC().Format(time.RFC3339) + `","message":{"id":"` + id + `","model":"` + model + `","usage":{"input_tokens":1000000,"output_tokens":1000000}}}` + "\n"
}

func local(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.Local)
}

func profileP(mult float64, models map[string]float64) Resolver {
	return func(cli string, _ time.Time) Attribution {
		return Attribution{ProfileID: "p", ProfileName: "P", Multiplier: mult, Models: models}
	}
}

type fixture struct {
	t     *testing.T
	home  string
	log   string
	store *Store
}

// repriceFixture scans four requests around local midnight: r1 the 26th, r2/r3 the 27th
// (just after midnight, just before the next), r4 the 28th. Models alternate A, B.
func repriceFixture(t *testing.T, resolve Resolver) *fixture {
	t.Helper()
	h := home(t)
	log := filepath.Join(h, ".claude", "projects", "p", "a.jsonl")
	if e := os.MkdirAll(filepath.Dir(log), 0700); e != nil {
		t.Fatal(e)
	}
	appendTo(t, log,
		lineAt("r1", modelA, local(2026, 10, 26, 12, 0)),
		lineAt("r2", modelB, local(2026, 10, 27, 0, 0).Add(30*time.Second)),
		lineAt("r3", modelA, local(2026, 10, 27, 23, 59).Add(30*time.Second)),
		lineAt("r4", modelB, local(2026, 10, 28, 0, 30)))
	now := repriceNow
	s := openAt(t, h, &now, resolve)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	if e := s.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	return &fixture{t, h, log, s}
}

// rowCosts maps each request (by its first two key characters) to its stored cost.
func (f *fixture) rowCosts() map[string]int64 {
	f.t.Helper()
	out := map[string]int64{}
	for _, r := range claudeRowsAt(f.t, f.store).Requests {
		out[r.Key[:2]] = r.Cost
	}
	return out
}

// costs is rowCosts after checking that the hourly buckets equal the sum of the rows.
func (f *fixture) costs() map[string]int64 {
	f.t.Helper()
	bucketsMatchDetails(f.t, f.store, claudeRowsAt(f.t, f.store))
	return f.rowCosts()
}

func (f *fixture) reprice(q RepriceQuery) RepriceResult {
	f.t.Helper()
	q.ProfileID = "p"
	v, e := f.store.Reprice(context.Background(), q)
	if e != nil {
		f.t.Fatal(e)
	}
	return v
}

func equalCosts(t *testing.T, got, want map[string]int64) {
	t.Helper()
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("costs %v, want %v", got, want)
		}
	}
}

func overrideCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if e := s.r.QueryRow("SELECT count(*) FROM reprice_overrides").Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

const usd = 1000000

func TestIngestPicksTheModelOverrideElseTheProfileDefault(t *testing.T) {
	f := repriceFixture(t, profileP(1, map[string]float64{modelA: .265}))
	// A: 18 USD x .265 = 4.77; B inherits the default 1: 6 USD.
	equalCosts(t, f.costs(), map[string]int64{"r1": 4770000, "r3": 4770000, "r2": 6 * usd, "r4": 6 * usd})
}

func TestRepriceDryRunMatchesTheRealRunAndWritesNothing(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	before := f.costs()
	rev := f.store.State().Revision
	q := RepriceQuery{All: true, Multiplier: .5, Models: map[string]float64{modelB: 2}}
	q.DryRun = true
	dry := f.reprice(q)
	// A: 2 x 9 = 18, B: 2 x 12 = 24 (6 x 2).
	if dry.Requests != 4 || dry.Aggregated != 0 || dry.OldCost != 48*usd || dry.NewCost != 42*usd || dry.OverrideID != 0 {
		t.Fatalf("dry run %+v", dry)
	}
	equalCosts(t, f.costs(), before)
	if overrideCount(t, f.store) != 0 || f.store.State().Revision != rev {
		t.Fatal("a dry run left a trace")
	}
	q.DryRun = false
	real := f.reprice(q)
	if real.Requests != dry.Requests || real.OldCost != dry.OldCost || real.NewCost != dry.NewCost || real.OverrideID != 1 {
		t.Fatalf("real %+v vs dry %+v", real, dry)
	}
	equalCosts(t, f.costs(), map[string]int64{"r1": 9 * usd, "r3": 9 * usd, "r2": 12 * usd, "r4": 12 * usd})
	if f.store.State().Revision == rev {
		t.Fatal("revision must move so pages refresh")
	}
	logs := claudeRowsAt(t, f.store)
	for _, r := range logs.Requests {
		if r.Multiplier != .5 && r.Model == modelA || r.Multiplier != 2 && r.Model == modelB {
			t.Fatalf("multiplier column: %+v", r)
		}
		if r.Details.Billed[0]+r.Details.Billed[3] < r.Cost-2 {
			t.Fatalf("billed components: %+v", r.Details.Billed)
		}
	}
}

func claudeRowsAt(t *testing.T, s *Store) Logs {
	t.Helper()
	logs, e := s.List(context.Background(), Query{From: 1, To: repriceNow.UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	return logs
}

func TestRepriceRepricesLikeAFreshIngest(t *testing.T) {
	// Repricing 1x history to .265 must give what ingesting at .265 gives, to the micro.
	a := repriceFixture(t, profileP(1, nil))
	a.reprice(RepriceQuery{All: true, Multiplier: .265})
	b := repriceFixture(t, profileP(.265, nil))
	ra, rb := claudeRowsAt(t, a.store), claudeRowsAt(t, b.store)
	for i := range ra.Requests {
		if ra.Requests[i].Cost != rb.Requests[i].Cost || ra.Requests[i].Details.Billed != rb.Requests[i].Details.Billed || ra.Requests[i].Multiplier != .265 {
			t.Fatalf("%+v vs %+v", ra.Requests[i], rb.Requests[i])
		}
	}
}

func TestRepriceDayRangeUsesInclusiveLocalDays(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	v := f.reprice(RepriceQuery{FromDay: "2026-10-27", ToDay: "2026-10-27", Multiplier: .5})
	if v.Requests != 2 {
		t.Fatalf("%+v", v)
	}
	// r2 and r3 straddle the day's first and last minute; r1 and r4 are outside.
	equalCosts(t, f.costs(), map[string]int64{"r1": 18 * usd, "r2": 3 * usd, "r3": 9 * usd, "r4": 6 * usd})
	v = f.reprice(RepriceQuery{FromDay: "2026-10-27", ToDay: "2026-10-28", Multiplier: .5})
	if v.Requests != 3 {
		t.Fatalf("%+v", v)
	}
	for _, bad := range []RepriceQuery{{FromDay: "2026-10-28", ToDay: "2026-10-27"}, {FromDay: "x", ToDay: "y"}, {All: true, FromDay: "2026-10-27", ToDay: "2026-10-27"}, {}} {
		bad.ProfileID, bad.Multiplier = "p", 1
		if _, e := f.store.Reprice(context.Background(), bad); e != ErrRange {
			t.Fatalf("%+v: %v", bad, e)
		}
	}
	if _, e := f.store.Reprice(context.Background(), RepriceQuery{All: true, Multiplier: 1}); e != ErrRange {
		t.Fatal("a profile is required", e)
	}
}

func TestRepriceCanBeScopedToOneModel(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	// The profile's current value for B is 3; A has its own entry that a B-scoped run must ignore.
	v := f.reprice(RepriceQuery{All: true, Model: modelB, Multiplier: .5, Models: map[string]float64{modelA: 9, modelB: 3}})
	if v.Requests != 2 || v.OldCost != 12*usd || v.NewCost != 36*usd {
		t.Fatalf("%+v", v)
	}
	equalCosts(t, f.costs(), map[string]int64{"r1": 18 * usd, "r3": 18 * usd, "r2": 18 * usd, "r4": 18 * usd})
}

func TestRepriceNeverTouchesRequestsWithoutThisProfile(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	var rows []Request
	for _, cli := range []string{"dsh", "claude"} {
		r := Request{CLI: cli, Key: "plain-" + cli, At: local(2026, 10, 27, 12, 0).UnixMilli(), Model: "deepseek-flash", Input: 1000000, Output: 1000000, Succeeded: true, Multiplier: 1}
		r.attribute(attributor{})
		rows = append(rows, r)
	}
	if e := f.store.commit(context.Background(), rows, cursor{Path: "x"}); e != nil {
		t.Fatal(e)
	}
	before := claudeRowsAt(t, f.store)
	f.reprice(RepriceQuery{All: true, Multiplier: 0})
	after := claudeRowsAt(t, f.store)
	for i, r := range after.Requests {
		if r.ProfileID == "" && (r.Cost != before.Requests[i].Cost || r.Multiplier != 1) {
			t.Fatalf("a request without a profile changed: %+v", r)
		}
		if r.ProfileID == "p" && r.Cost != 0 {
			t.Fatalf("p should be free: %+v", r)
		}
	}
	bucketsMatchDetails(t, f.store, after)
}

func TestRepriceRescalesBucketsOlderThanStoredRequests(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	hour := local(2026, 3, 1, 9, 0).UnixMilli() / 3600000 * 3600000
	old := Bucket{At: hour, CLI: "claude", ProfileID: "p", ProfileName: "P", Model: modelA}
	old.Summary = Summary{Count: 2, Success: 2, Costs: [4]int64{1000000, 0, 0, 2000000}, Cost: 3000000, BilledCosts: [4]int64{1000000, 0, 0, 2000000}}
	tx, e := f.store.w.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = writeBuckets(context.Background(), tx, map[bucketKey]Bucket{{hour, "claude", "p", modelA, ""}: old}); e != nil || tx.Commit() != nil {
		t.Fatal(e)
	}
	// A day range that misses March leaves it alone; all reaches it.
	v := f.reprice(RepriceQuery{FromDay: "2026-10-26", ToDay: "2026-10-28", Multiplier: .5})
	if v.Aggregated != 0 {
		t.Fatalf("%+v", v)
	}
	v = f.reprice(RepriceQuery{All: true, Multiplier: .5, DryRun: true})
	if v.Aggregated != 2 || v.Requests != 6 {
		t.Fatalf("%+v", v)
	}
	f.reprice(RepriceQuery{FromDay: "2026-03-01", ToDay: "2026-03-01", Multiplier: .5})
	var cost, b0, b3 int64
	if e = f.store.r.QueryRow("SELECT cost,b0,b3 FROM usage_hourly WHERE hour=?", hour).Scan(&cost, &b0, &b3); e != nil {
		t.Fatal(e)
	}
	if cost != 1500000 || b0 != 500000 || b3 != 1000000 {
		t.Fatalf("bucket %d %d %d", cost, b0, b3)
	}
}

func TestOverrideOutranksTheTimelineForLateIngestOnly(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	f.reprice(RepriceQuery{All: true, Multiplier: .5})
	// A late-arriving old request is covered; one dated after the repricing follows the timeline.
	appendTo(t, f.log, lineAt("r5", modelA, local(2026, 10, 27, 12, 0)), lineAt("r6", modelA, repriceNow.Add(time.Hour)))
	if e := f.store.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	got := map[string]float64{}
	for _, r := range claudeRowsFuture(t, f.store).Requests {
		got[r.Key[:2]] = r.Multiplier
	}
	if got["r5"] != .5 || got["r6"] != 1 || got["r1"] != .5 {
		t.Fatalf("%v", got)
	}
	// The latest override wins where both cover.
	f.reprice(RepriceQuery{FromDay: "2026-10-27", ToDay: "2026-10-27", Multiplier: .25})
	appendTo(t, f.log, lineAt("r7", modelA, local(2026, 10, 27, 13, 0)))
	if e := f.store.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, r := range claudeRowsFuture(t, f.store).Requests {
		if r.Key[:2] == "r7" && r.Multiplier != .25 {
			t.Fatalf("latest override: %+v", r)
		}
	}
}

func claudeRowsFuture(t *testing.T, s *Store) Logs {
	t.Helper()
	logs, e := s.List(context.Background(), Query{From: 1, To: repriceNow.Add(48 * time.Hour).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	return logs
}

func TestOverrideSurvivesAFullRebuild(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	f.reprice(RepriceQuery{FromDay: "2026-10-27", ToDay: "2026-10-27", Multiplier: .5})
	hour := local(2026, 3, 1, 9, 0).UnixMilli() / 3600000 * 3600000
	tx, _ := f.store.w.Begin()
	if e := writeBuckets(context.Background(), tx, map[bucketKey]Bucket{{hour, "claude", "p", modelA, ""}: {At: hour, CLI: "claude", ProfileID: "p", ProfileName: "P", Model: modelA, Summary: Summary{Count: 1, Costs: [4]int64{1000000, 0, 0, 0}, Cost: 1000000, BilledCosts: [4]int64{1000000, 0, 0, 0}}}}); e != nil || tx.Commit() != nil {
		t.Fatal(e)
	}
	f.reprice(RepriceQuery{All: true, Multiplier: .5, Model: modelA, DryRun: false, FromDay: "", ToDay: ""})
	want := f.rowCosts()
	if e := f.store.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	// The log of r1 is gone, so it is carried over and repriced under the override.
	if e := os.WriteFile(f.log, []byte(lineAt("r2", modelB, local(2026, 10, 27, 0, 0).Add(30*time.Second))+lineAt("r3", modelA, local(2026, 10, 27, 23, 59).Add(30*time.Second))+lineAt("r4", modelB, local(2026, 10, 28, 0, 30))), 0600); e != nil {
		t.Fatal(e)
	}
	rep, e := Rebuild(context.Background(), RebuildOptions{Directory: filepath.Join(f.home, "state"), Home: f.home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return repriceNow },
		Timeline: func(context.Context, map[string]time.Time, bool) (Resolver, []string, error) {
			return profileP(1, nil), nil, nil
		}})
	if e != nil || !rep.Reconciled || !rep.Replaced {
		t.Fatalf("%+v %v", rep, e)
	}
	now := repriceNow
	s := openAt(t, f.home, &now, profileP(1, nil))
	defer func() { _ = s.Close(context.Background()) }()
	f.store = s
	equalCosts(t, f.rowCosts(), want)
	if overrideCount(t, s) != 2 {
		t.Fatal("overrides are carried into the rebuilt database")
	}
	var cost int64
	if e = s.r.QueryRow("SELECT cost FROM usage_hourly WHERE hour=?", hour).Scan(&cost); e != nil || cost != 500000 {
		t.Fatalf("old bucket keeps its repricing: %d %v", cost, e)
	}
}

func TestSchemaV5GainsTheOverrideTableInPlace(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	want := f.costs()
	if e := f.store.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", filepath.Join(f.home, "state", "ai_usage.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("DROP TABLE reprice_overrides; PRAGMA user_version=5"); e != nil {
		t.Fatal(e)
	}
	_ = db.Close()
	now := repriceNow
	s := openAt(t, f.home, &now, profileP(1, nil))
	defer func() { _ = s.Close(context.Background()) }()
	if s.RebuildRequired() {
		t.Fatal("v5 must open without a rebuild")
	}
	var v int
	if e = s.w.QueryRow("PRAGMA user_version").Scan(&v); e != nil || v != schemaVersion {
		t.Fatalf("version %d %v", v, e)
	}
	f.store = s
	equalCosts(t, f.costs(), want)
	if f.reprice(RepriceQuery{All: true, Multiplier: .5}).OverrideID != 1 {
		t.Fatal("overrides work after the migration")
	}
}

func TestRepriceWaitsForARunningScanAndGivesUpWithBusy(t *testing.T) {
	f := repriceFixture(t, profileP(1, nil))
	f.store.scanMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, e := f.store.Reprice(ctx, RepriceQuery{ProfileID: "p", All: true, Multiplier: .5})
	f.store.scanMu.Unlock()
	if e != ErrBusy {
		t.Fatalf("%v", e)
	}
	if overrideCount(t, f.store) != 0 {
		t.Fatal("a refused reprice left an override")
	}
}
