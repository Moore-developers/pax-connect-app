package aiusage

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func near(v *float64, want float64) bool { return v != nil && math.Abs(*v-want) < 1e-9 }

func feed(t *testing.T, s *parseState, cli string, lines ...string) []*Request {
	t.Helper()
	var out []*Request
	for _, l := range lines {
		r, _ := s.parse(cli, []byte(l))
		if r != nil {
			out = append(out, r)
		}
	}
	return out
}

func TestClaudeElapsedIsLastBlockMinusMatchingAPIRequest(t *testing.T) {
	var s parseState
	got := feed(t, &s, "claude",
		`{"type":"api-request","id":"ref-1","timestamp":"2026-10-28T10:00:00.000Z","sessionId":"s"}`,
		`{"type":"assistant","requestRef":"ref-1","requestId":"r1","timestamp":"2026-10-28T10:00:02.500Z","thinkingDurationMs":99999,"message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"output_tokens":100}}}`,
		`{"type":"assistant","requestRef":"ref-1","requestId":"r1","timestamp":"2026-10-28T10:00:04.000Z","message":{"id":"m1","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"output_tokens":100}}}`,
		`{"type":"assistant","requestRef":"missing","requestId":"r2","timestamp":"2026-10-28T10:00:05.000Z","message":{"id":"m2","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"output_tokens":100}}}`,
		`{"type":"assistant","requestId":"r3","timestamp":"2026-10-28T10:00:06.000Z","message":{"id":"m3","model":"claude-sonnet-5-5","usage":{"input_tokens":1,"output_tokens":100}}}`)
	if len(got) != 4 {
		t.Fatalf("records: %d", len(got))
	}
	if !near(got[0].Elapsed, 2.5) || !near(got[1].Elapsed, 4) || !near(got[1].TPS, 25) {
		t.Fatalf("matched: %+v %+v", got[0], got[1])
	}
	for _, r := range got[2:] {
		if r.Elapsed != nil || r.TPS != nil {
			t.Fatalf("unmatched must stay unknown: %+v", r)
		}
	}
}

func TestDSHElapsedStartsAtFinalAttempt(t *testing.T) {
	var s parseState
	got := feed(t, &s, "dsh",
		`{"type":"session","version":4,"id":"s","createdAt":1793145600000}`,
		`{"type":"turn/start","data":{"turn":1}}`,
		`{"type":"step/start","time":1793145600000,"data":{"turn":1,"step":1}}`,
		`{"type":"assistant/message","seq":1,"time":1793145604100,"data":{"message":{"id":"a"},"usage":{"inputTokens":1,"outputTokens":40},"stream":[{"type":"chunk","time":1793145601000,"chunk":{"type":"block-start"}},{"type":"text-chunks","time0":1793145602000,"dt":[500,500]},{"type":"chunk","time":1793145604000,"chunk":{"type":"finish"}}]}}`,
		`{"type":"step/start","time":1793145610000,"data":{"turn":1,"step":2}}`,
		`{"type":"llm/retry","time":1793145620000,"data":{"retry":1}}`,
		`{"type":"llm/retry-started","time":1793145621000,"data":{"retry":1}}`,
		`{"type":"assistant/message","seq":2,"time":1793145625100,"data":{"message":{"id":"b"},"usage":{"inputTokens":1,"outputTokens":40},"stream":[{"type":"text-chunks","time0":1793145622000,"dt":[1000,1000]},{"type":"chunk","time":1793145625000,"chunk":{"type":"finish"}}]}}`,
		`{"type":"assistant/message","seq":3,"time":1793145630000,"data":{"message":{"id":"c"},"usage":{"inputTokens":1,"outputTokens":40},"stream":[]}}`)
	if len(got) != 3 || !near(got[0].Elapsed, 4) || !near(got[0].TPS, 10) {
		t.Fatalf("plain step: %+v", got)
	}
	if !near(got[1].Elapsed, 4) {
		t.Fatalf("retry must start at llm/retry-started, not step/start: %+v", got[1])
	}
	if got[2].Elapsed != nil {
		t.Fatalf("no stream, no guess: %+v", got[2])
	}
}

func codexLines(offsets ...string) []string {
	var out []string
	for _, l := range offsets {
		out = append(out, strings.Replace(l, "@", "2026-10-28T10:", 1))
	}
	return out
}

func TestCodexSendInferenceUsesTurnStartToolOutputsAndPreviousEnd(t *testing.T) {
	record := func(at string) string {
		return `{"timestamp":"@` + at + `Z","type":"token_usage_record","payload":{"response_id":"resp_x"}}`
	}
	tick := func(at string, n int) string {
		return `{"timestamp":"@` + at + `Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":100},"total_token_usage":{"total_tokens":` + strconv.Itoa(n) + `}}}}`
	}
	var s parseState
	got := feed(t, &s, "codex", codexLines(
		`{"timestamp":"@00:00.000Z","type":"session_meta","payload":{"id":"s"}}`,
		`{"timestamp":"@00:01.000Z","type":"event_msg","payload":{"type":"task_started"}}`,
		record("00:05.000"),
		`{"timestamp":"@00:07.000Z","type":"response_item","payload":{"type":"function_call_output"}}`,
		tick("00:07.001", 1), // the count is logged after the tool output, yet the response ended at 00:05
		`{"timestamp":"@00:08.000Z","type":"response_item","payload":{"type":"custom_tool_call_output"}}`,
		record("00:12.000"), tick("00:12.001", 2),
		tick("00:40.000", 2), // repeated count: neither a response nor a send baseline
		`{"timestamp":"@00:41.000Z","type":"response_item","payload":{"type":"message"}}`,
		record("00:50.000"), tick("00:50.001", 3))...)
	if len(got) != 3 {
		t.Fatalf("responses: %d", len(got))
	}
	// 1: turn start to its record; 2: latest tool output to its record; 3: previous end to its record.
	if !near(got[0].Elapsed, 4) || !near(got[1].Elapsed, 4) || !near(got[2].Elapsed, 38) || !near(got[0].TPS, 25) {
		t.Fatalf("elapsed: %v %v %v", *got[0].Elapsed, *got[1].Elapsed, *got[2].Elapsed)
	}
}

func TestCodexOldFilesEndAtTokenCountAndUnknownSendStaysNil(t *testing.T) {
	tick := func(at string, n string) string {
		return `{"timestamp":"@` + at + `Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":1,"output_tokens":30},"total_token_usage":{"total_tokens":` + n + `}}}}`
	}
	var s parseState
	got := feed(t, &s, "codex", codexLines(
		`{"timestamp":"@00:00.000Z","type":"session_meta","payload":{"id":"s"}}`,
		tick("00:03.000", "1"), // no task_started yet: send unknown
		`{"timestamp":"@00:04.000Z","type":"event_msg","payload":{"type":"task_started"}}`,
		tick("00:09.000", "2"))...)
	if len(got) != 2 || got[0].Elapsed != nil {
		t.Fatalf("unknown send: %+v", got[0])
	}
	if !near(got[1].Elapsed, 5) || !near(got[1].TPS, 6) {
		t.Fatalf("old format: %+v", got[1])
	}
}

func TestDeepSeekV41FlashUsesTheFlashTariffAtPeakAndOffPeak(t *testing.T) {
	for _, at := range []time.Time{time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC), time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)} {
		a := Request{Model: "deepseek-flash", At: at.UnixMilli(), Input: 1000000, Output: 1000000, Multiplier: 1}
		b := a
		b.Model = "deepseek-v4.1-flash"
		a.price()
		b.price()
		if b.Cost != a.Cost || b.Costs != a.Costs || b.Cost == 0 {
			t.Fatalf("%v: %+v %+v", at, a, b)
		}
	}
}

func openAt(t *testing.T, home string, now *time.Time, resolve Resolver) *Store {
	t.Helper()
	s, e := Open(context.Background(), Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return *now }, Timeline: timelineOf(resolve)})
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func home(t *testing.T) string {
	t.Helper()
	h, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return h
}

func writeFixture(t *testing.T, path, name string) {
	t.Helper()
	b, e := os.ReadFile("../../../tests/ai-usage/" + name)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}

func TestRedactedFixturesYieldMeasuredElapsedForEverySource(t *testing.T) {
	h := home(t)
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	writeFixture(t, filepath.Join(h, ".claude", "projects", "p", "a.jsonl"), "claude-redacted.jsonl")
	writeFixture(t, filepath.Join(h, ".codex", "sessions", "rollout.jsonl"), "codex-redacted.jsonl")
	dsh, e := os.ReadFile("../../../tests/ai-usage/dsh-v4-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	encoder, _ := zstd.NewWriter(nil)
	defer func() { _ = encoder.Close() }()
	dshPath := filepath.Join(h, ".dsh", "sessions", "a", "session.v4.jsonl.zstd")
	if e = os.MkdirAll(filepath.Dir(dshPath), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(dshPath, encoder.EncodeAll(dsh, nil), 0600); e != nil {
		t.Fatal(e)
	}
	_ = os.Chtimes(dshPath, now.Add(-time.Hour), now.Add(-time.Hour))
	s := openAt(t, h, &now, nil)
	defer func() { _ = s.Close(context.Background()) }()
	if e = s.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	logs, e := s.List(context.Background(), Query{From: 1, To: now.UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, r := range logs.Requests {
		switch {
		case r.CLI == "claude" && r.Succeeded:
			// Two content blocks of one request: the later block ends it, 00:23:30.000 to 00:23:38.352.
			if !near(r.Elapsed, 8.352) || !near(r.TPS, 286/8.352) {
				t.Fatalf("claude: %+v", r)
			}
		case r.CLI == "claude":
			if r.Elapsed != nil || r.TPS != nil {
				t.Fatalf("unmatched claude failure: %+v", r)
			}
		case r.CLI == "codex":
			if !near(r.Elapsed, 53.154) {
				t.Fatalf("codex: %+v", r)
			}
		case r.CLI == "dsh" && r.Succeeded:
			if !near(r.Elapsed, 1.874) || !near(r.TPS, 197/1.874) {
				t.Fatalf("dsh: %+v", r)
			}
		}
		seen[r.CLI] = true
	}
	if len(seen) != 3 || logs.Total.TPSCount != 3 || logs.Total.ElapsedCount != 3 {
		t.Fatalf("totals: %+v", logs.Total)
	}
	buckets, e := s.Summarize(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var tps int64
	for _, b := range buckets {
		tps += b.TPSCount
	}
	if tps != 3 {
		t.Fatalf("bucket tps count %d", tps)
	}
	last, e := s.LastUsed(context.Background())
	if e != nil || last["claude"] != time.Date(2025, 1, 1, 6, 24, 50, 5000000, time.UTC).UnixMilli() || len(last) != 3 {
		t.Fatalf("last used: %v %v", last, e)
	}
	now = now.AddDate(0, 0, 94)
	if e = s.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	last, e = s.LastUsed(context.Background())
	if e != nil || last["claude"] != time.Date(2025, 1, 1, 6, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("last used after retention falls back to the bucket hour: %v %v", last, e)
	}
}

func timelineOf(resolve Resolver) func(context.Context) (Resolver, error) {
	if resolve == nil {
		return nil
	}
	return func(context.Context) (Resolver, error) { return resolve, nil }
}
