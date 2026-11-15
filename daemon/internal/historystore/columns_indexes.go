package historystore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"pax-connect/daemon/internal/config"
)

const columnConnectionIndexes = `CREATE INDEX IF NOT EXISTS col_connections_started ON col_connections(started_ns DESC,flow_key);
CREATE INDEX IF NOT EXISTS col_connections_activity ON col_connections(last_activity_ms);
CREATE INDEX IF NOT EXISTS col_connections_identity ON col_connections(identity_id);`

func columnSummaryActivity(s config.LogConnectionSummary) int64 {
	at := s.Metrics.AsOf
	if s.LastLogAt.After(at) {
		at = s.LastLogAt
	}
	if at.IsZero() {
		at = s.Fact.StartedAt
	}
	return at.UnixMilli()
}

func initializeColumnFacts(ctx context.Context, s *store) error {
	if _, err := s.w.ExecContext(ctx, columnsDDL); err != nil {
		return err
	}
	var version string
	if err := s.w.QueryRowContext(ctx, "SELECT value FROM col_meta WHERE key='fact_version'").Scan(&version); err != nil {
		return err
	}
	if version != "1" && version != "2" {
		return fmt.Errorf("%w: column facts %s", ErrSchemaVersion, version)
	}
	if version == "1" {
		for _, name := range []string{"started_ns", "last_activity_ms"} {
			var exists int
			if err := s.w.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('col_connections') WHERE name=?", name).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				if _, err := s.w.ExecContext(ctx, "ALTER TABLE col_connections ADD COLUMN "+name+" INTEGER NOT NULL DEFAULT 0"); err != nil {
					return err
				}
			}
		}
		if err := backfillColumnConnectionTimes(ctx, s); err != nil {
			return err
		}
	}
	if _, err := s.w.ExecContext(ctx, columnConnectionIndexes); err != nil {
		return err
	}
	_, err := s.w.ExecContext(ctx, "UPDATE col_meta SET value='2' WHERE key='fact_version'")
	return err
}

func backfillColumnConnectionTimes(ctx context.Context, s *store) error {
	after := ""
	for {
		rows, err := s.w.QueryContext(ctx, "SELECT flow_key,started_at,last_log_at,as_of FROM col_connections WHERE flow_key>? ORDER BY flow_key LIMIT 128", after)
		if err != nil {
			return err
		}
		type value struct {
			key               string
			started, activity int64
		}
		var batch []value
		for rows.Next() {
			var key, started, last, asof string
			if err = rows.Scan(&key, &started, &last, &asof); err != nil {
				break
			}
			var st, la, as time.Time
			if st, err = parseColumnTime(started); err != nil {
				break
			}
			if la, err = parseColumnTime(last); err != nil {
				break
			}
			if as, err = parseColumnTime(asof); err != nil {
				break
			}
			sm := config.LogConnectionSummary{Fact: config.FlowFact{StartedAt: st}, LastLogAt: la, Metrics: config.ConnectionMetrics{AsOf: as}}
			batch = append(batch, value{key, st.UnixNano(), columnSummaryActivity(sm)})
		}
		re := rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if re != nil {
			return re
		}
		if len(batch) == 0 {
			return nil
		}
		if err = s.update(ctx, func(tx *sql.Tx) error {
			for _, v := range batch {
				if _, e := tx.ExecContext(ctx, "UPDATE col_connections SET started_ns=?,last_activity_ms=? WHERE flow_key=?", v.started, v.activity, v.key); e != nil {
					return e
				}
			}
			return nil
		}); err != nil {
			return err
		}
		after = batch[len(batch)-1].key
	}
}
