package main

import (
	"bytes"
	"context"
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"pax-connect/daemon/internal/aiprofile"
	"pax-connect/daemon/internal/aiusage"
)

const (
	krillID    = "11111111111111111111111111111111"
	derouterID = "22222222222222222222222222222222"
)

// world is a data directory with two saved Profiles and one recent request per CLI.
func world(t *testing.T, names ...string) (dir, home string) {
	t.Helper()
	// A short path: the daemon socket under it must fit a sockaddr.
	tmp, e := os.MkdirTemp("/tmp", "pxr")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	base, e := filepath.EvalSymlinks(tmp)
	if e != nil {
		t.Fatal(e)
	}
	home, dir = filepath.Join(base, "home"), filepath.Join(base, "data")
	for _, d := range []string{home, dir} {
		if e = os.MkdirAll(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	m, e := aiprofile.New(aiprofile.Options{Directory: filepath.Join(dir, "AIProfiles"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	for i, n := range names {
		id := []string{krillID, derouterID}[i]
		multiplier := []float64{.16, .26}[i]
		if _, e = m.Mutate(context.Background(), aiprofile.Mutation{Action: "save", OperationID: "save-" + id, Profile: aiprofile.Profile{ID: id, Name: n, BaseURL: "https://" + id[:4] + ".test", Multiplier: multiplier}, Key: "redacted"}); e != nil {
			t.Fatal(e)
		}
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	at := time.Now().Add(-time.Hour).UTC()
	stamp := func(d time.Duration) string { return at.Add(d).Format(time.RFC3339Nano) }
	write := func(path string, lines ...string) {
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(home, ".claude", "projects", "p", "a.jsonl"),
		`{"type":"assistant","sessionId":"s","requestId":"r","timestamp":"`+stamp(0)+`","message":{"id":"m","model":"claude-sonnet-5-5","usage":{"input_tokens":1000000,"output_tokens":1000000}}}`)
	write(filepath.Join(home, ".codex", "sessions", "rollout.jsonl"),
		`{"type":"session_meta","timestamp":"`+stamp(0)+`","payload":{"id":"c"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-6.1-sol","effort":"high"}}`,
		`{"type":"event_msg","timestamp":"`+stamp(3*time.Second)+`","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1000000,"output_tokens":1000000},"total_token_usage":{"total_tokens":2000000}}}}`)
	return dir, home
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if e := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, e := os.ReadFile(p)
		out[p] = string(b)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	return out
}

func runRebuild(t *testing.T, dir, home string, extra ...string) (code int, out, errs string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(append([]string{"-dir", dir, "-home", home}, extra...), &o, &e)
	return code, o.String(), e.String()
}

func seedCount(t *testing.T, dir string) (n int) {
	t.Helper()
	db, e := sql.Open("sqlite", "file:"+filepath.Join(dir, "AIProfiles", "profile_events.db")+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	if e = db.QueryRow("SELECT count(*) FROM profile_events WHERE event_key LIKE 'seed:%'").Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestRebuildSeedsTheTimelineAttributesEveryRequestAndIsIdempotent(t *testing.T) {
	dir, home := world(t, "Krill", "Derouter.ai")
	code, out, errs := runRebuild(t, dir, home)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	if seedCount(t, dir) != 2 || !strings.Contains(out, "codex -> Krill") || !strings.Contains(out, "claude -> Derouter.ai") || !strings.Contains(out, "buckets equal details: true") {
		t.Fatalf("report:\n%s", out)
	}
	logs := readLogs(t, dir, home)
	for _, r := range logs.Requests {
		switch r.CLI {
		case "codex":
			if r.ProfileName != "Krill" || r.Multiplier != .16 {
				t.Fatalf("codex: %+v", r)
			}
		case "claude":
			if r.ProfileName != "Derouter.ai" || r.Multiplier != .26 {
				t.Fatalf("claude: %+v", r)
			}
		}
	}
	if logs.All != 2 {
		t.Fatalf("requests: %d", logs.All)
	}
	// A second run seeds nothing more and rebuilds the same data.
	time.Sleep(1100 * time.Millisecond) // backups are named by the second
	code, out, errs = runRebuild(t, dir, home)
	if code != 0 || seedCount(t, dir) != 2 || !strings.Contains(out, "already seeded") || readLogs(t, dir, home).All != 2 {
		t.Fatalf("second run %d\n%s\n%s", code, out, errs)
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "backups", "ai-usage-*"))
	if len(backups) != 1 {
		t.Fatalf("one backup per run that had a database: %v", backups)
	}
}

func readLogs(t *testing.T, dir, home string) aiusage.Logs {
	t.Helper()
	s, e := aiusage.Open(context.Background(), aiusage.Options{Directory: dir, Home: home, Owner: uint32(os.Geteuid())})
	if e != nil || s.RebuildRequired() {
		t.Fatalf("open the rebuilt database: %v", e)
	}
	defer func() { _ = s.Close(context.Background()) }()
	logs, e := s.List(context.Background(), aiusage.Query{From: 1, To: time.Now().Add(time.Hour).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	return logs
}

func TestRebuildDryRunChangesNothing(t *testing.T) {
	dir, home := world(t, "Krill", "Derouter.ai")
	before := tree(t, dir)
	code, out, errs := runRebuild(t, dir, home, "-dry-run")
	if code != 0 || !strings.Contains(out, "DRY RUN") || !strings.Contains(out, "codex -> Krill") || !strings.Contains(out, "buckets equal details: true") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	after := tree(t, dir)
	if len(after) != len(before) {
		t.Fatalf("files before %d, after %d", len(before), len(after))
	}
	for name, content := range before {
		if after[name] != content {
			t.Fatalf("%s changed", name)
		}
	}
	if seedCount(t, dir) != 0 {
		t.Fatal("a dry run seeded events")
	}
}

func TestRebuildFailsClearlyForAMissingOrInexactProfileName(t *testing.T) {
	for _, names := range [][]string{{"Krill"}, {"Krill", "derouter.ai"}} {
		dir, home := world(t, names...)
		code, _, errs := runRebuild(t, dir, home)
		if code == 0 || !strings.Contains(errs, `profile "Derouter.ai"`) {
			t.Fatalf("%v: exit %d: %s", names, code, errs)
		}
		if _, e := os.Stat(filepath.Join(dir, "ai_usage.db")); !os.IsNotExist(e) {
			t.Fatal("a database was created despite the failure")
		}
		if seedCount(t, dir) != 0 {
			t.Fatal("seeds written despite the failure")
		}
	}
}

func TestRebuildRefusesWhileTheDaemonRuns(t *testing.T) {
	dir, home := world(t, "Krill", "Derouter.ai")
	// The profile store lock is held for as long as the daemon lives.
	m, e := aiprofile.New(aiprofile.Options{Directory: filepath.Join(dir, "AIProfiles"), Home: home, Owner: uint32(os.Geteuid())})
	if e != nil {
		t.Fatal(e)
	}
	code, _, errs := runRebuild(t, dir, home)
	if code == 0 || !strings.Contains(errs, "running") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	_ = m.Close()
	if _, e = os.Stat(filepath.Join(dir, "ai_usage.db")); !os.IsNotExist(e) {
		t.Fatal("the refused run touched the database")
	}
}

func TestRebuildRefusesWhileTheDaemonSocketAnswers(t *testing.T) {
	dir, home := world(t, "Krill", "Derouter.ai")
	if e := os.Mkdir(filepath.Join(dir, "run"), 0700); e != nil {
		t.Fatal(e)
	}
	l, e := net.Listen("unix", filepath.Join(dir, "run", "paxd.sock"))
	if e != nil {
		t.Fatal(e)
	}
	code, _, errs := runRebuild(t, dir, home)
	if code == 0 || !strings.Contains(errs, "running") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	// The socket file a stopped daemon leaves behind is no reason to refuse.
	_ = l.Close()
	if _, e = os.Stat(filepath.Join(dir, "run", "paxd.sock")); e != nil {
		if e = os.WriteFile(filepath.Join(dir, "run", "paxd.sock"), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if code, _, errs = runRebuild(t, dir, home, "-dry-run"); code != 0 {
		t.Fatalf("stale socket: exit %d: %s", code, errs)
	}
}
