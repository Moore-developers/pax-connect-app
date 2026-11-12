package aiusage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

func mergeCatalog(entries map[SessionKey]SessionCatalogEntry, v SessionCatalogEntry) {
	if !validSessionKey(v.Key) {
		return
	}
	old, found := entries[v.Key]
	if !found {
		entries[v.Key] = v
		return
	}
	activity := max(old.LastActivity, v.LastActivity)
	child := old.Child || v.Child
	if titlePriority(v.TitleSource) > titlePriority(old.TitleSource) || (titlePriority(v.TitleSource) == titlePriority(old.TitleSource) && v.LastActivity >= old.LastActivity) {
		old = v
	}
	old.LastActivity = activity
	old.Child = child
	entries[v.Key] = old
}

// scanCatalog has the same single owner as collection, with a separate durable
// version. Source I/O and metadata commits never change accounting revisions.
func (s *Store) scanCatalog(ctx context.Context, files []source, sourceErrors map[string]string) error {
	previous := make(map[SessionKey]SessionCatalogEntry)
	rows, err := s.r.QueryContext(ctx, `SELECT cli,session,title,title_source,title_status,last_activity,is_child FROM session_catalog`)
	if err != nil {
		return ErrUnavailable
	}
	for rows.Next() {
		var v SessionCatalogEntry
		if rows.Scan(&v.Key.CLI, &v.Key.Session, &v.Title, &v.TitleSource, &v.Status, &v.LastActivity, &v.Child) != nil {
			_ = rows.Close()
			return ErrUnavailable
		}
		previous[v.Key] = v
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return ErrUnavailable
	}
	cursors := map[string]catalogFile{}
	rows, err = s.r.QueryContext(ctx, `SELECT path,cli,inode,size,mtime,offset,state FROM session_catalog_files`)
	if err != nil {
		return ErrUnavailable
	}
	for rows.Next() {
		var c catalogFile
		var raw []byte
		if rows.Scan(&c.Path, &c.CLI, &c.Inode, &c.Size, &c.Mtime, &c.Offset, &raw) != nil || json.Unmarshal(raw, &c.State) != nil {
			_ = rows.Close()
			return ErrUnavailable
		}
		cursors[c.Path] = c
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return ErrUnavailable
	}
	sourceStatus := map[string]string{}
	for cli, code := range sourceErrors {
		sourceStatus[cli] = code
	}
	entries := map[SessionKey]SessionCatalogEntry{}
	previews := map[SessionKey]SessionCatalogEntry{}
	budget := int64(64 << 20)
	ready := true
	next := map[string]catalogFile{}
	updates := agentUpdates{}
	var unsupported []SessionKey
	for _, f := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c := readCatalogFile(ctx, f, cursors[f.path], &budget)
		refreshAgentName(ctx, &c)
		if c.Path == "" {
			c.Path = f.path
			c.CLI = f.cli
		}
		next[f.path] = c
		updates.merge(c.Updates)
		if c.State.Error == "unsupported" {
			unsupported = append(unsupported, c.State.Entry.Key)
		} else if c.State.Error != "" {
			sourceStatus[f.cli] = c.State.Error
		}
		if c.State.Pending {
			ready = false
		}
		mergeCatalog(entries, c.State.Entry)
		if c.CLI == "claude" && c.State.FileAgent == "" && c.State.ClaudePreview != "" {
			v := c.State.Entry
			v.Title, v.TitleSource = c.State.ClaudePreview, c.State.ClaudePreviewSource
			mergeCatalog(previews, v)
		}
		for _, v := range c.State.Entries {
			mergeCatalog(entries, v)
		}
	}
	// Resolve all formal sources first, including explicit custom-title clears.
	// Preview text is display metadata; it does not turn an untitled session into named.
	for key, preview := range previews {
		v := entries[key]
		if v.Title == "" {
			v.Title, v.TitleSource = preview.Title, preview.TitleSource
			entries[key] = v
		}
	}
	for _, key := range unsupported {
		if entry, ok := entries[key]; ok && entry.Status == "unsupported" {
			sourceStatus[key.CLI] = "unsupported"
		}
	}
	root := s.catalogRoot("codex")
	native, code := codexDatabaseSessions(ctx, root, &updates)
	if code != "" {
		sourceStatus["codex"] = code
	}
	for _, v := range native {
		mergeCatalog(entries, v)
	}
	index := codexIndexSessions(ctx, root)
	if index == nil {
		if _, e := os.Lstat(filepath.Join(root, "session_index.jsonl")); e == nil {
			sourceStatus["codex"] = "native_index_unavailable"
		}
	}
	for _, v := range index {
		mergeCatalog(entries, v)
	}
	// Existing relations survive source removal; newly found DSH relations can
	// require one bounded replay of a child file whose descriptor was absent.
	known := map[SessionKey]nativeAgent{}
	agents, e := s.r.QueryContext(ctx, `SELECT cli,session,agent,parent,first_at,last_at FROM session_agents`)
	if e != nil {
		return ErrUnavailable
	}
	for agents.Next() {
		var a nativeAgent
		if e = agents.Scan(&a.Key.CLI, &a.Key.Session, &a.Key.ID, &a.Parent, &a.FirstAt, &a.LastAt); e != nil {
			break
		}
		if a.Key.CLI != "claude" {
			known[SessionKey{a.Key.CLI, a.Key.Session}] = a
		}
	}
	if e == nil {
		e = agents.Err()
	}
	_ = agents.Close()
	if e != nil {
		return ErrUnavailable
	}
	for _, a := range updates.Agents {
		if a.Key.CLI != "claude" {
			known[SessionKey{a.Key.CLI, a.Key.Session}] = a
		}
	}
	available := map[agentKey]bool{}
	for path, c := range next {
		if a, ok := known[c.State.Entry.Key]; ok {
			if (c.CLI == "dsh" || c.CLI == "codex") && c.State.Agent.Key.ID == "" {
				c.State.Entry.Child = true
				c.State.Agent = a
				c.Offset = 0
				c.State.Pending = true
				ready = false
				next[path] = c
			}
			if c.State.Error == "" {
				available[a.Key] = true
			}
		}
		if c.State.Error == "" {
			for _, key := range c.State.KnownAgents {
				available[key] = true
			}
		}
		if c.State.Agent.Key.ID != "" && c.State.Error == "" {
			available[c.State.Agent.Key] = true
		}
	}
	for key, v := range entries {
		if _, ok := known[key]; ok {
			v.Child = true
			entries[key] = v
		}
	}
	// Child identity survives loss of the source that originally classified it.
	for key, v := range entries {
		v.Child = v.Child || previous[key].Child
		entries[key] = v
	}
	// A failed root is not a deletion. Keep identities and their last known names.
	for key, v := range previous {
		if sourceStatus[key.CLI] != "" {
			if _, ok := entries[key]; !ok {
				v.Status = "source_unavailable"
				if sourceStatus[key.CLI] == "unsupported" {
					v.Status = "unsupported"
				}
				entries[key] = v
			}
		}
	}
	// Retained history remains selectable after removal of native source records.
	rows, err = s.r.QueryContext(ctx, `SELECT cli,session,max(at) FROM requests WHERE session<>'' GROUP BY cli,session`)
	if err != nil {
		return ErrUnavailable
	}
	for rows.Next() {
		var key SessionKey
		var at int64
		if rows.Scan(&key.CLI, &key.Session, &at) != nil {
			_ = rows.Close()
			return ErrUnavailable
		}
		if _, ok := entries[key]; !ok {
			v := previous[key]
			v.Key = key
			v.Status = "source_missing"
			v.LastActivity = max(v.LastActivity, at)
			entries[key] = v
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return ErrUnavailable
	}
	floor := s.options.Now().AddDate(0, 0, -93)
	for key := range updates.Days {
		if key.Day < floor.Format("2006-01-02") {
			delete(updates.Days, key)
		}
	}
	for key, r := range updates.Requests {
		if r.At < floor.UnixMilli() {
			delete(updates.Requests, key)
		}
	}
	return s.commitCatalog(ctx, previous, entries, cursors, next, sourceStatus, ready, updates, available)
}

func (s *Store) commitCatalog(ctx context.Context, previous, entries map[SessionKey]SessionCatalogEntry, cursors, next map[string]catalogFile, sources map[string]string, ready bool, updates agentUpdates, available map[agentKey]bool) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	changed, err := commitAgents(ctx, tx, updates, available)
	if err != nil {
		return ErrUnavailable
	}
	for key, v := range entries {
		if previous[key] == v {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO session_catalog VALUES(?,?,?,?,?,?,?) ON CONFLICT(cli,session) DO UPDATE SET title=excluded.title,title_source=excluded.title_source,title_status=excluded.title_status,last_activity=excluded.last_activity,is_child=excluded.is_child`, key.CLI, key.Session, v.Title, v.TitleSource, v.Status, v.LastActivity, v.Child); err != nil {
			return ErrUnavailable
		}
		changed = true
	}
	for key := range previous {
		if _, ok := entries[key]; !ok {
			if _, err = tx.ExecContext(ctx, `DELETE FROM session_catalog WHERE cli=? AND session=?`, key.CLI, key.Session); err != nil {
				return ErrUnavailable
			}
			changed = true
		}
	}
	for path, c := range next {
		raw, e := json.Marshal(c.State)
		if e != nil {
			return ErrUnavailable
		}
		old, ok := cursors[path]
		oldRaw, _ := json.Marshal(old.State)
		changed = changed || old.State.AgentName != c.State.AgentName
		if ok && c.Inode == old.Inode && c.Size == old.Size && c.Mtime == old.Mtime && c.Offset == old.Offset && string(raw) == string(oldRaw) {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO session_catalog_files VALUES(?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET cli=excluded.cli,inode=excluded.inode,size=excluded.size,mtime=excluded.mtime,offset=excluded.offset,state=excluded.state`, path, c.CLI, c.Inode, c.Size, c.Mtime, c.Offset, raw); err != nil {
			return ErrUnavailable
		}
	}
	for path, old := range cursors {
		if _, ok := next[path]; !ok && sources[old.CLI] == "" {
			if _, err = tx.ExecContext(ctx, `DELETE FROM session_catalog_files WHERE path=?`, path); err != nil {
				return ErrUnavailable
			}
		}
	}
	for _, cli := range []string{"claude", "codex", "dsh"} {
		result, e := tx.ExecContext(ctx, `INSERT INTO session_catalog_status VALUES(?,?) ON CONFLICT(cli) DO UPDATE SET error=excluded.error WHERE error<>excluded.error`, cli, sources[cli])
		if e != nil {
			return ErrUnavailable
		}
		n, _ := result.RowsAffected()
		changed = changed || n > 0
	}
	var oldReady bool
	if tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='catalog_ready'`).Scan(&oldReady) != nil {
		return ErrUnavailable
	}
	if ready != oldReady {
		changed = true
		if _, err = tx.ExecContext(ctx, `UPDATE meta SET value=? WHERE key='catalog_ready'`, ready); err != nil {
			return ErrUnavailable
		}
	}
	if changed {
		if _, err = tx.ExecContext(ctx, `UPDATE meta SET value=value+1 WHERE key='catalog_revision'`); err != nil {
			return ErrUnavailable
		}
	}
	var revision uint64
	if tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='catalog_revision'`).Scan(&revision) != nil {
		return ErrUnavailable
	}
	if tx.Commit() != nil {
		return ErrUnavailable
	}
	s.mutate(func(state *State) {
		state.CatalogRevision = revision
		state.CatalogReady = ready
		state.CatalogError = ""
	})
	return nil
}
