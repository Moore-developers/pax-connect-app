package historystore

import (
	"context"
	"strings"

	"pax-connect/daemon/internal/config"
)

var (
	columnIdentityInsert   = columnInsert("col_identities", "digest,"+identityColumnNames, "ON CONFLICT(digest) DO UPDATE SET digest=excluded.digest RETURNING id")
	columnEventInsert      = columnInsert("col_events", eventColumnNames+","+stateColumnNames+","+metricsColumnNames+","+systemColumnNames, "")
	columnConnectionInsert = columnInsert("col_connections", connectionColumnNames+","+stateColumnNames+","+metricsColumnNames+",started_ns,last_activity_ms", "ON CONFLICT(flow_key) DO UPDATE SET "+columnUpdates(connectionColumnNames+","+stateColumnNames+","+metricsColumnNames+",started_ns,last_activity_ms"))
)

func (t *ColumnsTx) putIdentity(ctx context.Context, f config.FlowFact) (int64, error) {
	key := identityKey(f)
	if id, ok := t.identities[key]; ok {
		return id, nil
	}
	v := identityValues(f)
	args := make([]any, 1, len(v)+1)
	args[0] = key[:]
	args = append(args, v...)
	st, e := t.statement(ctx, columnIdentityInsert)
	if e != nil {
		return 0, e
	}
	var id int64
	e = st.QueryRowContext(ctx, args...).Scan(&id)
	if e == nil && len(t.identities) < columnBatchDictionaryLimit {
		if t.identities == nil {
			t.identities = map[[32]byte]int64{}
		}
		t.identities[key] = id
	}
	return id, e
}

const eventColumnNames = "seq,event_id,at,at_ns,kind,title,level,source,result,operation_id,operation_from,operation_to,truncated,raw_omitted,raw_present,flow_present,metrics_present,system_present,identity_id"
const connectionColumnNames = "flow_key,last_event_id,last_log_at,last_sequence,identity_id"

// PutEvent inserts immutable event evidence once. Replayed IDs leave all fields unchanged.
func (t *ColumnsTx) PutEvent(ctx context.Context, e config.HistoryEvent) (bool, error) {
	if exists, err := t.columnEventExists(ctx, e.ID); err != nil || exists {
		return false, err
	}
	var f config.FlowFact
	if e.Flow != nil {
		f = *e.Flow
	}
	var m config.ConnectionMetrics
	if e.Metrics != nil {
		m = *e.Metrics
	}
	var s config.SystemEvent
	if e.System != nil {
		s = *e.System
	}
	identity, err := t.putIdentity(ctx, f)
	if err != nil {
		return false, err
	}
	v := make([]any, 0, 52)
	v = append(v, int64(e.Sequence), e.ID, columnTime(e.At), e.At.UnixNano(), e.Kind, e.Title, e.Level, e.Source, e.Result, e.OperationID, e.OperationFrom, e.OperationTo, e.Truncated, e.RawOmitted, e.Raw != nil, e.Flow != nil, e.Metrics != nil, e.System != nil, identity)
	v = append(v, stateValues(f)...)
	v = append(v, metricsValues(m)...)
	v = append(v, systemValues(s)...)
	if err = t.exec(ctx, columnEventInsert, v...); err != nil {
		return false, err
	}
	if err = t.writeAnswers(ctx, 0, e.ID, f); err != nil {
		return false, err
	}
	for i, line := range e.Raw {
		if err = t.exec(ctx, "INSERT INTO col_raw VALUES(?,?,?)", e.ID, i, line); err != nil {
			return false, err
		}
	}
	for i, section := range s.Sections {
		if err = t.exec(ctx, "INSERT INTO col_sections VALUES(?,?,?,?,?)", e.ID, i, section.Kind, section.Title, section.Fields != nil); err != nil {
			return false, err
		}
		for j, field := range section.Fields {
			if err = t.exec(ctx, "INSERT INTO col_fields VALUES(?,?,?,?,?,?,?)", e.ID, i, j, field.Label, field.Value, field.Before, field.After); err != nil {
				return false, err
			}
		}
	}
	t.rememberEvent(e.ID, true)
	return true, nil
}

func (t *ColumnsTx) columnEventExists(ctx context.Context, id string) (bool, error) {
	if found, ok := t.events[id]; ok {
		return found, nil
	}
	var found bool
	err := t.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM col_events WHERE event_id=?)", id).Scan(&found)
	if err == nil {
		t.rememberEvent(id, found)
	}
	return found, err
}

func (t *ColumnsTx) rememberEvent(id string, found bool) {
	if _, ok := t.events[id]; !ok && len(t.events) >= columnBatchDictionaryLimit {
		return
	}
	if t.events == nil {
		t.events = map[string]bool{}
	}
	t.events[id] = found
}

// PutConnection replaces the latest summary, retaining immutable event snapshots.
func (t *ColumnsTx) PutConnection(ctx context.Context, c config.LogConnectionSummary) error {
	f, m := c.Fact, c.Metrics
	identity, err := t.putIdentity(ctx, f)
	if err != nil {
		return err
	}
	v := make([]any, 0, 31)
	v = append(v, c.Fact.Key, c.LastEventID, columnTime(c.LastLogAt), int64(c.LastSequence), identity)
	v = append(v, stateValues(f)...)
	v = append(v, metricsValues(m)...)
	v = append(v, f.StartedAt.UnixNano(), columnSummaryActivity(c))
	if err = t.exec(ctx, columnConnectionInsert, v...); err != nil {
		return err
	}
	return t.writeAnswers(ctx, 1, c.Fact.Key, f)
}
func (t *ColumnsTx) writeAnswers(ctx context.Context, kind int, key string, f config.FlowFact) error {
	if err := t.exec(ctx, "DELETE FROM col_answers WHERE owner_kind=? AND owner_key=?", kind, key); err != nil {
		return err
	}
	if f.DNS != nil {
		for i, answer := range f.DNS.Answers {
			if err := t.exec(ctx, "INSERT INTO col_answers VALUES(?,?,?,?)", kind, key, i, answer); err != nil {
				return err
			}
		}
	}
	return nil
}

func columnUpdates(names string) string {
	cols := strings.Split(names, ",")
	for i, n := range cols {
		cols[i] = n + "=excluded." + n
	}
	return strings.Join(cols, ",")
}
