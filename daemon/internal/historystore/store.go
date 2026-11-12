package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"modernc.org/sqlite"

	"pax-connect/daemon/internal/storage"
)

const (
	busyTimeoutMs = 5000
	readerConns   = 4
	// sqliteCorrupt and sqliteNotADB are the primary result codes that mean the file itself is unusable.
	sqliteCorrupt = 11
	sqliteNotADB  = 26
)

var errIntegrity = errors.New("historystore: integrity check failed")

// storeSpec describes one database file: its tables and the reader pool size (0 = no pool).
type storeSpec struct {
	ddl     string
	version int
	readers int
}

// store is one SQLite file with a single writer connection and a read-only pool.
type store struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

// prepareDirectory makes directory 0700 and owned by owner via the storage package.
func prepareDirectory(directory string, owner uint32) error {
	if err := storage.PreparePrivateDirectory(directory, owner); err != nil {
		return fmt.Errorf("historystore: directory: %w", err)
	}
	return nil
}

func dataSource(path string, readOnly bool) string {
	query := url.Values{}
	query.Add("_pragma", "busy_timeout("+strconv.Itoa(busyTimeoutMs)+")")
	if readOnly {
		query.Set("mode", "ro")
		query.Add("_pragma", "query_only(1)")
		// Bound the hot page working set per reader, avoiding repeated pread
		// across retained connection summaries and traffic ranges.
		query.Add("_pragma", "cache_size(-16384)")
	} else {
		query.Add("_pragma", "synchronous(NORMAL)")
		query.Set("_txlock", "immediate")
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
}

func isCorrupt(err error) bool {
	if errors.Is(err, errIntegrity) {
		return true
	}
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		code := sqliteErr.Code() & 0xff
		return code == sqliteCorrupt || code == sqliteNotADB
	}
	return false
}

// openStore opens or creates path. A corrupt file is quarantined and replaced
// once (HS-R6); a schema version mismatch returns ErrSchemaVersion untouched.
func openStore(ctx context.Context, path string, owner uint32, spec storeSpec) (*store, Recovered, error) {
	var recovered Recovered
	for attempt := 0; ; attempt++ {
		s, err := tryOpen(ctx, path, owner, spec)
		if err == nil {
			return s, recovered, nil
		}
		if attempt > 0 || !isCorrupt(err) {
			return nil, recovered, err
		}
		target, qerr := quarantine(path, "corrupt")
		if qerr != nil {
			return nil, recovered, qerr
		}
		recovered = Recovered{Quarantined: target}
	}
}

func tryOpen(ctx context.Context, path string, owner uint32, spec storeSpec) (*store, error) {
	if err := guardFile(path, owner); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", dataSource(path, false))
	if err != nil {
		return nil, fmt.Errorf("historystore: open writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	s := &store{path: path, w: w}
	if err = s.initialize(ctx, spec); err != nil {
		_ = w.Close()
		return nil, err
	}
	if spec.readers > 0 {
		s.r, err = openReader(path, spec.readers)
		if err != nil {
			_ = w.Close()
			return nil, err
		}
	}
	return s, nil
}

// openReader opens a read-only pool on an existing database file.
func openReader(path string, conns int) (*sql.DB, error) {
	r, err := sql.Open("sqlite", dataSource(path, true))
	if err != nil {
		return nil, fmt.Errorf("historystore: open reader: %w", err)
	}
	r.SetMaxOpenConns(conns)
	r.SetMaxIdleConns(conns)
	return r, nil
}

// initialize checks integrity, fixes the persistent pragmas (auto_vacuum must
// precede table creation) and creates or verifies the schema.
func (s *store) initialize(ctx context.Context, spec storeSpec) error {
	if err := s.quickCheck(ctx); err != nil {
		return err
	}
	if _, err := s.w.ExecContext(ctx, "PRAGMA auto_vacuum=INCREMENTAL"); err != nil {
		return fmt.Errorf("historystore: auto_vacuum: %w", err)
	}
	var mode string
	if err := s.w.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil || mode != "wal" {
		return fmt.Errorf("historystore: journal_mode %q: %w", mode, err)
	}
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("historystore: schema: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var tables int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table'").Scan(&tables); err != nil {
		return fmt.Errorf("historystore: schema: %w", err)
	}
	if tables == 0 {
		if _, err = tx.ExecContext(ctx, "CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;"+spec.ddl); err != nil {
			return fmt.Errorf("historystore: schema: %w", err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES('schema_version',?)", strconv.Itoa(spec.version)); err != nil {
			return fmt.Errorf("historystore: schema: %w", err)
		}
	} else if err = checkVersion(ctx, tx, spec.version); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("historystore: schema: %w", err)
	}
	return nil
}

// checkVersion returns ErrSchemaVersion unless meta.schema_version equals want.
func checkVersion(ctx context.Context, q queryer, want int) error {
	var value string
	err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='schema_version'").Scan(&value)
	if err != nil {
		if isCorrupt(err) {
			return err
		}
		return fmt.Errorf("%w: unreadable version", ErrSchemaVersion)
	}
	if value != strconv.Itoa(want) {
		return fmt.Errorf("%w: have %s want %d", ErrSchemaVersion, value, want)
	}
	return nil
}

// quickCheck runs PRAGMA quick_check and reports errIntegrity unless the first row is "ok".
func (s *store) quickCheck(ctx context.Context) error {
	return quickCheck(ctx, s.w)
}

func quickCheck(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errIntegrity
	}
	return nil
}

// update runs fn in one immediate write transaction on the writer connection.
// fn must not call other methods of the same store: it holds the only writer.
func (s *store) update(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("historystore: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("historystore: commit: %w", err)
	}
	return nil
}

// Reader returns the read-only pool (at most 4 connections). Callers must keep read transactions short.
func (s *store) Reader() *sql.DB { return s.r }

// Close checkpoints the WAL best-effort and closes both pools.
func (s *store) Close() error {
	var errs []error
	if s.r != nil {
		errs = append(errs, s.r.Close())
	}
	_, _ = s.w.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
	errs = append(errs, s.w.Close())
	return errors.Join(errs...)
}
