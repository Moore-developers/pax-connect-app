package aiusage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func claudeVersionFixture(t *testing.T, version string) (*Store, string) {
	t.Helper()
	h := home(t)
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	s := openAt(t, h, &now, nil)
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	path := filepath.Join(h, ".claude", "projects", "p", "session.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, `{"type":"user","sessionId":"session","version":"`+version+`","isSidechain":false}`+"\n")
	appendTo(t, path, `{"type":"assistant","sessionId":"session","version":"`+version+`","timestamp":"2026-11-09T12:00:00Z","requestId":"r","message":{"id":"m","model":"claude-sonnet-5-5","usage":{"input_tokens":10,"output_tokens":1}}}`+"\n")
	return s, path
}

func TestClaudeCatalogStringVersionKeepsIdentityAndTitles(t *testing.T) {
	for _, version := range []string{"2.1.301", "2.1.322"} {
		t.Run(version, func(t *testing.T) {
			s, path := claudeVersionFixture(t, version)
			ctx := context.Background()
			if err := s.Scan(ctx); err != nil {
				t.Fatal(err)
			}
			page, err := s.ListSessions(ctx, SessionCatalogQuery{CLI: "claude"})
			if err != nil || len(page.Items) != 1 || page.Items[0].Key.Session != "session" || page.Items[0].Status != "untitled" {
				t.Fatalf("existing source: %+v %v", page, err)
			}
			appendTo(t, path, `{"type":"custom-title","sessionId":"session","version":"`+version+`","customTitle":"Native title"}`+"\n")
			if err = s.Scan(ctx); err != nil {
				t.Fatal(err)
			}
			page, err = s.ListSessions(ctx, SessionCatalogQuery{CLI: "claude"})
			if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Native title" || page.Items[0].Status != "named" {
				t.Fatalf("native title: %+v %v", page, err)
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err = s.Scan(ctx); err != nil {
				t.Fatal(err)
			}
			page, err = s.ListSessions(ctx, SessionCatalogQuery{CLI: "claude"})
			if err != nil || len(page.Items) != 1 || page.Items[0].Title != "Native title" || page.Items[0].Status != "source_missing" {
				t.Fatalf("real removal must preserve history: %+v %v", page, err)
			}
		})
	}
}

func TestClaudeCatalogReplaysLegacyEmptyIdentityWithoutChangingLedger(t *testing.T) {
	s, path := claudeVersionFixture(t, "2.1.322")
	ctx := context.Background()
	if err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	// Reproduce the persisted pre-fix state: the file was consumed, but no
	// identity was parsed. Keep its inode, mtime, size and offset unchanged.
	var raw []byte
	if err := s.r.QueryRow(`SELECT state FROM session_catalog_files WHERE path=?`, path).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	delete(state, "ClaudeTitleVersion")
	state["Entry"] = SessionCatalogEntry{}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.w.Exec(`UPDATE session_catalog_files SET state=? WHERE path=?`, raw, path); err != nil {
		t.Fatal(err)
	}
	if _, err = s.w.Exec(`UPDATE session_catalog SET title_status='source_missing' WHERE cli='claude'`); err != nil {
		t.Fatal(err)
	}
	query := `SELECT json_group_array(json_array(cli,request_key,input,read,write,output,cost,c0,c1,c2,c3,cast(details AS TEXT))) FROM (SELECT * FROM requests ORDER BY cli,request_key)`
	var ledger string
	if err = s.r.QueryRow(query).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	usageCursor, err := s.cursor(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	before := s.State()
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
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != "untitled" || page.Revision <= before.CatalogRevision {
		t.Fatalf("legacy cursor was not repaired: %+v %v", page, err)
	}
	var after string
	if err = reopened.r.QueryRow(query).Scan(&after); err != nil || after != ledger {
		t.Fatal("directory replay changed ledger", err)
	}
	afterCursor, err := reopened.cursor(ctx, path)
	if err != nil || !reflect.DeepEqual(usageCursor, afterCursor) || reopened.State().Revision != before.Revision || reopened.State().Generation != before.Generation {
		t.Fatal("directory replay changed usage cursor or version", err)
	}
	stable := reopened.State()
	if err = reopened.Scan(ctx); err != nil || reopened.State() != stable {
		t.Fatal("idle scan changed directory after repair", err)
	}
}
