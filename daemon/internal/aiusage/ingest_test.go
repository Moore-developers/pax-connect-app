package aiusage

import (
	"context"
	"github.com/klauspost/compress/zstd"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClaudeIncrementalLongLinesDeduplicateAcrossRestart(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(home, ".claude", "projects", "p")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	line := `{"type":"assistant","sessionId":"session","timestamp":"2026-10-28T10:00:00Z","requestId":"request","message":{"id":"message","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":40}},"ignored":"` + strings.Repeat("x", 100000) + `"}` + "\n"
	path := filepath.Join(root, "session.jsonl")
	if e := os.WriteFile(path, []byte(line+line+`{"type":"assistant"`), 0600); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	opts := Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }}
	s, e := Open(ctx, opts)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs, e := s.List(ctx, Query{From: 1, To: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	if logs.All != 1 || logs.Total.Input != 60 || logs.Total.Output != 40 {
		t.Fatalf("usage: %+v", logs)
	}
	if e = s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	s, e = Open(ctx, opts)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs, e = s.List(ctx, Query{From: 1, To: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC).UnixMilli()})
	if e != nil || logs.All != 1 {
		t.Fatalf("restart: %+v %v", logs, e)
	}
}

func TestRedactedCodexSegmentsAndDSHCompleteFrameCursor(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	codex, e := os.ReadFile("../../../tests/ai-usage/codex-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	dsh, e := os.ReadFile("../../../tests/ai-usage/dsh-v4-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(home, ".codex", "sessions")
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "rollout.jsonl"), codex, 0600); e != nil {
		t.Fatal(e)
	}
	ddir := filepath.Join(home, ".dsh", "sessions", "a", "b")
	if e = os.MkdirAll(ddir, 0700); e != nil {
		t.Fatal(e)
	}
	encoder, e := zstd.NewWriter(nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = encoder.Close() }()
	compressed := encoder.EncodeAll(dsh, nil)
	path := filepath.Join(ddir, "session.v4.jsonl.zstd")
	if e = os.WriteFile(path, compressed, 0600); e != nil {
		t.Fatal(e)
	}
	past := now.Add(-time.Hour)
	if e = os.Chtimes(path, past, past); e != nil {
		t.Fatal(e)
	}
	s, e := Open(ctx, Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs, e := s.List(ctx, Query{From: 1, To: now.UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	if logs.All != 3 || logs.Success != 2 || logs.Failed != 1 || logs.Total.Input != 67553 || logs.Total.Output != 390 {
		t.Fatalf("samples: %+v", logs)
	}
	more := []byte(`{"type":"assistant/message","seq":99,"time":1735689700000,"data":{"message":{"id":"new"},"usage":{"inputTokens":5,"outputTokens":6}}}` + "\n")
	frame := encoder.EncodeAll(more, nil)
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Write(frame[:len(frame)-2]); e != nil {
		t.Fatal(e)
	}
	_ = f.Close()
	_ = os.Chtimes(path, past, past)
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs, _ = s.List(ctx, Query{From: 1, To: now.UnixMilli()})
	if logs.All != 3 {
		t.Fatal("partial frame counted")
	}
	f, e = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Write(frame[len(frame)-2:])
	if e != nil {
		t.Fatal(e)
	}
	_ = f.Close()
	_ = os.Chtimes(path, past, past)
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs, _ = s.List(ctx, Query{From: 1, To: now.UnixMilli()})
	if logs.All != 4 {
		t.Fatalf("append cursor: %+v", logs)
	}
	now = now.AddDate(0, 0, 94)
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	logs, _ = s.List(ctx, Query{From: 1, To: now.UnixMilli()})
	if logs.All != 0 {
		t.Fatal("retention failed")
	}
	buckets, e := s.Summarize(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var count int64
	for _, b := range buckets {
		count += b.Count
	}
	if count != 4 {
		t.Fatalf("pruned aggregates: %d", count)
	}
}

func TestCodexSessionMetaUsesRecordTimeAndOpensANewSegment(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	sample, e := os.ReadFile("../../../tests/ai-usage/codex-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	second := strings.ReplaceAll(string(sample), "2025-01-01T00:", "2025-01-01T01:")
	// Distinct requests in different segments need distinct response identities.
	second = strings.ReplaceAll(second, "response_redacted_0001", "response_redacted_0002")
	root := filepath.Join(home, ".codex", "sessions")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "rollout.jsonl"), append(sample, []byte(second)...), 0600); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s, e := Open(ctx, Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC) }})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := s.List(ctx, Query{From: 1, To: time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	if v.All != 2 || v.Requests[0].Segment == v.Requests[1].Segment {
		t.Fatalf("segments merged: %+v", v)
	}
}

func TestArchivedReplayAfterDetailRetentionDoesNotDoubleLifetime(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(home, ".claude", "projects", "p")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	sample, e := os.ReadFile("../../../tests/ai-usage/claude-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "a.jsonl"), sample, 0600); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	s, e := Open(ctx, Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	now = now.AddDate(0, 0, 94)
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "b.jsonl"), sample, 0600); e != nil {
		t.Fatal(e)
	}
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	buckets, e := s.Summarize(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var count int64
	for _, b := range buckets {
		count += b.Count
	}
	if count != 2 {
		t.Fatalf("replayed expired requests: %d", count)
	}
}

func TestHistoricalMultiplierAppliesToAllCostComponents(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(home, ".claude", "projects", "p")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	line := `{"type":"assistant","timestamp":"2025-01-01T00:00:00Z","sessionId":"s","requestId":"r","message":{"id":"m","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":1000000,"output_tokens":2000000}}}` + "\n"
	if e = os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(line), 0600); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s, e := Open(ctx, Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC) }, Timeline: timelineOf(func(string, time.Time) Attribution {
		return Attribution{ProfileID: "p", ProfileName: "P", Multiplier: 2}
	})})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	b, e := s.Summarize(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(b) != 1 || b[0].Cost != 66000000 || b[0].BilledCosts != [4]int64{6000000, 0, 0, 60000000} || b[0].Costs != [4]int64{3000000, 0, 0, 30000000} {
		t.Fatalf("cost components: %+v", b)
	}
}

func TestUnknownDSHVersionCannotCreateAnUnidentifiedFailure(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(home, ".dsh", "sessions", "a", "b")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	sample, e := os.ReadFile("../../../tests/ai-usage/dsh-v4-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	sample = []byte(strings.Replace(string(sample), `"version":4`, `"version":99`, 1))
	encoder, e := zstd.NewWriter(nil)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = encoder.Close() }()
	path := filepath.Join(root, "session.v99.jsonl.zstd")
	if e = os.WriteFile(path, encoder.EncodeAll(sample, nil), 0600); e != nil {
		t.Fatal(e)
	}
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	if e = os.Chtimes(path, past, past); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s, e := Open(ctx, Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(ctx) }()
	if e = s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	v, e := s.List(ctx, Query{From: 1, To: now.UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	if v.All != 0 || s.State().Skipped == 0 {
		t.Fatalf("unknown DSH wrote records: %+v", v)
	}
}

func TestCollectorConfigureAndCloseAreBounded(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(home, ".claude", "projects", "p")
	if e = os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	sample, e := os.ReadFile("../../../tests/ai-usage/claude-redacted.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "a.jsonl"), sample, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	s, e := Open(ctx, Options{Directory: filepath.Join(home, "state"), Home: home, Owner: uint32(os.Geteuid()), Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Configure(ctx); e != nil {
		t.Fatal(e)
	}
	var count int64
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		v, e := s.List(ctx, Query{From: 1, To: now.UnixMilli()})
		if e != nil {
			t.Fatal(e)
		}
		count = v.All
		if count == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if count != 2 {
		t.Fatalf("background collector did not commit: %d", count)
	}
	drain, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if e = s.Close(drain); e != nil {
		t.Fatal(e)
	}
}
