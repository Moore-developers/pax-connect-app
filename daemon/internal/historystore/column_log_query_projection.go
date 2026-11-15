package historystore

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"pax-connect/daemon/internal/config"
)

// GroupEventEach streams a complete group's facts for the production reducer.
// order: 0 chronological/sequence; 1 chronological/event ID; 2 timeline.
func (c *Columns) GroupEventEach(ctx context.Context, view, key string, order int, fn func(config.HistoryEvent) error) error {
	return columnGroupEventEach(ctx, c.r, view, key, "", nil, order, fn)
}

// GroupEventEach includes facts changed in the current write transaction.
func (t *ColumnsTx) GroupEventEach(ctx context.Context, view, key string, order int, fn func(config.HistoryEvent) error) error {
	return columnGroupEventEach(ctx, t.tx, view, key, "", nil, order, fn)
}

// GroupRelated counts distinct connected facts without a per-group Go set.
func (c *Columns) GroupRelated(ctx context.Context, view, key string) (uint64, error) {
	return columnGroupRelated(ctx, c.r, view, key)
}

// GroupRelated counts distinct connected facts inside the current transaction.
func (t *ColumnsTx) GroupRelated(ctx context.Context, view, key string) (uint64, error) {
	return columnGroupRelated(ctx, t.tx, view, key)
}
func columnGroupRelated(ctx context.Context, q columnQueryer, view, key string) (uint64, error) {
	col, err := columnViewKey(view)
	if err != nil {
		return 0, err
	}
	var n uint64
	err = q.QueryRowContext(ctx, "SELECT count(DISTINCT x.flow_key) FROM col_event_index x JOIN col_events e ON e.event_id=x.event_id WHERE x."+col+"<>'' AND x."+col+"=? AND e.flow_present=1", key).Scan(&n)
	return n, err
}

func columnGroupEventEach(ctx context.Context, q columnQueryer, view, key, where string, args []any, order int, fn func(config.HistoryEvent) error) error {
	col, err := columnViewKey(view)
	if err != nil {
		return err
	}
	ordering := "e.at_ns ASC,e.seq ASC"
	if order == 1 {
		ordering = "e.at_ns ASC,e.event_id ASC"
	}
	if order == 2 {
		ordering = "e.at_ns DESC,e.event_id DESC"
	}
	// A keyset cursor keeps a hot group linear as its event count grows.
	var at, seq int64
	var id string
	first := true
	for {
		query := columnEventSelect() + " JOIN col_event_index x ON x.event_id=e.event_id WHERE x." + col + "<>'' AND x." + col + "=?" + where
		a := append([]any{key}, args...)
		if !first {
			switch order {
			case 0:
				query += " AND (e.at_ns>? OR (e.at_ns=? AND e.seq>?))"
				a = append(a, at, at, seq)
			case 1:
				query += " AND (e.at_ns>? OR (e.at_ns=? AND e.event_id>?))"
				a = append(a, at, at, id)
			case 2:
				query += " AND (e.at_ns<? OR (e.at_ns=? AND e.event_id<?))"
				a = append(a, at, at, id)
			}
		}
		query += " ORDER BY " + ordering + " LIMIT 128"
		rows, er := q.QueryContext(ctx, query, a...)
		if er != nil {
			return er
		}
		var batch []config.HistoryEvent
		for rows.Next() {
			e, er := scanColumnEvent(rows)
			if er != nil {
				_ = rows.Close()
				return er
			}
			e.Raw = nil
			batch = append(batch, e)
		}
		er = rows.Err()
		_ = rows.Close()
		if er != nil {
			return er
		}
		if len(batch) == 0 {
			return nil
		}
		for _, e := range batch {
			if er = columnEventChildren(ctx, q, &e); er != nil {
				return er
			}
			if er = fn(e); er != nil {
				return er
			}
		}
		last := batch[len(batch)-1]
		at, seq, id = last.At.UnixNano(), int64(last.Sequence), last.ID
		first = false
	}
}

// DeleteGroup removes a now-empty projection and its ordered answer children.
func (t *ColumnsTx) DeleteGroup(ctx context.Context, view, key string) error {
	if view == "activity" {
		if err := t.exec(ctx, "DELETE FROM col_group_options WHERE group_key=?", key); err != nil {
			return err
		}
	}
	if err := t.exec(ctx, "DELETE FROM col_groups WHERE view=? AND group_key=?", view, key); err != nil {
		return err
	}
	return t.exec(ctx, "DELETE FROM col_answers WHERE owner_kind=2 AND owner_key=?", view+"\x00"+key)
}

// ColumnLogObject is one SQL-produced object directory row.
type ColumnLogObject struct {
	ID, Name, Symbol string
	Count            uint64
}

// ContainsKey verifies detail identity against the same complete SQL filter.
func (s *ColumnLogSelection) ContainsKey(ctx context.Context, f ColumnLogFilter, key string) (bool, error) {
	where, args := s.where(f)
	args = append(args, key)
	var n int
	err := s.q.QueryRowContext(ctx, "SELECT count(*) FROM log_catalog l WHERE "+where+" AND l.k=?", args...).Scan(&n)
	return n > 0, err
}

// Count returns the exact count after all filters, before page selection.
func (s *ColumnLogSelection) Count(ctx context.Context, f ColumnLogFilter) (uint64, error) {
	if s.basicActivity(f) {
		return s.basicActivityCount(ctx, f)
	}
	if s.simpleGroupFilter(f) {
		where, args := s.groupScope(f)
		var n uint64
		err := s.q.QueryRowContext(ctx, "SELECT count(*) FROM ("+s.completeProjection("g.group_key,g.level")+" UNION ALL SELECT group_key,level FROM log_delta) WHERE "+where, args...).Scan(&n)
		return n, err
	}
	if s.Window.View == "" && (f.ObjectID == "" || f.ObjectID == "all") && s.Window.Route == "" {
		where, args := s.rawScope(f)
		var n uint64
		err := s.q.QueryRowContext(ctx, "SELECT count(*) FROM snap_col_events e WHERE "+where, args...).Scan(&n)
		return n, err
	}
	where, args := s.where(f)
	var n uint64
	err := s.q.QueryRowContext(ctx, "SELECT count(*) FROM log_catalog l WHERE "+where, args...).Scan(&n)
	return n, err
}

// KeysPage selects and orders only current-page keys after the full filter.
func (s *ColumnLogSelection) KeysPage(ctx context.Context, f ColumnLogFilter, offset, limit int) ([]string, error) {
	if s.basicActivity(f) {
		return s.basicActivityPage(ctx, f, offset, limit)
	}
	where, args := s.where(f)
	source := "log_catalog l"
	key, at := "k", "last_ns"
	if s.simpleGroupFilter(f) {
		where, args = s.groupScope(f)
		source, key = "("+s.completeProjection("g.group_key,g.last_ns,g.level")+" UNION ALL SELECT group_key,last_ns,level FROM log_delta)", "group_key"
	}
	if s.Window.View == "" && (f.ObjectID == "" || f.ObjectID == "all") && s.Window.Route == "" {
		where, args = s.rawScope(f)
		source, key, at = "snap_col_events e", "event_id", "at_ns"
	}
	args = append(args, limit, offset)
	rows, err := s.q.QueryContext(ctx, "WITH page AS MATERIALIZED(SELECT "+key+" AS k,"+at+" AS last_ns FROM "+source+" WHERE "+where+" ORDER BY "+at+" DESC,"+key+" DESC LIMIT ? OFFSET ?) SELECT k FROM page ORDER BY last_ns DESC,k DESC", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *ColumnLogSelection) simpleGroupFilter(f ColumnLogFilter) bool {
	if s.Window.View == "" || f.ObjectID != "" && f.ObjectID != "all" || f.ConnectionKey != "" || f.ProblemID != "" {
		return false
	}
	if s.Window.View != "activity" {
		return true
	}
	return f.IncludeDNS && f.Scope == "" && f.Dimension != "system" && f.Quick == "" && len(f.Kinds) == 0 && len(f.Levels) == 0 && len(f.Transports) == 0 && len(f.Directions) == 0 && f.Application == "" && f.Domain == "" && f.TargetIP == "" && f.Route == "" && f.RulePackage == "" && f.DNSAnswerIP == "" && f.Port == 0 && f.MinLatency == 0
}
func (s *ColumnLogSelection) groupScope(f ColumnLogFilter) (string, []any) {
	if f.Level == "" {
		return "1", nil
	}
	return "level=?", []any{map[string]int{"info": 1, "warning": 2, "error": 3}[f.Level]}
}

func (s *ColumnLogSelection) rawScope(f ColumnLogFilter) (string, []any) {
	where := "e.at_ns>? AND e.at_ns>=? AND e.at_ns<=?"
	args := []any{columnQueryTime(s.Window.From), columnQueryTime(s.Window.Cutoff), columnQueryTime(s.Window.Until)}
	if f.Level != "" {
		where += " AND e.level=?"
		args = append(args, f.Level)
	}
	return where, args
}

// Options reads the full frozen scope, independently of rows and filters.
func (s *ColumnLogSelection) Options(ctx context.Context) (apps, routes, rules []string, err error) {
	return s.integerOptions(ctx)
}

// Objects computes totals over the complete filtered scope and applies only
// directory-name search when selecting the returned rows.
func (s *ColumnLogSelection) Objects(ctx context.Context, f ColumnLogFilter, search, selected string, offset, limit int) (items []ColumnLogObject, total, unknown, objects uint64, chosen *ColumnLogObject, err error) {
	where, args := s.where(f)
	id, name, symbol := s.ObjectExpressions(f.Dimension)
	source := "SELECT " + id + " AS id," + name + " AS name," + symbol + " AS symbol,last_ns,k,0 AS idref FROM log_catalog l WHERE " + where
	if narrow, ok := s.basicDomainObjectSource(f); ok {
		source, args = narrow, nil
	}
	known := "1"
	if s.Window.View == "activity" {
		known = "id<>'unknown'"
	}
	order := "name ASC,id ASC"
	pageOrder := "page.name ASC,page.id ASC"
	if s.Window.View == "activity" {
		order = "n DESC,column_lower(name) ASC,id ASC"
		pageOrder = "page.n DESC,column_lower(page.name) ASC,page.id ASC"
	}
	// Freeze the filtered source once: the old implementation re-read and
	// re-grouped the entire catalog for counts, selection and every page.
	query := `WITH source AS MATERIALIZED (` + source + `),
ranked AS MATERIALIZED (SELECT id,name,symbol,idref,count(*) OVER(PARTITION BY id) AS n,row_number() OVER(PARTITION BY id ORDER BY last_ns DESC,k DESC) AS rn FROM source WHERE ` + known + `),
groups AS MATERIALIZED (SELECT id,name,symbol,n,idref FROM ranked WHERE rn=1),
filtered AS MATERIALIZED (SELECT id,name,symbol,n,idref FROM groups WHERE instr(column_lower(name),column_lower(?))>0),
page AS MATERIALIZED (SELECT id,name,symbol,n,idref FROM filtered ORDER BY ` + order + ` LIMIT ? OFFSET ?),
counts AS (SELECT count(*) AS total,coalesce(sum(CASE WHEN id='unknown' THEN 1 ELSE 0 END),0) AS unknown FROM source)
SELECT counts.total,counts.unknown,(SELECT count(*) FROM filtered),chosen.id,chosen.name,chosen.symbol,chosen.n,chosen.idref,page.id,page.name,page.symbol,page.n,page.idref
FROM counts LEFT JOIN groups chosen ON ?<>'' AND chosen.id=? LEFT JOIN page ON 1 ORDER BY ` + pageOrder
	args = append(args, search, limit, offset, selected, selected)
	rows, e := s.q.QueryContext(ctx, query, args...)
	if e != nil {
		err = e
		return
	}
	var pageIdentities []int64
	var chosenIdentity int64
	for rows.Next() {
		var chosenID, chosenName, chosenSymbol, pageID, pageName, pageSymbol sql.NullString
		var chosenCount, pageCount, selectedRef, pageRef sql.NullInt64
		if err = rows.Scan(&total, &unknown, &objects, &chosenID, &chosenName, &chosenSymbol, &chosenCount, &selectedRef, &pageID, &pageName, &pageSymbol, &pageCount, &pageRef); err != nil {
			_ = rows.Close()
			return
		}
		if chosenID.Valid {
			chosen = &ColumnLogObject{ID: chosenID.String, Name: chosenName.String, Symbol: chosenSymbol.String, Count: uint64(chosenCount.Int64)}
			chosenIdentity = selectedRef.Int64
		}
		if pageID.Valid {
			items = append(items, ColumnLogObject{ID: pageID.String, Name: pageName.String, Symbol: pageSymbol.String, Count: uint64(pageCount.Int64)})
			pageIdentities = append(pageIdentities, pageRef.Int64)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err == nil && s.deferredDomainClassification {
		err = s.classifyDirectoryPage(ctx, items, pageIdentities, chosen, chosenIdentity)
	}
	return
}

func (s *ColumnLogSelection) classifyDirectoryPage(ctx context.Context, items []ColumnLogObject, identities []int64, chosen *ColumnLogObject, chosenIdentity int64) error {
	ids := map[int64]bool{}
	for _, id := range identities {
		ids[id] = true
	}
	if chosen != nil {
		ids[chosenIdentity] = true
	}
	if len(ids) == 0 {
		return nil
	}
	ordered := make([]int64, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ordered)), ",")
	args := make([]any, 0, 2*len(ordered))
	for range 2 {
		for _, id := range ordered {
			args = append(args, id)
		}
	}
	query := "SELECT id,coalesce(application_id,''),coalesce(process,'') FROM snap_col_identities WHERE id IN (" + marks + ") UNION ALL SELECT id,coalesce(application_id,''),coalesce(process,'') FROM log_identity_delta WHERE id IN (" + marks + ")"
	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	symbols := make(map[int64]string, len(ordered))
	for rows.Next() {
		var id int64
		var app, process string
		if err = rows.Scan(&id, &app, &process); err != nil {
			return err
		}
		symbols[id] = "gearshape"
		if _, ok := s.Window.Classify(app, process); ok {
			symbols[id] = "app"
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(symbols) != len(ordered) {
		return errors.New("history_expired")
	}
	for i := range items {
		items[i].Symbol = symbols[identities[i]]
	}
	if chosen != nil {
		chosen.Symbol = symbols[chosenIdentity]
	}
	return nil
}
