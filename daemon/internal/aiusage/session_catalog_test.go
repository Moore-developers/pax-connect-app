package aiusage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestCatalogDateRangeFiltersBeforeCountsSearchAndPagination(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := openAt(t, home(t), &now, nil)
	defer func() { _ = s.Close(ctx) }()
	_, err := s.w.Exec(`INSERT INTO session_catalog VALUES
 ('codex','before','Outside match','test','named',999,0),
 ('codex','start','Match start','test','named',1000,0),
 ('claude','middle','Match middle','test','named',1500,0),
 ('codex','end','Match end','test','named',1999,0),
 ('claude','after','Outside match','test','named',2000,0),
 ('dsh','unknown','Unknown date','test','untitled',0,0),
 ('codex','child','Match child','test','named',1100,1); INSERT INTO session_agents VALUES('codex','child','child','start',0,0,1)`)
	if err != nil {
		t.Fatal(err)
	}
	q := SessionCatalogQuery{From: 1000, To: 2000, PageSize: 1, Selected: &SessionKey{"codex", "before"}}
	page, err := s.ListSessions(ctx, q)
	if err != nil || page.Total != 3 || len(page.Items) != 1 || page.Items[0].Key.Session != "end" || page.Counts["codex"] != 2 || page.Counts["claude"] != 1 || page.Counts["dsh"] != 0 || page.Selected == nil {
		t.Fatalf("range/counts/selected: %+v %v", page, err)
	}
	q.PageToken = page.NextPageToken
	page, err = s.ListSessions(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Key.Session != "middle" {
		t.Fatalf("range second page: %+v %v", page, err)
	}
	changed := q
	changed.From = 1100
	if _, err = s.ListSessions(ctx, changed); err != ErrRange {
		t.Fatalf("date token reused across windows: %v", err)
	}
	q.PageToken = page.NextPageToken
	page, err = s.ListSessions(ctx, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Key.Session != "start" || page.NextPageToken != "" {
		t.Fatalf("inclusive lower boundary: %+v %v", page, err)
	}
	q.PageToken, q.Text = "", "Outside"
	page, err = s.ListSessions(ctx, q)
	if err != nil || page.Total != 0 || len(page.Items) != 0 || page.Counts["codex"] != 2 || page.Counts["claude"] != 1 {
		t.Fatalf("outside search leaked or date counts changed: %+v %v", page, err)
	}
	q.Text, q.CLI = "Match", "codex"
	page, err = s.ListSessions(ctx, q)
	if err != nil || page.Total != 2 || page.Counts["claude"] != 1 {
		t.Fatalf("date/CLI/search intersection: %+v %v", page, err)
	}
	for _, invalid := range []SessionCatalogQuery{{From: -1, To: 2000}, {To: 2000}, {From: 1000}, {From: 2000, To: 1000}, {From: 1000, To: 1000}} {
		if _, err = s.ListSessions(ctx, invalid); err != ErrRange {
			t.Fatalf("invalid date range %+v: %v", invalid, err)
		}
	}
}

func TestNativeCatalogIncludesZeroUsageAndRenamesIndependently(t *testing.T) {
	ctx := context.Background()
	h := home(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(h, ".codex")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err = db.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY,title TEXT,agent_nickname TEXT,updated_at_ms INTEGER);INSERT INTO threads VALUES('desktop','Desktop native',NULL,1),('shared','Codex same ID',NULL,2),('empty','',NULL,3)`); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(h, ".claude", "projects", "p", "one.jsonl")
	if err = os.MkdirAll(filepath.Dir(claude), 0700); err != nil {
		t.Fatal(err)
	}
	appendTo(t, claude, "{\"type\":\"ai-title\",\"sessionId\":\"shared\",\"aiTitle\":\"Claude original\"}\n")
	dsh := filepath.Join(h, ".dsh", "sessions", "p", "session.v3.jsonl.zstd")
	if err = os.MkdirAll(filepath.Dir(dsh), 0700); err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	if err = os.WriteFile(dsh, enc.EncodeAll([]byte("{\"type\":\"session\",\"id\":\"shared\",\"version\":3}\n{\"type\":\"session/title\",\"data\":{\"title\":\"DeepSeek original\"}}\n"), nil), 0600); err != nil {
		t.Fatal(err)
	}
	s := openAt(t, h, &now, nil)
	defer func() { _ = s.Close(ctx) }()
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessions(ctx, SessionCatalogQuery{PageSize: 2, Selected: &SessionKey{"codex", "desktop"}})
	if err != nil || !page.Ready || page.Total != 5 || len(page.Items) != 2 || page.NextPageToken == "" || page.Selected == nil || page.Selected.Title != "Desktop native" {
		t.Fatalf("zero-usage catalog: %+v %v", page, err)
	}
	if s.State().Revision != 0 {
		t.Fatal("metadata changed accounting version")
	}
	second, err := s.ListSessions(ctx, SessionCatalogQuery{PageSize: 2, PageToken: page.NextPageToken})
	if err != nil || len(second.Items) != 2 || second.Items[0].Key == page.Items[0].Key {
		t.Fatalf("pagination %+v %v", second, err)
	}
	appendTo(t, claude, "{\"type\":\"custom-title\",\"sessionId\":\"shared\",\"customTitle\":\"Claude renamed\"}\n")
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.ListSessions(ctx, SessionCatalogQuery{CLI: "claude", Text: "RENAMED", Selected: &SessionKey{"codex", "desktop"}})
	if err != nil || renamed.Total != 1 || renamed.Items[0].Title != "Claude renamed" || renamed.Selected == nil || renamed.Selected.Title != "Desktop native" || renamed.Revision <= page.Revision || s.State().Revision != 0 {
		t.Fatalf("rename/selected/filter: %+v %v", renamed, err)
	}
	stale, err := s.ListSessions(ctx, SessionCatalogQuery{PageSize: 2, PageToken: page.NextPageToken})
	if err != nil || !stale.ResetRequired || len(stale.Items) != 0 {
		t.Fatalf("stale page mixed revisions: %+v %v", stale, err)
	}
	before := s.State()
	if err = s.Scan(ctx); err != nil || s.State() != before {
		t.Fatalf("idle catalog changed: %+v %v", s.State(), err)
	}
}

func TestSessionCatalogSchema8UpgradeKeepsAccountingAndCursors(t *testing.T) {
	ctx := context.Background()
	h := home(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := openAt(t, h, &now, nil)
	path := filepath.Join(h, ".claude", "projects", "p", "a.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, claudeAt("one", "2026-11-07T10:00:00Z"))
	if err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	before, err := s.List(ctx, Query{From: now.Add(-3 * time.Hour).UnixMilli(), To: now.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	state := s.State()
	cursorBefore, err := s.cursor(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.w.Exec(`DROP TABLE session_catalog;DROP TABLE session_catalog_files;DROP TABLE session_catalog_status;DELETE FROM meta WHERE key IN('catalog_revision','catalog_ready');PRAGMA user_version=8`); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s = openAt(t, h, &now, nil)
	defer func() { _ = s.Close(ctx) }()
	var version int
	if err = s.r.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatal(version, err)
	}
	if s.State().Revision != state.Revision || s.State().Generation != state.Generation {
		t.Fatal("migration changed usage versions")
	}
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.List(ctx, Query{From: now.Add(-3 * time.Hour).UnixMilli(), To: now.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	cursorAfter, err := s.cursor(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if before.All != after.All || before.Total != after.Total || cursorBefore.Offset != cursorAfter.Offset || s.State().Revision != state.Revision {
		t.Fatal("catalog backfill changed usage or accounting cursor")
	}
	page, err := s.ListSessions(ctx, SessionCatalogQuery{})
	if err != nil || page.Total != 1 {
		t.Fatal(page, err)
	}
}

func TestCatalogFileRotationAndPartialCompressedAppend(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "session.v4.jsonl.zstd")
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	first := enc.EncodeAll([]byte("{\"type\":\"session\",\"id\":\"s\",\"version\":4}\n{\"type\":\"session/title\",\"data\":{\"title\":\"First\"}}\n"), nil)
	second := enc.EncodeAll([]byte("{\"type\":\"session/title\",\"data\":{\"title\":\"Renamed\"}}\n"), nil)
	if err = os.WriteFile(path, append(first, second[:len(second)-2]...), 0600); err != nil {
		t.Fatal(err)
	}
	read := func(old catalogFile) catalogFile {
		info, e := os.Stat(path)
		if e != nil {
			t.Fatal(e)
		}
		budget := int64(64 << 20)
		return readCatalogFile(ctx, source{path, "dsh", info}, old, &budget)
	}
	c := read(catalogFile{})
	if c.State.Entry.Title != "First" || c.Offset != int64(len(first)) {
		t.Fatal(c)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(second[len(second)-2:]); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	c = read(c)
	if c.State.Entry.Title != "Renamed" {
		t.Fatal(c)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, enc.EncodeAll([]byte("{\"type\":\"session\",\"id\":\"new\",\"version\":99}\n"), nil), 0600); err != nil {
		t.Fatal(err)
	}
	c = read(c)
	if c.State.Entry.Key.Session != "new" || c.State.Entry.Status != "unsupported" {
		t.Fatal(c)
	}
}

func TestCatalogHidesChildrenBeforeCountsSearchAndPagination(t *testing.T) {
	ctx := context.Background()
	h := home(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(h, ".codex")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.Exec(`CREATE TABLE threads(id TEXT PRIMARY KEY,title TEXT,agent_nickname TEXT,source TEXT,updated_at_ms INTEGER);
 INSERT INTO threads VALUES('parent','Main session',NULL,'cli',1),('child','Named child','Ada','{"subagent":{"thread_spawn":{"parent_thread_id":"parent"}}}',3);`)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "session_index.jsonl"), []byte("{\"id\":\"child\",\"thread_name\":\"Custom child title\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(h, ".claude", "projects", "p", "child.jsonl")
	if err = os.MkdirAll(filepath.Dir(claude), 0700); err != nil {
		t.Fatal(err)
	}
	appendTo(t, claude, "{\"type\":\"assistant\",\"sessionId\":\"sidechain\",\"isSidechain\":true}\n")
	appendTo(t, filepath.Join(filepath.Dir(claude), "main.jsonl"), "{\"type\":\"user\",\"sessionId\":\"main\",\"isSidechain\":false}\n")
	childPath := filepath.Join(filepath.Dir(claude), "main", "subagents", "agent-x.jsonl")
	if err = os.MkdirAll(filepath.Dir(childPath), 0700); err != nil {
		t.Fatal(err)
	}
	appendTo(t, childPath, "{\"type\":\"assistant\",\"sessionId\":\"main\",\"isSidechain\":true}\n{\"type\":\"ai-title\",\"sessionId\":\"main\",\"aiTitle\":\"Child must not rename main\"}\n")
	s := openAt(t, h, &now, nil)
	defer func() { _ = s.Close(ctx) }()
	if err = s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessions(ctx, SessionCatalogQuery{PageSize: 2})
	if err != nil || page.Total != 2 || len(page.Items) != 2 || page.NextPageToken != "" || page.Counts["codex"] != 1 || page.Counts["claude"] != 1 {
		t.Fatalf("children leaked into directory: %+v %v", page, err)
	}
	page, err = s.ListSessions(ctx, SessionCatalogQuery{Text: "child"})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("child search leaked: %+v %v", page, err)
	}
}

func TestCatalogSchema9UpgradeKeepsLedgerAndReclassifiesOldMetadata(t *testing.T) {
	ctx := context.Background()
	h := home(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(h, ".codex", "sessions", "one.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, "{\"type\":\"session_meta\",\"payload\":{\"id\":\"child\",\"source\":{\"subagent\":{\"other\":\"guardian\"}}}}\n")
	s := openAt(t, h, &now, nil)
	if err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	before := s.State()
	if _, err := s.w.Exec(`ALTER TABLE session_catalog DROP COLUMN is_child;PRAGMA user_version=9`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	s = openAt(t, h, &now, nil)
	defer func() { _ = s.Close(ctx) }()
	if s.State().Revision != before.Revision || s.State().Generation != before.Generation {
		t.Fatal("migration changed accounting")
	}
	if err := s.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessions(ctx, SessionCatalogQuery{})
	if err != nil || page.Total != 1 || !page.Items[0].Unlinked {
		t.Fatalf("unlinked old child must remain selectable: %+v %v", page, err)
	}
	var child bool
	if err = s.r.QueryRow(`SELECT is_child FROM session_catalog WHERE session='child'`).Scan(&child); err != nil || !child {
		t.Fatal("classification lost", err)
	}
}
