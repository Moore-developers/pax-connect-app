package historystore

import (
	"context"
	"database/sql"
	"fmt"
)

const columnLogOptionsDDL = `CREATE TABLE IF NOT EXISTS col_option_values(id INTEGER PRIMARY KEY,kind INTEGER NOT NULL,value TEXT NOT NULL,UNIQUE(kind,value));
CREATE TABLE IF NOT EXISTS col_group_options(group_key TEXT PRIMARY KEY,first_ns INTEGER NOT NULL,last_ns INTEGER NOT NULL,until_ns INTEGER NOT NULL,event_count INTEGER NOT NULL,flow_present INTEGER NOT NULL,app_id INTEGER NOT NULL,route_id INTEGER NOT NULL,rule_id INTEGER NOT NULL) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS col_group_options_window ON col_group_options(last_ns DESC,group_key DESC,first_ns,until_ns,event_count,flow_present,app_id,route_id,rule_id);`

func (t *ColumnsTx) putOptionValue(ctx context.Context, kind int, value string) (int64, error) {
	key := fmt.Sprint(kind) + "\x00" + value
	if id, ok := t.optionIDs[key]; ok {
		return id, nil
	}
	stmt, err := t.statement(ctx, "SELECT id FROM col_option_values WHERE kind=? AND value=?")
	if err != nil {
		return 0, err
	}
	var id int64
	err = stmt.QueryRowContext(ctx, kind, value).Scan(&id)
	if err == sql.ErrNoRows {
		stmt, err = t.statement(ctx, "INSERT INTO col_option_values(kind,value) VALUES(?,?) RETURNING id")
		if err != nil {
			return 0, err
		}
		err = stmt.QueryRowContext(ctx, kind, value).Scan(&id)
	}
	if err == nil {
		if t.optionIDs == nil {
			t.optionIDs = map[string]int64{}
		}
		if len(t.optionIDs) < columnBatchDictionaryLimit {
			t.optionIDs[key] = id
		}
	}
	return id, err
}
func (t *ColumnsTx) putGroupOptions(ctx context.Context, g ColumnGroup) error {
	if g.View != "activity" {
		return nil
	}
	app, err := t.putOptionValue(ctx, 0, g.Application)
	if err != nil {
		return err
	}
	route, err := t.putOptionValue(ctx, 1, g.RouteName)
	if err != nil {
		return err
	}
	rule, err := t.putOptionValue(ctx, 2, g.RulePackage)
	if err != nil {
		return err
	}
	return t.exec(ctx, `INSERT INTO col_group_options VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(group_key) DO UPDATE SET first_ns=excluded.first_ns,last_ns=excluded.last_ns,until_ns=excluded.until_ns,event_count=excluded.event_count,flow_present=excluded.flow_present,app_id=excluded.app_id,route_id=excluded.route_id,rule_id=excluded.rule_id`, g.Key, g.FirstAt.UnixNano(), g.LastAt.UnixNano(), g.Until.UnixNano(), int64(g.Count), g.Flow != nil, app, route, rule)
}

func (c *Columns) upgradeIntegerOptions(ctx context.Context) error {
	for {
		done := false
		err := c.Update(ctx, func(t *ColumnsTx) error {
			var after string
			if err := t.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT value FROM col_meta WHERE key='integer_option_upgrade_key'),'')").Scan(&after); err != nil {
				return err
			}
			rows, err := t.tx.QueryContext(ctx, "SELECT group_key,first_at,last_at,until_at,event_count,flow_present,app_name,route_name,rule_package FROM col_groups WHERE view='activity' AND group_key>? ORDER BY group_key LIMIT 128", after)
			if err != nil {
				return err
			}
			var batch []ColumnGroup
			for rows.Next() {
				var g ColumnGroup
				var first, last, until string
				var count int64
				var flow bool
				g.View = "activity"
				if err = rows.Scan(&g.Key, &first, &last, &until, &count, &flow, &g.Application, &g.RouteName, &g.RulePackage); err != nil {
					_ = rows.Close()
					return err
				}
				g.Count = uint64(count)
				if g.FirstAt, err = parseColumnTime(first); err != nil {
					_ = rows.Close()
					return err
				}
				if g.LastAt, err = parseColumnTime(last); err != nil {
					_ = rows.Close()
					return err
				}
				if g.Until, err = parseColumnTime(until); err != nil {
					_ = rows.Close()
					return err
				}
				if flow {
					g.Flow = &ColumnProjectedFlow{}
				}
				batch = append(batch, g)
			}
			err = rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
			if len(batch) == 0 {
				done = true
				_, err = t.tx.ExecContext(ctx, "UPDATE col_meta SET value='5' WHERE key='projection_version';DELETE FROM col_meta WHERE key='integer_option_upgrade_key';")
				return err
			}
			for _, g := range batch {
				if err = t.putGroupOptions(ctx, g); err != nil {
					return err
				}
			}
			_, err = t.tx.ExecContext(ctx, "INSERT INTO col_meta VALUES('integer_option_upgrade_key',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", batch[len(batch)-1].Key)
			return err
		})
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func (s *ColumnLogSelection) integerOptions(ctx context.Context) (apps, routes, rules []string, err error) {
	complete := "SELECT g.app_id,g.route_id,g.rule_id FROM snap_col_group_options g WHERE g.first_ns>? AND g.first_ns>=? AND g.last_ns<=? AND g.until_ns<=? AND g.flow_present=1 AND NOT EXISTS(SELECT 1 FROM log_delta d WHERE d.group_key=g.group_key)"
	args := []any{columnQueryTime(s.Window.From), columnQueryTime(s.Window.Cutoff), columnQueryTime(s.Window.Until), columnQueryTime(s.Window.Until)}
	if s.Window.Route != "" || s.Window.ConnectionKey != "" {
		complete = "SELECT g.app_id,g.route_id,g.rule_id FROM snap_col_group_options g JOIN log_scope t ON t.k=g.group_key WHERE t.n=g.event_count AND g.until_ns<=? AND g.flow_present=1"
		args = []any{columnQueryTime(s.Window.Until)}
	}
	query := `WITH selected AS MATERIALIZED(` + complete + ` GROUP BY g.app_id,g.route_id,g.rule_id), wanted AS(SELECT app_id AS id FROM selected UNION SELECT route_id FROM selected UNION SELECT rule_id FROM selected), values_scope AS(SELECT o.kind,o.value FROM wanted w JOIN snap_col_option_values o ON o.id=w.id UNION SELECT 0,app_name FROM log_delta WHERE flow_present=1 UNION SELECT 1,route_name FROM log_delta WHERE flow_present=1 UNION SELECT 2,rule_package FROM log_delta WHERE flow_present=1) SELECT kind,value FROM values_scope WHERE value<>'' GROUP BY kind,value ORDER BY kind,value`
	rows, e := s.q.QueryContext(ctx, query, args...)
	if e != nil {
		return nil, nil, nil, e
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind int
		var value string
		if e = rows.Scan(&kind, &value); e != nil {
			return nil, nil, nil, e
		}
		switch kind {
		case 0:
			if len(apps) < 500 {
				apps = append(apps, value)
			}
		case 1:
			if len(routes) < 500 {
				routes = append(routes, value)
			}
		case 2:
			if len(rules) < 500 {
				rules = append(rules, value)
			}
		}
	}
	return apps, routes, rules, rows.Err()
}
