package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionAnalysisPreservesLedgerAndCompletesLiveTurn(t *testing.T) {
	ctx := context.Background()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude", "projects", "p")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	lines := []byte(`{"type":"user","sessionId":"s","uuid":"t1","timestamp":"2026-11-05T23:59:00Z","message":{"content":"private text"}}
{"type":"assistant","sessionId":"s","timestamp":"2026-11-05T23:59:30Z","requestId":"r1","message":{"id":"m1","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":10,"cache_read_input_tokens":90,"output_tokens":20}}}
{"type":"user","sessionId":"s","timestamp":"2026-11-05T23:59:40Z","uuid":"tool","message":{"content":[{"type":"tool_result","content":"private"}]}}
{"type":"assistant","sessionId":"s","timestamp":"2026-11-06T00:00:20Z","requestId":"r2","message":{"id":"m2","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":20,"cache_read_input_tokens":100,"output_tokens":30}}}
`)
	if err = os.WriteFile(path, lines, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s, err := Open(ctx, Options{Home: home, Directory: filepath.Join(home, "state"), Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close(ctx) }()
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	from := now.Add(-12 * time.Hour).UnixMilli()
	to := now.UnixMilli()
	day, err := s.Analyze(ctx, from, to, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(day.Rows) != 1 || day.Rows[0].Count != 1 || day.Rows[0].Input != 120 || day.Rows[0].Turns != 0 {
		t.Fatalf("day %+v", day)
	}
	full, err := s.Analyze(ctx, from, to, []SessionKey{{"claude", "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Rows) != 1 || full.Rows[0].Count != 2 || full.Rows[0].Turn != "t1" || full.Rows[0].Complete {
		t.Fatalf("trajectory %+v", full)
	}
	if len(full.Requests) != 2 || full.Requests[0].RequestID == full.Requests[1].RequestID || full.Requests[0].Count != 1 || full.Requests[1].Count != 1 {
		t.Fatalf("individual requests lost: %+v", full.Requests)
	}
	if full.Requests[0].Input+full.Requests[1].Input != full.Rows[0].Input || full.Requests[0].Cost+full.Requests[1].Cost != full.Rows[0].Cost {
		t.Fatal("request totals differ from turn totals")
	}
	original := full.Rows[0].Cost
	// Simulate a pre-analysis cursor. A one-time metadata reread must not add ledger amounts.
	if _, err = s.w.Exec("UPDATE cursors SET state=json_remove(state,'$.AnalysisVersion')"); err != nil {
		t.Fatal(err)
	}
	s.stamps = map[string]stamp{}
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"system","subtype":"compact_boundary","sessionId":"s","timestamp":"2026-11-06T00:00:21Z"}` + "\n" + `{"type":"system","subtype":"turn_duration","sessionId":"s","timestamp":"2026-11-06T00:01:00Z"}` + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	complete, err := s.Analyze(ctx, from, to, []SessionKey{{"claude", "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(complete.Rows) != 1 || !complete.Rows[0].Complete || !complete.Rows[0].Compressed || complete.Rows[0].Count != 2 || complete.Rows[0].Cost != original {
		t.Fatalf("complete %+v", complete)
	}
	before := s.State()
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if s.State().Revision != before.Revision {
		t.Fatal("unchanged scan moved revision")
	}
}

func TestAnalysisIdentityAndInvalidRange(t *testing.T) {
	var p parseState
	for _, line := range []string{`{"type":"session_meta","timestamp":"2026-11-06T10:00:00Z","payload":{"id":"s"}}`, `{"type":"event_msg","timestamp":"2026-11-06T10:00:01Z","payload":{"type":"task_started","turn_id":"t"}}`} {
		p.parse("codex", []byte(line))
	}
	r, _ := p.parse("codex", []byte(`{"type":"event_msg","timestamp":"2026-11-06T10:00:02Z","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":20},"total_token_usage":{"input_tokens":100}}}}`))
	if r == nil || r.turn != "t" || r.Session != "s" {
		t.Fatalf("request %+v", r)
	}
	e, _ := p.parse("codex", []byte(`{"type":"event_msg","timestamp":"2026-11-06T10:00:03Z","payload":{"type":"task_complete","turn_id":"t"}}`))
	if e == nil || e.analysis == nil || e.analysis.Ended == 0 {
		t.Fatal("completion missing")
	}
	s := &Store{options: Options{Now: time.Now}}
	if _, err := s.Analyze(context.Background(), 0, 1, nil); err != ErrRange {
		t.Fatal(err)
	}
}
