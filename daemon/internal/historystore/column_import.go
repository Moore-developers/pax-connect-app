package historystore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"pax-connect/daemon/internal/config"
)

// ColumnImport reads a frozen v3 backup, never a live database. Its fingerprint
// binds committed checkpoints to that exact input, including raw/diagnostics.
type ColumnImport struct {
	db          *sql.DB
	fingerprint string
}

// ColumnImportBatch is the bounded input to the controller's production
// projection. It runs inside the same transaction as facts and checkpoints.
type ColumnImportBatch struct {
	Connections        []config.LogConnectionSummary
	Events             []config.HistoryEvent
	RemovedEvents      []config.HistoryEvent
	RemovedConnections []string
}

// ColumnImportProgress counts committed facts. Done concerns facts only; the
// controller separately checkpoints the derived group backfill before cutover.
type ColumnImportProgress struct {
	Connections, Events int64
	Done                bool
}

const columnImportDDL = `CREATE TABLE IF NOT EXISTS col_import(
 source TEXT PRIMARY KEY,connection_key TEXT NOT NULL,event_seq INTEGER NOT NULL,
 connections INTEGER NOT NULL,events INTEGER NOT NULL,phase INTEGER NOT NULL) WITHOUT ROWID;`

const columnImportLegacyVersion = 3

// OpenColumnImport opens an immutable, checkpointed private backup read-only.
// A nonempty WAL is rejected: immutable SQLite would otherwise omit those rows.
func OpenColumnImport(ctx context.Context, path string, owner uint32) (*ColumnImport, error) {
	if exists, err := guardExisting(path, owner); err != nil {
		return nil, err
	} else if !exists {
		return nil, os.ErrNotExist
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() != 0 {
		return nil, errors.New("historystore: column import requires a checkpointed backup")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	buf := make([]byte, 128<<10)
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		var n int
		n, err = f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			err = nil
			break
		}
		if err != nil {
			break
		}
	}
	closeErr := f.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1&_pragma=query_only(1)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = quickCheck(ctx, db); err == nil {
		err = checkVersion(ctx, db, columnImportLegacyVersion)
	}
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &ColumnImport{db: db, fingerprint: hex.EncodeToString(h.Sum(nil))}, nil
}

// Close releases the read-only backup handle.
func (s *ColumnImport) Close() error { return s.db.Close() }

// Batch imports at most limit facts (1..128). Interrupted batches roll back both
// facts and their position. Reopening or repeating a completed import is safe.
// project must not call Columns methods: it owns that store's single writer.
func (s *ColumnImport) Batch(ctx context.Context, dst *Columns, limit int,
	project func(context.Context, *ColumnsTx, ColumnImportBatch) error,
) (ColumnImportProgress, error) {
	var p ColumnImportProgress
	if limit < 1 || limit > 128 {
		return p, errors.New("historystore: column import batch must be 1..128")
	}
	err := dst.Update(ctx, func(tx *ColumnsTx) error {
		if _, err := tx.tx.ExecContext(ctx, columnImportDDL); err != nil {
			return err
		}
		var sources int
		if err := tx.tx.QueryRowContext(ctx, "SELECT count(*) FROM col_import WHERE source<>?", s.fingerprint).Scan(&sources); err != nil {
			return err
		}
		if sources != 0 {
			return errors.New("historystore: column import source changed")
		}
		if _, err := tx.tx.ExecContext(ctx, "INSERT OR IGNORE INTO col_import VALUES(?,'',0,0,0,0)", s.fingerprint); err != nil {
			return err
		}
		var key string
		var seq int64
		var phase int
		if err := tx.tx.QueryRowContext(ctx, "SELECT connection_key,event_seq,connections,events,phase FROM col_import WHERE source=?", s.fingerprint).Scan(&key, &seq, &p.Connections, &p.Events, &phase); err != nil {
			return err
		}
		batch := ColumnImportBatch{}
		var removalBefore int64
		var importGap bool
		if dst.owner != nil {
			if err := tx.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT CAST(value AS INTEGER) FROM meta WHERE key=?),0)", MetaLogRemovalBefore).Scan(&removalBefore); err != nil {
				return err
			}
			if err := tx.tx.QueryRowContext(ctx, "SELECT coalesce((SELECT value FROM meta WHERE key='column_import_gap'),'0')='1'").Scan(&importGap); err != nil {
				return err
			}
		}
		if phase == 0 {
			rows, err := s.db.QueryContext(ctx, "SELECT flow_key,body FROM connections WHERE flow_key>? ORDER BY flow_key LIMIT ?", key, limit)
			if err != nil {
				return err
			}
			for rows.Next() {
				var body []byte
				if err = rows.Scan(&key, &body); err != nil {
					break
				}
				var v config.LogConnectionSummary
				if err = json.Unmarshal(body, &v); err != nil {
					break
				}
				if v.Fact.Key != key {
					err = errors.New("historystore: legacy connection identity mismatch")
					break
				}
				batch.Connections = append(batch.Connections, v)
			}
			readErr := rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
			if readErr != nil {
				return readErr
			}
			imported := batch.Connections[:0]
			for _, v := range batch.Connections {
				if removalBefore > 0 && columnSummaryActivity(v) < removalBefore {
					continue
				}
				// New observations win even if they carried no visible event.
				var exists bool
				if err = tx.tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM col_connections WHERE flow_key=?)", v.Fact.Key).Scan(&exists); err != nil {
					return err
				}
				if exists {
					continue
				}
				if importGap {
					v = config.MarkLogGap(v)
				}
				if err = tx.PutConnection(ctx, v); err != nil {
					return err
				}
				imported = append(imported, v)
			}
			consumed := len(batch.Connections)
			batch.Connections = imported
			p.Connections += int64(consumed)
			if consumed < limit {
				phase = 1
			}
		} else if phase == 1 {
			rows, err := s.db.QueryContext(ctx, "SELECT seq,event_id,body,raw FROM events WHERE seq>? ORDER BY seq LIMIT ?", seq, limit)
			if err != nil {
				return err
			}
			for rows.Next() {
				var id string
				var body, raw []byte
				if err = rows.Scan(&seq, &id, &body, &raw); err != nil {
					break
				}
				var v config.HistoryEvent
				if err = json.Unmarshal(body, &v); err != nil {
					break
				}
				if v.ID != id || seq <= 0 {
					err = errors.New("historystore: legacy event identity mismatch")
					break
				}
				v.Sequence = uint64(seq)
				if len(raw) > 0 {
					if err = json.Unmarshal(raw, &v.Raw); err != nil {
						break
					}
				}
				batch.Events = append(batch.Events, v)
			}
			readErr := rows.Err()
			_ = rows.Close()
			if err != nil {
				return err
			}
			if readErr != nil {
				return readErr
			}
			consumed := len(batch.Events)
			imported := batch.Events[:0]
			for _, v := range batch.Events {
				if removalBefore > 0 && v.At.UnixMilli() < removalBefore {
					continue
				}
				var inserted bool
				if inserted, err = tx.PutEvent(ctx, v); err != nil {
					return err
				}
				if inserted {
					imported = append(imported, v)
				}
			}
			batch.Events = imported
			p.Events += int64(consumed)
			if consumed < limit {
				phase = 2
			}
		}
		if project != nil {
			if err := project(ctx, tx, batch); err != nil {
				if dst.owner != nil {
					return fmt.Errorf("%w: %w", ErrColumnProjection, err)
				}
				return err
			}
		}
		_, err := tx.tx.ExecContext(ctx, "UPDATE col_import SET connection_key=?,event_seq=?,connections=?,events=?,phase=? WHERE source=?", key, seq, p.Connections, p.Events, phase, s.fingerprint)
		p.Done = phase == 2
		return err
	})
	if err != nil {
		return ColumnImportProgress{}, fmt.Errorf("historystore: import columns: %w", err)
	}
	return p, nil
}

// Progress returns persisted progress without starting a write transaction.
func (s *ColumnImport) Progress(ctx context.Context, dst *Columns) (ColumnImportProgress, error) {
	var p ColumnImportProgress
	var phase int
	err := dst.Reader().QueryRowContext(ctx, "SELECT connections,events,phase FROM col_import WHERE source=?", s.fingerprint).Scan(&p.Connections, &p.Events, &phase)
	if err != nil {
		return p, err
	}
	p.Done = phase == 2
	return p, nil
}

// Fingerprint is a stable SHA-256 of the immutable input, for evidence reports.
func (s *ColumnImport) Fingerprint() string { return s.fingerprint }
