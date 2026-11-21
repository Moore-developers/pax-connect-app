package historystore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"database/sql/driver"
	"modernc.org/sqlite"
	"pax-connect/daemon/internal/config"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("pax_time_ns", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if args[0] == nil {
			return nil, nil
		}
		text, _ := args[0].(string)
		at, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return nil, err
		}
		if at.IsZero() {
			return int64(0), nil
		}
		return at.UnixNano(), nil
	})
}

// Close releases the snapshot cache after its readers and all owners stop.
func (c *Columns) Close() error {
	columnSnapshotInit.Lock()
	s := c.snapshots
	columnSnapshotInit.Unlock()
	if s != nil {
		s.mu.Lock()
		for _, v := range s.refs {
			v.valid.Store(false)
		}
		s.refs = map[uint64]*ColumnSnapshot{}
		_ = s.readers.Close()
		_ = s.cache.Close()
		s.mu.Unlock()
	}
	return c.store.Close()
}

// Event reads immutable event evidence at this snapshot, including ordered raw
// and native system fields. It survives deletion for an independent detail.
func (s *ColumnSnapshot) Event(ctx context.Context, id string) (config.HistoryEvent, bool, error) {
	var e config.HistoryEvent
	var found bool
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		var err error
		e, err = scanColumnEvent(q.QueryRowContext(ctx, snapshotSQL(columnEventSelect())+" WHERE e.event_id=?", id))
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return columnEventChildren(ctx, snapshotQuery{q}, &e)
	})
	return e, found, err
}

// Connection reads the frozen summary, never the latest replacement.
func (s *ColumnSnapshot) Connection(ctx context.Context, key string) (config.LogConnectionSummary, bool, error) {
	var v config.LogConnectionSummary
	var found bool
	err := s.WithRead(ctx, func(q ColumnQuery) error {
		var err error
		v, err = scanColumnConnection(q.QueryRowContext(ctx, snapshotSQL(columnConnectionSelect())+" WHERE c.flow_key=?", key))
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return columnAnswers(ctx, snapshotQuery{q}, 1, key, &v.Fact)
	})
	return v, found, err
}

// snapshotQuery adapts shared child readers without duplicating field mapping.
type snapshotQuery struct{ ColumnQuery }

func (q snapshotQuery) QueryContext(ctx context.Context, stmt string, args ...any) (*sql.Rows, error) {
	return q.ColumnQuery.QueryContext(ctx, snapshotSQL(stmt), args...)
}
func (q snapshotQuery) QueryRowContext(ctx context.Context, stmt string, args ...any) *sql.Row {
	return q.ColumnQuery.QueryRowContext(ctx, snapshotSQL(stmt), args...)
}
func snapshotSQL(stmt string) string {
	// Longest names first; ReplaceAll only sees trusted package-owned SQL.
	for _, name := range []string{"col_connections", "col_identities", "col_event_index", "col_texts", "col_sections", "col_answers", "col_members", "col_objects", "col_events", "col_fields", "col_groups", "col_raw"} {
		stmt = strings.ReplaceAll(stmt, name, "snap_"+name)
	}
	return stmt
}
