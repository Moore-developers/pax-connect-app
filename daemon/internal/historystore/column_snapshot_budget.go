package historystore

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Each charge includes row storage, its lookup index, dynamic values and
// allocator/page slack. It is deliberately conservative; the page counter is
// an additional committed-size check, never the first payload bound.
func snapshotRowCharge(t snapshotTable, alias string) string {
	values := []string{"512", "4*length(" + tableKey(t, alias) + ")"}
	for _, col := range strings.Split(t.columns, ",") {
		v := alias + "." + col
		values = append(values, "2*coalesce(length(CAST("+v+" AS BLOB)),0)")
	}
	return "(" + strings.Join(values, "+") + ")"
}

func snapshotReserve(cost, needed string) string {
	return "UPDATE v_control SET overflow=1,active=0 WHERE active=1 AND (" + needed + ") AND charge+" + cost + ">budget;"
}

func snapshotCaptureSQL(t snapshotTable, oldKey, born, revision string) string {
	needed := "EXISTS(SELECT 1 FROM v_refs WHERE revision>=" + born + " AND revision<" + revision + ")"
	cost := snapshotRowCharge(t, "OLD")
	return snapshotReserve(cost, needed) +
		"INSERT INTO v_" + t.name + " SELECT " + prefixSQL("OLD", t.columns) + "," + oldKey + "," + born + "," + revision + "," + cost + " WHERE (SELECT active FROM v_control)=1 AND " + needed + ";" +
		"UPDATE v_control SET charge=charge+" + cost + " WHERE active=1 AND " + needed + ";"
}

func snapshotCurrentSQL(t snapshotTable, key, revision string) string {
	needed := "NOT EXISTS(SELECT 1 FROM v_current WHERE table_name='" + t.name + "' AND row_key=" + key + ")"
	cost := "(512+4*length(" + key + "))"
	return snapshotReserve(cost, needed) +
		"UPDATE v_control SET charge=charge+" + cost + " WHERE active=1 AND " + needed + ";" +
		"INSERT INTO v_current SELECT '" + t.name + "'," + key + "," + revision + "," + cost + " WHERE (SELECT active FROM v_control)=1 ON CONFLICT(table_name,row_key) DO UPDATE SET revision=excluded.revision;"
}

func (s *columnSnapshots) releaseAll(ctx context.Context) error {
	for _, v := range s.refs {
		v.valid.Store(false)
	}
	s.refs = map[uint64]*ColumnSnapshot{}
	if _, err := s.cache.ExecContext(ctx, "DELETE FROM v_refs;UPDATE v_control SET active=0,overflow=0"); err != nil {
		return err
	}
	return s.collect(ctx)
}

// snapshotFailed runs outside the fact transaction. Its failure can never turn
// an already committed observation into an apparent rollback. Stop all TEMP
// capture hooks before allowing another fact batch to use the writer.
func (s *columnSnapshots) snapshotFailed(ctx context.Context) {
	s.failed.Store(true)
	for _, v := range s.refs {
		v.valid.Store(false)
	}
	s.refs = map[uint64]*ColumnSnapshot{}
	ctx = context.WithoutCancel(ctx)
	for _, table := range s.tables {
		for _, op := range []string{"insert", "update", "delete"} {
			if _, err := s.owner.w.ExecContext(ctx, "DROP TRIGGER IF EXISTS temp.snap_"+table.name+"_"+op); err != nil {
				slog.Warn("history snapshot capture shutdown failed", "library", "logs", "code", "history_store_failed")
			}
		}
	}
	if s.readers != nil {
		_ = s.readers.Close()
	}
	_ = s.cache.Close()
	if s.owner.owner != nil {
		// This is a metadata-only writer transaction. Calling Columns.Update
		// here would reenter the snapshot and owner locks held by the caller.
		if _, err := s.owner.w.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('column_projection_state','failed'),('column_projection_error','history_store_failed') ON CONFLICT(key) DO UPDATE SET value=excluded.value`); err != nil {
			slog.Warn("history snapshot failure marker failed", "library", "logs", "code", "history_store_failed")
		}
	}
	slog.Warn("history snapshot cache stopped", "library", "logs", "code", "history_store_failed")
}

func (s *columnSnapshots) finishSnapshotCommit(ctx context.Context) {
	s.revision++
	var removed, overflow int
	if err := s.cache.QueryRowContext(ctx, "SELECT removed,overflow FROM v_control").Scan(&removed, &overflow); err != nil {
		s.snapshotFailed(ctx)
		return
	}
	if removed != 0 {
		if s.owner.owner != nil {
			if meta, err := s.owner.owner.SnapshotMeta(ctx); err == nil {
				s.removalMeta.Store(&meta)
			} else {
				s.removalMeta.Store(nil)
			}
		}
		s.removalRevision.Store(s.revision)
	}
	if overflow != 0 {
		if err := s.releaseAll(ctx); err != nil {
			s.snapshotFailed(ctx)
		}
		return
	}
	if err := s.enforceBudget(ctx); err != nil {
		s.snapshotFailed(ctx)
	}
}

func (s *columnSnapshots) refreshCharge(ctx context.Context) error {
	parts := []string{"coalesce((SELECT sum(_snap_bytes) FROM v_current),0)"}
	for _, table := range s.tables {
		parts = append(parts, "coalesce((SELECT sum(_snap_bytes) FROM v_"+table.name+"),0)")
	}
	_, err := s.cache.ExecContext(ctx, fmt.Sprintf("UPDATE v_control SET charge=%s,budget=?", strings.Join(parts, "+")), s.budget)
	return err
}
