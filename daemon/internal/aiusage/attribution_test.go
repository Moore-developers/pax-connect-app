package aiusage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func claudeAt(id, at string) string {
	return `{"type":"assistant","sessionId":"one-session","requestId":"r-` + id + `","timestamp":"` + at + `","message":{"id":"` + id + `","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":1000000,"output_tokens":1000000}}}` + "\n"
}

// switchAt returns a timeline: Old until the switch, New (multiplier 2) from it on.
func switchAt(switched time.Time) Resolver {
	return func(cli string, at time.Time) Attribution {
		if at.Before(switched) {
			return Attribution{ProfileID: "old", ProfileName: "Old", Multiplier: 1}
		}
		return Attribution{ProfileID: "new", ProfileName: "New", Multiplier: 2}
	}
}

func TestEachRequestTakesTheProfileInEffectAtItsOwnTime(t *testing.T) {
	h := home(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(h, ".claude", "projects", "p", "a.jsonl")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	// One session spans the switch: ownership follows each request, not the session start.
	appendTo(t, path, claudeAt("m1", "2026-10-28T09:00:00Z"), claudeAt("m2", "2026-10-28T10:00:00Z"), claudeAt("m3", "2026-10-28T11:00:00Z"))
	s := openAt(t, h, &now, switchAt(time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)))
	defer func() { _ = s.Close(context.Background()) }()
	if e := s.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	logs := claudeRows(t, s)
	want := map[string]struct {
		profile string
		cost    int64
	}{"m1:r-m1": {"old", 18000000}, "m2:r-m2": {"new", 36000000}, "m3:r-m3": {"new", 36000000}}
	if logs.All != 3 {
		t.Fatalf("rows: %d", logs.All)
	}
	for _, r := range logs.Requests {
		w := want[r.Key]
		if r.ProfileID != w.profile || r.Cost != w.cost || r.Details.PriceVersion == "" {
			t.Fatalf("%s: %+v", r.Key, r)
		}
	}
	bucketsMatchDetails(t, s, logs)
	buckets, e := s.Summarize(context.Background())
	if e != nil || len(buckets) != 2 {
		t.Fatalf("one bucket per profile: %+v %v", buckets, e)
	}
}

func TestWithoutATimelineTheProfileIsEmptyAndDSHNeverHasOne(t *testing.T) {
	h := home(t)
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	writeFixture(t, filepath.Join(h, ".claude", "projects", "p", "a.jsonl"), "claude-redacted.jsonl")
	dsh, e := os.ReadFile("../../../tests/ai-usage/dsh-v4-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	encoder, _ := zstd.NewWriter(nil)
	defer func() { _ = encoder.Close() }()
	path := filepath.Join(h, ".dsh", "sessions", "a", "session.v4.jsonl.zstd")
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, encoder.EncodeAll(dsh, nil), 0600); e != nil {
		t.Fatal(e)
	}
	_ = os.Chtimes(path, now.Add(-time.Hour), now.Add(-time.Hour))
	// Even a resolver that names a Profile for everything must not touch DSH.
	always := func(string, time.Time) Attribution {
		return Attribution{ProfileID: "x", ProfileName: "X", Multiplier: 3}
	}
	for _, resolve := range []Resolver{nil, always} {
		dir := home(t)
		s, e := Open(context.Background(), Options{Directory: filepath.Join(dir, "state"), Home: h, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }, Timeline: timelineOf(resolve)})
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Scan(context.Background()); e != nil {
			t.Fatal(e)
		}
		logs, e := s.List(context.Background(), Query{From: 1, To: now.UnixMilli()})
		if e != nil || logs.All == 0 {
			t.Fatal(logs, e)
		}
		for _, r := range logs.Requests {
			switch {
			case r.CLI == "dsh" && (r.ProfileID != "" || r.ProfileName != "" || r.Multiplier != 1):
				t.Fatalf("DSH is read-only usage: %+v", r)
			case r.CLI == "claude" && resolve == nil && (r.ProfileID != "" || r.ProfileName != "" || r.Multiplier != 1):
				t.Fatalf("no timeline, no profile: %+v", r)
			case r.CLI == "claude" && resolve != nil && (r.ProfileName != "X" || r.Multiplier != 3):
				t.Fatalf("claude follows the timeline: %+v", r)
			}
		}
		_ = s.Close(context.Background())
	}
}

func TestEveryRequestIsPricedAndBucketsEqualDetails(t *testing.T) {
	h := home(t)
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	writeFixture(t, filepath.Join(h, ".claude", "projects", "p", "a.jsonl"), "claude-redacted.jsonl")
	writeFixture(t, filepath.Join(h, ".codex", "sessions", "rollout.jsonl"), "codex-redacted.jsonl")
	// A cache write without a tier and a model nobody priced yet.
	appendTo(t, filepath.Join(h, ".claude", "projects", "p", "b.jsonl"),
		`{"type":"assistant","sessionId":"s","requestId":"r1","timestamp":"2025-01-01T01:00:00Z","message":{"id":"w","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"cache_creation_input_tokens":1000000,"output_tokens":0}}}`+"\n",
		`{"type":"assistant","sessionId":"s","requestId":"r2","timestamp":"2025-01-01T01:00:01Z","message":{"id":"u","model":"brand-new-model","usage":{"input_tokens":5,"output_tokens":5}}}`+"\n")
	s := openAt(t, h, &now, nil)
	defer func() { _ = s.Close(context.Background()) }()
	if e := s.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	logs, e := s.List(context.Background(), Query{From: 1, To: now.UnixMilli()})
	if e != nil || logs.All < 4 {
		t.Fatal(logs, e)
	}
	seen := map[string]bool{}
	for _, r := range logs.Requests {
		seen[r.Model] = true
		switch r.Model {
		case "<synthetic>", "brand-new-model":
			if r.Cost != 0 {
				t.Fatalf("%s must cost 0: %+v", r.Model, r)
			}
		case "claude-sonnet-5-5":
			// 1,000,000 written tokens at the 5m rate of 2.5 USD per million.
			if r.Costs[2] != 2500000 {
				t.Fatalf("5m fallback: %+v", r.Costs)
			}
		default:
			if r.Cost <= 0 || r.InputPrice == nil {
				t.Fatalf("%s unpriced: %+v", r.Model, r)
			}
		}
	}
	for _, m := range []string{"<synthetic>", "brand-new-model", "claude-sonnet-5-5", "claude-opus-5-5", "gpt-6.1-sol"} {
		if !seen[m] {
			t.Fatalf("fixture lost %s: %v", m, seen)
		}
	}
	bucketsMatchDetails(t, s, logs)
}

func TestAnOlderSchemaIsNeverMigratedAndReportsRebuildRequired(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(home(t), "state")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "ai_usage.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE requests(cli TEXT, priced INTEGER); INSERT INTO requests VALUES('claude',1); PRAGMA user_version=4"); e != nil {
		t.Fatal(e)
	}
	_ = db.Close()
	if e = os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(path)
	s, e := Open(ctx, Options{Directory: dir, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatalf("an old schema must not fail the daemon: %v", e)
	}
	if !s.RebuildRequired() || s.State().Error != ErrRebuildCode {
		t.Fatalf("state: %+v", s.State())
	}
	if e = s.Configure(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs, e := s.List(ctx, Query{From: 1, To: 2})
	buckets, e2 := s.Buckets(ctx, Query{From: 1, To: 2, Granularity: "hour"})
	last, e3 := s.LastUsed(ctx)
	if e != nil || e2 != nil || e3 != nil || logs.All != 0 || len(buckets) != 0 || len(last) != 0 {
		t.Fatalf("reads stay empty: %+v %v %v %v", logs, e, e2, e3)
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("the old database was modified")
	}
	if _, e = os.Stat(path + "-wal"); !os.IsNotExist(e) {
		t.Fatal("a WAL appeared next to the old database")
	}
	// A schema newer than this build is an ordinary failure, not a rebuild.
	db, _ = sql.Open("sqlite", path)
	_, _ = db.Exec("PRAGMA user_version=99")
	_ = db.Close()
	if _, e = Open(ctx, Options{Directory: dir, Owner: uint32(os.Geteuid())}); e == nil {
		t.Fatal("a newer schema was accepted")
	}
}
