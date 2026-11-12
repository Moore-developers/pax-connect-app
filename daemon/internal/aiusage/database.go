package aiusage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
	"pax-connect/daemon/internal/storage"
)

// Store owns one writer, a small read pool and the bounded collector lifecycle.
type Store struct {
	w, r       *sql.DB
	options    Options
	mu         sync.Mutex
	scanMu     sync.Mutex
	state      State
	foreground func() bool
	wake       chan struct{}
	changed    chan struct{}
	scanned    bool
	lastPrune  time.Time // guarded by scanMu
	stamps     map[string]stamp
	stampMu    sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
}

// schemaVersion is PRAGMA user_version. A database from before version 5 is never migrated
// by the daemon: it reports ErrRebuildCode until `make ai-usage-rebuild` runs.
// Versions 5–7 gain additive tables/indexes atomically; analysis enrichment uses its own cursor flag.
const schemaVersion = 12

const hourlySchema = `CREATE TABLE IF NOT EXISTS usage_hourly(hour INTEGER NOT NULL,cli TEXT NOT NULL,profile_id TEXT NOT NULL,model TEXT NOT NULL,profile_name TEXT NOT NULL,count INTEGER NOT NULL,success INTEGER NOT NULL,input INTEGER NOT NULL,read INTEGER NOT NULL,write INTEGER NOT NULL,output INTEGER NOT NULL,c0 INTEGER NOT NULL,c1 INTEGER NOT NULL,c2 INTEGER NOT NULL,c3 INTEGER NOT NULL,cost INTEGER NOT NULL,tps_sum REAL NOT NULL,elapsed_sum REAL NOT NULL,tps_count INTEGER NOT NULL,elapsed_count INTEGER NOT NULL,b0 INTEGER NOT NULL,b1 INTEGER NOT NULL,b2 INTEGER NOT NULL,b3 INTEGER NOT NULL,service_source TEXT NOT NULL DEFAULT '',unconfirmed_channel INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(hour,cli,profile_id,model,service_source));`

const schema = `
CREATE TABLE IF NOT EXISTS requests(cli TEXT NOT NULL,request_key TEXT NOT NULL,at INTEGER NOT NULL,day TEXT NOT NULL,session TEXT NOT NULL,segment TEXT NOT NULL,profile_id TEXT NOT NULL,profile_name TEXT NOT NULL,model TEXT NOT NULL,streaming INTEGER NOT NULL,succeeded INTEGER NOT NULL,error TEXT NOT NULL,input INTEGER NOT NULL,read INTEGER NOT NULL,write INTEGER NOT NULL,output INTEGER NOT NULL,input_price REAL,output_price REAL,multiplier REAL NOT NULL,tps REAL,elapsed REAL,cost INTEGER NOT NULL,c0 INTEGER NOT NULL,c1 INTEGER NOT NULL,c2 INTEGER NOT NULL,c3 INTEGER NOT NULL,details TEXT NOT NULL DEFAULT '{}',PRIMARY KEY(cli,request_key));
CREATE INDEX IF NOT EXISTS requests_at ON requests(at);
CREATE INDEX IF NOT EXISTS requests_day ON requests(day);
CREATE INDEX IF NOT EXISTS requests_profile ON requests(profile_id,at);
CREATE INDEX IF NOT EXISTS requests_model ON requests(model,at);
CREATE INDEX IF NOT EXISTS requests_cli ON requests(cli,at);
` + hourlySchema + `
CREATE TABLE IF NOT EXISTS request_keys(key_hash BLOB PRIMARY KEY,at INTEGER NOT NULL) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS request_keys_at ON request_keys(at);
CREATE TABLE IF NOT EXISTS cursors(path TEXT PRIMARY KEY,inode INTEGER NOT NULL,size INTEGER NOT NULL,mtime INTEGER NOT NULL,offset INTEGER NOT NULL,state BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY,value INTEGER NOT NULL);
INSERT OR IGNORE INTO meta VALUES('revision',0);
INSERT OR IGNORE INTO meta VALUES('generation',0);
CREATE TABLE IF NOT EXISTS reprice_overrides(id INTEGER PRIMARY KEY AUTOINCREMENT,created_at INTEGER NOT NULL,profile_id TEXT NOT NULL,model_id TEXT NOT NULL,from_ms INTEGER NOT NULL,to_ms INTEGER NOT NULL,all_range INTEGER NOT NULL,multiplier REAL NOT NULL,models TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS reprice_profile ON reprice_overrides(profile_id,id);
` + codexEvidenceSchema + `
` + analysisSchema + `
` + sessionCatalogSchema + `
` + sessionAgentSchema + `
PRAGMA user_version=12;`

// Open validates private storage and schema without starting background collection.
// An older schema opens in a rebuild-required state: nothing is read, written or migrated.
func Open(ctx context.Context, o Options) (*Store, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if e := storage.PreparePrivateDirectory(o.Directory, o.Owner); e != nil {
		return nil, ErrUnavailable
	}
	path := filepath.Join(o.Directory, "ai_usage.db")
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		st, e := os.Lstat(path + suffix)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, ErrUnavailable
		}
		stat, ok := st.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != o.Owner || stat.Nlink != 1 || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
			return nil, ErrUnavailable
		}
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, ErrUnavailable
	}
	if e = f.Close(); e != nil {
		return nil, ErrUnavailable
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_txlock=immediate"}
	w, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, ErrUnavailable
	}
	w.SetMaxOpenConns(1)
	s := &Store{w: w, options: o, wake: make(chan struct{}, 1), changed: make(chan struct{}, 1), stamps: map[string]stamp{}}
	fail := func() (*Store, error) { _ = w.Close(); return nil, ErrUnavailable }
	var v int
	if e = w.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); e != nil || v < 0 || v > schemaVersion {
		return fail()
	}
	if v != 0 && v < 5 {
		_ = w.Close()
		s.w = nil
		s.state = State{Revision: 1, Error: ErrRebuildCode}
		return s, nil
	}
	if _, e = w.ExecContext(ctx, "PRAGMA journal_mode=WAL"); e != nil {
		return fail()
	}
	tx, e := w.BeginTx(ctx, nil)
	if e != nil {
		return fail()
	}
	if v >= 5 && v < 12 {
		if e = migrateHourlySources(ctx, tx); e != nil {
			_ = tx.Rollback()
			return fail()
		}
	}
	if _, e = tx.ExecContext(ctx, schema); e != nil {
		_ = tx.Rollback()
		return fail()
	}
	// Existing directory rows gain classification without touching the usage ledger.
	if v == 9 {
		if _, e = tx.ExecContext(ctx, `ALTER TABLE session_catalog ADD COLUMN is_child INTEGER NOT NULL DEFAULT 0; DELETE FROM session_catalog_files;`); e != nil {
			_ = tx.Rollback()
			return fail()
		}
	}
	if v >= 5 && v < 11 {
		if _, e = tx.ExecContext(ctx, `DELETE FROM session_catalog_files; UPDATE meta SET value=0 WHERE key='catalog_ready'; UPDATE meta SET value=value+1 WHERE key='catalog_revision';`); e != nil {
			_ = tx.Rollback()
			return fail()
		}
	}
	if v >= 5 && v < 7 {
		if e = seedCodexEvidence(ctx, tx); e != nil {
			_ = tx.Rollback()
			return fail()
		}
	}
	if e = backfillMissingPrices(ctx, tx, o.Now().AddDate(0, 0, -93).UnixMilli()); e != nil {
		_ = tx.Rollback()
		return fail()
	}
	if e = tx.Commit(); e != nil {
		_ = tx.Rollback()
		return fail()
	}

	u.RawQuery = "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	s.r, e = sql.Open("sqlite", u.String())
	if e != nil {
		return fail()
	}
	s.r.SetMaxOpenConns(4)
	if e = s.r.QueryRowContext(ctx, "SELECT (SELECT value FROM meta WHERE key='revision'),(SELECT value FROM meta WHERE key='generation'),(SELECT value FROM meta WHERE key='catalog_revision'),(SELECT value FROM meta WHERE key='catalog_ready')").Scan(&s.state.Revision, &s.state.Generation, &s.state.CatalogRevision, &s.state.CatalogReady); e != nil {
		_ = s.r.Close()
		return fail()
	}
	return s, nil
}

// RebuildRequired reports a database from an older schema, which only the rebuild
// program may replace.
func (s *Store) RebuildRequired() bool { return s.w == nil }

// State reads collection status. Reading never changes the collection cadence.
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// SetForeground installs the visibility probe: true while an AI usage page (or the
// dashboard) is on screen. Without one the collector stays on the background cadence.
func (s *Store) SetForeground(f func() bool) {
	s.mu.Lock()
	s.foreground = f
	s.mu.Unlock()
}

// Wake runs the next collection round now, e.g. when a usage page becomes visible.
func (s *Store) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Changes signals (coalesced) that State differs from what an earlier State call returned.
func (s *Store) Changes() <-chan struct{} { return s.changed }

// mutate applies f to the state and signals Changes only when the state really changed.
func (s *Store) mutate(f func(*State)) {
	s.mu.Lock()
	before := s.state
	f(&s.state)
	changed := s.state != before
	s.mu.Unlock()
	if changed {
		select {
		case s.changed <- struct{}{}:
		default:
		}
	}
}

// Close cancels collection and waits within the caller's deadline before closing storage.
func (s *Store) Close(ctx context.Context) error {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.RebuildRequired() {
		return nil
	}
	return errors.Join(s.r.Close(), s.w.Close())
}

type cursor struct {
	Path                       string
	Inode, Size, Mtime, Offset int64
	State                      parseState
}

func (s *Store) cursor(ctx context.Context, path string) (cursor, error) {
	c := cursor{Path: path}
	var raw []byte
	e := s.r.QueryRowContext(ctx, "SELECT inode,size,mtime,offset,state FROM cursors WHERE path=?", path).Scan(&c.Inode, &c.Size, &c.Mtime, &c.Offset, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return c, nil
	}
	if e != nil {
		return c, ErrUnavailable
	}
	if e = decodeState(raw, &c.State); e != nil {
		return c, ErrUnavailable
	}
	return c, nil
}
