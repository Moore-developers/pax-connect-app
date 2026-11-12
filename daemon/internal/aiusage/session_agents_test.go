package aiusage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func agentFixture(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	ctx := context.Background()
	h := home(t)
	from := time.Date(2026, 11, 7, 0, 0, 0, 0, time.Local)
	now := from.AddDate(0, 0, 2)
	s := openAt(t, h, &now, nil)
	t.Cleanup(func() { _ = s.Close(ctx) })
	stamp := from.Add(time.Hour).UTC().Format(time.RFC3339Nano)
	write := func(cli, name, body string) {
		t.Helper()
		dir := "sessions"
		if cli == "claude" {
			dir = "projects"
		}
		path := filepath.Join(h, "."+cli, dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if cli == "dsh" {
			enc, err := zstd.NewWriter(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = enc.Close() }()
			if err = os.WriteFile(path, enc.EncodeAll([]byte(body), nil), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			appendTo(t, path, body)
		}
		if err := os.Chtimes(path, now, now); err != nil {
			t.Fatal(err)
		}
	}
	write("codex", "root.jsonl", fmt.Sprintf("{\"type\":\"session_meta\",\"timestamp\":%q,\"payload\":{\"id\":\"root\"}}\n", stamp))
	for _, pair := range [][2]string{{"child", "root"}, {"nested", "child"}, {"empty", "root"}} {
		write("codex", pair[0]+".jsonl", fmt.Sprintf("{\"type\":\"session_meta\",\"timestamp\":%q,\"payload\":{\"id\":%q,\"source\":{\"subagent\":{\"thread_spawn\":{\"parent_thread_id\":%q}}}}}\n", stamp, pair[0], pair[1]))
	}
	write("claude", "p/root.jsonl", "{\"type\":\"user\",\"sessionId\":\"root\"}\n")
	write("claude", "p/root/subagents/agent-worker.jsonl", fmt.Sprintf("{\"type\":\"assistant\",\"sessionId\":\"root\",\"agentId\":\"worker\",\"isSidechain\":true,\"timestamp\":%q,\"requestId\":\"req\",\"message\":{\"id\":\"msg\",\"usage\":{}}}\n", stamp))
	write("dsh", "root/session.v4.jsonl.zstd", fmt.Sprintf("{\"type\":\"session\",\"id\":\"root\",\"version\":4}\n{\"type\":\"subagent/catalog\",\"time\":%d,\"data\":{\"childId\":\"child\",\"childCreatedAt\":%d}}\n", from.Add(time.Hour).UnixMilli(), from.Add(time.Hour).UnixMilli()))
	write("dsh", "child/session.v4.jsonl.zstd", fmt.Sprintf("{\"type\":\"session\",\"id\":\"child\",\"version\":4}\n{\"type\":\"subagent/descriptor\",\"time\":%d,\"data\":{\"version\":1}}\n", from.Add(time.Hour).UnixMilli()))
	var requests []Request
	for _, cli := range []string{"codex", "claude", "dsh"} {
		requests = append(requests, Request{CLI: cli, Session: "root", Key: "main", Model: "main-model", At: from.Add(time.Hour).UnixMilli(), Input: 100, turn: "one"})
		r := Request{CLI: cli, Session: "child", Key: "child", Model: "child-model", At: from.Add(time.Hour).UnixMilli(), Input: 200, turn: "one", Costs: [4]int64{10, 20, 30, 900}, Details: Details{Billed: [4]int64{1, 2, 3, 90}}}
		if cli == "claude" {
			r.Session, r.Key, r.turn = "root", "msg:req", ""
		}
		requests = append(requests, r)
	}
	requests = append(requests, Request{CLI: "codex", Session: "nested", Key: "nested", Model: "nested-model", At: from.Add(time.Hour).UnixMilli(), Input: 300, turn: "one"})
	if err := s.commit(ctx, requests, cursor{Path: "agent-fixture"}); err != nil {
		t.Fatal(err)
	}
	files, sources := s.sources()
	if err := s.scanCatalog(ctx, files, sources); err != nil {
		t.Fatal(err)
	}
	return s, from.UnixMilli(), from.AddDate(0, 0, 1).UnixMilli()
}

func TestNativeAgentModelsRollIntoParentWithoutDuplicatingAllUsage(t *testing.T) {
	s, from, to := agentFixture(t)
	a, err := s.Analyze(context.Background(), from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, row := range a.ContextUsage {
		if row.Model == "" {
			counts[row.CLI+"/"+row.Session] = row.Requests
		}
	}
	if counts["/"] != 7 || counts["codex/root"] != 3 || counts["claude/root"] != 2 || counts["dsh/root"] != 2 {
		t.Fatalf("parent totals must include descendants while global remains 7: %v", counts)
	}
	for _, row := range a.ContextUsage {
		if row.Model != "" {
			continue
		}
		if row.CLI == "" && (row.Turns != 6 || row.KnownTurnRequests != 6) {
			t.Fatal("global native turn identities collided", row)
		}
		if row.CLI == "codex" && row.Session == "root" && row.Turns != 3 {
			t.Fatal("descendant turn IDs must remain separate", row)
		}
	}

}

func TestCatalogKeepsRequestDateEvenWhenLatestActivityMovesToNextDay(t *testing.T) {
	s, from, to := agentFixture(t)
	page, err := s.ListSessions(context.Background(), SessionCatalogQuery{From: from, To: to})
	if err != nil || page.Total != 3 || page.Counts["codex"] != 1 || page.Counts["claude"] != 1 || page.Counts["dsh"] != 1 {
		t.Fatalf("request-date roots must remain selectable, linked children hidden: %+v %v", page, err)
	}
}

func TestAgentSummaryCountsEmptyAgentsAndOnlyInputCosts(t *testing.T) {
	s, from, to := agentFixture(t)
	a, err := s.Analyze(context.Background(), from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct{ count, input, cost int64 }{"/": {5, 900, 180}, "codex/root": {3, 500, 60}, "claude/root": {1, 200, 60}, "dsh/root": {1, 200, 60}}
	for _, row := range a.ContextUsage {
		want, ok := expected[row.CLI+"/"+row.Session]
		if row.Model != "" || !ok {
			continue
		}
		var input, cost, channel int64
		for _, b := range row.SubagentBands {
			input += b.Input
			cost += b.OfficialCost
			channel += b.ChannelCost
		}
		if row.Subagents != want.count || input != want.input || cost != want.cost || channel != want.cost/10 || row.SubagentsIncomplete {
			t.Fatalf("agent summary %s/%s: %+v", row.CLI, row.Session, row)
		}
		delete(expected, row.CLI+"/"+row.Session)
	}
	if len(expected) > 0 {
		t.Fatal("missing agent summaries", expected)
	}
	before := s.State()
	files, sources := s.sources()
	if err = s.scanCatalog(context.Background(), files, sources); err != nil || s.State() != before {
		t.Fatalf("idle metadata scan changed versions: %+v %v", s.State(), err)
	}
}

func TestCatalogRequestPresenceInvalidatesOldPageWithoutMetadataChange(t *testing.T) {
	s, from, to := agentFixture(t)
	ctx := context.Background()
	if _, err := s.w.Exec(`INSERT INTO session_catalog VALUES('codex','later','Later','test','named',?,0)`, to+1); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessions(ctx, SessionCatalogQuery{From: from, To: to, PageSize: 1})
	if err != nil || page.NextPageToken == "" {
		t.Fatal(page, err)
	}
	if err = s.commit(ctx, []Request{{CLI: "codex", Session: "later", Key: "new", Model: "m", At: from + 1, Input: 1}}, cursor{Path: "later"}); err != nil {
		t.Fatal(err)
	}
	stale, err := s.ListSessions(ctx, SessionCatalogQuery{From: from, To: to, PageSize: 1, PageToken: page.NextPageToken})
	if err != nil || !stale.ResetRequired {
		t.Fatal("usage-only page change accepted", stale, err)
	}
	fresh, err := s.ListSessions(ctx, SessionCatalogQuery{From: from, To: to})
	if err != nil || fresh.Total != 4 || fresh.Revision != page.Revision || fresh.UsageRevision <= page.UsageRevision {
		t.Fatal(fresh, err)
	}
}

func TestAgentSchema10MetadataReplayKeepsLedgerAndShowsMissingSource(t *testing.T) {
	s, from, to := agentFixture(t)
	ctx := context.Background()
	before := s.State()
	opts := s.options
	var ledger string
	query := `SELECT json_group_array(json_array(cli,request_key,input,read,write,output,cost,c0,c1,c2,c3,cast(details AS TEXT))) FROM (SELECT * FROM requests ORDER BY cli,request_key)`
	if err := s.r.QueryRow(query).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`DROP TABLE session_agents;DROP TABLE session_agent_days;DROP TABLE session_agent_requests;PRAGMA user_version=10`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close(ctx) }()
	files, sources := migrated.sources()
	if err = migrated.scanCatalog(ctx, files, sources); err != nil {
		t.Fatal(err)
	}
	var after string
	if err = migrated.r.QueryRow(query).Scan(&after); err != nil || after != ledger || migrated.State().Revision != before.Revision || migrated.State().Generation != before.Generation {
		t.Fatal("metadata migration changed ledger", err)
	}
	if err = os.Remove(filepath.Join(opts.Home, ".codex", "sessions", "child.jsonl")); err != nil {
		t.Fatal(err)
	}
	files, sources = migrated.sources()
	if err = migrated.scanCatalog(ctx, files, sources); err != nil {
		t.Fatal(err)
	}
	a, err := migrated.Analyze(ctx, from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range a.ContextUsage {
		if row.CLI == "codex" && row.Session == "root" && row.Model == "" {
			if row.Requests != 3 || row.Subagents != 3 || !row.SubagentsIncomplete {
				t.Fatal("known source loss must retain totals and mark incomplete", row)
			}
			return
		}
	}
	t.Fatal("parent disappeared after source loss")
}

func TestUnknownAndCyclicAgentParentsRemainSelectable(t *testing.T) {
	s, from, to := agentFixture(t)
	ctx := context.Background()
	if _, err := s.w.Exec(`INSERT INTO session_catalog VALUES('codex','orphan','Orphan','test','named',?,1),('codex','x','Cycle x','test','named',?,1),('codex','y','Cycle y','test','named',?,1); INSERT INTO session_agents VALUES('codex','orphan','orphan','missing',0,0,1),('codex','x','x','y',0,0,1),('codex','y','y','x',0,0,1)`, from+1, from+1, from+1); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessions(ctx, SessionCatalogQuery{From: from, To: to, CLI: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, v := range page.Items {
		if v.Unlinked {
			found++
		}
	}
	if found != 3 {
		t.Fatal("unresolved records disappeared", page)
	}
}

func TestClaudeInlineSidechainsDoNotCarryIdentityIntoParentOrOtherAgents(t *testing.T) {
	var state catalogFileState
	updates := agentUpdates{}
	lines := []string{
		`{"type":"assistant","sessionId":"root","agentId":"a","isSidechain":true,"timestamp":"2026-11-07T01:00:00Z","requestId":"one","message":{"id":"m1","usage":{}}}`,
		`{"type":"assistant","sessionId":"root","timestamp":"2026-11-08T01:00:00Z","requestId":"main","message":{"id":"main","usage":{}}}`,
		`{"type":"assistant","sessionId":"root","agentId":"b","isSidechain":true,"timestamp":"2026-11-07T02:00:00Z","requestId":"two","message":{"id":"m2","usage":{}}}`,
	}
	for _, line := range lines {
		state.parse("claude", []byte(line), &updates)
	}
	if len(updates.Agents) != 2 || len(updates.Days) != 2 || len(updates.Requests) != 2 || updates.Requests[agentRequestKey{"claude", "m2:two"}].Agent.ID != "b" || state.Agent.Key.ID != "" {
		t.Fatal("inline agents contaminated parent identity", updates)
	}
}
