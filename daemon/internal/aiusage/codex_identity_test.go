package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexCapturedSettingsAndCarrySnapshot(t *testing.T) {
	for _, newestFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "prior-first", true: "carry-first"}[newestFirst], func(t *testing.T) {
			h := home(t)
			now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
			root := filepath.Join(h, ".codex", "sessions")
			if e := os.MkdirAll(root, 0700); e != nil {
				t.Fatal(e)
			}
			for i, name := range []string{"codex-prior-identity.jsonl", "codex-carried-identity.jsonl"} {
				body, e := os.ReadFile("../../../tests/ai-usage/" + name)
				if e != nil {
					t.Fatal(e)
				}
				path := filepath.Join(root, name)
				if e = os.WriteFile(path, body, 0600); e != nil {
					t.Fatal(e)
				}
				offset := i
				if !newestFirst {
					offset = 1 - i
				}
				at := now.Add(time.Duration(offset) * time.Minute)
				_ = os.Chtimes(path, at, at)
			}
			s := openAt(t, h, &now, func(string, time.Time) Attribution {
				return Attribution{ProfileID: "p", ProfileName: "P", Multiplier: .16}
			})
			defer func() { _ = s.Close(context.Background()) }()
			if e := s.Scan(context.Background()); e != nil {
				t.Fatal(e)
			}
			logs, e := s.List(context.Background(), Query{From: 1, To: now.UnixMilli()})
			if e != nil {
				t.Fatal(e)
			}
			if logs.All != 2 || logs.Total.Input+logs.Total.Output != 1678016 {
				t.Fatalf("carried snapshot counted: count=%d tokens=%d", logs.All, logs.Total.Input+logs.Total.Output)
			}
			for _, r := range logs.Requests {
				if r.Model != "gpt-6.1-sol" || r.Cost == 0 {
					t.Fatalf("settings model not applied: model=%q cost=%d", r.Model, r.Cost)
				}
			}
			if diff, e := s.reconcile(context.Background(), 0); e != nil || len(diff) != 0 {
				t.Fatal(diff, e)
			}
		})
	}
}
