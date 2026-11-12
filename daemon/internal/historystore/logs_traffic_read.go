package historystore

import (
	"context"
	"database/sql"
)

// EachConnection streams retained connection summaries whose start time is in
// [fromMs,untilMs]. An optional key narrows the read to a single connection.
func (l *Logs) EachConnection(ctx context.Context, fromMs, untilMs int64, key string, fn func(LogConnection) error) error {
	return l.eachConnection(ctx, fromMs, untilMs, key, 0, fn)
}

// EachRecentConnection visits at most limit summaries, newest activity first.
func (l *Logs) EachRecentConnection(ctx context.Context, fromMs, untilMs int64, limit int, fn func(LogConnection) error) error {
	if limit <= 0 {
		return ErrInvalidLimits
	}
	return l.eachConnection(ctx, fromMs, untilMs, "", limit, fn)
}

func (l *Logs) eachConnection(ctx context.Context, fromMs, untilMs int64, key string, limit int, fn func(LogConnection) error) error {
	return eachConnection(ctx, l.r, fromMs, untilMs, key, limit, false, fn)
}

func eachConnection(ctx context.Context, source logsSnapshotQuery, fromMs, untilMs int64, key string, limit int, withEvent bool, fn func(LogConnection) error) error {
	columns, join := "c.body", ""
	if withEvent {
		columns, join = "c.body, e.body", " LEFT JOIN events e ON e.event_id=json_extract(c.body,'$.LastEventID')"
	}
	query := "SELECT " + columns + " FROM connections c" + join + " WHERE c.started_ms>=? AND c.started_ms<=?"
	args := []any{fromMs, untilMs}
	if key != "" {
		query = "SELECT " + columns + " FROM connections c" + join + " WHERE c.flow_key=?"
		args = []any{key}
	}
	if limit > 0 {
		query += " ORDER BY c.last_activity_ms DESC,c.flow_key DESC"
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := source.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v LogConnection
		destinations := []any{&v.Body}
		if withEvent {
			destinations = append(destinations, &v.LastEventBody)
		}
		if err = rows.Scan(destinations...); err != nil {
			return err
		}
		if err = fn(v); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ConnectionSnapshot streams retained summaries and their metadata from one SQLite snapshot.
func (l *Logs) ConnectionSnapshot(ctx context.Context, fromMs, untilMs int64, key string, fn func(LogConnection) error) (LogsMeta, error) {
	tx, err := l.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return LogsMeta{}, err
	}
	defer func() { _ = tx.Rollback() }()
	meta, err := readLogsMeta(ctx, tx)
	if err != nil {
		return meta, err
	}
	if err = eachConnection(ctx, tx, fromMs, untilMs, key, 0, true, fn); err != nil {
		return meta, err
	}
	return meta, tx.Commit()
}
