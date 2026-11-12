package aiusage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func appendClaudePreview(t *testing.T, path, preview string) {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"type": "last-prompt", "sessionId": "session", "lastPrompt": preview})
	if err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, string(raw)+"\n")
}

func TestClaudeCatalogLastPromptPreviewUpdatesWithoutChangingUsage(t *testing.T) {
	s, path := claudeVersionFixture(t, "2.1.322")
	ctx := context.Background()
	if err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	before := s.State()
	usageCursor, err := s.cursor(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	appendClaudePreview(t, path, "Old preview")
	preview := "Latest message " + strings.Repeat("长", 200)
	appendClaudePreview(t, path, "\n\t"+preview+"\u0000")
	appendTo(t, path, `{"type":"last-prompt","sessionId":"session","isSidechain":true,"lastPrompt":"Sidechain must not replace preview"}`+"\n")
	appendTo(t, path, `{"type":"last-prompt","sessionId":"other","lastPrompt":"Wrong identity must not replace preview"}`+"\n")
	agent := filepath.Join(filepath.Dir(path), "session", "subagents", "agent-a.jsonl")
	if err = os.MkdirAll(filepath.Dir(agent), 0700); err != nil {
		t.Fatal(err)
	}
	appendClaudePreview(t, agent, "Agent must not replace parent preview")
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	q := SessionCatalogQuery{CLI: "claude", Text: "Latest message", Selected: &SessionKey{"claude", "session"}}
	page, err := s.ListSessions(ctx, q)
	want := string([]rune(preview)[:160]) + "…"
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != want || page.Items[0].TitleSource != "claude.last-prompt" || page.Items[0].Status != "untitled" || page.Selected == nil || page.Selected.Title != want {
		t.Fatalf("bounded searchable preview/selected: %+v %v", page, err)
	}
	if s.State().Revision != before.Revision || s.State().Generation != before.Generation || page.Revision <= before.CatalogRevision {
		t.Fatal("preview update did not isolate directory version")
	}
	// Collection may advance its file cursor over new metadata, but it must
	// preserve the request totals and never re-ingest existing requests.
	var count, input int
	if err = s.r.QueryRow(`SELECT count(*),sum(input) FROM requests`).Scan(&count, &input); err != nil || count != 1 || input != 10 {
		t.Fatal("preview changed request totals", count, input, err)
	}
	afterCursor, err := s.cursor(ctx, path)
	if err != nil || afterCursor.Offset <= usageCursor.Offset {
		t.Fatal("new native metadata was not consumed", err)
	}
	a, err := s.AnalyzeLimits(ctx, s.options.Now().Add(-time.Hour).UnixMilli(), s.options.Now().UnixMilli()+1, nil)
	if err != nil || a.Limits == nil || len(a.Limits.Primary) != 1 || a.Limits.Primary[0].Title != "最近消息："+want {
		t.Fatalf("analysis display must label preview: %+v %v", a.Limits, err)
	}
	stable := s.State()
	if err = s.Scan(ctx); err != nil || s.State() != stable {
		t.Fatal("idle preview scan changed state", err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	pageBefore := page
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	page, err = s.ListSessions(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != "source_missing" || page.Items[0].Title != pageBefore.Items[0].Title {
		t.Fatalf("missing source must retain preview and issue: %+v %v", page, err)
	}
}

func TestClaudeCatalogFormalTitlePrecedesPreviewAndClearUsesLatestPreview(t *testing.T) {
	s, path := claudeVersionFixture(t, "2.1.322")
	ctx := context.Background()
	appendClaudePreview(t, path, "First preview")
	index := filepath.Join(filepath.Dir(path), "sessions-index.json")
	appendTo(t, index, `{"entries":[{"sessionId":"session","summary":"Indexed title"}]}`)
	steps := []struct{ line, title, source, status string }{
		{"", "Indexed title", "claude.index.summary", "named"},
		{`{"type":"ai-title","sessionId":"session","aiTitle":"Generated title"}`, "Generated title", "claude.ai", "named"},
		{`{"type":"custom-title","sessionId":"session","customTitle":"Custom title"}`, "Custom title", "claude.custom", "named"},
		{`{"type":"last-prompt","sessionId":"session","lastPrompt":"Latest preview"}`, "Custom title", "claude.custom", "named"},
		{`{"type":"custom-title","sessionId":"session","customTitle":""}`, "Latest preview", "claude.last-prompt", "untitled"},
		{`{"type":"last-prompt","sessionId":"session","lastPrompt":""}`, "Latest preview", "claude.last-prompt", "untitled"},
	}
	for _, step := range steps {
		if step.line != "" {
			appendTo(t, path, step.line+"\n")
		}
		if err := s.Scan(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := s.ListSessions(ctx, SessionCatalogQuery{CLI: "claude"})
		if err != nil || len(page.Items) != 1 || page.Items[0].Title != step.title || page.Items[0].TitleSource != step.source || page.Items[0].Status != step.status {
			t.Fatalf("title precedence (%s): %+v %v", step.line, page, err)
		}
	}
}

func TestClaudeCatalogPreviewReplaysOldCursorsAfterRestart(t *testing.T) {
	for _, tc := range []struct {
		name         string
		version      int
		line, source string
	}{
		{"v1", 1, `{"type":"last-prompt","sessionId":"session","lastPrompt":"Recovered preview"}`, "claude.last-prompt"},
		{"v2", 2, `{"type":"user","sessionId":"session","message":{"content":"Recovered preview"}}`, "claude.user-message"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, path := claudeVersionFixture(t, "2.1.322")
			ctx := context.Background()
			appendTo(t, path, tc.line+"\n")
			if err := s.Scan(ctx); err != nil {
				t.Fatal(err)
			}
			// v1 lacked native previews; v2 lacked user-message previews.
			if _, err := s.w.Exec(`UPDATE session_catalog_files SET state=json_remove(json_set(CAST(state AS TEXT),'$.ClaudeTitleVersion',?),'$.ClaudeLastPrompt','$.ClaudePreview','$.ClaudePreviewSource') WHERE path=?`, tc.version, path); err != nil {
				t.Fatal(err)
			}
			if _, err := s.w.Exec(`UPDATE session_catalog SET title='',title_source='' WHERE cli='claude'`); err != nil {
				t.Fatal(err)
			}
			before := s.State()
			cursorBefore, err := s.cursor(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			opts := s.options
			if err = s.Close(ctx); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, opts)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close(ctx) }()
			if err = reopened.Scan(ctx); err != nil {
				t.Fatal(err)
			}
			page, err := reopened.ListSessions(ctx, SessionCatalogQuery{CLI: "claude"})
			if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Recovered preview" || page.Items[0].TitleSource != tc.source {
				t.Fatalf("old unchanged source did not gain preview: %+v %v", page, err)
			}
			cursorAfter, err := reopened.cursor(ctx, path)
			if err != nil || !reflect.DeepEqual(cursorBefore, cursorAfter) || reopened.State().Revision != before.Revision || reopened.State().Generation != before.Generation {
				t.Fatal("metadata replay changed usage", err)
			}
		})
	}
}

func TestClaudeCatalogEmptyPreviewOnlySessionHasNoUsage(t *testing.T) {
	s, path := claudeVersionFixture(t, "2.1.322")
	// A real Claude empty session may contain only a last-prompt identity,
	// without lastPrompt text or user/assistant messages.
	if err := os.WriteFile(path, []byte(`{"type":"last-prompt","sessionId":"session","leafUuid":"leaf"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessions(ctx, SessionCatalogQuery{CLI: "claude"})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "" || page.Items[0].Status != "untitled" || s.State().Revision != 0 {
		t.Fatalf("empty native session must remain selectable without inventing text or usage: %+v %v", page, err)
	}
}

func TestClaudeCatalogNewUserMessageUpdatesPreviewWithoutLastPrompt(t *testing.T) {
	s, path := claudeVersionFixture(t, "2.1.322")
	ctx := context.Background()
	if err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	before := s.State()
	steps := []struct{ line, title, source string }{
		{`{"type":"user","sessionId":"session","message":{"content":"First user question"}}`, "First user question", "claude.user-message"},
		{`{"type":"last-prompt","sessionId":"session"}`, "First user question", "claude.user-message"},
		{`{"type":"user","sessionId":"session","message":{"content":[{"type":"text","text":"Next question"},{"type":"image","source":{}}]}}`, "Next question", "claude.user-message"},
		{`{"type":"user","sessionId":"session","isMeta":true,"message":{"content":"Not a user question"}}`, "Next question", "claude.user-message"},
		{`{"type":"user","sessionId":"session","message":{"content":[{"type":"tool_result","content":"Tool output"},{"type":"text","text":"Tool status"}]}}`, "Next question", "claude.user-message"},
		{`{"type":"user","sessionId":"session","isSidechain":true,"message":{"content":"Agent question"}}`, "Next question", "claude.user-message"},
		{`{"type":"assistant","sessionId":"session","message":{"content":"Assistant answer"}}`, "Next question", "claude.user-message"},
		{`{"type":"last-prompt","sessionId":"session","lastPrompt":"Native preview"}`, "Native preview", "claude.last-prompt"},
		{`{"type":"user","sessionId":"session","message":{"content":"Newer user question"}}`, "Newer user question", "claude.user-message"},
		{`{"type":"user","sessionId":"session","message":{"content":"<command-name>/clear</command-name><command-message>clear</command-message><command-args></command-args>"}}`, "/clear", "claude.user-message"},
		{`{"type":"user","sessionId":"session","message":{"content":"<local-command-stdout>Command finished</local-command-stdout>"}}`, "/clear", "claude.user-message"},
	}
	for _, step := range steps {
		appendTo(t, path, step.line+"\n")
		if err := s.Scan(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := s.ListSessions(ctx, SessionCatalogQuery{CLI: "claude"})
		if err != nil || len(page.Items) != 1 || page.Items[0].Title != step.title || page.Items[0].TitleSource != step.source || page.Items[0].Status != "untitled" {
			t.Fatalf("new user data (%s): %+v %v", step.line, page, err)
		}
		if s.State().Revision != before.Revision || s.State().Generation != before.Generation {
			t.Fatal("message preview changed accounting")
		}
	}
}
