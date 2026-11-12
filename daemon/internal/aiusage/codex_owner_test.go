package aiusage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Native child rollouts carry a second, inherited parent header. Their response
// thread is the child, while session_id remains the original parent.
func codexChildBody(at int64) string {
	return codexEvent("session_meta", at, map[string]any{"id": "child", "session_id": "parent", "forked_from_id": "parent", "source": map[string]any{"subagent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "parent"}}}}) +
		codexMeta("parent", at, false) + codexTask("child-turn", at+1) +
		codexEvent("event_msg", at+2, map[string]any{"type": "thread_settings_applied", "thread_id": "child", "thread_settings": map[string]any{"model": "gpt-6.1-sol"}}) +
		strings.Replace(codexResponseLine("child", "child-turn", "response-child", at+3), `"session_id":"child"`, `"session_id":"parent"`, 1) + codexTick(at+4, 100)
}

func TestCodexChildOwnershipAndRestart(t *testing.T) {
	s, path, now := codexTestStore(t)
	ctx := context.Background()
	at := now.Add(-time.Hour).UnixMilli()
	appendTo(t, path, codexChildBody(at))
	codexScan(t, s)
	got := codexLogs(t, s)
	if got.All != 1 || got.Requests[0].Session != "child" || got.Requests[0].Model != "gpt-6.1-sol" {
		t.Fatalf("child request lost: %+v", got.Requests)
	}
	key := got.Requests[0].Key
	var response string
	if e := s.w.QueryRow("SELECT response_id FROM codex_usage_evidence WHERE request_key=?", key).Scan(&response); e != nil || response != "response-child" {
		t.Fatal(response, e)
	}
	var session string
	if e := s.w.QueryRow("SELECT session FROM analysis_requests WHERE request_key=?", key).Scan(&session); e != nil || session != "child" {
		t.Fatal(session, e)
	}
	options := s.options
	if e := s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	var e error
	s, e = Open(ctx, options)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	appendTo(t, path, codexTask("next-turn", at+10), codexContext("next-turn", "gpt-6.1-sol", at+11), strings.Replace(codexResponseLine("child", "next-turn", "next-response", at+12), `"session_id":"child"`, `"session_id":"parent"`, 1), codexTick(at+13, 200))
	codexScan(t, s)
	for _, r := range codexLogs(t, s).Requests {
		if r.Session != "child" {
			t.Fatal(r.Session)
		}
	}
	before := s.State()
	codexScan(t, s)
	if s.State() != before {
		t.Fatal("idle scan changed state")
	}
	codexCheckBuckets(t, s)
}

func TestCodexChildRepairsLegacyCursorWithoutRepricing(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	appendTo(t, path, codexChildBody(at))
	codexScan(t, s)
	// Recreate a deployed cursor and row before the identity fix.
	var raw []byte
	if e := s.w.QueryRow("SELECT state FROM cursors WHERE path=?", path).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var state map[string]any
	_ = json.Unmarshal(raw, &state)
	delete(state, "CodexOwnerVersion")
	state["Session"] = "parent"
	state["AnalysisSession"] = "parent"
	b, _ := json.Marshal(state)
	for _, q := range []string{"UPDATE requests SET session='parent' WHERE cli='codex'", "UPDATE analysis_requests SET session='parent' WHERE cli='codex'", "UPDATE codex_usage_evidence SET session='parent',response_id='' WHERE 1"} {
		if _, e := s.w.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.w.Exec("UPDATE cursors SET state=? WHERE path=?", b, path); e != nil {
		t.Fatal(e)
	}
	before := codexLogs(t, s)
	version := s.State()
	s.stamps = map[string]stamp{}
	codexScan(t, s)
	after := codexLogs(t, s)
	if after.All != before.All || after.Total != before.Total || after.Requests[0].Key != before.Requests[0].Key || after.Requests[0].Session != "child" {
		t.Fatalf("repair changed accounting or missed owner: %+v", after.Requests)
	}
	if s.State().Generation != version.Generation+1 {
		t.Fatal("repair did not invalidate snapshots", s.State(), version)
	}
	codexScan(t, s)
	codexCheckBuckets(t, s)
}

func TestCodexChildInheritedResponsesAndAliases(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	root := codexMeta("parent", at-100, false) + codexTask("parent-turn", at-99) + codexContext("parent-turn", "gpt-6.1-sol", at-98) + codexResponseLine("parent", "parent-turn", "parent-response", at-97) + codexTick(at-96, 100)
	parentPath := strings.Replace(path, "a.jsonl", "parent.jsonl", 1)
	appendTo(t, parentPath, root)
	codexScan(t, s)
	body := codexChildBody(at)
	// Copied parent usage is explicitly parent-owned; replay of that response is
	// an alias, while the child's equal-valued response remains a separate request.
	inherited := codexTask("parent-turn", at+1) + codexResponseLine("parent", "parent-turn", "parent-response", at+2) + codexTick(at+3, 100)
	split := strings.Index(body, codexTask("child-turn", at+1))
	body = body[:split] + inherited + body[split:]
	appendTo(t, path, body)
	codexScan(t, s)
	got := codexLogs(t, s)
	if got.All != 2 {
		t.Fatalf("inherited request recounted: %d", got.All)
	}
	counts := map[string]int{}
	for _, r := range got.Requests {
		counts[r.Session]++
	}
	if counts["parent"] != 1 || counts["child"] != 1 {
		t.Fatal(counts)
	}
	alias := strings.Replace(path, "a.jsonl", "alias.jsonl", 1)
	// Different file/segment, same native response in the same child thread.
	appendTo(t, alias, codexChildBody(at+200))
	codexScan(t, s)
	if got = codexLogs(t, s); got.All != 2 {
		t.Fatalf("child response alias counted twice: %d", got.All)
	}
	codexCheckBuckets(t, s)
}

func TestCodexChildRepairRollbackAndRetiredKey(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	appendTo(t, path, codexChildBody(at))
	codexScan(t, s)
	var raw []byte
	_ = s.w.QueryRow("SELECT state FROM cursors WHERE path=?", path).Scan(&raw)
	var state map[string]any
	_ = json.Unmarshal(raw, &state)
	delete(state, "CodexOwnerVersion")
	b, _ := json.Marshal(state)
	if _, e := s.w.Exec("UPDATE cursors SET state=? WHERE path=?", b, path); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"UPDATE requests SET session='parent'", "UPDATE analysis_requests SET session='parent'", "UPDATE codex_usage_evidence SET session='parent'", "CREATE TRIGGER fail_owner BEFORE UPDATE OF session ON analysis_requests BEGIN SELECT RAISE(ABORT,'injected'); END"} {
		if _, e := s.w.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	s.stamps = map[string]stamp{}
	before := s.State()
	if e := s.Scan(context.Background()); e == nil {
		t.Fatal("expected injected failure")
	}
	var owner string
	_ = s.w.QueryRow("SELECT session FROM requests").Scan(&owner)
	if owner != "parent" || s.State().Revision != before.Revision || s.State().Generation != before.Generation {
		t.Fatal("partial repair committed", owner, s.State())
	}
	var offset int64
	_ = s.w.QueryRow("SELECT offset FROM cursors WHERE path=?", path).Scan(&offset)
	if offset == 0 {
		t.Fatal("cursor advanced on failure")
	}
	if _, e := s.w.Exec("DROP TRIGGER fail_owner; DELETE FROM requests; DELETE FROM analysis_requests"); e != nil {
		t.Fatal(e)
	}
	codexScan(t, s)
	if got := codexLogs(t, s); got.All != 0 {
		t.Fatal("retired request resurrected")
	}
}

func TestCodexNestedChildAndUnrelatedIdentity(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	body := strings.Replace(codexChildBody(at), `"parent_thread_id":"parent"`, `"parent_thread_id":"middle"`, 1)
	appendTo(t, path, body)
	codexScan(t, s)
	if r := codexLogs(t, s).Requests[0]; r.Session != "child" {
		t.Fatal(r.Session)
	}
	nested := codexTask("middle-turn", at+10) + strings.Replace(codexResponseLine("middle", "middle-turn", "middle-response", at+11), `"session_id":"middle"`, `"session_id":"parent"`, 1) + codexTick(at+12, 200)
	appendTo(t, path, nested)
	codexScan(t, s)
	var middle int
	if e := s.w.QueryRow("SELECT count(*) FROM requests WHERE session='middle'").Scan(&middle); e != nil || middle != 1 {
		t.Fatal("nested parent/root response rejected", middle, e)
	}
	var middleTurn string
	if e := s.w.QueryRow("SELECT a.turn FROM analysis_requests a JOIN requests r USING(cli,request_key) WHERE r.session='middle'").Scan(&middleTurn); e != nil || middleTurn != "middle-turn" {
		t.Fatal("inherited request borrowed child's turn", middleTurn, e)
	}
	before := s.State()
	appendTo(t, path, codexResponseLine("unrelated", "child-turn", "foreign", at+20), codexTick(at+21, 200))
	if e := s.Scan(context.Background()); e == nil || s.State().Revision != before.Revision {
		t.Fatal("unrelated thread accepted", e)
	}
}

func TestCodexChildRepairResumesAfterCommittedBatch(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	body := codexChildBody(at)
	i := strings.Index(body, codexTask("child-turn", at+1))
	body = body[:i] + strings.Repeat(codexEvent("event_msg", at, map[string]any{"type": "unrelated"}), 501) + body[i:]
	appendTo(t, path, body)
	codexScan(t, s)
	var raw []byte
	_ = s.w.QueryRow("SELECT state FROM cursors WHERE path=?", path).Scan(&raw)
	var state map[string]any
	_ = json.Unmarshal(raw, &state)
	delete(state, "CodexOwnerVersion")
	b, _ := json.Marshal(state)
	_, _ = s.w.Exec("UPDATE cursors SET state=? WHERE path=?", b, path)
	for _, q := range []string{"UPDATE requests SET session='parent'", "UPDATE analysis_requests SET session='parent'", "UPDATE codex_usage_evidence SET session='parent'", "CREATE TRIGGER fail_owner BEFORE UPDATE OF session ON analysis_requests BEGIN SELECT RAISE(ABORT,'injected'); END"} {
		if _, e := s.w.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	s.stamps = map[string]stamp{}
	if e := s.Scan(context.Background()); e == nil {
		t.Fatal("expected injected failure")
	}
	var offset int64
	_ = s.w.QueryRow("SELECT offset FROM cursors WHERE path=?", path).Scan(&offset)
	if offset <= 0 || offset >= int64(len(body)) {
		t.Fatal("expected committed partial replay", offset)
	}
	_, _ = s.w.Exec("DROP TRIGGER fail_owner")
	codexScan(t, s)
	if got := codexLogs(t, s); got.Requests[0].Session != "child" {
		t.Fatal("same stat skipped unfinished repair")
	}
	codexCheckBuckets(t, s)
}

func TestCodexChildPartialHeaderWaitsForCompleteLine(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	body := codexChildBody(now.Add(-time.Hour).UnixMilli())
	half := strings.Index(body, "\n") / 2
	appendTo(t, path, body[:half])
	codexScan(t, s)
	if got := codexLogs(t, s); got.All != 0 {
		t.Fatal("partial header counted")
	}
	appendTo(t, path, body[half:])
	codexScan(t, s)
	if got := codexLogs(t, s); got.All != 1 || got.Requests[0].Session != "child" {
		t.Fatal("completed header lost child identity")
	}
}
