package historystore

import (
	"context"
	"database/sql"
	"fmt"

	"pax-connect/daemon/internal/config"
)

// ProjectionProgress is the last committed key of one bounded rebuild phase.
func (c *Columns) ProjectionProgress(ctx context.Context, name string) (string, error) {
	var s string
	err := c.r.QueryRowContext(ctx, "SELECT value FROM col_projection_progress WHERE name=?", name).Scan(&s)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return s, err
}

// SetProjectionProgress commits progress together with the rows it describes.
func (t *ColumnsTx) SetProjectionProgress(ctx context.Context, name, value string) error {
	return t.exec(ctx, "INSERT INTO col_projection_progress VALUES(?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value", name, value)
}

func columnViewKey(view string) (string, error) {
	switch view {
	case "":
		return "event_id", nil
	case "activity":
		return "activity", nil
	case "access":
		return "access", nil
	case "problems":
		return "problem", nil
	case "operations":
		return "operation", nil
	}
	return "", fmt.Errorf("historystore: invalid column view %q", view)
}

// GroupKeys pages the complete key directory without decoding event facts.
func (c *Columns) GroupKeys(ctx context.Context, view, after string, limit int) ([]string, error) {
	col, err := columnViewKey(view)
	if err != nil {
		return nil, err
	}
	rows, err := c.r.QueryContext(ctx, "SELECT DISTINCT "+col+" FROM col_event_index WHERE "+col+"<>'' AND "+col+">? ORDER BY "+col+" LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func columnGroupEvents(ctx context.Context, q columnQueryer, view, key string) ([]config.HistoryEvent, error) {
	col, err := columnViewKey(view)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, columnEventSelect()+" JOIN col_event_index x ON x.event_id=e.event_id WHERE x."+col+"<>'' AND x."+col+"=? ORDER BY e.at_ns DESC,e.event_id DESC", key)
	if err != nil {
		return nil, err
	}
	var out []config.HistoryEvent
	for rows.Next() {
		e, er := scanColumnEvent(rows)
		if er != nil {
			_ = rows.Close()
			return nil, er
		}
		e.Raw = nil
		out = append(out, e)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range out {
		if err = columnEventChildren(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GroupEvents loads only one group's projection input. The offline S1 rebuild
// uses production whole-group functions; S2 supplies bounded window reducers.
func (c *Columns) GroupEvents(ctx context.Context, view, key string) ([]config.HistoryEvent, error) {
	return columnGroupEvents(ctx, c.r, view, key)
}

// GroupEvents reads this transaction's newly inserted facts as well as old ones.
func (t *ColumnsTx) GroupEvents(ctx context.Context, view, key string) ([]config.HistoryEvent, error) {
	return columnGroupEvents(ctx, t.tx, view, key)
}

// Event reads immutable evidence inside a fact/projection write transaction.
func (t *ColumnsTx) Event(ctx context.Context, id string) (config.HistoryEvent, bool, error) {
	e, err := scanColumnEvent(t.tx.QueryRowContext(ctx, columnEventSelect()+" WHERE e.event_id=?", id))
	if err == sql.ErrNoRows {
		return e, false, nil
	}
	if err != nil {
		return e, false, err
	}
	err = columnEventChildren(ctx, t.tx, &e)
	return e, err == nil, err
}

// Connection reads this transaction's latest summary.
func (t *ColumnsTx) Connection(ctx context.Context, key string) (config.LogConnectionSummary, bool, error) {
	s, err := scanColumnConnection(t.tx.QueryRowContext(ctx, columnConnectionSelect()+" WHERE c.flow_key=?", key))
	if err == sql.ErrNoRows {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	err = columnAnswers(ctx, t.tx, 1, key, &s.Fact)
	return s, err == nil, err
}

// Group reads one complete scalar projection and its ordered DNS answers.
func (c *Columns) Group(ctx context.Context, view, key string) (ColumnGroup, bool, error) {
	return readColumnGroup(ctx, c.r, "col_groups", "col_identities", "col_answers", view, key)
}

func readColumnGroup(ctx context.Context, q columnQueryer, groups, identities, answers, view, key string) (ColumnGroup, bool, error) {
	var identity int64
	if err := q.QueryRowContext(ctx, "SELECT identity_id FROM "+groups+" WHERE view=? AND group_key=?", view, key).Scan(&identity); err != nil {
		if err == sql.ErrNoRows {
			return ColumnGroup{}, false, nil
		}
		return ColumnGroup{}, false, err
	}
	names := groupColumnNames + "," + stateColumnNames + "," + metricsColumnNames
	g, err := scanColumnGroup(q.QueryRowContext(ctx, "SELECT "+prefixedColumns("g", names)+","+prefixedColumns("i", identityColumnNames)+" FROM "+groups+" g JOIN "+identities+" i ON i.id=g.identity_id WHERE g.view=? AND g.group_key=? AND i.id=?", view, key, identity))
	if err == sql.ErrNoRows {
		return g, false, nil
	}
	if err != nil {
		return g, false, err
	}
	if g.Flow != nil {
		err = columnAnswers(ctx, columnLogRewrite{q: q, answers: answers}, 2, view+"\x00"+key, &g.Flow.Fact)
	}
	return g, err == nil, err
}

func scanColumnGroup(row columnScanner) (ColumnGroup, error) {
	var g ColumnGroup
	var f columnFlowScan
	var m columnMetricsScan
	var p ColumnProjectedFlow
	var first, last, until string
	var ns, count, related, duration, id, up, down int64
	var flow, metrics bool
	dest := []any{&g.View, &g.Key, &g.EventID, &first, &last, &until, &ns, &count, &related, &g.Level, &g.Title, &g.ProblemID, &g.Category, &g.Kind, &g.CategoryName, &g.CategorySymbol, &g.SystemCategory, &g.ApplicationID, &g.Application, &g.Domain, &g.IP, &g.RouteID, &g.RouteName, &g.Direction, &g.ConnectionKey, &g.ConnectionID, &g.Session, &g.ConnectionRoute, &g.Target, &g.Stage, &g.Code, &duration, &g.HasConnection, &flow, &metrics, &id, &p.PrimaryDomain, &p.SourceKind, &p.SourceLabel, &p.SourceEvidence, &up, &down}
	var latency int64
	dest = append(dest, &g.RulePackage, &g.LatencyKnown, &latency)
	var firstNS, untilNS int64
	dest = append(dest, &firstNS, &untilNS)
	dest = append(dest, f.stateDest()...)
	dest = append(dest, m.dest()...)
	dest = append(dest, f.identityDest()...)
	err := row.Scan(dest...)

	if err != nil {
		return g, err
	}
	g.Count, g.Related, g.ConnectionDuration = uint64(count), uint64(related), uint64(duration)
	g.Latency = uint64(latency)
	if g.FirstAt, err = parseColumnTime(first); err != nil {
		return g, err
	}
	if g.LastAt, err = parseColumnTime(last); err != nil {
		return g, err
	}
	if g.Until, err = parseColumnTime(until); err != nil {
		return g, err
	}
	if p.Fact, err = f.finish(); err != nil {
		return g, err
	}
	p.Upload, p.Download = uint64(up), uint64(down)
	if flow {
		g.Flow = &p
	}
	met, err := m.finish()
	if err != nil {
		return g, err
	}
	if metrics {
		g.Metrics = &met
	}
	return g, nil
}

// Members reads one connection's integer-backed object directory.
func (c *Columns) Members(ctx context.Context, key string) ([]ColumnMember, error) {
	rows, err := c.r.QueryContext(ctx, `SELECT o.dimension,o.object_key,o.name,o.kind,m.route,m.route_name,m.direction,m.search,m.rule_package,m.started_at,m.latency_known,m.latency
 FROM col_members m JOIN col_objects o ON o.id=m.object_id WHERE flow_key=? ORDER BY o.dimension,o.object_key,m.route,m.direction`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ColumnMember
	for rows.Next() {
		var m ColumnMember
		var started string
		var lat int64
		if err = rows.Scan(&m.Dimension, &m.Object, &m.Name, &m.Kind, &m.Route, &m.RouteName, &m.Direction, &m.Search, &m.RulePackage, &started, &m.LatencyKnown, &lat); err != nil {
			return nil, err
		}
		if m.StartedAt, err = parseColumnTime(started); err != nil {
			return nil, err
		}
		m.Latency = uint64(lat)
		out = append(out, m)
	}
	return out, rows.Err()
}
