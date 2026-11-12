package aiusage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func codexEvent(kind string, at int64, p any) string {
	b, _ := json.Marshal(map[string]any{"type": kind, "timestamp": time.UnixMilli(at).UTC().Format(time.RFC3339Nano), "payload": p})
	return string(b) + "\n"
}
func codexMeta(session string, at int64, fork bool) string {
	p := map[string]any{"id": session}
	if fork {
		p["forked_from_id"] = "parent"
	}
	return codexEvent("session_meta", at, p)
}
func codexTask(turn string, at int64) string {
	return codexEvent("event_msg", at, map[string]any{"type": "task_started", "turn_id": turn})
}
func codexContext(turn, model string, at int64) string {
	return codexEvent("turn_context", at, map[string]any{"turn_id": turn, "model": model, "effort": "high"})
}
func codexTokens(in, out int64) map[string]any {
	return map[string]any{"input_tokens": in, "cached_input_tokens": in - 10, "cache_write_input_tokens": 0, "output_tokens": out, "reasoning_output_tokens": 0, "total_tokens": in + out}
}
func codexResponseLine(session, turn, id string, at int64) string {
	return codexEvent("token_usage_record", at, map[string]any{"session_id": session, "thread_id": session, "turn_id": turn, "response_id": id, "usage": codexTokens(100, 20)})
}
func codexTick(at, total int64) string {
	return codexEvent("event_msg", at, map[string]any{"type": "token_count", "info": map[string]any{"last_token_usage": codexTokens(100, 20), "total_token_usage": codexTokens(total, 20)}})
}
func codexTestStore(t *testing.T) (*Store, string, *time.Time) {
	t.Helper()
	h := home(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(h, ".codex/sessions/a.jsonl")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	s := openAt(t, h, &now, func(string, time.Time) Attribution {
		return Attribution{ProfileID: "p", ProfileName: "P", Multiplier: .16, Models: map[string]float64{"gpt-6.1-sol": .4}}
	})
	return s, path, &now
}
func codexLogs(t *testing.T, s *Store) Logs {
	t.Helper()
	v, e := s.List(context.Background(), Query{From: 1, To: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func codexScan(t *testing.T, s *Store) {
	t.Helper()
	if e := s.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func codexCheckBuckets(t *testing.T, s *Store) {
	t.Helper()
	if diff, e := s.reconcile(context.Background(), 0); e != nil || len(diff) > 0 {
		t.Fatal(diff, e)
	}
}

func TestCodexLateContextAcrossBatchRestartPreservesHistoricalPricing(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprint(override), func(t *testing.T) {
			s, path, now := codexTestStore(t)
			ctx := context.Background()
			if override {
				if _, e := s.w.Exec("INSERT INTO reprice_overrides VALUES(1,?,'p','gpt-6.1-sol',0,?,1,0.25,'')", now.UnixMilli(), now.UnixMilli()); e != nil {
					t.Fatal(e)
				}
			}
			at := now.Add(-time.Hour).UnixMilli()
			appendTo(t, path, codexMeta("s", at, false), codexTask("t", at+1), codexResponseLine("s", "t", "r", at+2), codexTick(at+3, 100), strings.Repeat(codexEvent("event_msg", at+4, map[string]any{"type": "unrelated"}), 500))
			codexScan(t, s)
			before := codexLogs(t, s)
			if before.All != 1 || before.Requests[0].Model != "unknown" {
				t.Fatal(before)
			}
			old := before.Requests[0]
			state := s.State()
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
			appendTo(t, path, codexContext("t", "gpt-6.1-sol", at+5))
			codexScan(t, s)
			after := codexLogs(t, s)
			want := old
			want.Model = "gpt-6.1-sol"
			want.Multiplier = .4
			if override {
				want.Multiplier = .25
			}
			want.price()
			got := after.Requests[0]
			if got.Key != old.Key || got.At != old.At || got.ProfileID != old.ProfileID || got.Elapsed == nil || *got.Elapsed != *old.Elapsed || got.Cost != want.Cost || got.Multiplier != want.Multiplier || got.Model != want.Model {
				t.Fatalf("late model lost original facts/pricing: %+v want %+v", got, want)
			}
			if s.State().Revision != state.Revision+1 || s.State().Generation != state.Generation+1 {
				t.Fatal(s.State(), state)
			}
			codexCheckBuckets(t, s)
			version := s.State()
			appendTo(t, path, codexContext("t", "gpt-6.1-sol", at+6))
			codexScan(t, s)
			if s.State().Revision != version.Revision || s.State().Generation != version.Generation {
				t.Fatal("same evidence changed watermark")
			}
		})
	}
}

func TestCodexIdentityDoesNotMergeEqualUsageWithoutCausalProof(t *testing.T) {
	for _, mode := range []string{"distinct-response", "different-session", "fork", "null-total", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			s, path, now := codexTestStore(t)
			defer func() { _ = s.Close(context.Background()) }()
			at := now.Add(-time.Hour).UnixMilli()
			first := codexMeta("s", at, false) + codexTask("t", at+1) + codexResponseLine("s", "t", "r1", at+2) + codexTick(at+3, 100)
			second := codexMeta("s", at+100, false) + codexTask("t", at+101) + codexResponseLine("s", "t", "r2", at+102) + codexTick(at+103, 100)
			switch mode {
			case "different-session":
				second = strings.ReplaceAll(second, `"s"`, `"other"`)
			case "fork":
				second = codexMeta("s", at+100, true) + codexTask("t", at+101) + codexTick(at+103, 100)
			case "null-total":
				second = codexMeta("s", at+100, false) + codexTask("t", at+101) + strings.Replace(codexTick(at+103, 100), `"total_token_usage":`+mustJSON(codexTokens(100, 20)), `"total_token_usage":null`, 1)
			case "rollback":
				second = codexTick(at+100, 200) + codexTick(at+103, 100)
			}
			appendTo(t, path, first, second)
			codexScan(t, s)
			want := int64(2)
			if mode == "rollback" {
				want = 3
			}
			if got := codexLogs(t, s); got.All != want {
				t.Fatalf("%s merged real requests: %d", mode, got.All)
			}
			codexCheckBuckets(t, s)
		})
	}
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestCodexLateContextNeedsMatchingResponseAndRejectsConflict(t *testing.T) {
	for _, mode := range []string{"missing-id", "wrong-turn", "mismatched-usage", "settings-conflict", "context-conflict", "switch-model"} {
		t.Run(mode, func(t *testing.T) {
			s, path, now := codexTestStore(t)
			defer func() { _ = s.Close(context.Background()) }()
			at := now.Add(-time.Hour).UnixMilli()
			body := codexMeta("s", at, false) + codexTask("t", at+1)
			if mode == "settings-conflict" {
				body += codexEvent("event_msg", at+1, map[string]any{"type": "thread_settings_applied", "thread_id": "s", "thread_settings": map[string]any{"model": "gpt-6.1-sol", "collaboration_mode": map[string]any{"settings": map[string]any{"model": "gpt-6.1-terra"}}}})
			}
			if mode == "context-conflict" {
				body += codexContext("t", "gpt-6.1-terra", at+1)
			}
			if mode != "missing-id" {
				response := codexResponseLine("s", "t", "r", at+2)
				if mode == "mismatched-usage" {
					response = strings.ReplaceAll(response, `"input_tokens":100`, `"input_tokens":101`)
				}
				body += response
			}
			body += codexTick(at+3, 100)
			contextLine := codexContext("t", "gpt-6.1-sol", at+4)
			if mode == "wrong-turn" {
				contextLine = codexContext("other", "gpt-6.1-sol", at+4)
			}
			if mode == "switch-model" {
				contextLine = codexTask("new-turn", at+4) + codexContext("new-turn", "gpt-6.1-sol", at+5)
			}
			appendTo(t, path, body)
			codexScan(t, s)
			appendTo(t, path, contextLine)
			codexScan(t, s)
			got := codexLogs(t, s)
			if got.All != 1 || got.Requests[0].Model != "unknown" {
				t.Fatalf("weak/conflicting model evidence used: %+v", got)
			}
			codexCheckBuckets(t, s)
		})
	}
}

func TestCodexRefinementFailureRollsBackRowBucketsCursorAndWatermarks(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	appendTo(t, path, codexMeta("s", at, false), codexTask("t", at+1), codexResponseLine("s", "t", "r", at+2), codexTick(at+3, 100))
	codexScan(t, s)
	old := codexLogs(t, s)
	state := s.State()
	cursorBefore, e := s.cursor(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.w.Exec("CREATE TRIGGER fail_codex_bucket BEFORE INSERT ON usage_hourly BEGIN SELECT RAISE(ABORT,'injected'); END"); e != nil {
		t.Fatal(e)
	}
	appendTo(t, path, codexContext("t", "gpt-6.1-sol", at+4))
	if e = s.Scan(context.Background()); e == nil {
		t.Fatal("injected failure was swallowed")
	}
	cursorAfter, e := s.cursor(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	got := codexLogs(t, s)
	if got.Requests[0].Model != "unknown" || got.Total != old.Total || cursorAfter.Offset != cursorBefore.Offset || s.State().Revision != state.Revision || s.State().Generation != state.Generation {
		t.Fatal("failed transaction leaked row/cursor/version")
	}
	codexCheckBuckets(t, s)
	if _, e = s.w.Exec("DROP TRIGGER fail_codex_bucket"); e != nil {
		t.Fatal(e)
	}
	codexScan(t, s)
	if got := codexLogs(t, s); got.Requests[0].Model != "gpt-6.1-sol" {
		t.Fatal("retry did not complete refinement")
	}
}

func TestCodexSameResponseAcrossFilesOnlyRefinesOnce(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	appendTo(t, path, codexMeta("s", at, false), codexTask("t", at+1), codexResponseLine("s", "t", "same", at+2), codexTick(at+3, 100))
	codexScan(t, s)
	original := codexLogs(t, s).Requests[0]
	version := s.State()
	second := filepath.Join(filepath.Dir(path), "b.jsonl")
	appendTo(t, second, codexMeta("s", at+100, false), codexTask("t", at+101), codexContext("t", "gpt-6.1-sol", at+101), codexResponseLine("s", "t", "same", at+102), codexTick(at+103, 100))
	codexScan(t, s)
	got := codexLogs(t, s)
	if got.All != 1 || got.Requests[0].Key != original.Key || got.Requests[0].Model != "gpt-6.1-sol" || s.State().Revision != version.Revision+1 || s.State().Generation != version.Generation+1 {
		t.Fatal("same response did not refine original", got, s.State(), version)
	}
	version = s.State()
	appendTo(t, second, codexResponseLine("s", "t", "same", at+104), codexTick(at+105, 100))
	codexScan(t, s)
	if s.State().Revision != version.Revision || codexLogs(t, s).All != 1 {
		t.Fatal("repeated response moved revision")
	}
	codexCheckBuckets(t, s)
}

func TestCodexSettingsBeforeTaskConflictingLateContextStaysUnknown(t *testing.T) {
	s, path, now := codexTestStore(t)
	defer func() { _ = s.Close(context.Background()) }()
	at := now.Add(-time.Hour).UnixMilli()
	settings := codexEvent("event_msg", at+1, map[string]any{"type": "thread_settings_applied", "thread_id": "s", "thread_settings": map[string]any{"model": "gpt-6.1-sol"}})
	appendTo(t, path, codexMeta("s", at, false), settings, codexTask("t", at+2), codexResponseLine("s", "t", "same", at+3), codexTick(at+4, 100))
	codexScan(t, s)
	if got := codexLogs(t, s); got.Requests[0].Model != "gpt-6.1-sol" {
		t.Fatal("settings were not applied", got)
	}
	appendTo(t, path, codexContext("t", "gpt-6.1-terra", at+5))
	codexScan(t, s)
	if got := codexLogs(t, s); got.Requests[0].Model != "unknown" || got.Total.Cost != 0 {
		t.Fatalf("conflicting late context kept priced model: %+v", got)
	}
	codexCheckBuckets(t, s)
}

func TestCodexAliasModelConflictPersistsAcrossRestartAndFurtherAliases(t *testing.T) {
	for _, firstModel := range []string{"gpt-6.1-sol", "gpt-6.1-terra"} {
		t.Run(firstModel, func(t *testing.T) {
			s, path, now := codexTestStore(t)
			ctx := context.Background()
			at := now.Add(-time.Hour).UnixMilli()
			other := "gpt-6.1-sol"
			if firstModel == other {
				other = "gpt-6.1-terra"
			}
			add := func(path, model string, offset int64) {
				appendTo(t, path, codexMeta("s", at+offset, false), codexTask("t", at+offset+1), codexContext("t", model, at+offset+2), codexResponseLine("s", "t", "same", at+offset+3), codexTick(at+offset+4, 100))
			}
			add(path, firstModel, 0)
			codexScan(t, s)
			alias := filepath.Join(filepath.Dir(path), "alias.jsonl")
			add(alias, other, 100)
			codexScan(t, s)
			if got := codexLogs(t, s); got.All != 1 || got.Requests[0].Model != "unknown" || got.Total.Cost != 0 {
				t.Fatalf("alias conflict depends on arrival order: %+v", got)
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
			third := filepath.Join(filepath.Dir(path), "third.jsonl")
			add(third, firstModel, 200)
			codexScan(t, s)
			appendTo(t, third, codexContext("t", firstModel, at+205))
			codexScan(t, s)
			if got := codexLogs(t, s); got.All != 1 || got.Requests[0].Model != "unknown" || got.Total.Cost != 0 {
				t.Fatalf("restart/third alias erased conflict: %+v", got)
			}
			codexCheckBuckets(t, s)
			// Missing source files must carry the already established sticky conflict.
			for _, source := range []string{path, alias, third} {
				if e := os.Remove(source); e != nil {
					t.Fatal(e)
				}
			}
			if e = s.Close(ctx); e != nil {
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
			add(path, firstModel, 300)
			codexScan(t, s)
			if got := codexLogs(t, s); got.All != 1 || got.Requests[0].Model != "unknown" {
				t.Fatal("rebuild cleared sticky conflict", got)
			}
			codexCheckBuckets(t, s)
		})
	}
}

func TestCodexCarrySettingsAreNotEvidenceOfTheCarriedRequestsModel(t *testing.T) {
	for _, carryFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(carryFirst), func(t *testing.T) {
			s, path, now := codexTestStore(t)
			defer func() { _ = s.Close(context.Background()) }()
			at := now.Add(-time.Hour).UnixMilli()
			source := codexMeta("s", at, false) + codexTask("t", at+1) + codexContext("t", "gpt-6.1-sol", at+2) + codexResponseLine("s", "t", "r", at+3) + codexTick(at+4, 100)
			carry := codexMeta("s", at+100, false) + codexTask("new-turn", at+101) + codexContext("new-turn", "gpt-6.1-terra", at+102) + codexTick(at+103, 100)
			alias := filepath.Join(filepath.Dir(path), "carry.jsonl")
			if carryFirst {
				appendTo(t, alias, carry)
				codexScan(t, s)
				appendTo(t, path, source)
			} else {
				appendTo(t, path, source)
				codexScan(t, s)
				appendTo(t, alias, carry)
			}
			codexScan(t, s)
			third := filepath.Join(filepath.Dir(path), "third.jsonl")
			appendTo(t, third, codexMeta("s", at+200, false), codexTask("t", at+201), codexContext("t", "gpt-6.1-sol", at+202), codexResponseLine("s", "t", "r", at+203), codexTick(at+204, 100))
			codexScan(t, s)
			got := codexLogs(t, s)
			if got.All != 1 || got.Requests[0].Model != "gpt-6.1-sol" || got.Total.Cost == 0 {
				t.Fatalf("new carry settings poisoned old request model: %+v", got)
			}
			codexCheckBuckets(t, s)
		})
	}
}
