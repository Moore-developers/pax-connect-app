package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

const (
	trafficFile    = "traffic.db"
	trafficVersion = 1
)

const trafficColumns = `bucket_ms INTEGER NOT NULL, dimension TEXT NOT NULL, object TEXT NOT NULL, route TEXT NOT NULL, direction TEXT NOT NULL,
 name TEXT NOT NULL DEFAULT '', source_kind TEXT NOT NULL DEFAULT '', route_name TEXT NOT NULL DEFAULT '',
 up_bytes INTEGER NOT NULL DEFAULT 0, down_bytes INTEGER NOT NULL DEFAULT 0,
 new_conns INTEGER NOT NULL DEFAULT 0, failures INTEGER NOT NULL DEFAULT 0,
 latency_ms INTEGER NOT NULL DEFAULT 0, latency_at_ms INTEGER NOT NULL DEFAULT 0, max_latency_ms INTEGER NOT NULL DEFAULT 0,
 latency_hist BLOB, rule_basis BLOB, search TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(bucket_ms, dimension, object, route, direction)`

const trafficDDL = `
CREATE TABLE traffic_5m(` + trafficColumns + `);
CREATE INDEX traffic_5m_object ON traffic_5m(dimension, object, bucket_ms);
CREATE TABLE recent_5s(` + trafficColumns + `);
CREATE TABLE coverage_5m(bucket_ms INTEGER PRIMARY KEY, bits BLOB NOT NULL);
CREATE TABLE flow_marks(
 flow_key TEXT PRIMARY KEY, started INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0,
 responded INTEGER NOT NULL DEFAULT 0, closed INTEGER NOT NULL DEFAULT 0, last_at_ms INTEGER NOT NULL);
CREATE INDEX flow_marks_last ON flow_marks(last_at_ms);
CREATE TABLE job_cursor(name TEXT PRIMARY KEY, session TEXT NOT NULL, seq INTEGER NOT NULL) WITHOUT ROWID;
`

// TrafficKey identifies one bucket row. BucketMs is the bucket start in Unix milliseconds
// (5 minutes for traffic_5m, 5 seconds for recent_5s).
type TrafficKey struct {
	BucketMs                            int64
	Dimension, Object, Route, Direction string
}

// TrafficRow is one aggregate row. LatencyHist and RuleBasis are opaque encodings owned by the caller.
type TrafficRow struct {
	TrafficKey
	Name, SourceKind, RouteName string
	UpBytes, DownBytes          int64
	NewConns, Failures          int64
	LatencyMs, LatencyAtMs      int64
	MaxLatencyMs                int64
	LatencyHist, RuleBasis      []byte
	Search                      string
	LegacyGap                   bool // read projection; never changes byte attribution
}

// Coverage is the 300-bit collection bitmap of one 5-minute bucket.
type Coverage struct {
	BucketMs int64
	Bits     []byte
}

// FlowMark records which one-shot counters a connection has already contributed.
// Closed marks expire shortly after LastAtMs; open ones after a day (see TrafficCutoffs).
type FlowMark struct {
	FlowKey                            string
	Started, Failed, Responded, Closed bool
	LastAtMs                           int64
}

// TrafficCutoffs gives per-table exclusive time bounds for Traffic.Prune; a zero time skips that table.
type TrafficCutoffs struct {
	// Aggregates bounds traffic_5m and coverage_5m by bucket.
	Aggregates time.Time
	// Recent bounds recent_5s by bucket.
	Recent time.Time
	// ClosedMarks and OpenMarks bound flow_marks by last_at for closed and never-closed flows.
	ClosedMarks, OpenMarks time.Time
}

// Traffic is the 30-day traffic database (traffic.db).
type Traffic struct{ *store }

// TrafficTx is the write transaction handed to Traffic.Update; it is valid only inside the callback.
// Cursor, flow marks and aggregate rows written in one callback commit atomically.
type TrafficTx struct {
	baseTx
	searchInsert *sql.Stmt
}

// OpenTraffic opens or creates traffic.db under historyDir. A corrupt file is renamed aside and
// replaced (see Recovered); an unsupported schema version returns ErrSchemaVersion.
func OpenTraffic(ctx context.Context, historyDir string, owner uint32) (*Traffic, Recovered, error) {
	if err := prepareDirectory(historyDir, owner); err != nil {
		return nil, Recovered{}, err
	}
	s, recovered, err := openStore(ctx, filepath.Join(historyDir, trafficFile), owner, storeSpec{ddl: trafficDDL, version: trafficVersion, readers: readerConns})
	if err != nil {
		return nil, recovered, err
	}
	if _, err = s.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS recent_cuts(`+trafficColumns+`); CREATE INDEX IF NOT EXISTS recent_cuts_time ON recent_cuts(bucket_ms);`+trafficSearchDDL); err != nil {
		_ = s.Close()
		return nil, recovered, err
	}
	pruneQuarantine(historyDir, regexp.MustCompile(`^traffic\.db$`))
	return &Traffic{s}, recovered, nil
}

// Update runs fn in one write transaction and commits it, or rolls back when fn fails.
func (t *Traffic) Update(ctx context.Context, fn func(*TrafficTx) error) error {
	return t.update(ctx, func(tx *sql.Tx) error {
		v := &TrafficTx{baseTx: baseTx{tx}}
		defer v.closeStatements()
		return fn(v)
	})
}

const trafficSelect = `SELECT name,source_kind,route_name,up_bytes,down_bytes,new_conns,failures,latency_ms,latency_at_ms,max_latency_ms,
latency_hist,rule_basis,search FROM %s WHERE bucket_ms=? AND dimension=? AND object=? AND route=? AND direction=?`

func getRow(ctx context.Context, q queryer, table string, k TrafficKey) (TrafficRow, bool, error) {
	row := TrafficRow{TrafficKey: k}
	err := q.QueryRowContext(ctx, fmt.Sprintf(trafficSelect, table), k.BucketMs, k.Dimension, k.Object, k.Route, k.Direction).Scan(
		&row.Name, &row.SourceKind, &row.RouteName, &row.UpBytes, &row.DownBytes, &row.NewConns, &row.Failures,
		&row.LatencyMs, &row.LatencyAtMs, &row.MaxLatencyMs, &row.LatencyHist, &row.RuleBasis, &row.Search)
	if errors.Is(err, sql.ErrNoRows) {
		return TrafficRow{}, false, nil
	}
	if err != nil {
		return TrafficRow{}, false, fmt.Errorf("historystore: get %s: %w", table, err)
	}
	return row, true, nil
}

func (t *TrafficTx) putRow(ctx context.Context, table string, r TrafficRow) error {
	_, err := t.tx.ExecContext(ctx, fmt.Sprintf(`INSERT OR REPLACE INTO %s(bucket_ms,dimension,object,route,direction,name,source_kind,route_name,
up_bytes,down_bytes,new_conns,failures,latency_ms,latency_at_ms,max_latency_ms,latency_hist,rule_basis,search)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, table),
		r.BucketMs, r.Dimension, r.Object, r.Route, r.Direction, r.Name, r.SourceKind, r.RouteName, r.UpBytes, r.DownBytes,
		r.NewConns, r.Failures, r.LatencyMs, r.LatencyAtMs, r.MaxLatencyMs, r.LatencyHist, r.RuleBasis, r.Search)
	return wrapPut(table, err)
}

// Traffic5m reads one traffic_5m row inside the transaction, for read-modify-write merging.
func (t *TrafficTx) Traffic5m(ctx context.Context, k TrafficKey) (TrafficRow, bool, error) {
	return getRow(ctx, t.tx, "traffic_5m", k)
}

// PutTraffic5m inserts or fully replaces a traffic_5m row.
func (t *TrafficTx) PutTraffic5m(ctx context.Context, r TrafficRow) error {
	return t.putRow(ctx, "traffic_5m", r)
}

// Recent5s reads one recent_5s row inside the transaction.
func (t *TrafficTx) Recent5s(ctx context.Context, k TrafficKey) (TrafficRow, bool, error) {
	return getRow(ctx, t.tx, "recent_5s", k)
}

// PutRecent5s inserts or fully replaces a recent_5s row.
func (t *TrafficTx) PutRecent5s(ctx context.Context, r TrafficRow) error {
	return t.putRow(ctx, "recent_5s", r)
}

// Coverage reads a bucket's coverage bitmap inside the transaction.
func (t *TrafficTx) Coverage(ctx context.Context, bucketMs int64) (Coverage, bool, error) {
	c := Coverage{BucketMs: bucketMs}
	err := t.tx.QueryRowContext(ctx, "SELECT bits FROM coverage_5m WHERE bucket_ms=?", bucketMs).Scan(&c.Bits)
	if errors.Is(err, sql.ErrNoRows) {
		return Coverage{}, false, nil
	}
	if err != nil {
		return Coverage{}, false, fmt.Errorf("historystore: get coverage: %w", err)
	}
	return c, true, nil
}

// PutCoverage inserts or replaces a bucket's coverage bitmap.
func (t *TrafficTx) PutCoverage(ctx context.Context, c Coverage) error {
	_, err := t.tx.ExecContext(ctx, "INSERT OR REPLACE INTO coverage_5m(bucket_ms,bits) VALUES(?,?)", c.BucketMs, c.Bits)
	return wrapPut("coverage", err)
}

// FlowMark reads a connection's counting mark inside the transaction.
func (t *TrafficTx) FlowMark(ctx context.Context, flowKey string) (FlowMark, bool, error) {
	m := FlowMark{FlowKey: flowKey}
	err := t.tx.QueryRowContext(ctx, "SELECT started,failed,responded,closed,last_at_ms FROM flow_marks WHERE flow_key=?", flowKey).
		Scan(&m.Started, &m.Failed, &m.Responded, &m.Closed, &m.LastAtMs)
	if errors.Is(err, sql.ErrNoRows) {
		return FlowMark{}, false, nil
	}
	if err != nil {
		return FlowMark{}, false, fmt.Errorf("historystore: get flow mark: %w", err)
	}
	return m, true, nil
}

// PutFlowMark inserts or replaces a connection's counting mark.
func (t *TrafficTx) PutFlowMark(ctx context.Context, m FlowMark) error {
	_, err := t.tx.ExecContext(ctx, "INSERT OR REPLACE INTO flow_marks(flow_key,started,failed,responded,closed,last_at_ms) VALUES(?,?,?,?,?,?)",
		m.FlowKey, m.Started, m.Failed, m.Responded, m.Closed, m.LastAtMs)
	return wrapPut("flow mark", err)
}

// FlowMarks returns every counting mark through the read pool. The table only holds
// active connections, so it stays small; the controller uses it to restore the pending delta.
func (t *Traffic) FlowMarks(ctx context.Context) ([]FlowMark, error) {
	rows, err := t.r.QueryContext(ctx, "SELECT flow_key,started,failed,responded,closed,last_at_ms FROM flow_marks")
	if err != nil {
		return nil, fmt.Errorf("historystore: flow marks: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FlowMark
	for rows.Next() {
		var m FlowMark
		if err = rows.Scan(&m.FlowKey, &m.Started, &m.Failed, &m.Responded, &m.Closed, &m.LastAtMs); err != nil {
			return nil, fmt.Errorf("historystore: flow marks: %w", err)
		}
		out = append(out, m)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("historystore: flow marks: %w", err)
	}
	return out, nil
}

// Prune deletes rows older than the given cutoffs in one bounded transaction.
// Call it repeatedly while Pruned.More is true.
func (t *Traffic) Prune(ctx context.Context, cutoffs TrafficCutoffs, limits Limits) (Pruned, error) {
	var targets []pruneTarget
	add := func(at time.Time, table, column, where string) {
		if !at.IsZero() {
			targets = append(targets, pruneTarget{table: table, column: column, before: at.UnixMilli(), where: where})
		}
	}
	add(cutoffs.Aggregates, "search_evidence", "bucket_ms", "resolution=0")
	add(cutoffs.Recent, "search_evidence", "bucket_ms", "resolution=1")
	add(cutoffs.Aggregates, "traffic_5m", "bucket_ms", "")
	add(cutoffs.Aggregates, "coverage_5m", "bucket_ms", "")
	add(cutoffs.Recent, "recent_5s", "bucket_ms", "")
	add(cutoffs.Recent, "recent_cuts", "bucket_ms", "")
	add(cutoffs.ClosedMarks, "flow_marks", "last_at_ms", "closed = 1")
	add(cutoffs.OpenMarks, "flow_marks", "last_at_ms", "closed = 0")
	return t.prune(ctx, targets, limits)
}

// EvictOldest deletes the oldest buckets in batches until LiveBytes is at most capBytes,
// and reports whether eviction happened (HS-R1). Writes are never refused for capacity.
func (t *Traffic) EvictOldest(ctx context.Context, capBytes int64, limits Limits) (bool, error) {
	return t.evictOldest(ctx, capBytes, limits, evictSpec{primaryTable: "traffic_5m", primaryColumn: "bucket_ms", targets: []pruneTarget{
		{table: "traffic_5m", column: "bucket_ms"},
		{table: "search_evidence", column: "bucket_ms"},
		{table: "coverage_5m", column: "bucket_ms"},
		{table: "recent_5s", column: "bucket_ms"},
		{table: "recent_cuts", column: "bucket_ms"},
	}})
}

// RecentCut reads an exact observation-time row for cutoff queries inside a 5-second bucket.
func (t *TrafficTx) RecentCut(ctx context.Context, k TrafficKey) (TrafficRow, bool, error) {
	return getRow(ctx, t.tx, "recent_cuts", k)
}

// PutRecentCut stores a cutoff contribution, atomically with both aggregate resolutions.
func (t *TrafficTx) PutRecentCut(ctx context.Context, r TrafficRow) error {
	return t.putRow(ctx, "recent_cuts", r)
}
