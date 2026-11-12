package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// block is one redacted Claude content-block record: every block repeats the
// message_start usage; only the final one has the true output (and, in real logs,
// stop_reason, which the parser does not need).
func block(at string, id string, output int) string {
	return `{"type":"assistant","sessionId":"s","requestId":"r-` + id + `","requestRef":"ref","timestamp":"2026-10-28T` + at + `Z","message":{"id":"` + id + `","model":"claude-sonnet-5-5","usage":{"input_tokens":1000000,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":` + strconv.Itoa(output) + `}}}` + "\n"
}

const requestMark = `{"type":"api-request","id":"ref","timestamp":"2026-10-28T10:00:00.000Z","sessionId":"s"}` + "\n"

func appendTo(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, e = f.WriteString(l); e != nil {
			t.Fatal(e)
		}
	}
}

func claudeStore(t *testing.T) (*Store, string) {
	t.Helper()
	h := home(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(h, ".claude", "projects", "p", "a.jsonl")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	return openAt(t, h, &now, nil), path
}

func claudeRows(t *testing.T, s *Store) Logs {
	t.Helper()
	logs, e := s.List(context.Background(), Query{From: 1, To: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	return logs
}

// bucketsMatchDetails fails unless the hourly aggregates equal the sum of the stored rows.
func bucketsMatchDetails(t *testing.T, s *Store, logs Logs) {
	t.Helper()
	var want Summary
	for _, r := range logs.Requests {
		want.add(summarize(r))
	}
	buckets, e := s.Summarize(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var got Summary
	for _, b := range buckets {
		got.add(b.Summary)
	}
	if got != want || logs.Total != want {
		t.Fatalf("buckets %+v, rows %+v, log totals %+v", got, want, logs.Total)
	}
}

func TestClaudeFinalBlockUpdatesStoredOutputWithinOneScan(t *testing.T) {
	s, path := claudeStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	appendTo(t, path, requestMark, block("10:00:02.000", "m1", 1), block("10:00:05.000", "m1", 1), block("10:00:10.000", "m1", 100000))
	if e := s.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	logs := claudeRows(t, s)
	if logs.All != 1 {
		t.Fatalf("rows: %d", logs.All)
	}
	r := logs.Requests[0]
	if r.Output != 100000 || r.Cost != 3000000 || r.Costs[3] != 1000000 || !near(r.Elapsed, 10) || !near(r.TPS, 10000) {
		t.Fatalf("final output must win: %+v", r)
	}
	bucketsMatchDetails(t, s, logs)
	if logs.Total.Output != 100000 || logs.Total.TPSCount != 1 {
		t.Fatalf("totals: %+v", logs.Total)
	}
}

func TestClaudeFinalBlockInALaterScanUpdatesTheStoredRow(t *testing.T) {
	ctx := context.Background()
	s, path := claudeStore(t)
	defer func() { _ = s.Close(ctx) }()
	appendTo(t, path, requestMark, block("10:00:02.000", "m1", 1), block("10:00:04.000", "m2", 5))
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs := claudeRows(t, s)
	bucketsMatchDetails(t, s, logs)
	if logs.All != 2 || logs.Total.Output != 6 || logs.Total.TPSCount != 2 || logs.Total.ElapsedCount != 2 {
		t.Fatalf("before: %+v", logs.Total)
	}
	// The cursor resumes after the first two lines; the final blocks arrive afterwards.
	appendTo(t, path, block("10:00:08.000", "m1", 40000), block("10:00:09.000", "m2", 50000))
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs = claudeRows(t, s)
	bucketsMatchDetails(t, s, logs)
	if logs.All != 2 || logs.Total.Output != 90000 || logs.Total.TPSCount != 2 || logs.Total.Cost != 2*2000000+900000 {
		t.Fatalf("after: %+v", logs.Total)
	}
	for _, r := range logs.Requests {
		if r.TPS == nil {
			t.Fatalf("flag must clear: %+v", r)
		}
	}
}

func TestClaudeWithoutAFinalBlockUsesTheLargestOutputSeen(t *testing.T) {
	ctx := context.Background()
	s, path := claudeStore(t)
	defer func() { _ = s.Close(ctx) }()
	appendTo(t, path, requestMark, block("10:00:02.000", "m1", 3), block("10:00:06.000", "m1", 7), block("10:00:08.000", "m1", 5))
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs := claudeRows(t, s)
	bucketsMatchDetails(t, s, logs)
	r := logs.Requests[0]
	// A smaller later block never lowers the output; elapsed still follows the latest block.
	if r.Output != 7 || !near(r.TPS, 7.0/8) || !near(r.Elapsed, 8) || r.Cost != 2000000+70 {
		t.Fatalf("largest output, normal pricing and TPS: %+v", r)
	}
	if logs.Total.Output != 7 || logs.Total.TPSCount != 1 {
		t.Fatalf("totals: %+v", logs.Total)
	}
}
