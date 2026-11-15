package historystore

import (
	"context"
	"database/sql"
	"encoding/json"

	"pax-connect/daemon/internal/config"
)

type columnSummaryEntry struct {
	value config.LogConnectionSummary
	found bool
}

func (t *LogsTx) rememberSummary(key string, value config.LogConnectionSummary, found bool) {
	if _, ok := t.summaries[key]; !ok && len(t.summaries) >= columnBatchDictionaryLimit {
		return
	}
	if t.summaries == nil {
		t.summaries = map[string]columnSummaryEntry{}
	}
	t.summaries[key] = columnSummaryEntry{value, found}
}

// Summary reads the writer's current standard value directly from columns.
// Only the upgrading writer may seed a not-yet-imported summary from v3 JSON.
func (t *LogsTx) Summary(ctx context.Context, key string) (config.LogConnectionSummary, bool, error) {
	if v, ok := t.summaries[key]; ok {
		return v.value, v.found, nil
	}
	v, ok, err := t.columns.Connection(ctx, key)
	if err != nil {
		return v, false, err
	}
	if !ok {
		state, _, e := t.Meta(ctx, "column_upgrade_state")
		if e != nil {
			return v, false, e
		}
		if state == "building" || state == "failed" {
			var body []byte
			err = t.tx.QueryRowContext(ctx, "SELECT body FROM connections WHERE flow_key=?", key).Scan(&body)
			switch err {
			case sql.ErrNoRows:
				err = nil
			case nil:
				err = json.Unmarshal(body, &v)
				if err == nil {
					var gap string
					gap, _, err = t.Meta(ctx, "column_import_gap")
					if gap == "1" {
						v = config.MarkLogGap(v)
					}
				}
				ok = err == nil
			}
		}
	}
	if err == nil {
		t.rememberSummary(key, v, ok)
	}
	return v, ok, err
}

// PutSummary writes a standard summary and its typed projection batch without
// encoding an intermediate legacy row. Facts and projections commit together.
func (t *LogsTx) PutSummary(ctx context.Context, v config.LogConnectionSummary) error {
	if err := t.columns.PutConnection(ctx, v); err != nil {
		return wrapPut("summary", err)
	}
	if t.batch != nil {
		t.batch.Connections = append(t.batch.Connections, v)
	}
	t.rememberSummary(v.Fact.Key, v, true)
	return nil
}

// MarkImportGap preserves a gap affecting old summaries not yet imported. It
// shares the gap transaction and cursor, and never rewrites the frozen v3 input.
func (t *LogsTx) MarkImportGap(ctx context.Context) error {
	state, _, err := t.Meta(ctx, "column_upgrade_state")
	if err != nil {
		return err
	}
	if state != "building" && state != "failed" {
		return nil
	}
	return t.SetMeta(ctx, "column_import_gap", "1")
}

// Summaries reads the complete current value set for an explicit gap rewrite.
// The owner finishes this cursor before changing any of its rows.
func (t *LogsTx) Summaries(ctx context.Context) ([]config.LogConnectionSummary, error) {
	return columnSummaries(ctx, t.tx, "ORDER BY c.flow_key", nil)
}

// SummariesPage is the bounded standard-value startup normalization surface.
func (t *LogsTx) SummariesPage(ctx context.Context, after string, limit int) ([]config.LogConnectionSummary, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimits
	}
	return columnSummaries(ctx, t.tx, "WHERE c.flow_key>? ORDER BY c.flow_key LIMIT ?", []any{after, limit})
}

func columnSummaries(ctx context.Context, q columnQueryer, suffix string, args []any) ([]config.LogConnectionSummary, error) {
	rows, err := q.QueryContext(ctx, columnConnectionSelect()+" "+suffix, args...)
	if err != nil {
		return nil, err
	}
	var values []config.LogConnectionSummary
	for rows.Next() {
		v, e := scanColumnConnection(rows)
		if e != nil {
			_ = rows.Close()
			return nil, e
		}
		values = append(values, v)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range values {
		if err = columnAnswers(ctx, q, 1, values[i].Fact.Key, &values[i].Fact); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// AppendEvent stores a typed immutable event once and assigns its durable head.
// Raw lines remain ordered child evidence, never an intermediate JSON payload.
func (t *LogsTx) AppendEvent(ctx context.Context, v config.HistoryEvent) (bool, int64, error) {
	if exists, err := t.EventExists(ctx, v.ID); err != nil || exists {
		return false, 0, err
	}
	seq := int64(v.Sequence)
	if seq == 0 {
		var err error
		seq, err = t.NextEventSequence(ctx)
		if err != nil {
			return false, 0, err
		}
	}
	v.Sequence = uint64(seq)
	inserted, err := t.columns.PutEvent(ctx, v)
	if err != nil || !inserted {
		return inserted, 0, err
	}
	if err = t.advanceHead(ctx, seq); err != nil {
		return false, 0, err
	}
	index := ColumnEventIndex{ID: v.ID, Sequence: v.Sequence, At: v.At}
	if v.Flow != nil {
		index.Route, index.FlowKey = v.Flow.RouteID, v.Flow.Key
	}
	if err = t.columns.PutEventIndex(ctx, index); err != nil {
		return false, 0, err
	}
	if t.batch != nil {
		t.batch.Events = append(t.batch.Events, v)
	}
	return true, seq, nil
}

// RecentEvents returns a bounded chronological Watch seed directly from facts.
// The list shape excludes raw lines just like ordinary product event streams.
func (l *Logs) RecentEvents(ctx context.Context, limit int) ([]config.HistoryEvent, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimits
	}
	values, err := columnFactEvents(ctx, l.r, "ORDER BY e.at_ns DESC,e.seq DESC LIMIT ?", []any{limit}, false)
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	return values, err
}

func columnFactEvents(ctx context.Context, q columnQueryer, suffix string, args []any, raw bool) ([]config.HistoryEvent, error) {
	rows, err := q.QueryContext(ctx, columnEventSelect()+" "+suffix, args...)
	if err != nil {
		return nil, err
	}
	var values []config.HistoryEvent
	for rows.Next() {
		v, e := scanColumnEvent(rows)
		if e != nil {
			_ = rows.Close()
			return nil, e
		}
		values = append(values, v)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range values {
		if !raw {
			values[i].Raw = nil
		}
		if err = columnEventChildren(ctx, q, &values[i]); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// RecentSummaries returns at most limit retained summaries, newest activity first.
func (l *Logs) RecentSummaries(ctx context.Context, fromMs, untilMs int64, limit int) ([]config.LogConnectionSummary, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimits
	}
	return columnSummaries(ctx, l.r, "WHERE c.started_ns>=? AND c.started_ns<=? ORDER BY c.last_activity_ms DESC,c.flow_key DESC LIMIT ?", []any{columnMillisNS(fromMs), columnMillisEndNS(untilMs), limit})
}
