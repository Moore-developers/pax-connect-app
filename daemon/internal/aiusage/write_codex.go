package aiusage

import (
	"context"
	"database/sql"
	"time"
)

func codexMarker(ctx context.Context, tx *sql.Tx, r Request) (bool, error) {
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO request_keys VALUES(?,?)", keyHash(r.CLI, r.Key), r.At)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// commitCodex shares the existing single writer/transaction with cursor and buckets.
// Only evidenced aliases can replace another key; permanent markers always survive.
func commitCodex(ctx context.Context, tx *sql.Tx, stmt *sql.Stmt, r Request, grouped map[bucketKey]Bucket, attr attributor) (int, bool, error) {
	repaired, err := repairCodexOwner(ctx, tx, r)
	if err != nil {
		return 0, false, err
	}
	if repaired {
		return 1, true, nil
	}
	evidence := *r.codex
	if evidence.Refine {
		return refineCodex(ctx, tx, stmt, r, grouped, attr)
	}
	_, exists, err := readRequest(ctx, tx, "codex", r.Key)
	if err != nil {
		return 0, false, err
	}
	known, err := codexEvidenceRows(ctx, tx, "request_key=?", r.Key)
	if err != nil {
		return 0, false, err
	}
	if len(known) > 0 {
		return 0, false, nil
	}
	if exists {
		// An old segment key (including a manually corrected one) is authoritative.
		return 0, false, storeCodexEvidence(ctx, tx, r.Key, r.Key, evidence)
	}
	fresh, err := codexMarker(ctx, tx, r)
	if err != nil {
		return 0, false, err
	}
	if !fresh {
		// A deleted/expired key is not an invitation to restore its request or amounts.
		return 0, false, storeCodexEvidence(ctx, tx, r.Key, "", evidence)
	}
	canonical, err := matchCodex(ctx, tx, evidence)
	if err != nil {
		return 0, false, err
	}
	if canonical != "" {
		old, present, err := readRequest(ctx, tx, "codex", canonical)
		if err != nil {
			return 0, false, err
		}
		if !present {
			return 0, false, storeCodexEvidence(ctx, tx, r.Key, canonical, evidence)
		}
		previous, err := codexEvidenceRows(ctx, tx, "request_key=?", canonical)
		if err != nil {
			return 0, false, err
		}
		// A carry read first loses to its causally earlier source, regardless of worker order.
		replace := len(previous) == 1 && previous[0].Carry && r.At <= previous[0].SegmentAt && r.At < old.At
		if replace {
			accumulate(grouped, old, -1)
			if _, err = tx.ExecContext(ctx, "DELETE FROM requests WHERE cli='codex' AND request_key=?", canonical); err != nil {
				return 0, false, err
			}
			if err = writeRequest(ctx, stmt, r); err != nil {
				return 0, false, err
			}
			accumulate(grouped, r, 1)
			if _, err = tx.ExecContext(ctx, "UPDATE codex_usage_evidence SET canonical_key=? WHERE canonical_key=?", r.Key, canonical); err != nil {
				return 0, false, err
			}
			return 1, true, storeCodexEvidence(ctx, tx, r.Key, r.Key, evidence)
		}
		if err = storeCodexEvidence(ctx, tx, r.Key, canonical, evidence); err != nil {
			return 0, false, err
		}
		if evidence.Response != "" {
			evidence.Refine = true
			r.codex = &evidence
			return refineCodex(ctx, tx, stmt, r, grouped, attr)
		}
		return 0, false, nil
	}
	if err = writeRequest(ctx, stmt, r); err != nil {
		return 0, false, err
	}
	accumulate(grouped, r, 1)
	return 1, false, storeCodexEvidence(ctx, tx, r.Key, r.Key, evidence)
}

func refineCodex(ctx context.Context, tx *sql.Tx, stmt *sql.Stmt, update Request, grouped map[bucketKey]Bucket, attr attributor) (int, bool, error) {
	e := update.codex
	rows, err := codexEvidenceRows(ctx, tx, "request_key=? AND session=? AND response_id=? AND turn_id=?", update.Key, e.Session, e.Response, e.Turn)
	if err != nil || len(rows) != 1 {
		return 0, false, err
	}
	old, present, err := readRequest(ctx, tx, "codex", rows[0].Canonical)
	if err != nil || !present {
		return 0, false, err
	}
	canonical := rows[0].Canonical
	var conflicting int
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM codex_usage_evidence WHERE canonical_key=? AND (conflict=1 OR (session=? AND response_id=? AND turn_id=? AND usage_hash=? AND response_id<>'' AND model<>'' AND model<>'unknown' AND ?<>'' AND ?<>'unknown' AND model<>?)))", canonical, rows[0].Session, rows[0].Response, rows[0].Turn, rows[0].Usage, update.Model, update.Model, update.Model).Scan(&conflicting); err != nil {
		return 0, false, err
	}
	conflict := e.Conflict || conflicting != 0
	if conflict {
		if _, err = tx.ExecContext(ctx, "UPDATE codex_usage_evidence SET conflict=1 WHERE canonical_key=?", canonical); err != nil {
			return 0, false, err
		}
		update.Model = "unknown"
	} else if update.Model != "" && update.Model != "unknown" {
		if _, err = tx.ExecContext(ctx, "UPDATE codex_usage_evidence SET model=? WHERE request_key=?", update.Model, update.Key); err != nil {
			return 0, false, err
		}
	}
	if old.Model == update.Model || update.Model == "" || (old.Model != "unknown" && !conflict) || (update.Model == "unknown" && !conflict) {
		return 0, false, nil
	}
	if !conflict && old.Details.PriceVersion != priceVersion {
		return 0, false, nil
	}
	next := old
	next.Model = update.Model
	// Keep the original Profile and time. Resolve that historical snapshot for this model,
	// then apply the latest explicit override covering that original request.
	if attr.resolve != nil && !conflict {
		historical := attr.resolve("codex", time.UnixMilli(old.At))
		if historical.ProfileID == old.ProfileID {
			next.Multiplier = historical.ForModel(next.Model)
		}
	}
	if !conflict {
		next.Multiplier = attr.multiplier(old.ProfileID, next.Model, old.At, next.Multiplier)
	}
	next.price()
	accumulate(grouped, old, -1)
	accumulate(grouped, next, 1)
	if err = writeRequest(ctx, stmt, next); err != nil {
		return 0, false, err
	}
	return 1, true, nil
}

func pruneEmptyCodexBuckets(ctx context.Context, tx *sql.Tx, grouped map[bucketKey]Bucket) error {
	for key, b := range grouped {
		if key.CLI != "codex" || b.Count > 0 {
			continue
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM usage_hourly WHERE hour=? AND cli=? AND profile_id=? AND model=? AND count=0 AND success=0 AND input=0 AND read=0 AND write=0 AND output=0 AND cost=0 AND c0=0 AND c1=0 AND c2=0 AND c3=0 AND b0=0 AND b1=0 AND b2=0 AND b3=0 AND tps_count=0 AND elapsed_count=0 AND abs(tps_sum)<0.000001 AND abs(elapsed_sum)<0.000001", key.At, key.CLI, key.Profile, key.Model)
		if err != nil {
			return err
		}
	}
	return nil
}
