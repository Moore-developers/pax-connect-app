package aiusage

import (
	"context"
	"database/sql"
)

// copyRetiredKeys preserves the permanent suppression contract during an explicit
// rebuild. Keys with retained details are rescanned/carried normally; missing details
// must not be resurrected merely because their original source still exists.
func (s *Store) copyRetiredKeys(ctx context.Context, old *sql.DB) error {
	var has int
	if err := old.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='request_keys'").Scan(&has); err != nil {
		return err
	}
	if has == 0 {
		return nil
	}
	retained := map[string]bool{}
	rows, err := old.QueryContext(ctx, "SELECT cli,request_key FROM requests")
	if err != nil {
		return err
	}
	for rows.Next() {
		var cli, key string
		if err = rows.Scan(&cli, &key); err != nil {
			_ = rows.Close()
			return err
		}
		retained[string(keyHash(cli, key))] = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err = old.QueryContext(ctx, "SELECT key_hash,at FROM request_keys")
	if err != nil {
		return err
	}
	for rows.Next() {
		var hash []byte
		var at int64
		if err = rows.Scan(&hash, &at); err != nil {
			_ = rows.Close()
			return err
		}
		if !retained[string(hash)] {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO request_keys VALUES(?,?)", hash, at); err != nil {
				_ = rows.Close()
				return err
			}
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if err = old.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='codex_usage_evidence'").Scan(&has); err != nil {
		return err
	}
	if has > 0 {
		rows, err = old.QueryContext(ctx, "SELECT "+codexEvidenceColumns+" FROM codex_usage_evidence")
		if err != nil {
			return err
		}
		for rows.Next() {
			var e codexStoredEvidence
			if err = rows.Scan(&e.Key, &e.Session, &e.Segment, &e.Turn, &e.Response, &e.Snapshot, &e.Usage, &e.SegmentAt, &e.At, &e.Carry, &e.Fork, &e.Canonical, &e.Model, &e.Conflict); err != nil {
				_ = rows.Close()
				return err
			}
			if e.Canonical != "" && !retained[string(keyHash("codex", e.Canonical))] {
				if err = storeCodexEvidence(ctx, tx, e.Key, e.Canonical, e.codexEvidence); err != nil {
					_ = rows.Close()
					return err
				}
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// The rescan did not reproduce this retained canonical key. Restore only its already
// stored v7 identity group after carryOver has actually written the request, so normal
// source-present rescans are never suppressed by premature evidence imports.
func copyCarriedCodexEvidence(ctx context.Context, old *sql.DB, tx *sql.Tx, key string) error {
	var has int
	if err := old.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='codex_usage_evidence'").Scan(&has); err != nil {
		return err
	}
	if has == 0 {
		return nil
	}
	rows, err := old.QueryContext(ctx, "SELECT "+codexEvidenceColumns+" FROM codex_usage_evidence WHERE canonical_key=?", key)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e codexStoredEvidence
		if err = rows.Scan(&e.Key, &e.Session, &e.Segment, &e.Turn, &e.Response, &e.Snapshot, &e.Usage, &e.SegmentAt, &e.At, &e.Carry, &e.Fork, &e.Canonical, &e.Model, &e.Conflict); err != nil {
			return err
		}
		if err = storeCodexEvidence(ctx, tx, e.Key, e.Canonical, e.codexEvidence); err != nil {
			return err
		}
	}
	return rows.Err()
}
