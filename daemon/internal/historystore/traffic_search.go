package historystore

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const trafficSearchDDL = `CREATE TABLE IF NOT EXISTS search_evidence(
 resolution INTEGER NOT NULL,bucket_ms INTEGER NOT NULL,dimension TEXT NOT NULL,object TEXT NOT NULL,
 route TEXT NOT NULL,direction TEXT NOT NULL,evidence TEXT NOT NULL,
 PRIMARY KEY(resolution,bucket_ms,dimension,object,route,direction,evidence));
 CREATE INDEX IF NOT EXISTS search_evidence_time ON search_evidence(bucket_ms);`

// PutSearchEvidence appends distinct text, without rewriting the aggregate row's
// accumulated index. resolution is 0 for five-minute buckets, 1 for exact cuts.
func (t *TrafficTx) PutSearchEvidence(ctx context.Context, resolution int, key TrafficKey, text string) error {
	if text == "" {
		return nil
	}
	if t.searchInsert == nil {
		var err error
		t.searchInsert, err = t.tx.PrepareContext(ctx, `INSERT OR IGNORE INTO search_evidence VALUES(?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
	}
	_, err := t.searchInsert.ExecContext(ctx, resolution, key.BucketMs, key.Dimension, key.Object, key.Route, key.Direction, strings.ToLower(text))
	return err
}

func (t *TrafficTx) closeStatements() {
	if t.searchInsert != nil {
		_ = t.searchInsert.Close()
	}
}

// MatchingSearch visits object identities whose retained evidence matches term.
// Normalization uses Go Unicode case folding, matching controller search policy.
func (r *TrafficRead) MatchingSearch(ctx context.Context, resolution int, from, until time.Time, dimensions []string, term string, visit func(TrafficKey) error) error {
	query := `SELECT DISTINCT dimension,object,route,direction FROM search_evidence WHERE resolution=? AND bucket_ms>=? AND bucket_ms<? AND instr(evidence,?)>0`
	args := []any{resolution, from.UnixMilli(), until.UnixMilli(), strings.ToLower(term)}
	if len(dimensions) > 0 {
		query += " AND dimension IN (" + strings.TrimSuffix(strings.Repeat("?,", len(dimensions)), ",") + ")"
		for _, d := range dimensions {
			args = append(args, d)
		}
	}
	rows, err := r.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("historystore: search evidence: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key TrafficKey
		if err = rows.Scan(&key.Dimension, &key.Object, &key.Route, &key.Direction); err != nil {
			return err
		}
		if err = visit(key); err != nil {
			return err
		}
	}
	return rows.Err()
}
