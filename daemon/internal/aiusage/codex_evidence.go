package aiusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
)

const codexEvidenceSchema = `
CREATE TABLE IF NOT EXISTS codex_usage_evidence(request_key TEXT PRIMARY KEY,session TEXT NOT NULL,segment TEXT NOT NULL,turn_id TEXT NOT NULL,response_id TEXT NOT NULL,snapshot TEXT NOT NULL,usage_hash TEXT NOT NULL,segment_at INTEGER NOT NULL,at INTEGER NOT NULL,carry INTEGER NOT NULL,fork INTEGER NOT NULL,canonical_key TEXT NOT NULL,model TEXT NOT NULL,conflict INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS codex_response ON codex_usage_evidence(session,response_id) WHERE response_id<>'';
CREATE INDEX IF NOT EXISTS codex_snapshot ON codex_usage_evidence(session,snapshot) WHERE snapshot<>'';
CREATE INDEX IF NOT EXISTS codex_canonical ON codex_usage_evidence(canonical_key);`
const codexEvidenceColumns = "request_key,session,segment,turn_id,response_id,snapshot,usage_hash,segment_at,at,carry,fork,canonical_key,model,conflict"

type codexStoredEvidence struct {
	codexEvidence
	Key, Canonical string
}

func storeCodexEvidence(ctx context.Context, tx *sql.Tx, key, canonical string, e codexEvidence) error {
	_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO codex_usage_evidence("+codexEvidenceColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", key, e.Session, e.Segment, e.Turn, e.Response, e.Snapshot, e.Usage, e.SegmentAt, e.At, e.Carry, e.Fork, canonical, e.Model, e.Conflict)
	return err
}
func codexEvidenceRows(ctx context.Context, tx *sql.Tx, where string, args ...any) ([]codexStoredEvidence, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+codexEvidenceColumns+" FROM codex_usage_evidence WHERE "+where+" LIMIT 3", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []codexStoredEvidence
	for rows.Next() {
		var e codexStoredEvidence
		if err = rows.Scan(&e.Key, &e.Session, &e.Segment, &e.Turn, &e.Response, &e.Snapshot, &e.Usage, &e.SegmentAt, &e.At, &e.Carry, &e.Fork, &e.Canonical, &e.Model, &e.Conflict); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// matchCodex never uses token equality alone. Response IDs must have matching usage;
// no-ID carries need a complete cumulative snapshot from before the new segment began.
func matchCodex(ctx context.Context, tx *sql.Tx, e codexEvidence) (string, error) {
	if e.Session == "" {
		return "", nil
	}
	if e.Response != "" {
		rows, err := codexEvidenceRows(ctx, tx, "session=? AND response_id=?", e.Session, e.Response)
		if err != nil {
			return "", err
		}
		for _, old := range rows {
			if old.Usage == e.Usage && old.Turn == e.Turn && old.Canonical != "" {
				return old.Canonical, nil
			}
		}
	}
	if e.Fork || e.Snapshot == "" {
		return "", nil
	}
	rows, err := codexEvidenceRows(ctx, tx, "session=? AND snapshot=?", e.Session, e.Snapshot)
	if err != nil {
		return "", err
	}
	if len(rows) == 3 {
		return "", nil
	} // ambiguous/busy snapshot: retain rather than guess
	var canonical string
	for _, old := range rows {
		if old.Canonical == "" || old.Fork || old.Segment == e.Segment || old.Usage != e.Usage {
			continue
		}
		if old.Response != "" && e.Response != "" && old.Response != e.Response {
			continue
		}
		causal := e.Carry && old.At <= e.SegmentAt || old.Carry && e.At <= old.SegmentAt
		if !causal {
			continue
		}
		if canonical != "" && canonical != old.Canonical {
			return "", nil
		}
		canonical = old.Canonical
	}
	return canonical, nil
}

// seedCodexEvidence adds only an existing cursor's terminal request. It cannot restore
// missing rows/tombstones, replay files, or infer a historical model from current state.
func seedCodexEvidence(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "SELECT state FROM cursors")
	if err != nil {
		return err
	}
	var states []parseState
	for rows.Next() {
		var raw []byte
		var s parseState
		if err = rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return err
		}
		if json.Unmarshal(raw, &s) != nil {
			_ = rows.Close()
			return ErrUnavailable
		}
		if s.Session != "" && s.Segment != "" && s.Sequence > 0 && s.Total != "" {
			states = append(states, s)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, s := range states {
		key := s.Segment + ":" + strconv.FormatInt(s.Sequence, 10)
		r, ok, err := readRequest(ctx, tx, "codex", key)
		if err != nil {
			return err
		}
		if !ok || r.Session != s.Session || r.Segment != s.Segment {
			continue
		}
		var total codexUsage
		if json.Unmarshal([]byte(s.Total), &total) != nil || !total.complete() {
			continue
		}
		counts := [4]int64{r.Input, r.Read, r.Write, r.Output}
		if !s.Codex.LastUsage.complete() || s.Codex.LastUsage.counts() != counts {
			continue
		}
		evidence := codexEvidence{Session: r.Session, Segment: r.Segment, At: r.At, SegmentAt: s.SegmentAt, Usage: codexHash(s.Codex.LastUsage), Snapshot: codexHash(struct {
			Total codexUsage
			Last  codexUsage
		}{total, s.Codex.LastUsage}), Fork: s.Codex.Fork, Model: r.Model, Conflict: s.Codex.Conflict}
		if err = storeCodexEvidence(ctx, tx, key, key, evidence); err != nil {
			return err
		}
	}
	return nil
}
