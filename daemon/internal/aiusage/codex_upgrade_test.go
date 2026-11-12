package aiusage

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestCodexV6UpgradeDoesNotInventEvidenceOrResurrectTombstones(t *testing.T) {
	s, path, now := codexTestStore(t)
	ctx := context.Background()
	at := now.Add(-3600000000000).UnixMilli()
	appendTo(t, path, codexMeta("s", at, false), codexTask("t", at+1), codexTick(at+3, 100))
	codexScan(t, s)
	before := codexLogs(t, s)
	key := before.Requests[0].Key
	cursorBefore, e := s.cursor(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	// An actual v6 cursor has no complete last usage or response correlation state.
	legacy := cursorBefore
	legacy.State.Codex = codexParseState{}
	raw, e := json.Marshal(legacy.State)
	if e != nil {
		t.Fatal(e)
	}
	absent := "s:" + jsonNumber(at+100) + ":1"
	if _, e = s.w.Exec("UPDATE cursors SET state=? WHERE path=?", raw, path); e != nil {
		t.Fatal(e)
	}
	if _, e = s.w.Exec("INSERT INTO request_keys VALUES(?,?)", keyHash("codex", absent), at+103); e != nil {
		t.Fatal(e)
	}
	if _, e = s.w.Exec("DROP TABLE codex_usage_evidence; PRAGMA user_version=6"); e != nil {
		t.Fatal(e)
	}
	state := s.State()
	options := s.options
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, options)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	var version, n int
	if e = s.w.QueryRow("PRAGMA user_version").Scan(&version); e != nil || version != schemaVersion {
		t.Fatal(version, e)
	}
	if e = s.w.QueryRow("SELECT count(*) FROM codex_usage_evidence").Scan(&n); e != nil || n != 0 {
		t.Fatal("legacy incomplete facts seeded strong evidence", n, e)
	}
	appendTo(t, path, codexMeta("s", at+100, false), codexTask("t", at+101), codexTick(at+103, 100))
	codexScan(t, s)
	after := codexLogs(t, s)
	if after.All != 1 || after.Requests[0].Key != key || s.State().Revision != state.Revision || s.State().Generation != state.Generation {
		t.Fatal("upgrade restored a tombstoned request", after, s.State(), state)
	}
	cursorAfter, e := s.cursor(ctx, path)
	if e != nil || cursorAfter.Offset <= cursorBefore.Offset {
		t.Fatal("new source was not consumed", e)
	}
}

func TestCodexRebuildKeepsRetiredKeysAndReplaysNewEvidence(t *testing.T) {
	s, path, now := codexTestStore(t)
	ctx := context.Background()
	at := now.Add(-3600000000000).UnixMilli()
	appendTo(t, path, codexMeta("s", at, false), codexTask("t", at+1), codexResponseLine("s", "t", "r", at+2), codexTick(at+3, 100), codexContext("t", "gpt-6.1-sol", at+4))
	codexScan(t, s)
	key := codexLogs(t, s).Requests[0].Key
	absent := "s:" + jsonNumber(at+100) + ":1"
	if _, e := s.w.Exec("INSERT INTO request_keys VALUES(?,?)", keyHash("codex", absent), at+103); e != nil {
		t.Fatal(e)
	}
	appendTo(t, path, codexMeta("s", at+100, false), codexTask("t", at+101), codexTick(at+103, 100))
	codexScan(t, s)
	// No source reset: the explicit rebuild alone replays all original files.
	options := s.options
	if e := s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	rep, e := Rebuild(ctx, RebuildOptions{Directory: options.Directory, Home: options.Home, Owner: uint32(os.Geteuid()), Now: options.Now, Timeline: func(context.Context, map[string]time.Time, bool) (Resolver, []string, error) {
		resolve, e := options.Timeline(ctx)
		return resolve, nil, e
	}})
	if e != nil || !rep.Reconciled || !rep.Replaced {
		t.Fatal(rep, e)
	}
	s, e = Open(ctx, options)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	logs := codexLogs(t, s)
	if logs.All != 1 || logs.Requests[0].Key != key {
		t.Fatal("rebuild restored retired duplicate", logs)
	}
	var n int
	if e = s.w.QueryRow("SELECT count(*) FROM request_keys WHERE key_hash=?", keyHash("codex", absent)).Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	codexCheckBuckets(t, s)
}

func jsonNumber(v int64) string { return strconv.FormatInt(v, 10) }

func TestCodexRebuildCarriesEvidenceWhenCanonicalSourceIsGone(t *testing.T) {
	s, path, now := codexTestStore(t)
	ctx := context.Background()
	at := now.Add(-time.Hour).UnixMilli()
	appendTo(t, path, codexMeta("s", at, false), codexTask("t", at+1), codexResponseLine("s", "t", "r", at+2), codexTick(at+3, 100))
	codexScan(t, s)
	original := codexLogs(t, s).Requests[0]
	options := s.options
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	rep, e := Rebuild(ctx, RebuildOptions{Directory: options.Directory, Home: options.Home, Owner: uint32(os.Geteuid()), Now: options.Now, Timeline: func(context.Context, map[string]time.Time, bool) (Resolver, []string, error) {
		r, e := options.Timeline(ctx)
		return r, nil, e
	}})
	if e != nil || !rep.Replaced {
		t.Fatal(rep, e)
	}
	s, e = Open(ctx, options)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	appendTo(t, path, codexMeta("s", at+100, false), codexTask("t", at+101), codexTick(at+103, 100))
	codexScan(t, s)
	logs := codexLogs(t, s)
	if logs.All != 1 || logs.Requests[0].Key != original.Key {
		t.Fatalf("rebuild lost source-missing canonical evidence: %+v", logs)
	}
	codexCheckBuckets(t, s)
}
