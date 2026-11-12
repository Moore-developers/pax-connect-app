package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// LogsMeta is one consistent read of the log database metadata. HeadSequence is the
// highest event sequence ever assigned, not the row count: deletions never rewind it.
type LogsMeta struct {
	LegacyImportIncomplete bool
	RetentionHours         int
	SettingsRevision       uint64
	LogsRevision           uint64
	LogDeletionGeneration  uint64
	LogRemovalBeforeMs     int64
	ClearGeneration        uint64
	HeadSequence           uint64
	DiagnosticsHead        int64
	SummaryGeneration      string
	JournalGeneration      string
	ClearedAt              time.Time
	Partial                bool
	CapacityEvicted        bool
	ErrorCode              string
}

// SnapshotMeta reads every meta value and the event head through the read pool.
func (l *Logs) SnapshotMeta(ctx context.Context) (LogsMeta, error) {
	return readLogsMeta(ctx, l.r)
}

type logsSnapshotQuery interface {
	queryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readLogsMeta(ctx context.Context, source logsSnapshotQuery) (LogsMeta, error) {
	m := LogsMeta{RetentionHours: 24}
	rows, err := source.QueryContext(ctx, "SELECT key,value FROM meta")
	if err != nil {
		return m, fmt.Errorf("historystore: logs meta: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return m, fmt.Errorf("historystore: logs meta: %w", err)
		}
		m.apply(key, value)
	}
	if err = rows.Err(); err != nil {
		return m, fmt.Errorf("historystore: logs meta: %w", err)
	}
	m.Partial = m.Partial || m.LegacyImportIncomplete
	var head int64
	if err = source.QueryRowContext(ctx, "SELECT coalesce((SELECT seq FROM sqlite_sequence WHERE name='events'),0)").Scan(&head); err != nil {
		return m, fmt.Errorf("historystore: event head: %w", err)
	}
	if head > 0 {
		m.HeadSequence = uint64(head)
	}
	if err = source.QueryRowContext(ctx, "SELECT coalesce(max(rowid),0) FROM diagnostics").Scan(&m.DiagnosticsHead); err != nil {
		return m, fmt.Errorf("historystore: diagnostics head: %w", err)
	}
	return m, nil
}

// apply maps one meta row. Malformed counters read as their default so one bad row cannot
// take log queries down with it.
func (m *LogsMeta) apply(key, value string) {
	if n, err := strconv.ParseUint(value, 10, 64); err == nil {
		switch key {
		case MetaSettingsRevision:
			m.SettingsRevision = n
		case MetaLogsRevision:
			m.LogsRevision = n
		case MetaLogRemovalBefore:
			m.LogRemovalBeforeMs = int64(n)
		case MetaLogDeletionGeneration:
			m.LogDeletionGeneration = n
		case MetaClearGeneration:
			m.ClearGeneration = n
		case MetaLogClearedAt:
			if n > 0 {
				m.ClearedAt = time.UnixMilli(int64(n))
			}
		case MetaRetentionHours:
			if n > 0 {
				m.RetentionHours = int(n)
			}
		}
	}
	switch key {
	case MetaSummaryGeneration:
		m.SummaryGeneration = value
	case MetaJournalGeneration:
		m.JournalGeneration = value
	case MetaErrorCode:
		m.ErrorCode = value
	case MetaPartial:
		m.Partial = value == "1"
	case "migration_pending":
		m.LegacyImportIncomplete = value == "1"
	case MetaCapacityEvicted:
		m.CapacityEvicted = value == "1"
	}
}

// EachLogEvent streams events with fromMs <= at_ms <= untilMs and at_ms >= cutoffMs.
// Rows come in rowid order: a sequential table scan beats an index walk plus random table
// lookups, and the caller orders the decoded events itself. Bodies keep their full JSON.
// The callback must be fast and must not retain the row.
func (l *Logs) EachLogEvent(ctx context.Context, fromMs, untilMs, cutoffMs int64, fn func(LogEvent) error) error {
	rows, err := l.r.QueryContext(ctx, `SELECT seq,event_id,at_ms,body FROM events
WHERE at_ms >= ? AND at_ms >= ? AND at_ms <= ?`, fromMs, cutoffMs, untilMs)
	if err != nil {
		return fmt.Errorf("historystore: log events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e LogEvent
		if err = rows.Scan(&e.Sequence, &e.EventID, &e.AtMs, &e.Body); err != nil {
			return fmt.Errorf("historystore: log event row: %w", err)
		}
		if err = fn(e); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("historystore: log events: %w", err)
	}
	return nil
}

// ReadLogEvent returns one stored event by its stable identity, including its raw lines.
func (l *Logs) ReadLogEvent(ctx context.Context, eventID string) (LogEvent, bool, error) {
	var e LogEvent
	err := l.r.QueryRowContext(ctx, "SELECT seq,event_id,at_ms,body,raw FROM events WHERE event_id=?", eventID).
		Scan(&e.Sequence, &e.EventID, &e.AtMs, &e.Body, &e.Raw)
	if errors.Is(err, sql.ErrNoRows) {
		return LogEvent{}, false, nil
	}
	if err != nil {
		return LogEvent{}, false, fmt.Errorf("historystore: log event %s: %w", eventID, err)
	}
	return e, true, nil
}

// LogEventsAfter returns up to limit events with after < seq <= head, oldest first. It is
// the incremental scan of QueryLogUpdates; the caller applies deadlines to the row bodies.
func (l *Logs) LogEventsAfter(ctx context.Context, after, head int64, limit int) ([]LogEvent, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimits
	}
	rows, err := l.r.QueryContext(ctx, "SELECT seq,event_id,at_ms,body FROM events WHERE seq>? AND seq<=? ORDER BY seq LIMIT ?", after, head, limit)
	if err != nil {
		return nil, fmt.Errorf("historystore: log events after: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LogEvent
	for rows.Next() {
		var e LogEvent
		if err = rows.Scan(&e.Sequence, &e.EventID, &e.AtMs, &e.Body); err != nil {
			return nil, fmt.Errorf("historystore: log event row: %w", err)
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("historystore: log events after: %w", err)
	}
	return out, nil
}

// RecentLogEvents returns the newest limit events in chronological order (oldest first),
// matching what Watch publishes after a replace.
func (l *Logs) RecentLogEvents(ctx context.Context, limit int) ([]LogEvent, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimits
	}
	rows, err := l.r.QueryContext(ctx, "SELECT seq,event_id,at_ms,body FROM events ORDER BY at_ms DESC, seq DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("historystore: recent events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LogEvent
	for rows.Next() {
		var e LogEvent
		if err = rows.Scan(&e.Sequence, &e.EventID, &e.AtMs, &e.Body); err != nil {
			return nil, fmt.Errorf("historystore: recent event row: %w", err)
		}
		out = append(out, e)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("historystore: recent events: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// LogConnections reads the stored summaries of keys. Missing rows are simply absent.
func (l *Logs) LogConnections(ctx context.Context, keys []string) (map[string]LogConnection, error) {
	out := make(map[string]LogConnection, len(keys))
	const chunk = 500
	for start := 0; start < len(keys); start += chunk {
		end := min(len(keys), start+chunk)
		args := make([]any, 0, end-start)
		query := connectionSelect + " WHERE flow_key IN ("
		for i := start; i < end; i++ {
			if i > start {
				query += ","
			}
			query += "?"
			args = append(args, keys[i])
		}
		query += ")"
		rows, err := l.r.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("historystore: log connections: %w", err)
		}
		for rows.Next() {
			c, err := scanConnection(rows)
			if err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[c.FlowKey] = c
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("historystore: log connections: %w", err)
		}
		_ = rows.Close()
	}
	return out, nil
}

// EachFlowLogEvent streams the events of one connection that fall inside the query window,
// oldest first. It bounds QueryLogUpdates member expansion.
func (l *Logs) EachFlowLogEvent(ctx context.Context, flowKey string, fromMs, untilMs, cutoffMs int64, fn func(LogEvent) error) error {
	return l.eachLogEventBy(ctx, "flow_key", flowKey, fromMs, untilMs, cutoffMs, fn)
}

// EachProblemLogEvent streams the events grouped under one problem key.
func (l *Logs) EachProblemLogEvent(ctx context.Context, problemKey string, fromMs, untilMs, cutoffMs int64, fn func(LogEvent) error) error {
	return l.eachLogEventBy(ctx, "problem_key", problemKey, fromMs, untilMs, cutoffMs, fn)
}

func (l *Logs) eachLogEventBy(ctx context.Context, column, key string, fromMs, untilMs, cutoffMs int64, fn func(LogEvent) error) error {
	query := "SELECT seq,event_id,at_ms,body FROM events WHERE " + column + "=? AND at_ms>=? AND at_ms>=? AND at_ms<=? ORDER BY at_ms, seq"
	rows, err := l.r.QueryContext(ctx, query, key, fromMs, cutoffMs, untilMs)
	if err != nil {
		return fmt.Errorf("historystore: log events by %s: %w", column, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e LogEvent
		if err = rows.Scan(&e.Sequence, &e.EventID, &e.AtMs, &e.Body); err != nil {
			return fmt.Errorf("historystore: log event row: %w", err)
		}
		if err = fn(e); err != nil {
			return err
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("historystore: log events by %s: %w", column, err)
	}
	return nil
}

// LogDiagnostics returns the diagnostics of one connection with fromMs < at_ms <= untilMs
// and rowid <= head, oldest sequence first. head freezes a token's admitted rows; pass
// math.MaxInt64 for the un-bounded event detail path.
func (l *Logs) LogDiagnostics(ctx context.Context, flowKey string, fromMs, untilMs, head int64) ([]LogDiagnostic, error) {
	rows, err := l.r.QueryContext(ctx, `SELECT flow_key,seq,at_ms,text FROM diagnostics
WHERE flow_key=? AND at_ms>? AND at_ms<=? AND rowid<=? ORDER BY seq`, flowKey, fromMs, untilMs, head)
	if err != nil {
		return nil, fmt.Errorf("historystore: log diagnostics: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LogDiagnostic
	for rows.Next() {
		var d LogDiagnostic
		if err = rows.Scan(&d.FlowKey, &d.Seq, &d.AtMs, &d.Text); err != nil {
			return nil, fmt.Errorf("historystore: log diagnostic row: %w", err)
		}
		out = append(out, d)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("historystore: log diagnostics: %w", err)
	}
	return out, nil
}

// FullLogRange is the unbounded diagnostic window used by GetLogDetail's event identity path.
const (
	FullLogRangeMin = math.MinInt64
	FullLogRangeMax = math.MaxInt64
)
