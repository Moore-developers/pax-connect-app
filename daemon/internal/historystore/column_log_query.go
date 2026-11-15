package historystore

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"time"

	"pax-connect/daemon/internal/config"
)

// ColumnLogWindow fixes event membership; all further filtering is SQL over
// its complete group projections. The callback only projects boundary groups.
type ColumnLogWindow struct {
	View, Route, Collection string
	ConnectionKey           string
	From, Until, Cutoff     time.Time
	Apps                    []config.RoutingApplication
	Classify                func(string, string) (config.RoutingApplication, bool)
	DirectoryFilter         *ColumnLogFilter
}

// Query timestamps can span protobuf's year 1..9999 even though observed logs
// are contemporary. Saturate the SQL nanosecond boundary instead of wrapping.
func columnQueryTime(t time.Time) int64 {
	if t.Before(time.Unix(0, math.MinInt64)) {
		return math.MinInt64
	}
	if t.After(time.Unix(0, math.MaxInt64)) {
		return math.MaxInt64
	}
	return t.UnixNano()
}
func columnCutoffLower(t time.Time) int64 {
	n := columnQueryTime(t)
	if n > math.MinInt64 {
		return n - 1
	}
	return n
}

// ColumnLogSelection exists only during one snapshot read, with TEMP state on
// that connection. Its facts/rows must not be retained after WithRead returns.
type ColumnLogSelection struct {
	q                            ColumnQuery
	Window                       ColumnLogWindow
	completeSQL                  string
	deferredDomainClassification bool
}

// ColumnLogStream exposes the same bounded event reducer input without
// constructing a whole-window catalog, for touched-group incremental updates.
func ColumnLogStream(q ColumnQuery, w ColumnLogWindow) *ColumnLogSelection {
	return &ColumnLogSelection{q: q, Window: w}
}

type columnLogRewrite struct {
	q       columnQueryer
	answers string
}

func (r columnLogRewrite) sql(s string) string {
	s = strings.ReplaceAll(s, "col_", "snap_col_")
	if r.answers != "" {
		s = strings.ReplaceAll(s, "snap_col_answers", r.answers)
	}
	return s
}
func (r columnLogRewrite) QueryContext(ctx context.Context, s string, a ...any) (*sql.Rows, error) {
	return r.q.QueryContext(ctx, r.sql(s), a...)
}
func (r columnLogRewrite) QueryRowContext(ctx context.Context, s string, a ...any) *sql.Row {
	return r.q.QueryRowContext(ctx, r.sql(s), a...)
}

// PrepareColumnLogs installs an indexed temporary delta only for groups cut by
// time/route boundaries. Complete groups continue to use immutable saved rows.
func PrepareColumnLogs(ctx context.Context, q ColumnQuery, w ColumnLogWindow, project func(context.Context, *ColumnLogSelection, string) (ColumnGroup, error)) (*ColumnLogSelection, error) {
	s := &ColumnLogSelection{q: q, Window: w}
	_, err := q.ExecContext(ctx, `DROP VIEW IF EXISTS temp.log_groups;DROP VIEW IF EXISTS temp.log_identities;DROP VIEW IF EXISTS temp.log_answers;DROP VIEW IF EXISTS temp.log_catalog;
DROP TABLE IF EXISTS temp.log_scope;DROP TABLE IF EXISTS temp.log_delta;DROP TABLE IF EXISTS temp.log_identity_delta;DROP TABLE IF EXISTS temp.log_answer_delta;DROP TABLE IF EXISTS temp.log_apps;
DROP TABLE IF EXISTS temp.log_app_matches;DROP TABLE IF EXISTS temp.log_identity_matches;
CREATE TEMP TABLE log_delta AS SELECT * FROM snap_col_groups WHERE 0;
CREATE UNIQUE INDEX temp.log_delta_key ON log_delta(view,group_key);
CREATE TEMP TABLE log_identity_delta AS SELECT * FROM snap_col_identities WHERE 0;
CREATE UNIQUE INDEX temp.log_identity_key ON log_identity_delta(id);
CREATE TEMP TABLE log_answer_delta AS SELECT * FROM snap_col_answers WHERE 0;
`)
	if err != nil {
		return nil, err
	}

	if _, err = q.ExecContext(ctx, "CREATE TEMP TABLE log_app_matches(app_id TEXT,process TEXT,id TEXT,name TEXT,PRIMARY KEY(app_id,process)) WITHOUT ROWID;CREATE TEMP TABLE log_identity_matches(id INTEGER PRIMARY KEY)"); err != nil {
		return nil, err
	}
	if w.View != "" && (w.Route != "" || w.ConnectionKey != "") {
		col, er := columnViewKey(w.View)
		if er != nil {
			return nil, er
		}
		query := "CREATE TEMP TABLE log_scope AS SELECT " + col + " AS k,count(*) AS n FROM snap_col_event_index WHERE " + col + "<>'' AND at_ns>? AND at_ns>=? AND at_ns<=?"
		args := []any{columnQueryTime(w.From), columnQueryTime(w.Cutoff), columnQueryTime(w.Until)}
		if w.Route != "" {
			query += " AND route=?"
			args = append(args, w.Route)
		}
		if w.ConnectionKey != "" {
			query += " AND flow_key=?"
			args = append(args, w.ConnectionKey)
		}
		if _, er = q.ExecContext(ctx, query+" GROUP BY "+col, args...); er != nil {
			return nil, er
		}
		if _, er = q.ExecContext(ctx, "CREATE UNIQUE INDEX temp.log_scope_key ON log_scope(k)"); er != nil {
			return nil, er
		}
	}
	if w.View != "" {
		after := ""
		afterNS := int64(math.MaxInt64)
		for {

			query := `SELECT g.group_key,g.last_ns FROM snap_col_groups g WHERE g.view=? AND g.last_ns>? AND g.first_ns<=? AND (g.first_ns<=? OR g.last_ns>? OR (?='activity' AND g.until_ns>?)) AND (g.last_ns<? OR (g.last_ns=? AND g.group_key<?)) ORDER BY g.last_ns DESC,g.group_key DESC LIMIT 128`
			args := []any{w.View, columnQueryTime(w.From), columnQueryTime(w.Until), max(columnQueryTime(w.From), columnCutoffLower(w.Cutoff)), columnQueryTime(w.Until), w.View, columnQueryTime(w.Until), afterNS, afterNS, after}
			if w.Route != "" || w.ConnectionKey != "" {
				query = "SELECT g.group_key,g.last_ns FROM snap_col_groups g JOIN log_scope t ON t.k=g.group_key WHERE g.view=? AND (t.n<>g.event_count OR (g.view='activity' AND g.until_ns>?)) AND (g.last_ns<? OR (g.last_ns=? AND g.group_key<?)) ORDER BY g.last_ns DESC,g.group_key DESC LIMIT 128"
				args = []any{w.View, columnQueryTime(w.Until), afterNS, afterNS, after}
			}

			rows, er := q.QueryContext(ctx, query, args...)
			if er != nil {
				return nil, er
			}
			var keys []string
			for rows.Next() {
				var key string
				if er = rows.Scan(&key, &afterNS); er != nil {
					_ = rows.Close()
					return nil, er
				}
				keys = append(keys, key)
			}
			er = rows.Err()
			_ = rows.Close()
			if er != nil {
				return nil, er
			}
			if len(keys) == 0 {
				break
			}
			for _, key := range keys {
				g, er := project(ctx, s, key)
				if er != nil {
					return nil, er
				}
				if g.Count > 0 {
					if er = s.putDelta(ctx, g); er != nil {
						return nil, er
					}
				}
			}
			after = keys[len(keys)-1]
		}
	}
	base := `SELECT g.* FROM snap_col_groups g WHERE g.view=? AND g.first_ns>? AND g.first_ns>=? AND g.last_ns<=? AND (g.view<>'activity' OR g.until_ns<=?) AND NOT EXISTS(SELECT 1 FROM log_delta d WHERE d.view=g.view AND d.group_key=g.group_key)`
	if w.Route != "" || w.ConnectionKey != "" {
		base = bindLogSQL("SELECT g.* FROM snap_col_groups g JOIN log_scope t ON t.k=g.group_key WHERE g.view=? AND t.n=g.event_count AND (g.view<>'activity' OR g.until_ns<=?)", []any{w.View, columnQueryTime(w.Until)})
	} else {
		base = bindLogSQL(base, []any{w.View, columnQueryTime(w.From), columnQueryTime(w.Cutoff), columnQueryTime(w.Until), columnQueryTime(w.Until)})
	}
	s.completeSQL = base
	_, err = q.ExecContext(ctx, `CREATE TEMP VIEW log_groups AS `+base+` UNION ALL SELECT * FROM log_delta;
CREATE TEMP VIEW log_identities AS SELECT * FROM snap_col_identities UNION ALL SELECT * FROM log_identity_delta;
CREATE TEMP VIEW log_answers AS SELECT a.* FROM snap_col_answers a WHERE NOT EXISTS(SELECT 1 FROM log_delta d WHERE a.owner_kind=2 AND a.owner_key=d.view||char(0)||d.group_key) UNION ALL SELECT * FROM log_answer_delta;`)
	if err != nil {
		return nil, err
	}
	if w.View == "activity" && w.Classify != nil {
		if w.DirectoryFilter != nil {
			_, s.deferredDomainClassification = s.basicDomainObjectSource(*w.DirectoryFilter)
		}
		if !s.deferredDomainClassification {
			if err = s.classifyInputs(ctx); err != nil {
				return nil, err
			}
		}
	}
	if err = s.catalog(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ColumnLogSelection) putDelta(ctx context.Context, g ColumnGroup) error {
	var id int64
	if err := s.q.QueryRowContext(ctx, "SELECT -count(*)-1 FROM log_identity_delta").Scan(&id); err != nil {
		return err
	}
	f := config.FlowFact{}
	if g.Flow != nil {
		f = g.Flow.Fact
	}
	v := append([]any{id, identityDigest(identityValues(f))}, identityValues(f)...)
	if _, err := s.q.ExecContext(ctx, columnInsert("log_identity_delta", "id,digest,"+identityColumnNames, ""), v...); err != nil {
		return err
	}
	names := groupColumnNames + "," + stateColumnNames + "," + metricsColumnNames
	if _, err := s.q.ExecContext(ctx, columnInsert("log_delta", names, ""), columnGroupValues(g, id)...); err != nil {
		return err
	}
	if f.DNS != nil {
		for i, a := range f.DNS.Answers {
			if _, err := s.q.ExecContext(ctx, "INSERT INTO log_answer_delta VALUES(?,?,?,?)", 2, g.View+"\x00"+g.Key, i, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ColumnLogSelection) completeProjection(columns string) string {
	return strings.Replace(s.completeSQL, "SELECT g.*", "SELECT "+columns, 1)
}

// Group reconstructs one current page group; it never scans other groups.
func (s *ColumnLogSelection) Group(ctx context.Context, key string) (ColumnGroup, bool, error) {
	return readColumnGroup(ctx, s.q, "log_groups", "log_identities", "log_answers", s.Window.View, key)
}

// Event reads one immutable event from the selected commit version.
func (s *ColumnLogSelection) Event(ctx context.Context, id string, raw bool) (config.HistoryEvent, bool, error) {
	r := columnLogRewrite{q: s.q}
	var identity int64
	if err := s.q.QueryRowContext(ctx, "SELECT identity_id FROM snap_col_events WHERE event_id=?", id).Scan(&identity); err != nil {
		if err == sql.ErrNoRows {
			return config.HistoryEvent{}, false, nil
		}
		return config.HistoryEvent{}, false, err
	}
	e, err := scanColumnEvent(r.QueryRowContext(ctx, columnEventSelect()+" WHERE e.event_id=? AND i.id=?", id, identity))
	if err == sql.ErrNoRows {
		return e, false, nil
	}
	if err != nil {
		return e, false, err
	}
	if !raw {
		e.Raw = nil
	}
	err = columnEventChildren(ctx, r, &e)
	return e, err == nil, err
}

// Connection retrieves the summary fixed by this commit version.
func (s *ColumnLogSelection) Connection(ctx context.Context, key string) (config.LogConnectionSummary, bool, error) {
	r := columnLogRewrite{q: s.q}
	var identity int64
	if err := s.q.QueryRowContext(ctx, "SELECT identity_id FROM snap_col_connections WHERE flow_key=?", key).Scan(&identity); err != nil {
		if err == sql.ErrNoRows {
			return config.LogConnectionSummary{}, false, nil
		}
		return config.LogConnectionSummary{}, false, err
	}
	v, err := scanColumnConnection(r.QueryRowContext(ctx, columnConnectionSelect()+" WHERE c.flow_key=? AND i.id=?", key, identity))
	if err == sql.ErrNoRows {
		return v, false, nil
	}
	if err == nil {
		err = columnAnswers(ctx, r, 1, key, &v.Fact)
	}
	return v, err == nil, err
}

// EachGroupEvent streams in production projection order, or timeline order.
// Pages are bounded and cursors close before ordered child evidence is loaded.
func (s *ColumnLogSelection) EachGroupEvent(ctx context.Context, key string, order int, fn func(config.HistoryEvent) error) error {
	where := " AND x.at_ns>? AND x.at_ns>=? AND x.at_ns<=?"
	args := []any{columnQueryTime(s.Window.From), columnQueryTime(s.Window.Cutoff), columnQueryTime(s.Window.Until)}
	if s.Window.Route != "" {
		where += " AND x.route=?"
		args = append(args, s.Window.Route)
	}
	if s.Window.ConnectionKey != "" {
		where += " AND x.flow_key=?"
		args = append(args, s.Window.ConnectionKey)
	}
	return columnGroupEventEach(ctx, columnLogRewrite{q: s.q}, s.Window.View, key, where, args, order, fn)
}

// Related counts the exact distinct connection set in SQLite, not a Go map.
func (s *ColumnLogSelection) Related(ctx context.Context, key string) (uint64, error) {
	col, err := columnViewKey(s.Window.View)
	if err != nil {
		return 0, err
	}
	where := "x." + col + "<>'' AND x." + col + "=? AND x.at_ns>? AND x.at_ns>=? AND x.at_ns<=?"
	a := []any{key, columnQueryTime(s.Window.From), columnQueryTime(s.Window.Cutoff), columnQueryTime(s.Window.Until)}
	if s.Window.Route != "" {
		where += " AND x.route=?"
		a = append(a, s.Window.Route)
	}
	if s.Window.ConnectionKey != "" {
		where += " AND x.flow_key=?"
		a = append(a, s.Window.ConnectionKey)
	}
	var n uint64
	err = s.q.QueryRowContext(ctx, "SELECT count(DISTINCT x.flow_key) FROM snap_col_event_index x JOIN snap_col_events e ON e.event_id=x.event_id WHERE "+where+" AND e.flow_present=1", a...).Scan(&n)
	return n, err
}
