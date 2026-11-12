package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Read native desktop identities even when they have no completed request or rollout.
func codexDatabaseSessions(ctx context.Context, root string, updates *agentUpdates) ([]SessionCatalogEntry, string) {
	path := filepath.Join(root, "state_5.sqlite")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, ""
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 512<<20 {
		return nil, "native_database_unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(100)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, "native_database_unavailable"
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	columns, err := db.QueryContext(ctx, `PRAGMA table_info(threads)`)
	if err != nil {
		return nil, "native_database_unavailable"
	}
	known := map[string]bool{}
	for columns.Next() {
		var cid, nn, pk int
		var name, kind string
		var def any
		if columns.Scan(&cid, &name, &kind, &nn, &def, &pk) != nil {
			_ = columns.Close()
			return nil, "native_database_unavailable"
		}
		known[name] = true
	}
	err = columns.Err()
	_ = columns.Close()
	if err != nil {
		return nil, "native_database_unavailable"
	}
	if !known["id"] || !known["title"] {
		return nil, "unsupported"
	}
	nick, activity, source, created := "NULL", "0", "NULL", "0"
	if known["created_at_ms"] {
		created = "coalesce(created_at_ms,0)"
	} else if known["created_at"] {
		created = "coalesce(created_at,0)*1000"
	}
	if known["source"] {
		source = "source"
	}
	if known["agent_nickname"] {
		nick = "agent_nickname"
	}
	if known["updated_at_ms"] {
		activity = "coalesce(updated_at_ms,0)"
	} else if known["updated_at"] {
		activity = "coalesce(updated_at,0)*1000"
	}
	rows, err := db.QueryContext(ctx, `SELECT id,title,`+nick+`,`+activity+`,`+source+`,`+created+` FROM threads LIMIT 10001`)
	if err != nil {
		return nil, "native_database_unavailable"
	}
	defer func() { _ = rows.Close() }()
	var entries []SessionCatalogEntry
	for rows.Next() {
		if len(entries) == 10000 {
			return nil, "capacity"
		}
		var id string
		var title, nickname, origin sql.NullString
		var at, first int64
		if rows.Scan(&id, &title, &nickname, &at, &origin, &first) != nil {
			return nil, "native_database_unavailable"
		}
		if !validSessionKey(SessionKey{"codex", id}) {
			continue
		}
		v := SessionCatalogEntry{Key: SessionKey{"codex", id}, LastActivity: at, Status: "untitled", Child: codexChildSource(origin.String) || cleanSessionTitle(nickname.String) != ""}
		if cleanSessionTitle(title.String) != "" {
			v.setTitle(title.String, "codex.database")
		} else if cleanSessionTitle(nickname.String) != "" {
			v.setTitle("子会话 · "+nickname.String, "codex.agent_nickname")
		}
		if v.Child {
			a := nativeAgent{Key: agentKey{"codex", id, id}, Parent: codexParentSource(origin.String), FirstAt: first, LastAt: at}
			updates.activity(a, first)
			updates.activity(a, at)
		}
		entries = append(entries, v)
	}
	if rows.Err() != nil {
		return nil, "native_database_unavailable"
	}
	return entries, ""
}

// Native source metadata identifies spawned agents even when they have a title.
func codexChildSource(source string) bool {
	var v struct {
		Subagent json.RawMessage `json:"subagent"`
	}
	if json.Unmarshal([]byte(source), &v) == nil && len(v.Subagent) > 0 && string(v.Subagent) != "null" {
		return true
	}
	return source == "subagent" || strings.HasPrefix(source, "subagent_")
}
