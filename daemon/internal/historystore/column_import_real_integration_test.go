//go:build integration

package historystore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
)

// Acceptance: exercise the actual converter against a frozen private database,
// compare every product field and every converted SQL column, and measure the
// physical coexistence/WAL peak after each bounded committed batch.
func TestColumnImportReal(t *testing.T) {
	source, dir := os.Getenv("PAX_COLUMN_IMPORT_SOURCE"), os.Getenv("PAX_COLUMN_IMPORT_DEST")
	if source == "" || dir == "" {
		t.Skip("explicit private source and destination required")
	}
	ctx := context.Background()
	s := mustColumnImport(t, source)
	c := mustColumnsImportTarget(t, dir)
	start := time.Now()
	var peakDB, peakWAL, peakTogether int64
	var maxBatch time.Duration
	interrupted := false
	var p ColumnImportProgress
	for {
		at := time.Now()
		var err error
		p, err = s.Batch(ctx, c, 128, nil)
		if err != nil {
			t.Fatal(err)
		}
		maxBatch = max(maxBatch, time.Since(at))
		dbBytes, walBytes := columnFileBytes(filepath.Join(dir, logsFile)), columnFileBytes(filepath.Join(dir, logsFile)+"-wal")
		peakDB = max(peakDB, dbBytes)
		peakWAL = max(peakWAL, walBytes)
		peakTogether = max(peakTogether, dbBytes+walBytes)
		if os.Getenv("PAX_COLUMN_IMPORT_RESUME") == "1" && !interrupted && p.Events >= 10000 && !p.Done {
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestColumnImportCrashChild$")
			cmd.Env = append(os.Environ(), "PAX_COLUMN_CRASH_SOURCE="+source, "PAX_COLUMN_CRASH_TARGET="+dir)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 91 {
				t.Fatalf("crash: %v %s", err, out)
			}
			c = mustColumnsImportTarget(t, dir)
			after, err := s.Progress(ctx, c)
			if err != nil || after != p {
				t.Fatalf("crash changed progress: %+v %+v %v", p, after, err)
			}
			t.Logf("COLUMN_IMPORT_CRASH connections=%d events=%d exit=91 uncommitted_changes=0", p.Connections, p.Events)
			interrupted = true
		}
		if p.Done {
			break
		}
	}
	t.Logf("COLUMN_IMPORT counts=%+v ms=%.3f max_batch_ms=%.3f main_peak=%d wal_peak=%d coexist_peak=%d source_sha256=%s", p, float64(time.Since(start).Microseconds())/1000, float64(maxBatch.Microseconds())/1000, peakDB, peakWAL, peakTogether, s.Fingerprint())
	if os.Getenv("PAX_COLUMN_IMPORT_RESUME") == "1" && !interrupted {
		t.Fatal("resume test never interrupted")
	}
	before := columnDatabaseDigests(t, c.Reader())
	if repeat := completeColumnImport(t, s, c); repeat != p {
		t.Fatal("repeat changed progress")
	}
	after := columnDatabaseDigests(t, c.Reader())
	if !reflect.DeepEqual(before, after) {
		t.Fatal("repeat changed SQL values")
	}
	if ref := os.Getenv("PAX_COLUMN_IMPORT_REFERENCE"); ref != "" {
		db, err := sql.Open("sqlite", "file:"+filepath.Join(ref, logsFile)+"?mode=ro&immutable=1")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if !reflect.DeepEqual(before, columnDatabaseDigests(t, db)) {
			t.Fatal("clean and resumed SQL columns differ")
		}
	}
	columnCheckImportedFacts(t, s, c, p)
	var identity, refs int
	if err := c.Reader().QueryRow("SELECT count(*) FROM col_identities").Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if err := c.Reader().QueryRow("SELECT (SELECT count(*) FROM col_events WHERE flow_present=1)+(SELECT count(*) FROM col_connections)").Scan(&refs); err != nil {
		t.Fatal(err)
	}
	t.Logf("COLUMN_IDENTITIES unique=%d references=%d reused_percent=%.3f", identity, refs, 100*(1-float64(identity)/float64(refs)))
	rows, err := c.Reader().Query("SELECT name,sum(pgsize) FROM dbstat GROUP BY name ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		var size int64
		if err = rows.Scan(&name, &size); err != nil {
			t.Fatal(err)
		}
		t.Logf("COLUMN_DBSTAT name=%s bytes=%d", name, size)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for name, digest := range before {
		t.Logf("COLUMN_SQL_SHA256 column=%s sha256=%s", name, digest)
	}
}

func columnFileBytes(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func columnCheckImportedFacts(t *testing.T, s *ColumnImport, c *Columns, p ColumnImportProgress) {
	t.Helper()
	ctx := context.Background()
	var events, connections int64
	rows, err := s.db.Query("SELECT seq,body,raw FROM events ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var seq int64
		var body, raw []byte
		if err = rows.Scan(&seq, &body, &raw); err != nil {
			t.Fatal(err)
		}
		var want config.HistoryEvent
		if err = json.Unmarshal(body, &want); err != nil {
			t.Fatal(err)
		}
		want.Sequence = uint64(seq)
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &want.Raw); err != nil {
				t.Fatal(err)
			}
		}
		got, found, err := c.Event(ctx, want.ID)
		if err != nil || !found || !reflect.DeepEqual(got, want) {
			t.Fatalf("event roundtrip seq=%d found=%v err=%v", seq, found, err)
		}
		events++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	rows, err = s.db.Query("SELECT body FROM connections ORDER BY flow_key")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		var want config.LogConnectionSummary
		if err = json.Unmarshal(body, &want); err != nil {
			t.Fatal(err)
		}
		got, found, err := c.Connection(ctx, want.Fact.Key)
		if err != nil || !found || !reflect.DeepEqual(got, want) {
			t.Fatalf("connection roundtrip found=%v err=%v", found, err)
		}
		connections++
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if events != p.Events || connections != p.Connections {
		t.Fatalf("count mismatch events=%d connections=%d %+v", events, connections, p)
	}
	t.Logf("COLUMN_IMPORT_ROUNDTRIP events=%d connections=%d differences=0", events, connections)
}

func columnDatabaseDigests(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	tables, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'col_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var n string
		if err = tables.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err = tables.Err(); err != nil {
		t.Fatal(err)
	}
	_ = tables.Close()
	result := map[string]string{}
	for _, name := range names {
		info, err := db.Query("PRAGMA table_info(" + name + ")")
		if err != nil {
			t.Fatal(err)
		}
		type key struct {
			order int
			name  string
		}
		var keys []key
		for info.Next() {
			var cid, notnull, pk int
			var col, typ string
			var def any
			if err = info.Scan(&cid, &col, &typ, &notnull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			if pk > 0 {
				keys = append(keys, key{pk, col})
			}
		}
		if err = info.Err(); err != nil {
			t.Fatal(err)
		}
		_ = info.Close()
		sort.Slice(keys, func(i, j int) bool { return keys[i].order < keys[j].order })
		var order []string
		for _, k := range keys {
			order = append(order, k.name)
		}
		rows, err := db.Query("SELECT * FROM " + name + " ORDER BY " + strings.Join(order, ","))
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		hashes := make([]hash.Hash, len(cols))
		for i := range hashes {
			hashes[i] = sha256.New()
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				var b []byte
				switch x := v.(type) {
				case nil:
					b = []byte("n")
				case int64:
					b = append([]byte("i"), binary.BigEndian.AppendUint64(nil, uint64(x))...)
				case string:
					b = append([]byte("s"), []byte(x)...)
				case []byte:
					b = append([]byte("b"), x...)
				default:
					t.Fatalf("unexpected SQL type %T", v)
				}
				_, _ = hashes[i].Write(binary.BigEndian.AppendUint64(nil, uint64(len(b))))
				_, _ = hashes[i].Write(b)
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		for i, col := range cols {
			result[name+"."+col] = fmt.Sprintf("%x", hashes[i].Sum(nil))
		}
	}
	return result
}
