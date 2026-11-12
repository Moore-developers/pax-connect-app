package historystore

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

const (
	logsFile    = "logs.db"
	logsVersion = 3
)

// Meta keys written by the log job. Values are integers except error_code and the
// generation tokens; log_cleared_at is Unix milliseconds.
const (
	MetaRetentionHours        = "retention_hours"
	MetaSettingsRevision      = "settings_revision"
	MetaLogsRevision          = "logs_revision"
	MetaLogDeletionGeneration = "log_deletion_generation"
	MetaLogRemovalBefore      = "log_removal_before_ms"
	MetaSummaryGeneration     = "summary_generation"
	MetaClearGeneration       = "clear_generation"
	MetaLogClearedAt          = "log_cleared_at"
	MetaJournalGeneration     = "journal_generation"
	MetaPartial               = "partial"
	MetaErrorCode             = "error_code"
	MetaCapacityEvicted       = "capacity_evicted"
)

const logsDDL = `
CREATE TABLE connections(
 flow_key TEXT PRIMARY KEY, started_ms INTEGER NOT NULL DEFAULT 0, last_activity_ms INTEGER NOT NULL,
 app_id TEXT NOT NULL DEFAULT '', domain TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '',
 issue_code TEXT NOT NULL DEFAULT '', body BLOB NOT NULL);
CREATE INDEX connections_activity ON connections(last_activity_ms);
CREATE INDEX connections_app ON connections(app_id, last_activity_ms);
CREATE INDEX connections_domain ON connections(domain, last_activity_ms);
CREATE INDEX connections_ip ON connections(ip, last_activity_ms);
CREATE INDEX connections_issue ON connections(issue_code, last_activity_ms);
CREATE TABLE events(
 seq INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL UNIQUE, at_ms INTEGER NOT NULL,
 kind TEXT NOT NULL DEFAULT '', flow_key TEXT NOT NULL DEFAULT '', level TEXT NOT NULL DEFAULT '',
 source TEXT NOT NULL DEFAULT '', problem_key TEXT NOT NULL DEFAULT '', body BLOB NOT NULL, raw BLOB);
CREATE INDEX events_at ON events(at_ms, event_id);
CREATE INDEX events_flow ON events(flow_key, at_ms);
CREATE INDEX events_problem ON events(problem_key, at_ms);
CREATE TABLE diagnostics(
 flow_key TEXT NOT NULL, seq INTEGER NOT NULL, at_ms INTEGER NOT NULL, text TEXT NOT NULL,
 PRIMARY KEY(flow_key, seq));
CREATE INDEX diagnostics_at ON diagnostics(at_ms);
CREATE TABLE commands(id TEXT PRIMARY KEY, signature TEXT NOT NULL DEFAULT '', at_ms INTEGER NOT NULL, result BLOB);
CREATE TABLE job_cursor(name TEXT PRIMARY KEY, session TEXT NOT NULL, seq INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO meta(key,value) VALUES
 ('retention_hours','24'),('settings_revision','0'),('logs_revision','0'),
 ('log_deletion_generation','0'),('summary_generation',''),('clear_generation','0'),
 ('log_cleared_at','0'),('journal_generation',''),('partial','0'),('error_code',''),('capacity_evicted','0');
`

// LogConnection is one summary row. The indexed columns support range queries;
// Body is the caller's JSON encoding of config.LogConnectionSummary.
type LogConnection struct {
	FlowKey                      string
	StartedMs, LastActivityMs    int64
	AppID, Domain, IP, IssueCode string
	Body                         []byte
	// LastEventBody is optional read evidence from the same ConnectionSnapshot; never raw text.
	LastEventBody []byte
}

// LogEvent is one stored event. Sequence is assigned by SQLite on first insert;
// EventID is unique so a replayed batch never duplicates or renumbers an event.
// Body is the caller's JSON encoding of config.HistoryEvent without its raw lines. Raw is
// the JSON encoding of those lines, kept in a separate trailing column so list scans never
// read the large overflow pages; only ReadLogEvent returns it.
type LogEvent struct {
	EventID                                  string
	AtMs                                     int64
	Sequence                                 int64 // 0 lets SQLite assign the next head sequence
	Kind, FlowKey, Level, Source, ProblemKey string
	Body, Raw                                []byte
}

// LogDiagnostic is one scrubbed diagnostic text of a connection.
type LogDiagnostic struct {
	FlowKey string
	Seq     int64
	AtMs    int64
	Text    string
}

// Logs is the 24-hour log database (logs.db).
type Logs struct {
	*store
	observer logsReadObserver
}

// LogsTx is the write transaction handed to Logs.Update; it is valid only inside the callback.
type LogsTx struct {
	baseTx
	changes *LogsChanges
}

// OpenLogs opens or creates logs.db under historyDir. A corrupt file is renamed aside and
// replaced (see Recovered); an unsupported schema version returns ErrSchemaVersion.
func OpenLogs(ctx context.Context, historyDir string, owner uint32) (*Logs, Recovered, error) {
	if err := prepareDirectory(historyDir, owner); err != nil {
		return nil, Recovered{}, err
	}
	s, recovered, err := openStore(ctx, filepath.Join(historyDir, logsFile), owner, storeSpec{ddl: logsDDL, version: logsVersion, readers: readerConns})
	if err != nil {
		return nil, recovered, err
	}
	pruneQuarantine(historyDir, regexp.MustCompile(`^logs\.db$`))
	return &Logs{store: s}, recovered, nil
}

// Update runs fn in one write transaction and commits it, or rolls back when fn fails.
func (l *Logs) Update(ctx context.Context, fn func(*LogsTx) error) error {
	l.observer.Lock()
	defer l.observer.Unlock()
	d := LogsChanges{Connections: map[string]LogConnection{}}
	err := l.update(ctx, func(tx *sql.Tx) error {
		if err := fn(&LogsTx{baseTx: baseTx{tx}, changes: &d}); err != nil {
			return err
		}
		if l.observer.publish != nil {
			var err error
			d.Meta, err = readLogsMeta(ctx, tx)
			return err
		}
		return nil
	})
	if err == nil && l.observer.publish != nil {
		l.observer.publish(context.WithoutCancel(ctx), d)
	}
	return err
}

const connectionSelect = `SELECT flow_key,started_ms,last_activity_ms,app_id,domain,ip,issue_code,body FROM connections`

// PutConnection inserts or updates the summary row. Body may be nil for an empty JSON object.
func (t *LogsTx) PutConnection(ctx context.Context, c LogConnection) error {
	body := c.Body
	if body == nil {
		body = []byte{}
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO connections(flow_key,started_ms,last_activity_ms,app_id,domain,ip,issue_code,body)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(flow_key) DO UPDATE SET
 started_ms=excluded.started_ms, last_activity_ms=excluded.last_activity_ms, app_id=excluded.app_id,
 domain=excluded.domain, ip=excluded.ip, issue_code=excluded.issue_code, body=excluded.body`,
		c.FlowKey, c.StartedMs, c.LastActivityMs, c.AppID, c.Domain, c.IP, c.IssueCode, body)
	if err == nil && t.changes != nil {
		c.Body = append([]byte(nil), body...)
		t.changes.Connections[c.FlowKey] = c
	}
	return wrapPut("connection", err)
}

// Connection reads one summary row through the write transaction.
func (t *LogsTx) Connection(ctx context.Context, flowKey string) (LogConnection, bool, error) {
	return readConnection(ctx, t.tx, flowKey)
}

// Connections returns every summary row in the transaction. The gap marker rewrites all
// still-open summaries, so it needs the complete set before writing any of them.
func (t *LogsTx) Connections(ctx context.Context) ([]LogConnection, error) {
	rows, err := t.tx.QueryContext(ctx, connectionSelect+" ORDER BY flow_key")
	if err != nil {
		return nil, fmt.Errorf("historystore: connections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LogConnection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("historystore: connections: %w", err)
	}
	return out, nil
}

func readConnection(ctx context.Context, q queryer, flowKey string) (LogConnection, bool, error) {
	var c LogConnection
	err := q.QueryRowContext(ctx, connectionSelect+" WHERE flow_key=?", flowKey).Scan(
		&c.FlowKey, &c.StartedMs, &c.LastActivityMs, &c.AppID, &c.Domain, &c.IP, &c.IssueCode, &c.Body)
	if err == sql.ErrNoRows {
		return LogConnection{}, false, nil
	}
	if err != nil {
		return LogConnection{}, false, fmt.Errorf("historystore: connection %s: %w", flowKey, err)
	}
	return c, true, nil
}

func scanConnection(row interface{ Scan(...any) error }) (LogConnection, error) {
	var c LogConnection
	err := row.Scan(&c.FlowKey, &c.StartedMs, &c.LastActivityMs, &c.AppID, &c.Domain, &c.IP, &c.IssueCode, &c.Body)
	if err != nil {
		return LogConnection{}, fmt.Errorf("historystore: connection row: %w", err)
	}
	return c, nil
}

// PutEvent inserts an event once. inserted is false when EventID already exists;
// seq is the head sequence assigned on first insert (0 when ignored).
func (t *LogsTx) PutEvent(ctx context.Context, e LogEvent) (inserted bool, seq int64, err error) {
	body := e.Body
	if body == nil {
		body = []byte{}
	}
	var raw any
	if len(e.Raw) > 0 {
		raw = e.Raw
	}
	exists, err := t.EventExists(ctx, e.EventID)
	if err != nil || exists {
		return false, 0, err
	}
	var res sql.Result
	if e.Sequence > 0 {
		res, err = t.tx.ExecContext(ctx, `INSERT INTO events(seq,event_id,at_ms,kind,flow_key,level,source,problem_key,body,raw)
VALUES(?,?,?,?,?,?,?,?,?,?)`, e.Sequence, e.EventID, e.AtMs, e.Kind, e.FlowKey, e.Level, e.Source, e.ProblemKey, body, raw)
	} else {
		res, err = t.tx.ExecContext(ctx, `INSERT INTO events(event_id,at_ms,kind,flow_key,level,source,problem_key,body,raw)
VALUES(?,?,?,?,?,?,?,?,?)`, e.EventID, e.AtMs, e.Kind, e.FlowKey, e.Level, e.Source, e.ProblemKey, body, raw)
	}
	if err != nil {
		return false, 0, fmt.Errorf("historystore: put event: %w", err)
	}
	if e.Sequence > 0 {
		if t.changes != nil {
			e.Body = append([]byte(nil), body...)
			e.Raw = nil
			t.changes.Events = append(t.changes.Events, e)
		}
		return true, e.Sequence, nil
	}
	seq, err = res.LastInsertId()
	if err != nil {
		return false, 0, fmt.Errorf("historystore: put event sequence: %w", err)
	}
	if t.changes != nil {
		e.Sequence = seq
		e.Body = append([]byte(nil), body...)
		e.Raw = nil
		t.changes.Events = append(t.changes.Events, e)
	}
	return true, seq, nil
}

// NextEventSequence returns the sequence the next event insert will receive. It reads
// sqlite_sequence so deleted events never rewind the head, matching the legacy counter.
func (t *LogsTx) NextEventSequence(ctx context.Context) (int64, error) {
	var next int64
	err := t.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT seq FROM sqlite_sequence WHERE name='events'),0)+1").Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("historystore: next event sequence: %w", err)
	}
	return next, nil
}

// EventExists reports whether EventID is already stored.
func (t *LogsTx) EventExists(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	if err := t.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE event_id=?)", eventID).Scan(&exists); err != nil {
		return false, fmt.Errorf("historystore: event lookup: %w", err)
	}
	return exists, nil
}

// PutDiagnostic inserts or replaces the diagnostic at (FlowKey, Seq).
func (t *LogsTx) PutDiagnostic(ctx context.Context, d LogDiagnostic) error {
	_, err := t.tx.ExecContext(ctx, "INSERT OR REPLACE INTO diagnostics(flow_key,seq,at_ms,text) VALUES(?,?,?,?)",
		d.FlowKey, d.Seq, d.AtMs, d.Text)
	return wrapPut("diagnostic", err)
}

// TrimDiagnostics keeps only the newest keep diagnostics of one connection.
func (t *LogsTx) TrimDiagnostics(ctx context.Context, flowKey string, keep int) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM diagnostics WHERE flow_key=? AND seq NOT IN
 (SELECT seq FROM diagnostics WHERE flow_key=? ORDER BY seq DESC LIMIT ?)`, flowKey, flowKey, keep)
	if err != nil {
		return fmt.Errorf("historystore: trim diagnostics: %w", err)
	}
	return nil
}

func wrapPut(what string, err error) error {
	if err != nil {
		return fmt.Errorf("historystore: put %s: %w", what, err)
	}
	return nil
}

func logsTargets(before int64) []pruneTarget {
	return []pruneTarget{
		{table: "connections", column: "last_activity_ms", before: before, removalMeta: MetaLogRemovalBefore},
		{table: "events", column: "at_ms", before: before, removalMeta: MetaLogRemovalBefore},
		{table: "diagnostics", column: "at_ms", before: before, removalMeta: MetaLogRemovalBefore},
	}
}

// Prune deletes rows older than before by their time index, in one bounded transaction.
// Call it repeatedly while Pruned.More is true.
func (l *Logs) Prune(ctx context.Context, before time.Time, limits Limits) (Pruned, error) {
	l.observer.Lock()
	defer l.observer.Unlock()
	p, err := l.prune(ctx, logsTargets(before.UnixMilli()), limits)
	l.publishRemoval(context.WithoutCancel(ctx), p.Deleted > 0, before.UnixMilli())
	return p, err
}

// EvictOldest deletes the oldest events, falling back to the same frontier for every
// table, until LiveBytes is at most capBytes (HS-R1). Writes are never refused for capacity.
func (l *Logs) EvictOldest(ctx context.Context, capBytes int64, limits Limits) (bool, error) {
	l.observer.Lock()
	defer l.observer.Unlock()
	removed, err := l.evictOldest(ctx, capBytes, limits, evictSpec{primaryTable: "events", primaryColumn: "at_ms", targets: logsTargets(0)})
	if removed {
		meta, readErr := l.SnapshotMeta(context.WithoutCancel(ctx))
		if readErr != nil {
			l.publishRemoval(context.WithoutCancel(ctx), true, 0)
		} else {
			l.publishRemoval(context.WithoutCancel(ctx), true, meta.LogRemovalBeforeMs)
		}
	}
	return removed, err
}
