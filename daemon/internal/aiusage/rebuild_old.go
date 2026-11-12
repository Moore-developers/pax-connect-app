package aiusage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

func keyHash(cli, key string) []byte {
	h := sha256.Sum256([]byte(cli + ":" + key))
	return h[:]
}

// openOld opens the working copy of the old database. Versions 3 to 7 share the columns
// the rebuild reads; an older or newer one is not understood, and is refused.
func openOld(ctx context.Context, path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, ErrUnavailable
	}
	var v int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cannot read the old database: %w", err)
	}
	if v == 0 {
		_ = db.Close()
		return nil, nil // an empty file: nothing to carry
	}
	if v < 3 || v > schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("old database has schema version %d; the rebuild reads 3 to %d", v, schemaVersion)
	}
	return db, nil
}

// earliestData finds the first retained time per CLI, from requests or older buckets.
func earliestData(ctx context.Context, old *sql.DB) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	for _, q := range []string{"SELECT cli,min(at) FROM requests GROUP BY cli", "SELECT cli,min(hour) FROM usage_hourly GROUP BY cli"} {
		rows, err := old.QueryContext(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("cannot read the old database: %w", err)
		}
		for rows.Next() {
			var cli string
			var ms int64
			if err = rows.Scan(&cli, &ms); err != nil {
				_ = rows.Close()
				return nil, ErrUnavailable
			}
			if t := time.UnixMilli(ms); out[cli].IsZero() || t.Before(out[cli]) {
				out[cli] = t
			}
		}
		_ = rows.Close()
		if rows.Err() != nil {
			return nil, ErrUnavailable
		}
	}
	return out, nil
}

// scanOld reads one old row. Tokens and time are kept; Profile and price are left for the
// caller to recompute. Timing that an older parser only estimated is dropped.
func scanOld(rows *sql.Rows) (Request, error) {
	var r Request
	var details []byte
	if err := rows.Scan(&r.CLI, &r.Key, &r.At, &r.Session, &r.Segment, &r.Model, &r.Streaming, &r.Succeeded, &r.Error, &r.Input, &r.Read, &r.Write, &r.Output, &r.TPS, &r.Elapsed, &details); err != nil {
		return r, err
	}
	if err := json.Unmarshal(details, &r.Details); err != nil {
		return r, err
	}
	var timing struct{ TimingSource string }
	if err := json.Unmarshal(details, &timing); err != nil {
		return r, err
	}
	if timing.TimingSource != "" && timing.TimingSource != "measured" {
		r.TPS, r.Elapsed = nil, nil
	}
	return r, nil
}
