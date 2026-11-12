package historystore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uid() uint32 { return uint32(os.Geteuid()) }

// historyDir returns a not-yet-created history directory beneath a parent whose
// name contains a space, as "Application Support" does on macOS.
func historyDir(t *testing.T) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(parent, "Application Support")
	if err = os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(base, "history")
}

func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dataSource(path, false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeGarbage(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("this is not a database ", 400)), 0600); err != nil {
		t.Fatal(err)
	}
}

func mustLogs(t *testing.T, dir string) *Logs {
	t.Helper()
	l, _, err := OpenLogs(context.Background(), dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func mustTraffic(t *testing.T, dir string) *Traffic {
	t.Helper()
	tr, _, err := OpenTraffic(context.Background(), dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func mustJournal(t *testing.T, dir string) *Journal {
	t.Helper()
	j, err := OpenJournal(dir, uid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var baseHour = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

func obs(session string, seq uint64) Observation {
	return Observation{Session: session, Seq: seq, AtMs: int64(seq), Kind: "sample", FlowKey: "f", Body: []byte(`{"k":1}`)}
}
