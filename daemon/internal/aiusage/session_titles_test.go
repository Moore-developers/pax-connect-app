package aiusage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestSessionTitlesUseMetadataAndLatestRename(t *testing.T) {
	root := t.TempDir()
	s := &Store{options: Options{Roots: map[string]string{"codex": root}}}
	path := filepath.Join(root, "session_index.jsonl")
	if got := s.sessionTitles(context.Background()); len(got) != 0 {
		t.Fatal(got)
	}
	raw := `{"id":"s","thread_name":"old"}
invalid line
{"id":"s","thread_name":"  优化\n会话分析  "}
{"id":"untitled","first_user_message":"private prompt"}
{"id":"long","thread_name":"` + strings.Repeat("长", 200) + `"}
`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	got := s.sessionTitles(context.Background())
	if got["s"] != "优化 会话分析" || got["untitled"] != "" || len([]rune(got["long"])) != 161 {
		t.Fatal(got)
	}
	// Index edits are read on the next query without changing usage storage.
	if err := os.WriteFile(path, []byte(`{"id":"s","thread_name":"renamed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if s.sessionTitles(context.Background())["s"] != "renamed" {
		t.Fatal("stale title")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), path); err != nil {
		t.Fatal(err)
	}
	if got := s.sessionTitles(context.Background()); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestSessionAnalysisUsesNativeTitlesWithoutChangingUsage(t *testing.T) {
	ctx := context.Background()
	h := home(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	claudePath := filepath.Join(h, ".claude", "projects", "p", "shared.jsonl")
	dshPath := filepath.Join(h, ".dsh", "sessions", "p", "shared", "session.v4.jsonl.zstd")
	for _, path := range []string{claudePath, dshPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	claude := `{"type":"assistant","sessionId":"shared","timestamp":"` + now.Add(-time.Hour).UTC().Format(time.RFC3339) + `","requestId":"r","message":{"id":"m","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":100,"output_tokens":10}}}
{"type":"ai-title","sessionId":"shared","aiTitle":"Claude generated"}
{"type":"custom-title","sessionId":"other","customTitle":"Wrong session"}
{"type":"custom-title","sessionId":"shared","customTitle":"Claude renamed"}
{"type":"ai-title","sessionId":"shared","aiTitle":"Later generated"}
`
	if err := os.WriteFile(claudePath, []byte(claude), 0600); err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = encoder.Close() }()
	dsh := `{"type":"session","id":"shared","version":4,"createdAt":` + strconv.FormatInt(now.Add(-time.Hour).UnixMilli(), 10) + `}
{"type":"request/context","data":{"model":"deepseek-v3.2"}}
{"type":"assistant/message","seq":1,"time":` + strconv.FormatInt(now.Add(-time.Hour).UnixMilli(), 10) + `,"data":{"usage":{"inputTokens":100,"outputTokens":10}}}
{"type":"session/title","data":{"title":"DeepSeek original"}}
{"type":"session/title","data":{"title":"DeepSeek renamed"}}
`
	if err = os.WriteFile(dshPath, encoder.EncodeAll([]byte(dsh), nil), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(dshPath, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	s := openAt(t, h, &now, nil)
	defer func() { _ = s.Close(ctx) }()
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	read := func(want map[string]string) Analysis {
		t.Helper()
		a, e := s.Analyze(ctx, now.Add(-2*time.Hour).UnixMilli(), now.UnixMilli(), nil)
		if e != nil || len(a.Rows) != 2 {
			t.Fatalf("analysis rows=%d error=%v", len(a.Rows), e)
		}
		for _, row := range a.Rows {
			if row.SessionTitle != want[row.CLI] || row.Count != 1 || row.Input != 100 {
				t.Fatalf("native title/usage mismatch: cli=%s title=%q count=%d input=%d", row.CLI, row.SessionTitle, row.Count, row.Input)
			}
		}
		return a
	}
	before := read(map[string]string{"claude": "Claude renamed", "dsh": "DeepSeek renamed"})
	appendTo(t, claudePath, `{"type":"custom-title","sessionId":"shared","customTitle":"Claude latest"}`+"\n")
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	after := read(map[string]string{"claude": "Claude latest", "dsh": "DeepSeek renamed"})
	if before.State.Revision != after.State.Revision {
		t.Fatal("display metadata changed accounting revision")
	}
}

func TestNativeSessionTitlesIgnorePromptAndUncommittedTail(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := filepath.Join(root, "claude.jsonl")
	appendTo(t, claude, `{"type":"user","sessionId":"s","message":{"content":"private prompt"}}`+"\n")
	if title, found := readNativeTitleForTest(ctx, claude, SessionKey{"claude", "s"}); found || title != "" {
		t.Fatal("prompt became a title")
	}
	appendTo(t, claude, `{"type":"ai-title","sessionId":"other","aiTitle":"Wrong identity"}`+"\n")
	if _, found := readNativeTitleForTest(ctx, claude, SessionKey{"claude", "s"}); found {
		t.Fatal("foreign session title")
	}
	appendTo(t, claude, `{"type":"custom-title","sessionId":"s","customTitle":""}`+"\n", `{"type":"ai-title","sessionId":"s","aiTitle":"Generated"}`+"\n")
	if title, found := readNativeTitleForTest(ctx, claude, SessionKey{"claude", "s"}); !found || title != "" {
		t.Fatal("cleared custom title resurrected")
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = encoder.Close() }()
	path := filepath.Join(root, "dsh.jsonl.zstd")
	complete := encoder.EncodeAll([]byte("{\"type\":\"session\",\"id\":\"s\",\"version\":4}\n{\"type\":\"session/title\",\"data\":{\"title\":\"Committed\"}}\n"), nil)
	partial := encoder.EncodeAll([]byte("{\"type\":\"session/title\",\"data\":{\"title\":\"Uncommitted\"}}\n"), nil)
	if err = os.WriteFile(path, append(complete, partial[:len(partial)-2]...), 0600); err != nil {
		t.Fatal(err)
	}
	if title, found := readNativeTitleForTest(ctx, path, SessionKey{"dsh", "s"}); !found || title != "Committed" {
		t.Fatalf("incomplete live frame replaced title: %q %v", title, found)
	}
	if _, found := readNativeTitleForTest(ctx, path, SessionKey{"dsh", "other"}); found {
		t.Fatal("replaced source reused old cursor identity")
	}
	link := filepath.Join(root, "link")
	if err = os.Symlink(claude, link); err != nil {
		t.Fatal(err)
	}
	if _, found := readNativeTitleForTest(ctx, link, SessionKey{"claude", "s"}); found {
		t.Fatal("followed source symlink")
	}
}

func TestSessionTitlesRecoverDesktopAndSubagentMetadata(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY, title TEXT, agent_nickname TEXT);
 INSERT INTO threads VALUES ('desktop', 'Desktop title', NULL), ('child', '', 'Ada'), ('empty', '', NULL), ('renamed', 'Old title', NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "session_index.jsonl"), []byte(`{"id":"renamed","thread_name":"Current title"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Store{options: Options{Roots: map[string]string{"codex": root}}}
	got := s.sessionTitles(context.Background())
	if got["desktop"] != "Desktop title" || got["child"] != "子会话 · Ada" || got["empty"] != "" || got["renamed"] != "Current title" {
		t.Fatalf("title metadata missing or overwritten: %v", got)
	}
	if err = os.Remove(filepath.Join(root, "session_index.jsonl")); err != nil {
		t.Fatal(err)
	}
	if got = s.sessionTitles(context.Background()); got["desktop"] != "Desktop title" {
		t.Fatal("database metadata must work without index")
	}
	if err = os.WriteFile(filepath.Join(root, "session_index.jsonl"), []byte(`{"id":"desktop","thread_name":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got = s.sessionTitles(context.Background()); got["desktop"] != "" {
		t.Fatal("explicit cleared title must not be resurrected")
	}
}

func TestSessionTitlesDatabaseFailureKeepsIndex(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt", "unsupported-schema", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "state_5.sqlite")
			switch kind {
			case "corrupt":
				if err := os.WriteFile(path, []byte("not sqlite"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unsupported-schema":
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`CREATE TABLE threads(id TEXT, first_user_message TEXT); INSERT INTO threads VALUES ('private', 'private prompt')`); err != nil {
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(root, "elsewhere"), path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "session_index.jsonl"), []byte(`{"id":"s","thread_name":"Index title"}`), 0600); err != nil {
				t.Fatal(err)
			}
			s := &Store{options: Options{Roots: map[string]string{"codex": root}}}
			got := s.sessionTitles(context.Background())
			if len(got) != 1 || got["s"] != "Index title" {
				t.Fatal(got)
			}
		})
	}
}

func readNativeTitleForTest(ctx context.Context, path string, key SessionKey) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", false
	}
	budget := int64(64 << 20)
	c := readCatalogFile(ctx, source{path, key.CLI, info}, catalogFile{}, &budget)
	return c.State.Entry.Title, c.State.Entry.Key == key && c.State.Entry.TitleSource != ""
}

// Titles are optional Codex metadata. The index overrides database metadata,
// including explicit cleared titles. Unavailable sources never fail accounting.
// No prompt fallback, database writes, network access or source mutations.
func (s *Store) sessionTitles(ctx context.Context) map[string]string {
	root := s.options.Roots["codex"]
	if root == "" {
		root = filepath.Join(s.options.Home, ".codex")
	}
	titles := make(map[string]string)
	entries, _ := codexDatabaseSessions(ctx, root, nil)
	for _, entry := range entries {
		if entry.Title != "" {
			titles[entry.Key.Session] = entry.Title
		}
	}
	for id, entry := range codexIndexSessions(ctx, root) {
		titles[id] = entry.Title
	}
	return titles
}
