package aiusage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestAntigravitySQLiteIngestAndDedupe(t *testing.T) {
	ctx := context.Background()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := filepath.Join(home, ".gemini", antigravity, "conversations")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sourceDir, "12345678-1234-1234-1234-123456789abc.db")
	source, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	source.SetMaxOpenConns(1)
	if _, err := source.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA wal_autocheckpoint=0;
		PRAGMA user_version=1;
		CREATE TABLE gen_metadata (idx INTEGER PRIMARY KEY, data BLOB);
		CREATE TABLE steps (idx INTEGER PRIMARY KEY, step_type, status, step_format, metadata BLOB);
	`); err != nil {
		t.Fatal(err)
	}
	summary, err := sql.Open("sqlite", filepath.Join(filepath.Dir(sourceDir), "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = summary.Close() }()
	session := "12345678-1234-1234-1234-123456789abc"
	if _, err := summary.Exec(`
		PRAGMA user_version=3;
		CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, title TEXT, last_modified_time TEXT);
		INSERT INTO conversation_summaries VALUES (?, 'Native Antigravity title', '2026-11-22 12:00:00+00:00');
		INSERT INTO conversation_summaries VALUES ('87654321-1234-1234-1234-123456789abc', 'Legacy title', '2026-11-22 12:00:00+00:00');
	`, session); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	execution := []byte("12345678-1234-1234-1234-123456789def")
	userAt := agyVarint(nil, 1, uint64(now.Add(-time.Minute).Unix()))
	user := agyBytes(agyBytes(nil, 1, userAt), 12, execution)
	if _, err := source.Exec("INSERT INTO steps VALUES (0,14,3,0,?)", user); err != nil {
		t.Fatal(err)
	}
	add := func(idx int, response, model string, completeTiming bool) {
		t.Helper()
		usage := agyVarint(nil, 1, 1532)
		usage = agyVarint(usage, 2, 1000)
		usage = agyVarint(usage, 3, 100)
		usage = agyVarint(usage, 5, 2000)
		usage = agyVarint(usage, 9, 30) // Already included in output.
		usage = agyBytes(usage, 11, []byte(response))
		timestamp := agyVarint(nil, 1, uint64(now.Add(time.Duration(idx-4)*time.Second).Unix()))
		step := agyBytes(nil, 7, timestamp)
		step = agyBytes(step, 9, usage)
		chat := agyBytes(nil, 1, []byte("prompt outside usage fields"))
		chat = agyBytes(chat, 4, usage)
		chat = agyBytes(chat, 11, agyVarint(nil, 1, 2))
		if completeTiming {
			chat = agyBytes(chat, 12, agyVarint(nil, 2, 500_000_000))
		}
		chat = agyBytes(chat, 19, []byte(model))
		if model == "gemini-4-neon" {
			step = agyBytes(step, 12, execution)
			window := agyVarint(nil, 4, 512000)
			chat = agyBytes(chat, 9, agyBytes(nil, 10, window))
			chat = agyBytes(chat, 21, []byte("Gemini 4 Neon (High)"))
		}
		if _, err := source.Exec("INSERT INTO gen_metadata VALUES (?,?)", idx, agyBytes(nil, 1, chat)); err != nil {
			t.Fatal(err)
		}
		if _, err := source.Exec("INSERT INTO steps VALUES (?,15,3,0,?)", idx, step); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "response-1", "gemini-4-neon", true)
	opts := Options{Directory: filepath.Join(home, "pax"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }}
	open := func() *Store {
		t.Helper()
		store, err := Open(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	check := func(store *Store, count, input int64) {
		t.Helper()
		if err := store.Scan(ctx); err != nil {
			t.Fatal(err)
		}
		log, err := store.List(ctx, Query{From: now.Add(-time.Hour).UnixMilli(), To: now.Add(time.Hour).UnixMilli(), CLI: antigravity})
		if err != nil || log.All != count || log.Total.Input != input || log.Total.Output != 100*count || log.Total.Cost != 4800 {
			t.Fatalf("usage: %+v, error %v", log.Total, err)
		}
		for _, r := range log.Requests {
			missing := r.Model == "claude-opus-5-5"
			if r.ProfileID != "" || r.Multiplier != 1 || r.Details.ServiceSource != "unknown" || missing != (r.InputPrice == nil) {
				t.Fatalf("usage-only price: %+v", r)
			}
			if missing && (r.Elapsed != nil || r.TPS != nil) || !missing && (!near(r.Elapsed, 2.5) || !near(r.TPS, 40)) {
				t.Fatalf("request timing: %+v", r)
			}
		}
		buckets, err := store.Summarize(ctx)
		if err != nil || len(buckets) != int(count) {
			t.Fatalf("hourly usage: %+v, error %v", buckets, err)
		}
		for _, b := range buckets {
			if b.Count != 1 || b.Model == "gemini-4-neon" &&
				(b.ElapsedCount != 1 || b.TPSCount != 1 || b.ElapsedSum != 2.5 || b.TPSSum != 40) ||
				b.Model == "claude-opus-5-5" && (b.ElapsedCount != 0 || b.TPSCount != 0) {
				t.Fatalf("hourly timing: %+v", b)
			}
		}
		catalog, err := store.ListSessions(ctx, SessionCatalogQuery{CLI: antigravity})
		if err != nil || len(catalog.Items) != 1 || catalog.Items[0].Title != "Native Antigravity title" {
			t.Fatalf("catalog: %+v, error %v", catalog, err)
		}
		analysis, err := store.AnalyzeLimits(ctx, now.Add(-time.Hour).UnixMilli(), now.UnixMilli(), []SessionKey{{antigravity, session}})
		if err != nil || analysis.Limits == nil || analysis.Limits.Trend == nil {
			t.Fatalf("analysis unavailable: %v", err)
		}
		limits := analysis.Limits
		if limits.Summary.Turns != 1 || limits.Summary.TurnsComplete != (count == 1) ||
			len(limits.Trend.Requests) != int(count) {
			t.Fatalf("analysis counts: %+v", limits.Summary)
		}
		first := limits.Trend.Requests[0]
		if first.TurnNumber == nil || *first.TurnNumber != 0 || first.Request.Effort != "high" || first.Request.SessionTitle != "Native Antigravity title" {
			t.Fatalf("analysis metadata: %+v", first)
		}
		if count == 1 && (limits.Summary.Current.NativeContextLimit == nil || *limits.Summary.Current.NativeContextLimit != 512000 ||
			limits.Summary.Current.Limit == nil || *limits.Summary.Current.Limit != 350000) {
			t.Fatalf("native context separate from economic limit: %+v", limits.Summary.Current)
		}
		if count == 2 && (limits.Trend.Requests[1].TurnNumber != nil || limits.Summary.Current.NativeContextLimit != nil) {
			t.Fatalf("latest request has unknown round and capacity: %+v", limits.Summary)
		}
	}
	store := open()
	check(store, 1, 3000)
	// Replay an unchanged native file as an upgrade from the previous cursor.
	if _, err := store.w.ExecContext(ctx, `
		UPDATE requests SET tps=NULL,elapsed=NULL,details=json_remove(details,'$.ReasoningEffort','$.EffortSource','$.NativeContextLimit') WHERE cli='antigravity';
		UPDATE usage_hourly SET tps_sum=0,elapsed_sum=0,tps_count=0,elapsed_count=0 WHERE cli='antigravity';
		DELETE FROM analysis_requests WHERE cli='antigravity';
		DELETE FROM analysis_turns WHERE cli='antigravity';
		UPDATE cursors SET offset=1 WHERE path=?;
	`, path); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store = open()
	before := store.State().Revision
	check(store, 1, 3000)
	if store.State().Revision != before+1 {
		t.Fatalf("expected backfill to publish one revision after %d, got %d", before, store.State().Revision)
	}
	add(2, "response-1", "gemini-4-neon", true)
	add(3, "response-2", "claude-opus-5-5", false)
	check(store, 2, 6000)
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store = open()
	defer func() { _ = store.Close(ctx) }()
	before = store.State().Revision
	check(store, 2, 6000)
	if store.State().Revision != before {
		t.Fatalf("expected restart to keep revision %d, got %d", before, store.State().Revision)
	}
	catalogBefore := store.State().CatalogRevision
	if _, err := summary.Exec("UPDATE conversation_summaries SET title='Renamed title' WHERE conversation_id=?", session); err != nil {
		t.Fatal(err)
	}
	if err := store.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	catalog, err := store.ListSessions(ctx, SessionCatalogQuery{CLI: antigravity, Selected: &SessionKey{antigravity, session}})
	if err != nil || catalog.Selected == nil || catalog.Selected.Title != "Renamed title" ||
		store.State().Revision != before || store.State().CatalogRevision != catalogBefore+1 {
		t.Fatalf("title-only scan: %+v, state %+v, error %v", catalog, store.State(), err)
	}
}

func TestAntigravityNeonLimitBoundaries(t *testing.T) {
	line := modelLimit("gemini-4-neon")
	if line == nil || line.Baseline == nil || *line.Baseline != .034625 ||
		line.FlatLimit != line.Limit || line.FlatWarn != line.Warn {
		t.Fatalf("Neon preset: %+v", line)
	}
	for _, tt := range []struct {
		input int64
		band  int32
	}{{300000, 0}, {300001, 1}, {350000, 1}, {350001, 2}} {
		if got := limitBand(tt.input, line); got != tt.band {
			t.Errorf("%d tokens: band %d, want %d", tt.input, got, tt.band)
		}
	}
}

func TestAntigravityPublishedRateDates(t *testing.T) {
	for _, tc := range []struct {
		day        string
		model      string
		input      float64
		cacheWrite float64
	}{
		{"2026-11-30", "gemini-4-carbon", 2, 0},
		{"2026-12-01", "gemini-4-carbon", 4, 0},
		{"2026-12-31", "gemini-3.8-flash", .75, 0},
		{"2027-01-01", "gemini-3.8-flash", 1.5, 0},
		{"2027-01-11", "gemini-4-neon", 3, 0},
		{"2027-01-12", "gemini-4-neon", 3, 3.75},
	} {
		t.Run(tc.day, func(t *testing.T) {
			date, err := time.Parse(time.DateOnly, tc.day)
			if err != nil {
				t.Fatal(err)
			}
			r := Request{CLI: antigravity, Model: tc.model, At: date.UnixMilli()}
			price, version := r.antigravityTariff()
			if price.input != tc.input || price.short != tc.cacheWrite || version != antigravityPriceVersion {
				t.Fatalf("price: %+v, version %q", price, version)
			}
		})
	}
}

func agyBytes(buf []byte, field protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(buf, field, protowire.BytesType), value)
}

func agyVarint(buf []byte, field protowire.Number, value uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(buf, field, protowire.VarintType), value)
}
