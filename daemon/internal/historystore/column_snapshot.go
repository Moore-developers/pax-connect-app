package historystore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const columnVersionBudget = 32 << 20

var columnSnapshotSerial atomic.Uint64
var errColumnCursor = errors.New("history_cursor_expired")

// ColumnQuery is one short, immutable read. Main is opened read-only; TEMP
// tables may hold a bounded query window. It must not escape WithRead.
type ColumnQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
type snapshotTable struct{ name, columns, key string }
type columnSnapshots struct {
	mu              sync.Mutex
	owner           *Columns
	cache           *sql.DB
	readers         *sql.DB
	uri             string
	tables          []snapshotTable
	refs            map[uint64]*ColumnSnapshot
	next, revision  uint64
	budget          int64
	failed          atomic.Bool
	removalRevision atomic.Uint64
	removalMeta     atomic.Pointer[LogsMeta]
}

// ColumnSnapshot fixes both mutable rows/indexes and immutable event evidence.
// Only overwritten/deleted versions are retained, shared across live readers.
type ColumnSnapshot struct {
	state         *columnSnapshots
	id, revision  uint64
	meta          LogsMeta
	created, last time.Time
	valid         atomic.Bool
}

// Snapshot creates a fixed revision without copying retained data or holding a
// database transaction between pages. The caller releases it with Close.
func (c *Columns) Snapshot(ctx context.Context) (*ColumnSnapshot, error) {
	// All initialization and writer transactions share the writer connection. A
	// separate mutex protects installing that shared state for offline callers.
	if err := lockColumnSnapshotInit(ctx); err != nil {
		return nil, err
	}
	if c.snapshots == nil {
		s, err := newColumnSnapshots(ctx, c)
		if err != nil {
			columnSnapshotInit.Unlock()
			return nil, err
		}
		c.snapshots = s
	}
	s := c.snapshots
	columnSnapshotInit.Unlock()
	if err := s.lock(ctx); err != nil {
		return nil, err
	}
	defer s.mu.Unlock()
	if s.failed.Load() {
		return nil, errors.New("history_store_failed")
	}
	if err := s.expire(ctx); err != nil {
		return nil, err
	}
	var meta LogsMeta
	var err error
	if c.owner != nil {
		meta, err = c.owner.SnapshotMeta(ctx)
	} else {
		var hasMeta bool
		err = c.r.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='meta' AND type='table')").Scan(&hasMeta)
		if err == nil && hasMeta {
			meta, err = readLogsMeta(ctx, c.r)
		} else if err == nil {
			meta = LogsMeta{RetentionHours: 24}
			var seq sql.NullInt64
			err = c.r.QueryRowContext(ctx, "SELECT max(seq) FROM col_events").Scan(&seq)
			meta.HeadSequence = uint64(seq.Int64)
		}
	}
	if err != nil {
		return nil, err
	}
	s.next++
	v := &ColumnSnapshot{state: s, id: s.next, revision: s.revision, meta: meta, created: time.Now(), last: time.Now()}
	v.valid.Store(true)
	if _, err = s.cache.ExecContext(ctx, "INSERT INTO v_refs VALUES(?,?)", v.id, v.revision); err != nil {
		return nil, err
	}
	s.refs[v.id] = v
	if _, err = s.cache.ExecContext(ctx, "UPDATE v_control SET active=1"); err != nil {
		delete(s.refs, v.id)
		return nil, err
	}
	return v, nil
}

var columnSnapshotInit sync.Mutex

func lockColumnSnapshotInit(ctx context.Context) error {
	for !columnSnapshotInit.TryLock() {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		columnSnapshotInit.Unlock()
		return err
	}
	return nil
}

// Meta is the committed metadata at this read's revision.
func (s *ColumnSnapshot) Meta() LogsMeta { return s.meta }

// Valid reports explicit eviction; WithRead also checks idle and total TTL.
func (s *ColumnSnapshot) Valid() bool { return s != nil && s.valid.Load() }

// Removed reports a committed fact deletion after this revision. Opened
// details keep reading their old rows, while list cursors are invalidated.
func (s *ColumnSnapshot) Removed() bool {
	return s != nil && s.state.removalRevision.Load() > s.revision
}

// ListValid retains only a proven safe suffix following time-prefix removal.
func (s *ColumnSnapshot) ListValid(fromMs int64) bool {
	if !s.Valid() {
		return false
	}
	if !s.Removed() {
		return true
	}
	m := s.state.removalMeta.Load()
	return m != nil && m.LogRemovalBeforeMs > 0 && fromMs >= m.LogRemovalBeforeMs && m.SummaryGeneration == s.meta.SummaryGeneration && m.JournalGeneration == s.meta.JournalGeneration
}

// Close releases this reference, permitting old versions to be reclaimed.
func (s *ColumnSnapshot) Close() error {
	if s == nil || s.state == nil {
		return nil
	}
	v := s.state
	v.mu.Lock()
	defer v.mu.Unlock()
	s.valid.Store(false)
	delete(v.refs, s.id)
	if v.failed.Load() {
		return nil
	}
	if _, err := v.cache.Exec("DELETE FROM v_refs WHERE id=?", s.id); err != nil {
		return err
	}
	return v.collect(context.Background())
}

// WithRead provides revision-visible snap_col_* TEMP views. A page owns only a
// short lock; writer WAL checkpoints are never held by an idle token.
func (s *ColumnSnapshot) WithRead(ctx context.Context, fn func(ColumnQuery) error) error {
	if s == nil {
		return errColumnCursor
	}
	v := s.state
	if err := v.lock(ctx); err != nil {
		return err
	}
	defer v.mu.Unlock()
	if v.failed.Load() || !s.valid.Load() {
		return errColumnCursor
	}
	if err := v.expire(ctx); err != nil {
		return err
	}
	if !s.valid.Load() {
		return errColumnCursor
	}
	s.last = time.Now()
	conn, err := v.readers.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	// The views depend on the snapshot and writer revisions, not on the token
	// identity. Other live tokens at the same revision retain the same old rows.
	// Keep the marker on this SQL connection, since TEMP views are per-connection.
	if _, err = conn.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS pax_snapshot_views(snapshot_revision INTEGER NOT NULL,current_revision INTEGER NOT NULL)"); err != nil {
		return err
	}
	var installedSnapshot, installedCurrent uint64
	err = conn.QueryRowContext(ctx, "SELECT snapshot_revision,current_revision FROM temp.pax_snapshot_views LIMIT 1").Scan(&installedSnapshot, &installedCurrent)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && installedSnapshot == s.revision && installedCurrent == v.revision {
		return fn(conn)
	}
	// Do not leave a valid marker behind if rebuilding any view fails halfway.
	if _, err = conn.ExecContext(ctx, "DELETE FROM temp.pax_snapshot_views"); err != nil {
		return err
	}
	var attached int
	rows, err := conn.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return err
	}
	for rows.Next() {
		var n int
		var name, path string
		if err = rows.Scan(&n, &name, &path); err != nil {
			break
		}
		if name == "pax_versions" {
			attached = 1
		}
	}
	readErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if readErr != nil {
		return readErr
	}
	if attached == 0 {
		if _, err = conn.ExecContext(ctx, "ATTACH DATABASE ? AS pax_versions", v.uri); err != nil {
			return err
		}
	}
	for _, t := range v.tables {
		name := "snap_" + t.name
		// With the commit mutex held, the current revision is already immutable
		// for this callback. Keep that common case as a flattenable indexed view;
		// joining compound UNION views otherwise materializes whole identity
		// tables even for a single page key.
		if s.revision == v.revision {
			if _, err = conn.ExecContext(ctx, "DROP VIEW IF EXISTS temp."+name+";CREATE TEMP VIEW "+name+" AS SELECT * FROM main."+t.name); err != nil {
				return err
			}
			continue
		}
		var hasOld bool
		if err = conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pax_versions.v_"+t.name+" WHERE _snap_from<=? AND _snap_until>?)", s.revision, s.revision).Scan(&hasOld); err != nil {
			return err
		}
		// Rows written after this snapshot must be hidden from main. When none
		// exist for the table, the per-row v_current probe (which builds a hex key
		// for every main row) is a no-op, so drop it and keep the view cheap.
		var changed bool
		if err = conn.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pax_versions.v_current WHERE table_name=? AND revision>?)", t.name, s.revision).Scan(&changed); err != nil {
			return err
		}
		if !hasOld && (!changed || t.name == "col_identities" || t.name == "col_objects") {
			if _, err = conn.ExecContext(ctx, "DROP VIEW IF EXISTS temp."+name+";CREATE TEMP VIEW "+name+" AS SELECT * FROM main."+t.name); err != nil {
				return err
			}
			continue
		}
		if !changed {
			stmt := "DROP VIEW IF EXISTS temp." + name + ";CREATE TEMP VIEW " + name + " AS SELECT " + t.columns + " FROM main." + t.name + " UNION ALL SELECT " + t.columns + " FROM pax_versions.v_" + t.name + " WHERE _snap_from<=" + fmt.Sprint(s.revision) + " AND _snap_until>" + fmt.Sprint(s.revision)
			if _, err = conn.ExecContext(ctx, stmt); err != nil {
				return err
			}
			continue
		}
		stmt := "DROP VIEW IF EXISTS temp." + name + ";CREATE TEMP VIEW " + name + " AS SELECT " + prefixSQL("a", t.columns) + " FROM main." + t.name + " a LEFT JOIN pax_versions.v_current b ON b.table_name='" + t.name + "' AND b.row_key=" + tableKey(t, "a") + " WHERE coalesce(b.revision,0)<=" + fmt.Sprint(s.revision) + " UNION ALL SELECT " + t.columns + " FROM pax_versions.v_" + t.name + " WHERE _snap_from<=" + fmt.Sprint(s.revision) + " AND _snap_until>" + fmt.Sprint(s.revision)
		if _, err = conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err = conn.ExecContext(ctx, "INSERT INTO temp.pax_snapshot_views VALUES(?,?)", s.revision, v.revision); err != nil {
		return err
	}
	return fn(conn)
}
func prefixSQL(alias, names string) string {
	return alias + "." + strings.ReplaceAll(names, ",", ","+alias+".")
}
func tableKey(t snapshotTable, alias string) string {
	parts := strings.Split(t.key, ",")
	for i, k := range parts {
		parts[i] = "hex(CAST(" + alias + "." + k + " AS BLOB))"
	}
	return strings.Join(parts, "||':'||")
}
func (s *columnSnapshots) lock(ctx context.Context) error {
	for !s.mu.TryLock() {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return err
	}
	return nil
}

// snapshotUpdate is the single column writer boundary, including import and
// retention. SQLite TEMP triggers capture exact OLD values in the same commit.
func (c *Columns) snapshotUpdate(ctx context.Context, fn func(*ColumnsTx) error) error {
	if err := lockColumnSnapshotInit(ctx); err != nil {
		return err
	}
	s := c.snapshots
	if s != nil {
		if err := s.lock(ctx); err != nil {
			columnSnapshotInit.Unlock()
			return err
		}
		columnSnapshotInit.Unlock()
		defer s.mu.Unlock()
		if !s.failed.Load() {
			if err := s.expire(ctx); err != nil {
				s.snapshotFailed(ctx)
			}
		}
	} else {
		defer columnSnapshotInit.Unlock()
	}
	capturing := s != nil && !s.failed.Load()
	if capturing {
		if _, err := s.cache.ExecContext(ctx, "UPDATE v_control SET budget=?", s.budget); err != nil {
			s.snapshotFailed(ctx)
			capturing = false
		}
	}
	err := c.update(ctx, func(tx *sql.Tx) error {
		if capturing {
			if _, err := tx.ExecContext(ctx, "UPDATE pax_versions.v_control SET revision=?,removed=0,overflow=0", s.revision+1); err != nil {
				return err
			}
		}
		t := &ColumnsTx{tx: tx, prepared: map[string]*sql.Stmt{}}
		defer t.closeStatements()
		return fn(t)
	})
	if err != nil {
		return err
	}
	if capturing {
		s.finishSnapshotCommit(context.WithoutCancel(ctx))
	}
	return nil
}

func (s *columnSnapshots) expire(ctx context.Context) error {
	now := time.Now()
	changed := false
	for id, v := range s.refs {
		if now.Sub(v.last) > 2*time.Minute || now.Sub(v.created) > 5*time.Minute {
			v.valid.Store(false)
			delete(s.refs, id)
			if _, err := s.cache.ExecContext(ctx, "DELETE FROM v_refs WHERE id=?", id); err != nil {
				return err
			}
			changed = true
		}
	}
	if changed {
		return s.collect(ctx)
	}
	return nil
}
func (s *columnSnapshots) collect(ctx context.Context) error {
	for _, t := range s.tables {
		if _, err := s.cache.ExecContext(ctx, "DELETE FROM v_"+t.name+" WHERE NOT EXISTS(SELECT 1 FROM v_refs WHERE revision>=_snap_from AND revision<_snap_until)"); err != nil {
			return err
		}
	}
	if len(s.refs) == 0 {
		_, err := s.cache.ExecContext(ctx, "DELETE FROM v_current;UPDATE v_control SET active=0,charge=0")
		return err
	}
	return s.refreshCharge(ctx)
}
func (s *columnSnapshots) enforceBudget(ctx context.Context) error {
	if err := s.collect(ctx); err != nil {
		return err
	}
	for {
		var pages, free, size int64
		if err := s.cache.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); err != nil {
			return err
		}
		if err := s.cache.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
			return err
		}
		if err := s.cache.QueryRowContext(ctx, "PRAGMA page_size").Scan(&size); err != nil {
			return err
		}
		if (pages-free)*size <= s.budget {
			return nil
		}
		var oldest *ColumnSnapshot
		for _, v := range s.refs {
			if oldest == nil || v.created.Before(oldest.created) {
				oldest = v
			}
		}
		if oldest == nil {
			return errors.New("historystore: snapshot cache metadata exceeds budget")
		}
		oldest.valid.Store(false)
		delete(s.refs, oldest.id)
		if _, err := s.cache.ExecContext(ctx, "DELETE FROM v_refs WHERE id=?", oldest.id); err != nil {
			return err
		}
		if err := s.collect(ctx); err != nil {
			return err
		}
	}
}

func newColumnSnapshots(ctx context.Context, c *Columns) (*columnSnapshots, error) {
	uri := fmt.Sprintf("file:pax-column-versions-%d?mode=memory&cache=shared", columnSnapshotSerial.Add(1))
	cache, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, err
	}
	cache.SetMaxOpenConns(1)
	s := &columnSnapshots{owner: c, cache: cache, uri: uri, refs: map[uint64]*ColumnSnapshot{}, budget: columnVersionBudget}
	success := false
	attached := false
	defer func() {
		if !success {
			if attached {
				cleanup := context.WithoutCancel(ctx)
				for _, table := range s.tables {
					for _, op := range []string{"insert", "update", "delete"} {
						_, _ = c.w.ExecContext(cleanup, "DROP TRIGGER IF EXISTS temp.snap_"+table.name+"_"+op)
					}
				}
				_, _ = c.w.ExecContext(cleanup, "DETACH DATABASE pax_versions")
			}
			_ = cache.Close()
			if s.readers != nil {
				_ = s.readers.Close()
			}
		}
	}()
	if _, err = cache.ExecContext(ctx, "PRAGMA auto_vacuum=FULL;CREATE TABLE v_control(revision INTEGER,active INTEGER,removed INTEGER,charge INTEGER,budget INTEGER,overflow INTEGER);INSERT INTO v_control VALUES(0,0,0,0,33554432,0);CREATE TABLE v_refs(id INTEGER PRIMARY KEY,revision INTEGER);CREATE TABLE v_current(table_name TEXT,row_key TEXT,revision INTEGER,_snap_bytes INTEGER,PRIMARY KEY(table_name,row_key)) WITHOUT ROWID;"); err != nil {
		return nil, err
	}
	rows, err := c.r.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'col_%' AND name NOT IN ('col_meta','col_import','col_projection_progress') ORDER BY name")
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			break
		}
		names = append(names, n)
	}
	readErr := rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}
	for _, name := range names {
		rows, err = c.r.QueryContext(ctx, "PRAGMA table_info("+name+")")
		if err != nil {
			return nil, err
		}
		var cols, keys []string
		for rows.Next() {
			var id, required, pk int
			var col, typ string
			var def any
			if err = rows.Scan(&id, &col, &typ, &required, &def, &pk); err != nil {
				break
			}
			cols = append(cols, col)
			if pk > 0 {
				keys = append(keys, col)
			}
		}
		readErr = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		if readErr != nil {
			return nil, readErr
		}
		if len(keys) == 0 {
			return nil, fmt.Errorf("historystore: snapshot table %s has no primary key", name)
		}
		t := snapshotTable{name, strings.Join(cols, ","), strings.Join(keys, ",")}
		s.tables = append(s.tables, t)
		defs := append([]string{}, cols...)
		defs = append(defs, "_snap_key TEXT", "_snap_from INTEGER", "_snap_until INTEGER", "_snap_bytes INTEGER")
		if _, err = cache.ExecContext(ctx, "CREATE TABLE v_"+name+"("+strings.Join(defs, ",")+");CREATE INDEX vi_"+name+" ON v_"+name+"(_snap_key,_snap_from,_snap_until)"); err != nil {
			return nil, err
		}
	}
	if _, err = c.w.ExecContext(ctx, "ATTACH DATABASE ? AS pax_versions", uri); err != nil {
		return nil, err
	}
	attached = true
	for _, t := range s.tables {
		oldKey, newKey := tableKey(t, "OLD"), tableKey(t, "NEW")
		active := "(SELECT active FROM pax_versions.v_control)=1"
		revision := "(SELECT revision FROM pax_versions.v_control)"
		born := "coalesce((SELECT revision FROM v_current WHERE table_name='" + t.name + "' AND row_key=" + oldKey + "),0)"
		capture := snapshotCaptureSQL(t, oldKey, born, revision)
		current := func(key string) string { return snapshotCurrentSQL(t, key, revision) }

		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			body := ""
			if op != "INSERT" {
				body += capture
			}
			if op != "DELETE" {
				body += current(newKey)
			} else {
				body += "DELETE FROM v_current WHERE table_name='" + t.name + "' AND row_key=" + oldKey + ";"
				if t.name == "col_connections" || t.name == "col_events" {
					body += "UPDATE v_control SET removed=1;"
				}
			}
			condition := active
			if op == "UPDATE" {
				var same []string
				for _, col := range strings.Split(t.columns, ",") {
					same = append(same, "OLD."+col+" IS NEW."+col)
				}
				condition += " AND NOT (" + strings.Join(same, " AND ") + ")"
			}
			timing := "BEFORE"
			if op == "INSERT" {
				timing = "AFTER"
			}
			sql := "CREATE TEMP TRIGGER snap_" + t.name + "_" + strings.ToLower(op) + " " + timing + " " + op + " ON main." + t.name + " WHEN " + condition + " BEGIN " + body + " END;"
			if _, err = c.w.ExecContext(ctx, sql); err != nil {
				return nil, fmt.Errorf("snapshot trigger %s: %w", t.name, err)
			}
		}
	}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "cache_size(-4096)", "temp_store(FILE)"}}
	s.readers, err = sql.Open("sqlite", (&url.URL{Scheme: "file", Path: c.path, RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, err
	}
	s.readers.SetMaxOpenConns(4)
	success = true
	return s, nil
}
