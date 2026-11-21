package historystore

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// Prune removes bounded facts and their query projections atomically. The
// projector refreshes only groups touched by removed events or summaries.
func (l *Logs) Prune(ctx context.Context, before time.Time, limits Limits) (Pruned, error) {
	if err := limits.validate(); err != nil {
		return Pruned{}, err
	}
	var result Pruned
	err := l.Update(ctx, func(t *LogsTx) error {
		deadline := time.Now().Add(limits.Duration)
		cutoff := before.UnixMilli()
		for _, kind := range []string{"connections", "events", "diagnostics"} {
			for result.Deleted < limits.Rows && time.Now().Before(deadline) {
				n, err := t.pruneColumnChunk(ctx, kind, cutoff, min(deleteChunk, limits.Rows-result.Deleted))
				if err != nil {
					return err
				}
				result.Deleted += n
				if n == 0 {
					break
				}
			}
		}
		if result.Deleted > 0 {
			if _, err := t.tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=CAST(max(CAST(value AS INTEGER),CAST(excluded.value AS INTEGER)) AS TEXT)`, MetaLogRemovalBefore, strconv.FormatInt(cutoff, 10)); err != nil {
				return err
			}

		}
		for _, q := range []string{"SELECT EXISTS(SELECT 1 FROM col_connections WHERE last_activity_ms<?)", "SELECT EXISTS(SELECT 1 FROM col_events WHERE at_ns<?)", "SELECT EXISTS(SELECT 1 FROM diagnostics WHERE at_ms<?)"} {
			edge := cutoff
			if q == "SELECT EXISTS(SELECT 1 FROM col_events WHERE at_ns<?)" {
				edge = columnMillisNS(cutoff)
			}
			var more bool
			if err := t.tx.QueryRowContext(ctx, q, edge).Scan(&more); err != nil {
				return err
			}
			result.More = result.More || more
		}
		return nil
	})
	return result, err
}

func (t *LogsTx) pruneColumnChunk(ctx context.Context, kind string, cutoff int64, limit int) (int, error) {
	switch kind {
	case "connections":
		rows, err := t.tx.QueryContext(ctx, "SELECT flow_key FROM col_connections WHERE last_activity_ms<? ORDER BY last_activity_ms LIMIT ?", cutoff, limit)
		if err != nil {
			return 0, err
		}
		keys, err := scanColumnKeys(rows)
		if err != nil {
			return 0, err
		}
		for _, key := range keys {
			delete(t.summaries, key)
			if t.batch != nil {
				t.batch.RemovedConnections = append(t.batch.RemovedConnections, key)
			}
			if _, err = t.tx.ExecContext(ctx, "DELETE FROM col_connections WHERE flow_key=?", key); err != nil {
				return 0, err
			}
			if _, err = t.tx.ExecContext(ctx, "DELETE FROM col_traffic WHERE flow_key=?", key); err != nil {
				return 0, err
			}
			if _, err = t.tx.ExecContext(ctx, "DELETE FROM col_members WHERE flow_key=?", key); err != nil {
				return 0, err
			}
			if _, err = t.tx.ExecContext(ctx, "DELETE FROM col_answers WHERE owner_kind=1 AND owner_key=?", key); err != nil {
				return 0, err
			}
		}
		return len(keys), nil
	case "events":
		rows, err := t.tx.QueryContext(ctx, "SELECT event_id FROM col_events WHERE at_ns<? ORDER BY at_ns LIMIT ?", columnMillisNS(cutoff), limit)
		if err != nil {
			return 0, err
		}
		keys, err := scanColumnKeys(rows)
		if err != nil {
			return 0, err
		}
		for _, key := range keys {
			var index ColumnEventIndex
			if err = t.tx.QueryRowContext(ctx, "SELECT activity,access,problem,operation FROM col_event_index WHERE event_id=?", key).Scan(&index.Activity, &index.Access, &index.Problem, &index.Operation); err != nil && err != sql.ErrNoRows {
				return 0, err
			}
			// The removed event is supplied to production projection solely to identify
			// touched group keys. Its body is never retained after this transaction.
			e, eerr := scanColumnEvent(t.tx.QueryRowContext(ctx, columnEventSelect()+" WHERE e.event_id=?", key))
			if eerr != nil {
				return 0, eerr
			}
			e.Raw = nil
			if eerr = columnEventChildren(ctx, t.tx, &e); eerr != nil {
				return 0, eerr
			}
			if t.batch != nil {
				t.batch.RemovedEvents = append(t.batch.RemovedEvents, e)
			}
			for _, table := range []string{"col_events", "col_event_index", "col_raw", "col_texts", "col_sections", "col_fields"} {
				if _, err = t.tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE event_id=?", key); err != nil {
					return 0, err
				}
			}
			t.columns.rememberEvent(key, false)
			if _, err = t.tx.ExecContext(ctx, "DELETE FROM col_answers WHERE owner_kind=0 AND owner_key=?", key); err != nil {
				return 0, err
			}
			// Empty groups disappear immediately. Nonempty touched groups are refreshed
			// by the same configured production projector before this commit.
			for _, g := range [][3]string{{"activity", "activity", index.Activity}, {"access", "access", index.Access}, {"problems", "problem", index.Problem}, {"operations", "operation", index.Operation}} {
				if g[2] == "" {
					continue
				}
				q := `DELETE FROM col_groups WHERE view=? AND group_key=? AND NOT EXISTS(SELECT 1 FROM col_event_index WHERE ` + g[1] + `=? AND ` + g[1] + `<>'')`
				if _, err = t.tx.ExecContext(ctx, q, g[0], g[2], g[2]); err != nil {
					return 0, err
				}
			}
		}
		return len(keys), nil
	case "diagnostics":
		res, err := t.tx.ExecContext(ctx, "DELETE FROM diagnostics WHERE rowid IN(SELECT rowid FROM diagnostics WHERE at_ms<? LIMIT ?)", cutoff, limit)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		return int(n), err
	}
	return 0, fmt.Errorf("historystore: unknown prune target %s", kind)
}

func scanColumnKeys(rows *sql.Rows) ([]string, error) {
	var keys []string
	var err error
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			break
		}
		keys = append(keys, key)
	}
	re := rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	return keys, re
}

func (t *ColumnsTx) pruneColumnOrphans(ctx context.Context, limit int) error {
	for _, q := range []string{
		`DELETE FROM col_group_options WHERE group_key IN(SELECT group_key FROM col_group_options o WHERE NOT EXISTS(SELECT 1 FROM col_groups g WHERE g.view='activity' AND g.group_key=o.group_key) LIMIT ?)`,
		`DELETE FROM col_option_values WHERE id IN(SELECT id FROM col_option_values v WHERE id NOT IN(SELECT app_id FROM col_group_options UNION SELECT route_id FROM col_group_options UNION SELECT rule_id FROM col_group_options) LIMIT ?)`,
		`DELETE FROM col_traffic WHERE flow_key IN(SELECT flow_key FROM col_traffic t WHERE NOT EXISTS(SELECT 1 FROM col_connections c WHERE c.flow_key=t.flow_key) LIMIT ?)`,
		// The key is view + NUL + group key. SQLite TEXT substr stops at NUL,
		// so split the BLOB and cast back before probing the group primary key.
		`DELETE FROM col_answers WHERE owner_kind=2 AND owner_key IN(SELECT a.owner_key FROM col_answers a WHERE a.owner_kind=2 AND NOT EXISTS(SELECT 1 FROM col_groups g WHERE g.view=CAST(substr(CAST(a.owner_key AS BLOB),1,instr(CAST(a.owner_key AS BLOB),x'00')-1) AS TEXT) AND g.group_key=CAST(substr(CAST(a.owner_key AS BLOB),instr(CAST(a.owner_key AS BLOB),x'00')+1) AS TEXT)) LIMIT ?)`,
		`DELETE FROM col_objects WHERE id IN(SELECT id FROM col_objects o WHERE NOT EXISTS(SELECT 1 FROM col_members m WHERE m.object_id=o.id) LIMIT ?)`,
		`DELETE FROM col_identities WHERE id IN(SELECT id FROM col_identities i WHERE NOT EXISTS(SELECT 1 FROM col_events e WHERE e.identity_id=i.id) AND NOT EXISTS(SELECT 1 FROM col_connections c WHERE c.identity_id=i.id) AND NOT EXISTS(SELECT 1 FROM col_groups g WHERE g.identity_id=i.id) LIMIT ?)`,
	} {
		if _, err := t.tx.ExecContext(ctx, q, limit); err != nil {
			return err
		}
	}
	return nil
}
