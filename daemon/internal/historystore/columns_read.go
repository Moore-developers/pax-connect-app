package historystore

import (
	"context"
	"database/sql"

	"pax-connect/daemon/internal/config"
)

const columnPageSize = 128

var columnEventSelectSQL = "SELECT " + prefixedColumns("e", eventColumnNames+","+stateColumnNames+","+metricsColumnNames+","+systemColumnNames) + "," + prefixedColumns("i", identityColumnNames) + " FROM col_events e JOIN col_identities i ON i.id=e.identity_id"
var columnConnectionSelectSQL = "SELECT " + prefixedColumns("c", connectionColumnNames+","+stateColumnNames+","+metricsColumnNames) + "," + prefixedColumns("i", identityColumnNames) + " FROM col_connections c JOIN col_identities i ON i.id=c.identity_id"

func columnEventSelect() string      { return columnEventSelectSQL }
func columnConnectionSelect() string { return columnConnectionSelectSQL }
func scanColumnEvent(row columnScanner) (config.HistoryEvent, error) {
	var e config.HistoryEvent
	var f columnFlowScan
	var m columnMetricsScan
	var s config.SystemEvent
	var seq, atNS, identity int64
	var at string
	var systemStarted *string
	var raw, flow, metrics, system, sections bool
	dest := []any{&seq, &e.ID, &at, &atNS, &e.Kind, &e.Title, &e.Level, &e.Source, &e.Result, &e.OperationID, &e.OperationFrom, &e.OperationTo, &e.Truncated, &e.RawOmitted, &raw, &flow, &metrics, &system, &identity}
	dest = append(dest, f.stateDest()...)
	dest = append(dest, m.dest()...)
	dest = append(dest, &s.Type, &s.Name, &s.Result, &s.ErrorCode, &s.Category.Key, &s.Category.Name, &s.Category.Symbol, &systemStarted, &sections)
	dest = append(dest, f.identityDest()...)
	if err := row.Scan(dest...); err != nil {
		return e, err
	}
	e.Sequence = uint64(seq)
	var err error
	if e.At, err = parseColumnTime(at); err != nil {
		return e, err
	}
	fact, err := f.finish()
	if err != nil {
		return e, err
	}
	if flow {
		e.Flow = &fact
	}
	met, err := m.finish()
	if err != nil {
		return e, err
	}
	if metrics {
		e.Metrics = &met
	}
	if system {
		s.StartedAt, err = parseColumnTimePtr(systemStarted)
		if err != nil {
			return e, err
		}
		if sections {
			s.Sections = []config.EventSection{}
		}
		e.System = &s
	}
	if raw {
		e.Raw = []string{}
	}
	return e, nil
}
func scanColumnConnection(row columnScanner) (config.LogConnectionSummary, error) {
	var c config.LogConnectionSummary
	var f columnFlowScan
	var m columnMetricsScan
	var key, last string
	var seq, identity int64
	dest := []any{&key, &c.LastEventID, &last, &seq, &identity}
	dest = append(dest, f.stateDest()...)
	dest = append(dest, m.dest()...)
	dest = append(dest, f.identityDest()...)
	if err := row.Scan(dest...); err != nil {
		return c, err
	}
	c.LastSequence = uint64(seq)
	var err error
	if c.LastLogAt, err = parseColumnTime(last); err != nil {
		return c, err
	}
	if c.Fact, err = f.finish(); err != nil {
		return c, err
	}
	c.Metrics, err = m.finish()
	return c, err
}

// Event reads one immutable event with ordered child evidence.
func (c *Columns) Event(ctx context.Context, id string) (config.HistoryEvent, bool, error) {
	e, err := scanColumnEvent(c.r.QueryRowContext(ctx, columnEventSelect()+" WHERE e.event_id=?", id))
	if err == sql.ErrNoRows {
		return e, false, nil
	}
	if err != nil {
		return e, false, err
	}
	err = c.eventChildren(ctx, &e)
	return e, err == nil, err
}

// Connection reads one current summary.
func (c *Columns) Connection(ctx context.Context, key string) (config.LogConnectionSummary, bool, error) {
	s, err := scanColumnConnection(c.r.QueryRowContext(ctx, columnConnectionSelect()+" WHERE c.flow_key=?", key))
	if err == sql.ErrNoRows {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	err = c.answers(ctx, 1, key, &s.Fact)
	return s, err == nil, err
}

// EventsPage reads at most limit events after the sequence, closing SQL cursors
// before loading children or returning to an importing writer.
func (c *Columns) EventsPage(ctx context.Context, after uint64, limit int) ([]config.HistoryEvent, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := c.r.QueryContext(ctx, columnEventSelect()+" WHERE e.seq>? ORDER BY e.seq LIMIT ?", int64(after), limit)
	if err != nil {
		return nil, err
	}
	var out []config.HistoryEvent
	for rows.Next() {
		e, er := scanColumnEvent(rows)
		if er != nil {
			_ = rows.Close()
			return nil, er
		}
		out = append(out, e)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range out {
		if err = c.eventChildren(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ConnectionsPage reads summaries in stable key order, after the supplied key.
func (c *Columns) ConnectionsPage(ctx context.Context, after string, limit int) ([]config.LogConnectionSummary, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := c.r.QueryContext(ctx, columnConnectionSelect()+" WHERE c.flow_key>? ORDER BY c.flow_key LIMIT ?", after, limit)
	if err != nil {
		return nil, err
	}
	var out []config.LogConnectionSummary
	for rows.Next() {
		s, er := scanColumnConnection(rows)
		if er != nil {
			_ = rows.Close()
			return nil, er
		}
		out = append(out, s)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	for i := range out {
		if err = c.answers(ctx, 1, out[i].Fact.Key, &out[i].Fact); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// EachEvent streams fixed-size pages without retaining the full database.
func (c *Columns) EachEvent(ctx context.Context, fn func(config.HistoryEvent) error) error {
	var after uint64
	for {
		page, err := c.EventsPage(ctx, after, columnPageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, e := range page {
			if err = fn(e); err != nil {
				return err
			}
			after = e.Sequence
		}
	}
}

// EachConnection streams fixed-size pages without retaining the full database.
func (c *Columns) EachConnection(ctx context.Context, fn func(config.LogConnectionSummary) error) error {
	after := ""
	for {
		page, err := c.ConnectionsPage(ctx, after, columnPageSize)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		for _, s := range page {
			if err = fn(s); err != nil {
				return err
			}
			after = s.Fact.Key
		}
	}
}
