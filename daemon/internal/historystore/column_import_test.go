package historystore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"pax-connect/daemon/internal/config"
)

func columnImportFixture(t *testing.T) string {
	t.Helper()
	dir := historyDir(t)
	if err := prepareDirectory(dir, uid()); err != nil {
		t.Fatal(err)
	}
	st, _, err := openStore(context.Background(), filepath.Join(dir, logsFile), uid(), storeSpec{ddl: logsDDL, version: 3, readers: readerConns})
	if err != nil {
		t.Fatal(err)
	}
	err = st.update(context.Background(), func(tx *sql.Tx) error {
		for i := 0; i < 3; i++ {
			f := config.FlowFact{Key: fmt.Sprintf("flow-%d", i), StartedAt: baseHour, Domain: "example.test"}
			v := config.LogConnectionSummary{Fact: f, LastEventID: fmt.Sprintf("e-%03d", i), LastLogAt: baseHour}
			body, e := json.Marshal(v)
			if e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO connections(flow_key,last_activity_ms,body) VALUES(?,?,?)", f.Key, baseHour.UnixMilli(), body); e != nil {
				return e
			}
		}
		for i := 1; i <= 37; i++ {
			v := config.HistoryEvent{ID: fmt.Sprintf("e-%03d", i), At: baseHour, Kind: "created", Flow: &config.FlowFact{Key: fmt.Sprintf("flow-%d", i%3)}, Raw: []string{"second", "first", "second"}}
			body, e := json.Marshal(v)
			if e != nil {
				return e
			}
			raw, e := json.Marshal(v.Raw)
			if e != nil {
				return e
			}
			if _, e = tx.Exec("INSERT INTO events(event_id,at_ms,body,raw) VALUES(?,?,?,?)", v.ID, v.At.UnixMilli(), body, raw); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, logsFile)
}

func mustColumnImport(t *testing.T, path string) *ColumnImport {
	t.Helper()
	s, e := OpenColumnImport(context.Background(), path, uid())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func mustColumnsImportTarget(t *testing.T, dir string) *Columns {
	t.Helper()
	c, e := OpenColumns(context.Background(), dir, uid())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
func completeColumnImport(t *testing.T, s *ColumnImport, c *Columns) ColumnImportProgress {
	t.Helper()
	for {
		p, e := s.Batch(context.Background(), c, 7, nil)
		if e != nil {
			t.Fatal(e)
		}
		if p.Done {
			return p
		}
	}
}

func TestColumnImportResumeCrashAndRepeat(t *testing.T) {
	source := columnImportFixture(t)
	s := mustColumnImport(t, source)
	clean := mustColumnsImportTarget(t, historyDir(t))
	want := completeColumnImport(t, s, clean)
	if want.Connections != 3 || want.Events != 37 {
		t.Fatalf("counts: %+v", want)
	}
	dir := historyDir(t)
	resumed := mustColumnsImportTarget(t, dir)
	for i := 0; i < 2; i++ {
		if _, e := s.Batch(context.Background(), resumed, 5, nil); e != nil {
			t.Fatal(e)
		}
	}
	before, e := s.Progress(context.Background(), resumed)
	if e != nil {
		t.Fatal(e)
	}
	if before.Events != 5 {
		t.Fatalf("expected committed events, got %+v", before)
	}
	if e = resumed.Close(); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestColumnImportCrashChild$")
	cmd.Env = append(os.Environ(), "PAX_COLUMN_CRASH_SOURCE="+source, "PAX_COLUMN_CRASH_TARGET="+dir)
	output, e := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 91 {
		t.Fatalf("crash child: %v %s", e, output)
	}
	resumed = mustColumnsImportTarget(t, dir)
	after, e := s.Progress(context.Background(), resumed)
	if e != nil {
		t.Fatal(e)
	}
	if after != before {
		t.Fatalf("uncommitted position escaped: %+v -> %+v", before, after)
	}
	got := completeColumnImport(t, s, resumed)
	if got != want {
		t.Fatalf("progress: %+v want %+v", got, want)
	}
	if repeat := completeColumnImport(t, s, resumed); repeat != want {
		t.Fatal(repeat)
	}
	for _, table := range []string{"col_meta", "col_identities", "col_connections", "col_events", "col_answers", "col_raw", "col_sections", "col_fields", "col_import"} {
		a := columnTableValues(t, clean, table)
		b := columnTableValues(t, resumed, table)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("clean/resume/repeat differ: %s", table)
		}
	}
}

func TestColumnImportCrashChild(t *testing.T) {
	path := os.Getenv("PAX_COLUMN_CRASH_SOURCE")
	if path == "" {
		t.Skip("subprocess only")
	}
	s := mustColumnImport(t, path)
	c := mustColumnsImportTarget(t, os.Getenv("PAX_COLUMN_CRASH_TARGET"))
	_, e := s.Batch(context.Background(), c, 5, func(_ context.Context, tx *ColumnsTx, b ColumnImportBatch) error {
		if len(b.Events) == 0 {
			return errors.New("expected event batch")
		}
		// Force dirty pages out before exiting, without commit or Close.
		if _, err := tx.tx.Exec("PRAGMA cache_size=1"); err != nil {
			return err
		}
		os.Exit(91)
		return nil
	})
	t.Fatalf("child did not exit: %v", e)
}

func columnTableValues(t *testing.T, c *Columns, table string) [][]any {
	t.Helper()
	rows, e := c.Reader().Query("SELECT * FROM " + table + " ORDER BY 1,2")
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = rows.Close() }()
	cols, e := rows.Columns()
	if e != nil {
		t.Fatal(e)
	}
	var result [][]any
	for rows.Next() {
		v := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range v {
			ptr[i] = &v[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			t.Fatal(e)
		}
		for i, x := range v {
			if b, ok := x.([]byte); ok {
				v[i] = append([]byte{}, b...)
			}
		}
		result = append(result, v)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return result
}

func TestColumnImportAtomicFailureAndInput(t *testing.T) {
	s := mustColumnImport(t, columnImportFixture(t))
	c := mustColumnsImportTarget(t, historyDir(t))
	wantErr := errors.New("projection failed")
	if _, err := s.Batch(context.Background(), c, 5, func(context.Context, *ColumnsTx, ColumnImportBatch) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatal(err)
	}
	if n := count(t, c.Reader(), "SELECT count(*) FROM col_connections"); n != 0 {
		t.Fatalf("facts escaped rollback: %d", n)
	}
	completeColumnImport(t, s, c)
	other := columnImportFixture(t)
	// A different physical backup must not silently resume this target.
	db := rawDB(t, other)
	if _, err := db.Exec("UPDATE meta SET value='123' WHERE key='logs_revision'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	changed := mustColumnImport(t, other)
	if _, err := changed.Batch(context.Background(), c, 5, nil); err == nil {
		t.Fatal("accepted a different source")
	}
	if _, err := s.Batch(context.Background(), c, 129, nil); err == nil {
		t.Fatal("accepted an unbounded batch")
	}
}
