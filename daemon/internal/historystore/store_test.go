package historystore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"pax-connect/daemon/internal/storage"
)

func modeOf(t *testing.T, path string) (os.FileMode, uint32) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm(), info.Sys().(*syscall.Stat_t).Uid
}

func TestStoresOpenWithPrivateModesAndPragmas(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	logs := mustLogs(t, dir)
	traffic := mustTraffic(t, dir)
	journal := mustJournal(t, dir)
	if err := logs.Update(ctx, func(tx *LogsTx) error {
		return tx.PutConnection(ctx, LogConnection{FlowKey: "o", LastActivityMs: 1, Body: []byte("{}")})
	}); err != nil {
		t.Fatal(err)
	}
	if err := traffic.Update(ctx, func(tx *TrafficTx) error { return tx.SetMeta(ctx, "x", "y") }); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(ctx, baseHour, []Observation{obs("s", 1)}, Cursor{"s", 1}); err != nil {
		t.Fatal(err)
	}
	hourPath := filepath.Join(dir, "journal", "2026102608.db")
	for path, want := range map[string]os.FileMode{
		dir: 0700, filepath.Join(dir, "journal"): 0700,
		filepath.Join(dir, "logs.db"): 0600, filepath.Join(dir, "logs.db-wal"): 0600, filepath.Join(dir, "logs.db-shm"): 0600,
		filepath.Join(dir, "traffic.db"): 0600, filepath.Join(dir, "traffic.db-wal"): 0600,
		hourPath: 0600, hourPath + "-wal": 0600,
	} {
		if mode, owner := modeOf(t, path); mode != want || owner != uid() {
			t.Fatalf("%s: mode %o owner %d, want %o owner %d", path, mode, owner, want, uid())
		}
	}
	for _, item := range []struct {
		s    *store
		want string
	}{{logs.store, strconv.Itoa(logsVersion)}, {traffic.store, "1"}} {
		s := item.s
		if s.w.Stats().MaxOpenConnections != 1 || s.r.Stats().MaxOpenConnections != 4 {
			t.Fatalf("pools: writer %d reader %d", s.w.Stats().MaxOpenConnections, s.r.Stats().MaxOpenConnections)
		}
		var mode string
		var vacuum, sync int
		if s.w.QueryRow("PRAGMA journal_mode").Scan(&mode) != nil || s.w.QueryRow("PRAGMA auto_vacuum").Scan(&vacuum) != nil || s.w.QueryRow("PRAGMA synchronous").Scan(&sync) != nil {
			t.Fatal("pragma read failed")
		}
		if mode != "wal" || vacuum != 2 || sync != 1 {
			t.Fatalf("journal_mode=%s auto_vacuum=%d synchronous=%d", mode, vacuum, sync)
		}
		if v, ok, err := s.Meta(ctx, "schema_version"); err != nil || !ok || v != item.want {
			t.Fatalf("schema_version %q %v %v", v, ok, err)
		}
		if _, err := s.r.Exec("CREATE TABLE intruder(a)"); err == nil {
			t.Fatal("read pool accepted a write")
		}
	}
}

func TestReaderIsNotBlockedByLongWriteTransaction(t *testing.T) {
	logs := mustLogs(t, historyDir(t))
	ctx := context.Background()
	inTx, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- logs.Update(ctx, func(tx *LogsTx) error {
			if err := tx.PutConnection(ctx, LogConnection{FlowKey: "slow", LastActivityMs: 1, Body: []byte("{}")}); err != nil {
				return err
			}
			close(inTx)
			<-release
			return nil
		})
	}()
	<-inTx
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if n := count(t, logs.Reader(), "SELECT count(*) FROM connections"); n != 0 {
		t.Fatalf("reader saw uncommitted row: %d", n)
	}
	if _, _, err := logs.Meta(readCtx, "schema_version"); err != nil {
		t.Fatal("reader blocked by writer:", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := count(t, logs.Reader(), "SELECT count(*) FROM connections"); n != 1 {
		t.Fatalf("committed row invisible: %d", n)
	}
}

func TestCorruptDatabaseIsQuarantinedAndRecreated(t *testing.T) {
	dir := historyDir(t)
	if err := prepareDirectory(dir, uid()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"logs.db", "traffic.db"} {
		writeGarbage(t, filepath.Join(dir, name))
		writeGarbage(t, filepath.Join(dir, name+"-wal"))
	}
	ctx := context.Background()
	logs, rec, err := OpenLogs(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logs.Close() }()
	traffic, recTraffic, err := OpenTraffic(ctx, dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = traffic.Close() }()
	for name, r := range map[string]Recovered{"logs.db": rec, "traffic.db": recTraffic} {
		if !r.Happened() || !strings.HasPrefix(filepath.Base(r.Quarantined), name+".corrupt-") {
			t.Fatalf("%s not reported: %+v", name, r)
		}
		if _, err = os.Stat(r.Quarantined); err != nil {
			t.Fatal("quarantined file missing:", err)
		}
	}
	if err = logs.Update(ctx, func(tx *LogsTx) error {
		return tx.PutConnection(ctx, LogConnection{FlowKey: "o", LastActivityMs: 1, Body: []byte("{}")})
	}); err != nil {
		t.Fatal("fresh database unusable:", err)
	}
	if _, rec, err = OpenLogs(ctx, dir, uid()); err == nil && rec.Happened() {
		t.Fatal("healthy database was quarantined")
	}
}

func TestUnsafeFilesAreRefused(t *testing.T) {
	ctx := context.Background()
	dir := historyDir(t)
	if err := prepareDirectory(dir, uid()); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "logs.db")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenLogs(ctx, dir, uid()); !errors.Is(err, ErrUnsafeFile) {
		t.Fatalf("symlinked logs.db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "traffic.db"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "traffic.db"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenTraffic(ctx, dir, uid()); !errors.Is(err, ErrUnsafeFile) {
		t.Fatalf("0644 traffic.db: %v", err)
	}
	if _, _, err := OpenLogs(ctx, dir, uid()+1); !errors.Is(err, storage.ErrPrivateFile) {
		t.Fatalf("directory owned by another uid accepted: %v", err)
	}
	// Owner check on the file itself: a file that looks private but belongs to someone else.
	if _, err := guardExisting(filepath.Join(dir, "victim"), uid()+1); !errors.Is(err, ErrUnsafeFile) {
		t.Fatalf("foreign-owned file accepted: %v", err)
	}
	j := mustJournal(t, dir)
	if err := os.Symlink(victim, filepath.Join(dir, "journal", "2026102608.db-wal")); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(ctx, baseHour, []Observation{obs("s", 1)}, Cursor{"s", 1}); !errors.Is(err, ErrUnsafeFile) {
		t.Fatalf("symlinked journal wal: %v", err)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target modified: %q %v", data, err)
	}
}

func TestLogsAndTrafficSchemaMismatchReturnsSentinel(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	for name, open := range map[string]func() error{
		"logs.db":    func() error { _, _, err := OpenLogs(ctx, dir, uid()); return err },
		"traffic.db": func() error { _, _, err := OpenTraffic(ctx, dir, uid()); return err },
	} {
		if err := open(); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if _, err := rawDB(t, path).Exec("UPDATE meta SET value='99' WHERE key='schema_version'"); err != nil {
			t.Fatal(err)
		}
		if err := open(); !errors.Is(err, ErrSchemaVersion) {
			t.Fatalf("%s: %v", name, err)
		}
		entries, _ := filepath.Glob(path + ".*")
		if len(entries) != 0 {
			t.Fatalf("%s: mismatched file must stay in place, found %v", name, entries)
		}
	}
}

func TestQuarantineMovesCompanionsTogether(t *testing.T) {
	dir := historyDir(t)
	if err := prepareDirectory(dir, uid()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		writeGarbage(t, path+suffix)
	}
	target, err := quarantine(path, "corrupt")
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err = os.Stat(target + suffix); err != nil {
			t.Fatal(err)
		}
		if _, err = os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left in place", suffix)
		}
	}
}

func TestQuarantineFilesAreBoundedPerStoreKind(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	mustJournal(t, dir) // creates history/ and history/journal/
	stamps := []string{"20261020T000000Z", "20261021T000000Z", "20261022T000000Z", "20261023T000000Z", "20261024T000000Z", "20261024T000000Z-1"}
	seed := func(parent, base, tag string) {
		for _, stamp := range stamps {
			name := filepath.Join(parent, base+"."+tag+"-"+stamp)
			writeGarbage(t, name)
			writeGarbage(t, name+"-wal")
		}
	}
	seed(dir, "logs.db", "corrupt")
	seed(dir, "traffic.db", "schema")
	seed(filepath.Join(dir, "journal"), "2026102608.db", "corrupt")
	seed(filepath.Join(dir, "journal"), "2026102609.db", "schema")
	keep := filepath.Join(dir, "unrelated.db.corrupt-20200101T000000Z")
	writeGarbage(t, keep)

	if _, _, err := OpenLogs(ctx, dir, uid()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenTraffic(ctx, dir, uid()); err != nil {
		t.Fatal(err)
	}
	mustJournal(t, dir)
	left := func(pattern string) []string {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		var mains []string
		for _, m := range matches {
			if !strings.HasSuffix(m, "-wal") {
				mains = append(mains, filepath.Base(m))
			}
		}
		return mains
	}
	for pattern, want := range map[string]int{
		filepath.Join(dir, "logs.db.corrupt-*"): 3, filepath.Join(dir, "traffic.db.schema-*"): 3,
		filepath.Join(dir, "journal", "*.db.*-*"): 3, filepath.Join(dir, "unrelated.db.corrupt-*"): 1,
	} {
		if got := left(pattern); len(got) != want {
			t.Fatalf("%s: %v", pattern, got)
		}
	}
	newest := left(filepath.Join(dir, "logs.db.corrupt-*"))
	for _, want := range []string{"logs.db.corrupt-20261024T000000Z", "logs.db.corrupt-20261024T000000Z-1", "logs.db.corrupt-20261023T000000Z"} {
		found := false
		for _, n := range newest {
			found = found || n == want
		}
		if !found {
			t.Fatalf("newest not kept: %v", newest)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "logs.db.corrupt-20261020T000000Z-wal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("companion of a deleted quarantine file left behind")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("unrelated file deleted:", err)
	}
}

func TestReadPoolPageCacheIsBoundedAndQueryOnly(t *testing.T) {
	s := mustTraffic(t, historyDir(t))
	ctx := context.Background()
	var cache, queryOnly int
	if err := s.Reader().QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cache); err != nil {
		t.Fatal(err)
	}
	if err := s.Reader().QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		t.Fatal(err)
	}
	if cache != -16384 || queryOnly != 1 {
		t.Fatalf("reader cache/query_only=%d/%d", cache, queryOnly)
	}
}
