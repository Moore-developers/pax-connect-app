package aiusage

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// legacySchema is the version 4 layout this rebuild replaces.
const legacySchema = `
CREATE TABLE requests(cli TEXT NOT NULL,request_key TEXT NOT NULL,at INTEGER NOT NULL,day TEXT NOT NULL,session TEXT NOT NULL,segment TEXT NOT NULL,profile_id TEXT NOT NULL,profile_name TEXT NOT NULL,model TEXT NOT NULL,streaming INTEGER NOT NULL,succeeded INTEGER NOT NULL,error TEXT NOT NULL,input INTEGER NOT NULL,read INTEGER NOT NULL,write INTEGER NOT NULL,output INTEGER NOT NULL,input_price REAL,output_price REAL,multiplier REAL NOT NULL,tps REAL,first_token REAL,elapsed REAL,cost INTEGER NOT NULL,c0 INTEGER NOT NULL,c1 INTEGER NOT NULL,c2 INTEGER NOT NULL,c3 INTEGER NOT NULL,details TEXT NOT NULL DEFAULT '{}',priced INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(cli,request_key));
CREATE TABLE usage_hourly(hour INTEGER NOT NULL,cli TEXT NOT NULL,profile_id TEXT NOT NULL,model TEXT NOT NULL,profile_name TEXT NOT NULL,count INTEGER NOT NULL,success INTEGER NOT NULL,input INTEGER NOT NULL,read INTEGER NOT NULL,write INTEGER NOT NULL,output INTEGER NOT NULL,c0 INTEGER NOT NULL,c1 INTEGER NOT NULL,c2 INTEGER NOT NULL,c3 INTEGER NOT NULL,cost INTEGER NOT NULL,tps_sum REAL NOT NULL,first_sum REAL NOT NULL,elapsed_sum REAL NOT NULL,tps_count INTEGER NOT NULL,first_count INTEGER NOT NULL,elapsed_count INTEGER NOT NULL,unpriced INTEGER NOT NULL,b0 INTEGER NOT NULL,b1 INTEGER NOT NULL,b2 INTEGER NOT NULL,b3 INTEGER NOT NULL,est_tps_sum REAL NOT NULL DEFAULT 0,est_elapsed_sum REAL NOT NULL DEFAULT 0,est_tps_count INTEGER NOT NULL DEFAULT 0,est_elapsed_count INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(hour,cli,profile_id,model));
CREATE TABLE request_keys(key_hash BLOB PRIMARY KEY,at INTEGER NOT NULL) WITHOUT ROWID;
CREATE TABLE cursors(path TEXT PRIMARY KEY,inode INTEGER NOT NULL,size INTEGER NOT NULL,mtime INTEGER NOT NULL,offset INTEGER NOT NULL,version INTEGER NOT NULL,state BLOB NOT NULL);
CREATE TABLE meta(key TEXT PRIMARY KEY,value INTEGER NOT NULL);
INSERT INTO meta VALUES('revision',7),('parser_version',9),('replay_floor',1),('correction:x',1);
PRAGMA user_version=4;`

var rebuildNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// oldWorld writes a version 4 database next to logs of which one request survived.
func oldWorld(t *testing.T) (dir, home string, longAgo int64) {
	t.Helper()
	home = home2(t)
	dir = filepath.Join(home, "state")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	logPath := filepath.Join(home, ".claude", "projects", "p", "a.jsonl")
	if e := os.MkdirAll(filepath.Dir(logPath), 0700); e != nil {
		t.Fatal(e)
	}
	appendTo(t, logPath, claudeAt("alive", "2026-10-28T09:00:00Z"), claudeAt("later", "2026-10-28T11:00:00Z"), claudeAt("ancient", "2026-03-02T09:00:00Z"))
	db, e := sql.Open("sqlite", filepath.Join(dir, "ai_usage.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	if _, e = db.Exec(legacySchema); e != nil {
		t.Fatal(e)
	}
	day := func(ms int64) string { return time.UnixMilli(ms).In(time.Local).Format("2006-01-02") }
	alive := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC).UnixMilli()
	gone := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).UnixMilli()
	unknown := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC).UnixMilli()
	longAgo = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	insert := `INSERT INTO requests VALUES(?,?,?,?,'s','seg','','未归属',?,1,1,'',?,0,?,?,NULL,NULL,1,?,NULL,?,0,0,0,0,0,?,0)`
	for _, r := range []struct {
		cli, key string
		at       int64
		model    string
		input    int64
		write    int64
		output   int64
		tps, el  any
		details  string
	}{
		// Still in the logs, stored with a stale owner and no price.
		{"claude", "alive:r-alive", alive, "claude-sonnet-4-5-20250929", 1000000, 0, 1, nil, nil, `{"Version":9,"PriceReason":"x"}`},
		// The log is gone. Cache write without a tier; an estimated timing to be dropped.
		{"claude", "gone", gone, "claude-sonnet-5-5", 1000000, 1000000, 10, 5.0, 2.0, `{"Version":9,"TimingSource":"adjacent_record_estimate","AttributionSource":"unknown"}`},
		{"claude", "unknown-model", unknown, "brand-new-model", 100, 0, 50, 10.0, 5.0, `{"Version":9,"TimingSource":"measured"}`},
	} {
		if _, e = db.Exec(insert, r.cli, r.key, r.at, day(r.at), r.model, r.input, r.write, r.output, r.tps, r.el, r.details); e != nil {
			t.Fatal(e)
		}
	}
	// Buckets: one far outside the retained window (kept), one inside (rebuilt from details).
	bucket := `INSERT INTO usage_hourly VALUES(?,?,?,?,?,2,2,2000,0,0,300,1000,0,0,2000,9999,5.0,0,2,0,2,0,0,0,0,0,0,0,0,0,0)`
	if _, e = db.Exec(bucket, longAgo/3600000*3600000, "codex", "", "gpt-6.1-sol", "未归属"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(bucket, gone/3600000*3600000, "claude", "", "claude-sonnet-5-5", "未归属"); e != nil {
		t.Fatal(e)
	}
	return dir, home, longAgo
}

func home2(t *testing.T) string { return home(t) }

func fileHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, e := os.ReadFile(p)
		out[strings.TrimPrefix(p, root)] = string(b)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return out
}

// krill answers like a switch timeline: Codex and Claude both belong to one Profile from the start.
func krill(string, time.Time) Attribution {
	return Attribution{ProfileID: "p1", ProfileName: "Krill", Multiplier: .5}
}

func rebuildOptions(dir, home string, dry bool, seen *map[string]time.Time) RebuildOptions {
	return RebuildOptions{Directory: dir, Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return rebuildNow }, DryRun: dry,
		Timeline: func(_ context.Context, earliest map[string]time.Time, _ bool) (Resolver, []string, error) {
			if seen != nil {
				*seen = earliest
			}
			return krill, []string{"seeded"}, nil
		}}
}

func TestRebuildCarriesOldRowsRescansLogsAndReconciles(t *testing.T) {
	ctx := context.Background()
	dir, home, longAgo := oldWorld(t)
	before, e := os.ReadFile(filepath.Join(dir, "ai_usage.db"))
	if e != nil {
		t.Fatal(e)
	}
	var earliest map[string]time.Time
	rep, e := Rebuild(ctx, rebuildOptions(dir, home, false, &earliest))
	if e != nil {
		t.Fatalf("%v\n%+v", e, rep)
	}
	if !rep.Replaced || !rep.Reconciled || rep.QuickCheck != "ok" || len(rep.Mismatches) != 0 {
		t.Fatalf("report: %+v", rep)
	}
	if earliest["codex"].UnixMilli() != longAgo/3600000*3600000 || earliest["claude"].IsZero() {
		t.Fatalf("earliest retained data: %v", earliest)
	}
	// The backup is a byte copy of the old database and survives the replacement.
	backup, e := os.ReadFile(filepath.Join(rep.Backup, "ai_usage.db"))
	if e != nil || !bytes.Equal(backup, before) {
		t.Fatalf("backup: %v", e)
	}
	for _, name := range []string{"ai_usage.db-wal", "ai_usage.db-shm"} {
		if _, e = os.Stat(filepath.Join(dir, name)); !os.IsNotExist(e) {
			t.Fatalf("%s left behind", name)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, ".ai-usage-rebuild-*")); len(entries) != 0 {
		t.Fatalf("work directory left: %v", entries)
	}
	now := rebuildNow
	s, e := Open(ctx, Options{Directory: dir, Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }, Timeline: timelineOf(krill)})
	if e != nil || s.RebuildRequired() {
		t.Fatalf("the daemon must open the rebuilt database: %v", e)
	}
	defer func() { _ = s.Close(ctx) }()
	if v := s.State(); v.Revision < 8 || v.Generation != 1 {
		t.Fatalf("a rebuild continues the old revision (7) and advances the generation: %+v", v)
	}
	logs := claudeRows(t, s)
	if logs.All != 4 || rep.Carried["claude"] != 2 {
		t.Fatalf("2 rescanned + 2 carried: %d %+v", logs.All, rep.Carried)
	}
	byKey := map[string]Request{}
	for _, r := range logs.Requests {
		byKey[r.Key] = r
		if r.ProfileName != "Krill" || r.Multiplier != .5 {
			t.Fatalf("attribution follows the timeline for every row: %+v", r)
		}
	}
	if byKey["gone"].Details.PriceVersion == "" || byKey["gone"].Costs[2] != 2500000 {
		t.Fatalf("carried row repriced with the 5m fallback: %+v", byKey["gone"])
	}
	if g := byKey["gone"]; g.Input != 1000000 || g.Output != 10 || g.At != time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).UnixMilli() || g.Elapsed != nil || g.TPS != nil {
		t.Fatalf("tokens and time kept, estimated timing dropped: %+v", g)
	}
	if u := byKey["unknown-model"]; u.Cost != 0 || u.Elapsed == nil || *u.Elapsed != 5 {
		t.Fatalf("unknown model costs 0, measured timing kept: %+v", u)
	}
	if a := byKey["alive:r-alive"]; a.Output != 1000000 || a.Cost == 0 {
		t.Fatalf("rescanned row follows the log, not the stale row: %+v", a)
	}
	if len(rep.UnknownModels) != 1 || rep.UnknownModels[0].Model != "brand-new-model" {
		t.Fatalf("unknown models: %+v", rep.UnknownModels)
	}
	// Buckets inside the window equal the details. Outside it the old bucket is kept and
	// re-multiplied by the Profile in effect (3000 raw * .5), and the bucket the rescan
	// made from a log older than the window is dropped.
	buckets, e := s.Buckets(ctx, Query{From: 1, To: now.UnixMilli() + 3600000, Granularity: "hour"})
	if e != nil {
		t.Fatal(e)
	}
	var window, older Summary
	for _, b := range buckets {
		if b.At < rep.WindowFrom.UnixMilli() {
			older.add(b.Summary)
			if b.CLI != "codex" || b.ProfileName != "Krill" {
				t.Fatalf("old bucket attribution: %+v", b)
			}
		} else {
			window.add(b.Summary)
		}
	}
	var want Summary
	for _, r := range logs.Requests {
		want.add(summarize(r))
	}
	if window != want || older.Count != 2 || older.Cost != 1500 || older.BilledCosts[0] != 500 || older.Costs[0] != 1000 || rep.OldBuckets != 1 || rep.DroppedBuckets != 1 {
		t.Fatalf("buckets: window %+v want %+v older %+v rep %+v", window, want, older, rep)
	}
	// Reconcile again from the daemon's own view.
	bucketsMatchWindow(t, s, rep.WindowFrom.UnixMilli(), logs)
	// Rebuilding the rebuilt database changes nothing.
	next := rebuildOptions(dir, home, false, nil)
	next.Now = func() time.Time { return rebuildNow.Add(time.Minute) }
	again, e := Rebuild(ctx, next)
	if e != nil || again.Carried["claude"] != 2 || again.Lifetime["claude"] != rep.Lifetime["claude"] || again.Details != nil && again.Details["claude"] != rep.Details["claude"] {
		t.Fatalf("second rebuild: %v\n%+v\nfirst %+v", e, again, rep)
	}
}

func bucketsMatchWindow(t *testing.T, s *Store, from int64, logs Logs) {
	t.Helper()
	var got, want Summary
	buckets, e := s.Buckets(context.Background(), Query{From: from, To: time.Now().UnixMilli() * 2, Granularity: "hour"})
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range buckets {
		got.add(b.Summary)
	}
	for _, r := range logs.Requests {
		want.add(summarize(r))
	}
	if got != want {
		t.Fatalf("buckets %+v rows %+v", got, want)
	}
}

func TestRebuildDryRunReportsAndChangesNothing(t *testing.T) {
	dir, home, _ := oldWorld(t)
	root := filepath.Dir(dir)
	before := fileHashes(t, root)
	var dryFlag bool
	opts := rebuildOptions(dir, home, true, nil)
	inner := opts.Timeline
	opts.Timeline = func(ctx context.Context, e map[string]time.Time, dry bool) (Resolver, []string, error) {
		dryFlag = dry
		return inner(ctx, e, dry)
	}
	rep, e := Rebuild(context.Background(), opts)
	if e != nil || !rep.DryRun || rep.Replaced || !rep.Reconciled || rep.Backup != "" || !dryFlag {
		t.Fatalf("dry run: %v %+v", e, rep)
	}
	if rep.Details["claude"].Requests != 4 || rep.Carried["claude"] != 2 {
		t.Fatalf("report: %+v", rep)
	}
	after := fileHashes(t, root)
	if len(after) != len(before) {
		t.Fatalf("files changed: %d -> %d", len(before), len(after))
	}
	for name, content := range before {
		if after[name] != content {
			t.Fatalf("%s changed", name)
		}
	}
}

func TestRebuildRefusesAnUnreadableOldSchemaAndLeavesItAlone(t *testing.T) {
	dir, home, _ := oldWorld(t)
	db, _ := sql.Open("sqlite", filepath.Join(dir, "ai_usage.db"))
	_, _ = db.Exec("PRAGMA user_version=2")
	_ = db.Close()
	before := fileHashes(t, filepath.Dir(dir))
	if _, e := Rebuild(context.Background(), rebuildOptions(dir, home, false, nil)); e == nil {
		t.Fatal("schema 2 accepted")
	}
	for name, content := range fileHashes(t, filepath.Dir(dir)) {
		if !strings.Contains(name, "backups") && before[name] != content {
			t.Fatalf("%s changed", name)
		}
	}
}

func TestRebuildWithoutAnOldDatabaseJustScans(t *testing.T) {
	home := home2(t)
	dir := filepath.Join(home, "state")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	logPath := filepath.Join(home, ".claude", "projects", "p", "a.jsonl")
	_ = os.MkdirAll(filepath.Dir(logPath), 0700)
	appendTo(t, logPath, claudeAt("m", "2026-10-28T09:00:00Z"))
	rep, e := Rebuild(context.Background(), rebuildOptions(dir, home, false, nil))
	if e != nil || !rep.Replaced || rep.Backup != "" || rep.Details["claude"].Requests != 1 {
		t.Fatalf("%v %+v", e, rep)
	}
}
