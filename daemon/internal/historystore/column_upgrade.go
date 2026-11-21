package historystore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const columnUpgradeBackup = "logs.v3.backup.db"

func verifyUpgradeSourceTable(ctx context.Context, current, backup *sql.DB, table string) error {
	order := "1"
	switch table {
	case "connections":
		order = "flow_key"
	case "events":
		order = "seq"
	case "diagnostics":
		order = "flow_key,seq"
	case "commands":
		order = "id"
	case "job_cursor":
		order = "name"
	case "meta":
		order = "key"
	}
	digest := func(db *sql.DB) ([]byte, error) {
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY "+order)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		cols, err := rows.Columns()
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		for rows.Next() {
			v := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range v {
				dest[i] = &v[i]
			}
			if err = rows.Scan(dest...); err != nil {
				return nil, err
			}
			for _, x := range v {
				var data []byte
				switch value := x.(type) {
				case nil:
					data = []byte("n")
				case int64:
					data = append([]byte("i"), binary.BigEndian.AppendUint64(nil, uint64(value))...)
				case string:
					data = append([]byte("s"), value...)
				case []byte:
					data = append([]byte("b"), value...)
				default:
					return nil, fmt.Errorf("historystore: unexpected backup value %T", value)
				}
				_, _ = h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
				_, _ = h.Write(data)
			}
		}
		return h.Sum(nil), rows.Err()
	}
	a, err := digest(current)
	if err != nil {
		return err
	}
	b, err := digest(backup)
	if err != nil {
		return err
	}
	if strings.Compare(string(a), string(b)) != 0 {
		return errors.New("historystore: existing backup does not match v3 source")
	}
	return nil
}

const columnAuxDDL = `CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS diagnostics(flow_key TEXT NOT NULL,seq INTEGER NOT NULL,at_ms INTEGER NOT NULL,text TEXT NOT NULL,PRIMARY KEY(flow_key,seq));
CREATE INDEX IF NOT EXISTS diagnostics_at ON diagnostics(at_ms);
CREATE TABLE IF NOT EXISTS commands(id TEXT PRIMARY KEY,signature TEXT NOT NULL DEFAULT '',at_ms INTEGER NOT NULL,result BLOB);
CREATE TABLE IF NOT EXISTS job_cursor(name TEXT PRIMARY KEY,session TEXT NOT NULL,seq INTEGER NOT NULL) WITHOUT ROWID;
INSERT OR IGNORE INTO meta VALUES('schema_version','4'),('retention_hours','24'),('settings_revision','0'),('logs_revision','0'),('log_deletion_generation','0'),('summary_generation',''),('clear_generation','0'),('log_cleared_at','0'),('journal_generation',''),('partial','0'),('error_code',''),('capacity_evicted','0'),('column_head_sequence','0');`

func openColumnLogs(ctx context.Context, path string, owner uint32) (*store, Recovered, error) {
	var recovered Recovered
	for attempt := 0; ; attempt++ {
		s, err := tryOpenColumnLogs(ctx, path, owner)
		if err == nil {
			return s, recovered, nil
		}
		if attempt > 0 || !isCorrupt(err) {
			return nil, recovered, err
		}
		target, e := quarantine(path, "corrupt")
		if e != nil {
			return nil, recovered, e
		}
		recovered = Recovered{Quarantined: target}
	}
}

func tryOpenColumnLogs(ctx context.Context, path string, owner uint32) (*store, error) {
	if err := guardFile(path, owner); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", dataSource(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	s := &store{path: path, w: w}
	ok := false
	defer func() {
		if !ok {
			_ = s.Close()
		}
	}()
	if err = s.quickCheck(ctx); err != nil {
		return nil, err
	}
	var count int
	if err = w.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='meta'").Scan(&count); err != nil {
		return nil, err
	}
	version := ""
	if count != 0 {
		if err = w.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='schema_version'").Scan(&version); err != nil {
			return nil, err
		}
		if version != "3" && version != "4" {
			return nil, fmt.Errorf("%w: logs %s", ErrSchemaVersion, version)
		}
	}
	if _, err = w.ExecContext(ctx, "PRAGMA auto_vacuum=INCREMENTAL;PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	if version == "3" {
		if err = prepareColumnUpgrade(ctx, s, owner); err != nil {
			if errors.Is(err, ErrUnsafeFile) || isCorrupt(err) {
				return nil, err
			}
			// A backup/import failure degrades queries while the new-table
			// collector remains usable. Original v3 facts stay in place.
			if err = markColumnUpgradeFailed(ctx, s); err != nil {
				return nil, err
			}
		}
	}
	if _, err = w.ExecContext(ctx, columnAuxDDL); err != nil {
		return nil, err
	}
	if err = initializeColumnFacts(ctx, s); err != nil {
		return nil, err
	}
	if err = (&Columns{store: s}).EnsureProjections(ctx); err != nil {
		return nil, err
	}
	s.r, err = openReader(path, readerConns)
	if err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}

func markColumnUpgradeFailed(ctx context.Context, s *store) error {
	var head int64
	if err := s.w.QueryRowContext(ctx, "SELECT coalesce((SELECT seq FROM sqlite_sequence WHERE name='events'),0)").Scan(&head); err != nil {
		return err
	}
	return s.update(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, columnAuxDDL); err != nil {
			return err
		}
		if err := setMeta(ctx, tx, "column_head_sequence", strconv.FormatInt(head, 10)); err != nil {
			return err
		}
		if err := setMeta(ctx, tx, "schema_version", "4"); err != nil {
			return err
		}
		return setMeta(ctx, tx, "column_upgrade_state", "failed")
	})
}

func prepareColumnUpgrade(ctx context.Context, s *store, owner uint32) error {
	backup := filepath.Join(filepath.Dir(s.path), columnUpgradeBackup)
	exists, err := guardExisting(backup, owner)
	if err != nil {
		return err
	}
	if !exists {
		required, e := s.LiveBytes(ctx)
		if e != nil {
			return e
		}
		var stat syscall.Statfs_t
		if err = syscall.Statfs(filepath.Dir(s.path), &stat); err != nil {
			return err
		}
		if int64(stat.Bavail)*int64(stat.Bsize) < required {
			return errors.New("historystore: insufficient upgrade space")
		}
		// VACUUM INTO reads the full committed database, including any recovered WAL.
		// Exclusive creation prevents following a symlink or overwriting a backup.
		temporary := backup + ".pending"
		if exists, e := guardExisting(temporary, owner); e != nil {
			return e
		} else if exists {
			if e = os.Remove(temporary); e != nil {
				return e
			}
		}
		f, e := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
		if _, err = s.w.ExecContext(ctx, "VACUUM INTO ?", temporary); err != nil {
			return err
		}
		if err = os.Rename(temporary, backup); err != nil {
			return err
		}
		f, e = os.Open(backup)
		if e != nil {
			return e
		}
		e = f.Sync()
		_ = f.Close()
		if e != nil {
			return e
		}
		directory, e := os.Open(filepath.Dir(backup))
		if e != nil {
			return e
		}
		e = directory.Sync()
		_ = directory.Close()
		if e != nil {
			return e
		}
	}
	src, err := OpenColumnImport(ctx, backup, owner)
	if err != nil {
		return fmt.Errorf("historystore: verify upgrade backup: %w", err)
	}
	defer func() { _ = src.Close() }()
	if exists {
		// A backup from a previously aborted attempt is accepted only while the
		// original facts still agree; never silently import stale user history.
		for _, table := range []string{"connections", "events", "diagnostics", "commands", "job_cursor", "meta"} {
			if err = verifyUpgradeSourceTable(ctx, s.w, src.db, table); err != nil {
				return err
			}
		}
	}
	var head int64
	if err = s.w.QueryRowContext(ctx, "SELECT coalesce((SELECT seq FROM sqlite_sequence WHERE name='events'),0)").Scan(&head); err != nil {
		return err
	}
	return s.update(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, columnAuxDDL); e != nil {
			return e
		}
		if e := setMeta(ctx, tx, "column_head_sequence", strconv.FormatInt(head, 10)); e != nil {
			return e
		}
		if e := setMeta(ctx, tx, "column_upgrade_backup", columnUpgradeBackup); e != nil {
			return e
		}
		if e := setMeta(ctx, tx, "column_upgrade_fingerprint", src.Fingerprint()); e != nil {
			return e
		}
		if e := setMeta(ctx, tx, "column_upgrade_state", "building"); e != nil {
			return e
		}
		return setMeta(ctx, tx, "schema_version", "4")
	})
}

// UpgradeState is independent of collection state: sampling continues while old
// facts are imported and queries explicitly report history_converting.
func (l *Logs) UpgradeState(ctx context.Context) (string, error) {
	v, _, err := l.Meta(ctx, "column_upgrade_state")
	return v, err
}

// Ready rejects incomplete query material without stopping collection.
func (l *Logs) Ready(ctx context.Context) error {
	projection, _, err := l.Meta(ctx, "column_projection_state")
	if err != nil {
		return err
	}
	if projection == "failed" {
		return errors.New("history_store_failed")
	}
	state, err := l.UpgradeState(ctx)
	if err != nil {
		return err
	}
	if state == "building" {
		return errors.New("history_converting")
	}
	if state == "failed" {
		return errors.New("history_store_failed")
	}
	if ready, err := l.columns.TrafficProjectionReady(ctx); err != nil {
		return err
	} else if !ready {
		return errors.New("history_building")
	}
	return nil
}

// PrepareUpgrade opens the verified backup once before the owner starts its
// batch loop. It returns false for an already ready library.
func (l *Logs) PrepareUpgrade(ctx context.Context, owner uint32) (bool, error) {
	state, err := l.UpgradeState(ctx)
	if err != nil {
		return false, err
	}
	if state == "" || state == "ready" {
		return false, nil
	}
	if state == "failed" {
		return false, errors.New("history_store_failed")
	}
	if l.importer != nil {
		return true, nil
	}
	src, err := OpenColumnImport(ctx, filepath.Join(filepath.Dir(l.path), columnUpgradeBackup), owner)
	if err != nil {
		return false, l.failUpgrade(ctx, err)
	}
	expected, _, err := l.Meta(ctx, "column_upgrade_fingerprint")
	if err != nil {
		_ = src.Close()
		return false, l.failUpgrade(ctx, err)
	}
	if expected != src.Fingerprint() {
		_ = src.Close()
		return false, l.failUpgrade(ctx, errors.New("historystore: backup fingerprint changed"))
	}
	l.importer = src
	return true, nil
}

// UpgradeBatch commits at most128 old facts and their supplied projections. The
// log owner interleaves calls with normal journal consumption and maintenance.
func (l *Logs) UpgradeBatch(ctx context.Context, project func(context.Context, *ColumnsTx, ColumnImportBatch) error) (bool, error) {
	if l.importer == nil {
		return true, nil
	}
	projection, _, err := l.Meta(ctx, "column_projection_state")
	if err != nil {
		return false, err
	}
	if projection == "failed" {
		return false, l.failUpgrade(ctx, ErrColumnProjection)
	}
	state, err := l.UpgradeState(ctx)
	if err != nil {
		return false, err
	}
	if state == "ready" {
		return true, nil
	}
	p, err := l.importer.Batch(ctx, l.columns, 128, project)
	if err != nil {
		return false, l.failUpgrade(ctx, err)
	}
	if p.Done {
		if err = l.Update(ctx, func(tx *LogsTx) error { return tx.SetMeta(ctx, "column_upgrade_phase", "projection") }); err != nil {
			return false, l.failUpgrade(ctx, err)
		}
	}
	return p.Done, nil
}

// UpgradeFinalize verifies the import before deleting old facts. Call after the
// controller's derived-row rebuild is complete; background vacuum may follow.
func (l *Logs) UpgradeFinalize(ctx context.Context) error {
	if l.importer == nil {
		return nil
	}
	projection, _, err := l.Meta(ctx, "column_projection_state")
	if err != nil {
		return err
	}
	if projection == "failed" {
		return l.failUpgrade(ctx, ErrColumnProjection)
	}
	state, err := l.UpgradeState(ctx)
	if err != nil {
		return err
	}
	if state == "ready" {
		err = l.importer.Close()
		l.importer = nil
		return err
	}
	if err := verifyColumnImport(ctx, l.columns, l.importer); err != nil {
		return l.failUpgrade(ctx, err)
	}
	err = l.Update(ctx, func(tx *LogsTx) error {
		for _, table := range []string{"events", "connections"} {
			if _, e := tx.tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); e != nil {
				return e
			}
		}
		if _, e := tx.tx.ExecContext(ctx, `UPDATE meta SET value=CAST(CAST(value AS INTEGER)+1 AS TEXT) WHERE key=?`, MetaLogsRevision); e != nil {
			return e
		}
		if e := tx.SetMeta(ctx, "column_upgrade_phase", "complete"); e != nil {
			return e
		}
		return tx.SetMeta(ctx, "column_upgrade_state", "ready")
	})
	if err != nil {
		return l.failUpgrade(ctx, err)
	}
	err = l.importer.Close()
	l.importer = nil
	return err
}

func (l *Logs) failUpgrade(ctx context.Context, err error) error {
	if ctx.Err() == nil {
		_ = l.Update(context.WithoutCancel(ctx), func(tx *LogsTx) error {
			return tx.SetMeta(context.WithoutCancel(ctx), "column_upgrade_state", "failed")
		})
	}
	return err
}

// Close releases both the read-only upgrade input and current log database.
func (l *Logs) Close() error {
	var err error
	if l.importer != nil {
		err = l.importer.Close()
		l.importer = nil
	}
	return errors.Join(err, l.columns.Close())
}

func verifyColumnImport(ctx context.Context, dst *Columns, _ *ColumnImport) error {
	// The old tables have remained immutable since the verified backup was made.
	// SQL does the cross-check by indexed identity, avoiding one Go/SQL round trip
	// per event while the owner is finalizing its derived rows.
	var removalBefore int64
	if err := dst.r.QueryRowContext(ctx, "SELECT coalesce((SELECT CAST(value AS INTEGER) FROM meta WHERE key=?),0)", MetaLogRemovalBefore).Scan(&removalBefore); err != nil {
		return err
	}
	var mismatch bool
	if err := dst.r.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events old LEFT JOIN col_events n ON n.event_id=old.event_id WHERE old.at_ms>=? AND(n.seq IS NULL OR n.seq<>old.seq))`, removalBefore).Scan(&mismatch); err != nil {
		return err
	}
	if mismatch {
		return errors.New("historystore: imported event identity or sequence mismatch")
	}
	if err := dst.r.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM connections old LEFT JOIN col_connections n ON n.flow_key=old.flow_key WHERE old.last_activity_ms>=? AND(n.flow_key IS NULL OR n.last_sequence<coalesce(json_extract(old.body,'$.LastSequence'),0)))`, removalBefore).Scan(&mismatch); err != nil {
		return err
	}
	if mismatch {
		return errors.New("historystore: imported summary missing or stale")
	}
	return nil
}
