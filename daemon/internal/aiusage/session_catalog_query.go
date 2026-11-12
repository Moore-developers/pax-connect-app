package aiusage

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// Metadata and retained request presence use the same snapshot and pagination version.
func catalogCandidates(ctx context.Context, tx *sql.Tx, q SessionCatalogQuery) ([]SessionCatalogEntry, map[string]int64, *SessionCatalogEntry, map[string]string, error) {
	g, err := readAgentGraph(ctx, tx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	included := map[SessionKey]bool{}
	for key, v := range g.Catalog {
		included[key] = q.To == 0 || (v.LastActivity >= q.From && v.LastActivity < q.To)
	}
	if q.To != 0 {
		rows, e := tx.QueryContext(ctx, `SELECT DISTINCT cli,session FROM requests WHERE at>=? AND at<?`, q.From, q.To)
		if e != nil {
			return nil, nil, nil, nil, e
		}
		for rows.Next() {
			var k SessionKey
			if e = rows.Scan(&k.CLI, &k.Session); e != nil {
				break
			}
			for _, scope := range g.scopes(k) {
				included[scope] = true
			}
		}
		if e == nil {
			e = rows.Err()
		}
		_ = rows.Close()
		if e != nil {
			return nil, nil, nil, nil, e
		}
		active, e := g.active(ctx, tx, q.From, q.To)
		if e != nil {
			return nil, nil, nil, nil, e
		}
		for k := range active {
			if a, ok := g.Agents[k]; ok {
				for _, scope := range g.owners(a) {
					included[scope] = true
				}
			}
		}
	}
	// A child's native activity also orders its main conversation as recently used.
	activity := g.lastActivity()
	counts := map[string]int64{}
	sources := map[string]string{}
	var selected *SessionCatalogEntry
	var items []SessionCatalogEntry
	text := strings.ToLower(q.Text)
	for key, v := range g.Catalog {
		linked := len(g.scopes(key)) > 1
		v.Unlinked = v.Child && !linked
		v.LastActivity = max(v.LastActivity, activity[key])
		if q.Selected != nil && key == *q.Selected {
			copy := v
			selected = &copy
		}
		if !included[key] || linked {
			continue
		}
		counts[key.CLI]++
		if q.CLI != "" && key.CLI != q.CLI {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(v.Title), text) && !strings.Contains(strings.ToLower(key.Session), text) {
			continue
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.LastActivity != b.LastActivity {
			return a.LastActivity > b.LastActivity
		}
		if a.Key.CLI != b.Key.CLI {
			return a.Key.CLI < b.Key.CLI
		}
		return a.Key.Session < b.Key.Session
	})
	rows, err := tx.QueryContext(ctx, `SELECT cli,error FROM session_catalog_status WHERE error<>''`)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cli, code string
		if err = rows.Scan(&cli, &code); err != nil {
			return nil, nil, nil, nil, err
		}
		sources[cli] = code
	}
	return items, counts, selected, sources, rows.Err()
}

// lastActivity includes native descendants, so both session lists order the same
// conversation by its latest activity rather than its cost.
func (g agentGraph) lastActivity() map[SessionKey]int64 {
	activity := map[SessionKey]int64{}
	for key, v := range g.Catalog {
		for _, scope := range g.scopes(key) {
			activity[scope] = max(activity[scope], v.LastActivity)
		}
	}
	return activity
}
