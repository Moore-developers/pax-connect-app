package historystore

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// TrafficRead is a WAL read transaction. Its first metadata read pins the database
// version before the controller copies its matching uncommitted memory tail.
type TrafficRead struct{ tx *sql.Tx }

// BeginRead opens a query-only transaction and pins its WAL snapshot.
func (t *Traffic) BeginRead(ctx context.Context) (*TrafficRead, error) {
	tx, err := t.r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM meta").Scan(&n); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return &TrafficRead{tx: tx}, nil
}

// Close releases the read transaction and its WAL snapshot.
func (r *TrafficRead) Close() error { return r.tx.Rollback() }

// Meta reads metadata at this read's pinned version.
func (r *TrafficRead) Meta(ctx context.Context, key string) (string, bool, error) {
	return readMeta(ctx, r.tx, key)
}

// Rows streams aggregates in [from,until), with an optional dimension filter. recent
// selects 5-second boundary rows; false selects the 5-minute history.
func (r *TrafficRead) Rows(ctx context.Context, recent bool, from, until time.Time, dimensions []string, fn func(TrafficRow) error) error {
	table := "traffic_5m"
	if recent {
		table = "recent_cuts"
	}
	query := `SELECT bucket_ms,dimension,object,route,direction,name,source_kind,route_name,up_bytes,down_bytes,new_conns,failures,
 latency_ms,latency_at_ms,max_latency_ms,latency_hist,rule_basis,search FROM ` + table + ` WHERE bucket_ms>=? AND bucket_ms<?`
	args := []any{from.UnixMilli(), until.UnixMilli()}
	if len(dimensions) > 0 {
		query += " AND dimension IN ("
		for i, d := range dimensions {
			if i > 0 {
				query += ","
			}
			query += "?"
			args = append(args, d)
		}
		query += ")"
	}
	query += " ORDER BY bucket_ms,dimension,object,route,direction"
	var legacyBefore int64
	if imported, _, err := r.Meta(ctx, "legacy_route_imported"); err != nil {
		return err
	} else if imported == "1" {
		raw, _, err := r.Meta(ctx, "coverage_start_ms")
		if err != nil {
			return err
		}
		legacyBefore, _ = strconv.ParseInt(raw, 10, 64)
	}
	rows, err := r.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("historystore: traffic range: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v TrafficRow
		if err = rows.Scan(&v.BucketMs, &v.Dimension, &v.Object, &v.Route, &v.Direction, &v.Name, &v.SourceKind, &v.RouteName, &v.UpBytes, &v.DownBytes, &v.NewConns, &v.Failures, &v.LatencyMs, &v.LatencyAtMs, &v.MaxLatencyMs, &v.LatencyHist, &v.RuleBasis, &v.Search); err != nil {
			return err
		}
		v.LegacyGap = v.Dimension == "route" && (v.SourceKind == "legacy" || legacyBefore > 0 && v.BucketMs < legacyBefore)
		if err = fn(v); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Coverage streams the coverage bitmaps intersecting [from,until).
func (r *TrafficRead) Coverage(ctx context.Context, from, until time.Time, fn func(Coverage) error) error {
	rows, err := r.tx.QueryContext(ctx, "SELECT bucket_ms,bits FROM coverage_5m WHERE bucket_ms>=? AND bucket_ms<?", from.Truncate(5*time.Minute).UnixMilli(), until.UnixMilli())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v Coverage
		if err = rows.Scan(&v.BucketMs, &v.Bits); err != nil {
			return err
		}
		if err = fn(v); err != nil {
			return err
		}
	}
	return rows.Err()
}
