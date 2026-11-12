package aiusage

import (
	"context"
	"database/sql"
	"time"
)

// Agent metadata is separate from the request ledger and its accounting cursors.
const sessionAgentSchema = `
CREATE TABLE IF NOT EXISTS session_agents(cli TEXT NOT NULL,session TEXT NOT NULL,agent TEXT NOT NULL,parent TEXT NOT NULL,first_at INTEGER NOT NULL,last_at INTEGER NOT NULL,source_available INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(cli,session,agent));
CREATE TABLE IF NOT EXISTS session_agent_days(cli TEXT NOT NULL,session TEXT NOT NULL,agent TEXT NOT NULL,day TEXT NOT NULL,first_at INTEGER NOT NULL,last_at INTEGER NOT NULL,PRIMARY KEY(cli,session,agent,day));
CREATE TABLE IF NOT EXISTS session_agent_requests(cli TEXT NOT NULL,request_key TEXT NOT NULL,session TEXT NOT NULL,agent TEXT NOT NULL,PRIMARY KEY(cli,request_key));
`

type agentKey struct{ CLI, Session, ID string }
type nativeAgent struct {
	Key             agentKey
	Parent          string
	FirstAt, LastAt int64
	Available       bool
}
type agentDayKey struct {
	Agent agentKey
	Day   string
}
type agentDay struct{ First, Last int64 }
type agentRequestKey struct{ CLI, Request string }
type agentRequest struct {
	Agent agentKey
	At    int64
}
type agentUpdates struct {
	Agents   map[agentKey]nativeAgent
	Days     map[agentDayKey]agentDay
	Requests map[agentRequestKey]agentRequest
}

func firstPositive(a, b int64) int64 {
	if a == 0 {
		return b
	}
	if b == 0 {
		return a
	}
	return min(a, b)
}
func validAgentKey(k agentKey) bool {
	return validSessionKey(SessionKey{k.CLI, k.Session}) && k.ID != "" && len(k.ID) <= 512
}

func (u *agentUpdates) record(a nativeAgent) {
	if u == nil || !validAgentKey(a.Key) || len(a.Parent) > 512 {
		return
	}
	if u.Agents == nil {
		u.Agents = map[agentKey]nativeAgent{}
	}
	old := u.Agents[a.Key]
	if a.Parent == "" {
		a.Parent = old.Parent
	}
	a.FirstAt = firstPositive(old.FirstAt, a.FirstAt)
	a.LastAt = max(old.LastAt, a.LastAt)
	u.Agents[a.Key] = a
}
func (u *agentUpdates) activity(a nativeAgent, at int64) {
	if u == nil {
		return
	}
	u.record(a)
	if at <= 0 || !validAgentKey(a.Key) {
		return
	}
	if u.Days == nil {
		u.Days = map[agentDayKey]agentDay{}
	}
	k := agentDayKey{a.Key, time.UnixMilli(at).In(time.Local).Format("2006-01-02")}
	old := u.Days[k]
	u.Days[k] = agentDay{firstPositive(old.First, at), max(old.Last, at)}
}
func (u *agentUpdates) merge(v agentUpdates) {
	for _, a := range v.Agents {
		u.record(a)
	}
	for k, d := range v.Days {
		if u.Days == nil {
			u.Days = map[agentDayKey]agentDay{}
		}
		old := u.Days[k]
		u.Days[k] = agentDay{firstPositive(old.First, d.First), max(old.Last, d.Last)}
	}
	for k, a := range v.Requests {
		if u.Requests == nil {
			u.Requests = map[agentRequestKey]agentRequest{}
		}
		u.Requests[k] = a
	}
}

func commitAgents(ctx context.Context, tx *sql.Tx, u agentUpdates, available map[agentKey]bool) (bool, error) {
	changed := false
	exec := func(query string, args ...any) error {
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		changed = changed || n > 0
		return err
	}
	previous := map[agentKey]nativeAgent{}
	rows, err := tx.QueryContext(ctx, `SELECT cli,session,agent,parent,first_at,last_at,source_available FROM session_agents`)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var a nativeAgent
		if err = rows.Scan(&a.Key.CLI, &a.Key.Session, &a.Key.ID, &a.Parent, &a.FirstAt, &a.LastAt, &a.Available); err != nil {
			break
		}
		previous[a.Key] = a
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return false, err
	}
	for _, a := range u.Agents {
		old, found := previous[a.Key]
		if a.Parent == "" {
			a.Parent = old.Parent
		}
		a.FirstAt = firstPositive(a.FirstAt, old.FirstAt)
		a.LastAt = max(a.LastAt, old.LastAt)
		a.Available = old.Available
		if found && a == old {
			continue
		}
		previous[a.Key] = a
		err := exec(`INSERT INTO session_agents(cli,session,agent,parent,first_at,last_at) VALUES(?,?,?,?,?,?) ON CONFLICT(cli,session,agent) DO UPDATE SET parent=CASE WHEN excluded.parent<>'' THEN excluded.parent ELSE parent END,first_at=CASE WHEN first_at=0 THEN excluded.first_at WHEN excluded.first_at=0 THEN first_at ELSE min(first_at,excluded.first_at) END,last_at=max(last_at,excluded.last_at) WHERE (excluded.parent<>'' AND parent<>excluded.parent) OR (excluded.first_at>0 AND (first_at=0 OR excluded.first_at<first_at)) OR last_at<excluded.last_at`, a.Key.CLI, a.Key.Session, a.Key.ID, a.Parent, a.FirstAt, a.LastAt)
		if err != nil {
			return false, err
		}
	}
	for k, d := range u.Days {
		if err := exec(`INSERT INTO session_agent_days VALUES(?,?,?,?,?,?) ON CONFLICT(cli,session,agent,day) DO UPDATE SET first_at=min(first_at,excluded.first_at),last_at=max(last_at,excluded.last_at) WHERE first_at>excluded.first_at OR last_at<excluded.last_at`, k.Agent.CLI, k.Agent.Session, k.Agent.ID, k.Day, d.First, d.Last); err != nil {
			return false, err
		}
	}
	for k, r := range u.Requests {
		a := r.Agent
		if err := exec(`INSERT INTO session_agent_requests VALUES(?,?,?,?) ON CONFLICT(cli,request_key) DO UPDATE SET session=excluded.session,agent=excluded.agent WHERE session<>excluded.session OR agent<>excluded.agent`, k.CLI, k.Request, a.Session, a.ID); err != nil {
			return false, err
		}
	}
	for key, a := range previous {
		if a.Available == available[key] {
			continue
		}
		if err = exec(`UPDATE session_agents SET source_available=? WHERE cli=? AND session=? AND agent=?`, available[key], key.CLI, key.Session, key.ID); err != nil {
			return false, err
		}
	}
	return changed, nil
}

type agentGraph struct {
	Names      map[agentKey]string
	Agents     map[agentKey]nativeAgent
	Sessions   map[SessionKey]nativeAgent
	Catalog    map[SessionKey]SessionCatalogEntry
	Incomplete bool
}

func readAgentGraph(ctx context.Context, tx *sql.Tx) (agentGraph, error) {
	g := agentGraph{Agents: map[agentKey]nativeAgent{}, Sessions: map[SessionKey]nativeAgent{}, Catalog: map[SessionKey]SessionCatalogEntry{}}
	rows, err := tx.QueryContext(ctx, `SELECT cli,session,title,title_source,title_status,last_activity,is_child FROM session_catalog`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var v SessionCatalogEntry
		if err = rows.Scan(&v.Key.CLI, &v.Key.Session, &v.Title, &v.TitleSource, &v.Status, &v.LastActivity, &v.Child); err != nil {
			break
		}
		g.Catalog[v.Key] = v
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return g, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT cli,session,agent,parent,first_at,last_at,source_available FROM session_agents`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var a nativeAgent
		if err = rows.Scan(&a.Key.CLI, &a.Key.Session, &a.Key.ID, &a.Parent, &a.FirstAt, &a.LastAt, &a.Available); err != nil {
			break
		}
		g.Agents[a.Key] = a
		if a.Key.CLI != "claude" {
			g.Sessions[SessionKey{a.Key.CLI, a.Key.Session}] = a
		}
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return g, err
	}
	g.Names, err = readAgentNames(ctx, tx)
	if err != nil {
		return g, err
	}
	err = tx.QueryRowContext(ctx, `SELECT (SELECT value=0 FROM meta WHERE key='catalog_ready') OR EXISTS(SELECT 1 FROM session_catalog_status WHERE error<>'')`).Scan(&g.Incomplete)
	return g, err
}

// Ancestors include the native session itself; broken/cyclic relationships stop here.
func (g agentGraph) scopes(k SessionKey) []SessionKey {
	out := []SessionKey{k}
	seen := map[SessionKey]bool{k: true}
	for {
		a, ok := g.Sessions[k]
		if !ok || a.Parent == "" {
			break
		}
		p := SessionKey{k.CLI, a.Parent}
		if seen[p] {
			return out[:1]
		}
		if _, ok = g.Catalog[p]; !ok {
			break
		}
		out = append(out, p)
		seen[p] = true
		k = p
	}
	return out
}
func (g agentGraph) owners(a nativeAgent) []SessionKey {
	if a.Key.CLI == "claude" {
		return g.scopes(SessionKey{a.Key.CLI, a.Key.Session})
	}
	scopes := g.scopes(SessionKey{a.Key.CLI, a.Key.Session})
	return scopes[1:]
}
func (g agentGraph) incomplete(a nativeAgent) bool {
	return !a.Available || (a.Key.CLI != "claude" && len(g.owners(a)) == 0)
}
func (g agentGraph) active(ctx context.Context, tx *sql.Tx, from, to int64) (map[agentKey]bool, error) {
	out := map[agentKey]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT cli,session,agent FROM session_agent_days WHERE (first_at>=? AND first_at<?) OR (last_at>=? AND last_at<?)`, from, to, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k agentKey
		if err = rows.Scan(&k.CLI, &k.Session, &k.ID); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}
