package historystore

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"time"
)

// LogsMeta is one consistent read of the log database metadata. HeadSequence is the
// highest event sequence ever assigned, not the row count: deletions never rewind it.
type LogsMeta struct {
	LegacyImportIncomplete bool
	ProjectionFailed       bool
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
	if m.ProjectionFailed {
		m.ErrorCode = "history_store_failed"
	}
	if m.ErrorCode == "" {
		var trafficReady string
		if err = source.QueryRowContext(ctx, "SELECT value FROM col_meta WHERE key='traffic_ready'").Scan(&trafficReady); err != nil && err != sql.ErrNoRows {
			return m, err
		}
		if trafficReady == "0" {
			m.ErrorCode = "history_building"
		}
	}
	if m.ErrorCode == "history_store_failed" || m.ErrorCode == "history_building" || m.ErrorCode == "history_converting" {
		m.Partial = true
	}
	var head int64
	if err = source.QueryRowContext(ctx, "SELECT coalesce((SELECT CAST(value AS INTEGER) FROM meta WHERE key='column_head_sequence'),0)").Scan(&head); err != nil {
		return m, err
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
		if m.ErrorCode == "" {
			m.ErrorCode = value
		}
	case "column_upgrade_state":
		if value == "building" {
			m.ErrorCode = "history_converting"
			m.Partial = true
		}
		if value == "failed" {
			m.ErrorCode = "history_store_failed"
			m.Partial = true
		}
	case "column_projection_state":
		m.ProjectionFailed = value == "failed"
	case MetaPartial:
		m.Partial = value == "1"
	case "migration_pending":
		m.LegacyImportIncomplete = value == "1"
	case MetaCapacityEvicted:
		m.CapacityEvicted = value == "1"
	}
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
