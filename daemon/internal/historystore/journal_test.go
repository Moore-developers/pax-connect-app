package historystore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalAppendCommitsRecordsAndCursorTogether(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	j := mustJournal(t, dir)
	if err := j.Append(ctx, baseHour, []Observation{obs("s", 1), obs("s", 2)}, Cursor{"s", 2}); err != nil {
		t.Fatal(err)
	}
	// A bad record in the middle of a batch must roll back the records and the cursor.
	bad := []Observation{obs("s", 3), {Session: "s", Seq: 1 << 63, Kind: "k", Body: []byte("{}")}}
	if err := j.Append(ctx, baseHour, bad, Cursor{"s", 4}); !errors.Is(err, ErrCursorRange) {
		t.Fatalf("append: %v", err)
	}
	if got, ok, err := j.Cursor(ctx); err != nil || !ok || got != (Cursor{"s", 2}) {
		t.Fatalf("cursor after failed batch: %+v %v %v", got, ok, err)
	}
	rows, err := j.ReadAfter(ctx, Cursor{}, 10)
	if err != nil || len(rows) != 2 {
		t.Fatalf("records after failed batch: %d %v", len(rows), err)
	}
	// Replays are ignored, not duplicated or fatal.
	if err = j.Append(ctx, baseHour, []Observation{obs("s", 2), obs("s", 3)}, Cursor{"s", 3}); err != nil {
		t.Fatal(err)
	}
	if rows, _ = j.ReadAfter(ctx, Cursor{}, 10); len(rows) != 3 {
		t.Fatalf("records after replay: %d", len(rows))
	}
}

func TestJournalCursorFallsBackWhenNewestFileIsCorruptOrEmpty(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	j := mustJournal(t, dir)
	if err := j.Append(ctx, baseHour, []Observation{obs("s", 1)}, Cursor{"s", 1}); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(ctx, baseHour.Add(time.Hour), []Observation{obs("s", 2)}, Cursor{"s", 2}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(dir, "journal", "2026102609.db")
	empty := filepath.Join(dir, "journal", "2026102610.db")
	writeGarbage(t, newest)
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	restarted := mustJournal(t, dir)
	got, ok, err := restarted.Cursor(ctx)
	if err != nil || !ok || got != (Cursor{"s", 1}) {
		t.Fatalf("recovered cursor: %+v %v %v", got, ok, err)
	}
	dropped := restarted.TakeDropped()
	if len(dropped) != 1 || dropped[0].Reason != "corrupt" || !dropped[0].Hour.Equal(baseHour.Add(time.Hour)) {
		t.Fatalf("dropped report: %+v", dropped)
	}
	if _, err = os.Stat(dropped[0].Quarantined); err != nil || !strings.Contains(dropped[0].Quarantined, ".db.corrupt-") {
		t.Fatalf("quarantine: %v", err)
	}
	if _, err = os.Stat(newest); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("corrupt hour file still in place")
	}
	if again := restarted.TakeDropped(); len(again) != 0 {
		t.Fatal("report not cleared")
	}
}

func TestJournalReadAfterCursorKeepsOrderAcrossHourFiles(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	j := mustJournal(t, dir)
	// Session b restarts at seq 1: order must follow append order, not session or seq.
	batches := []struct {
		hour time.Time
		obs  []Observation
		cur  Cursor
	}{
		{baseHour, []Observation{obs("a", 1), obs("a", 2)}, Cursor{"a", 2}},
		{baseHour.Add(time.Hour), []Observation{obs("a", 3), obs("b", 1)}, Cursor{"b", 1}},
		{baseHour.Add(2 * time.Hour), []Observation{obs("b", 2), obs("b", 3)}, Cursor{"b", 3}},
	}
	for _, b := range batches {
		if err := j.Append(ctx, b.hour, b.obs, b.cur); err != nil {
			t.Fatal(err)
		}
	}
	keys := func(rows []Observation) string {
		var parts []string
		for _, r := range rows {
			parts = append(parts, r.Session+string(rune('0'+r.Seq)))
		}
		return strings.Join(parts, ",")
	}
	for _, c := range []struct {
		after Cursor
		limit int
		want  string
	}{
		{Cursor{}, 10, "a1,a2,a3,b1,b2,b3"},
		{Cursor{"a", 2}, 10, "a3,b1,b2,b3"},
		{Cursor{"a", 3}, 10, "b1,b2,b3"},
		{Cursor{"b", 1}, 10, "b2,b3"},
		{Cursor{"b", 3}, 10, ""},
		{Cursor{"a", 1}, 3, "a2,a3,b1"},
		{Cursor{"gone", 9}, 2, "a1,a2"},
	} {
		rows, err := j.ReadAfter(ctx, c.after, c.limit)
		if err != nil || keys(rows) != c.want {
			t.Fatalf("after %+v limit %d: %q (%v), want %q", c.after, c.limit, keys(rows), err, c.want)
		}
	}
	if rows, err := j.ReadAfter(ctx, Cursor{}, 1); err != nil || len(rows) != 1 || string(rows[0].Body) != `{"k":1}` || rows[0].Kind != "sample" {
		t.Fatalf("fields: %+v %v", rows, err)
	}
	if _, err := j.ReadAfter(ctx, Cursor{}, 0); !errors.Is(err, ErrInvalidLimits) {
		t.Fatal("zero limit accepted")
	}
}

func TestJournalReadReportsCorruptMiddleFileUntilOwnerRepairs(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	j := mustJournal(t, dir)
	for i := 0; i < 3; i++ {
		n := uint64(i + 1)
		if err := j.Append(ctx, baseHour.Add(time.Duration(i)*time.Hour), []Observation{obs("s", n)}, Cursor{"s", n}); err != nil {
			t.Fatal(err)
		}
	}
	writeGarbage(t, filepath.Join(dir, "journal", "2026102609.db"))
	_ = os.Remove(filepath.Join(dir, "journal", "2026102609.db-wal"))
	_ = os.Remove(filepath.Join(dir, "journal", "2026102609.db-shm"))
	rows, err := j.ReadAfter(ctx, Cursor{}, 10)
	var damage *JournalDamage
	if !errors.As(err, &damage) || len(rows) != 0 {
		t.Fatalf("reader silently skipped corruption: %v %v", rows, err)
	}
	if dropped := j.TakeDropped(); len(dropped) != 0 {
		t.Fatal("reader quarantined file", dropped)
	}
	if err = j.RepairHour(ctx, damage.Hour); err != nil {
		t.Fatal(err)
	}
	losses, err := j.PendingLosses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, loss := range losses {
		if err = j.AppendLoss(ctx, baseHour.Add(3*time.Hour), loss.ID, obs("s", 3), Cursor{"s", 3}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = j.ReadAfter(ctx, Cursor{}, 10)
	if err != nil || len(rows) != 2 || rows[0].Seq != 1 || rows[1].Seq != 3 {
		t.Fatalf("rows %+v err %v", rows, err)
	}
	if d := j.TakeDropped(); len(d) != 1 {
		t.Fatalf("dropped: %+v", d)
	}
}

func TestJournalSchemaMismatchStartsNewFile(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	j := mustJournal(t, dir)
	if err := j.Append(ctx, baseHour, []Observation{obs("s", 1)}, Cursor{"s", 1}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "journal", "2026102608.db")
	if _, err := rawDB(t, path).Exec("UPDATE meta SET value='99' WHERE key='schema_version'"); err != nil {
		t.Fatal(err)
	}
	restarted := mustJournal(t, dir)
	if _, ok, err := restarted.Cursor(ctx); err != nil || ok {
		t.Fatalf("foreign-version file must be skipped: %v %v", ok, err)
	}
	if err := restarted.Append(ctx, baseHour, []Observation{obs("s", 2)}, Cursor{"s", 2}); err != nil {
		t.Fatal(err)
	}
	dropped := restarted.TakeDropped()
	if len(dropped) != 1 || dropped[0].Reason != "schema" || !strings.Contains(dropped[0].Quarantined, ".db.schema-") {
		t.Fatalf("dropped: %+v", dropped)
	}
	losses, err := restarted.PendingLosses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, loss := range losses {
		if err = restarted.AppendLoss(ctx, baseHour, loss.ID, obs("s", 2), Cursor{"s", 2}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := restarted.ReadAfter(ctx, Cursor{}, 10)
	if err != nil || len(rows) != 1 || rows[0].Seq != 2 {
		t.Fatalf("new file rows: %+v %v", rows, err)
	}
}

func TestJournalFilesDeleteAndEvictOver(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	j := mustJournal(t, dir)
	for i := 0; i < 5; i++ {
		n := uint64(i + 1)
		if err := j.Append(ctx, baseHour.Add(time.Duration(i)*time.Hour), []Observation{obs("s", n)}, Cursor{"s", n}); err != nil {
			t.Fatal(err)
		}
	}
	files, err := j.Files()
	if err != nil || len(files) != 5 || !files[0].Hour.Equal(baseHour) || files[0].Bytes <= 0 {
		t.Fatalf("files: %+v %v", files, err)
	}
	evicted, err := j.EvictOver(1<<40, 3)
	if err != nil || len(evicted) != 2 || !evicted[0].Equal(baseHour) {
		t.Fatalf("file-count eviction: %v %v", evicted, err)
	}
	if evicted, err = j.EvictOver(1, 6); err != nil || len(evicted) != 2 {
		t.Fatalf("byte eviction must keep the newest file: %v %v", evicted, err)
	}
	if files, _ = j.Files(); len(files) != 1 || !files[0].Hour.Equal(baseHour.Add(4*time.Hour)) {
		t.Fatalf("remaining: %+v", files)
	}
	if err = j.DeleteHour(baseHour.Add(4 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = j.DeleteHour(baseHour); err != nil {
		t.Fatal("deleting a missing hour:", err)
	}
	if err = j.Append(ctx, baseHour.Add(4*time.Hour), []Observation{obs("s", 9)}, Cursor{"s", 9}); err != nil {
		t.Fatal("append after deleting the active file:", err)
	}
}

func TestReadAfterNeverReplaysSessionRecordsBehindCursor(t *testing.T) {
	dir := historyDir(t)
	ctx := context.Background()
	j := mustJournal(t, dir)
	if err := j.Append(ctx, baseHour, []Observation{obs("s", 1), obs("s", 2), obs("s", 3)}, Cursor{"s", 3}); err != nil {
		t.Fatal(err)
	}
	// Empty batch: the cursor advances to seq 5 without a matching row.
	if err := j.Append(ctx, baseHour.Add(time.Hour), nil, Cursor{"s", 5}); err != nil {
		t.Fatal(err)
	}
	if err := j.Append(ctx, baseHour.Add(2*time.Hour), []Observation{obs("t", 1)}, Cursor{"t", 1}); err != nil {
		t.Fatal(err)
	}
	seqs := func(after Cursor) string {
		rows, err := j.ReadAfter(ctx, after, 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Session+string(rune('0'+r.Seq)))
		}
		return strings.Join(out, ",")
	}
	for after, want := range map[Cursor]string{
		{"s", 5}: "t1", // no exact row; nothing of session s, later session still returned
		{"s", 9}: "t1", // beyond every stored seq
		{"s", 2}: "s3,t1",
		{"t", 7}: "",
	} {
		if got := seqs(after); got != want {
			t.Fatalf("after %+v: %q, want %q", after, got, want)
		}
	}
	// Only later seqs exist for the session: they are returned, older sessions are not.
	if err := j.Append(ctx, baseHour.Add(3*time.Hour), []Observation{obs("u", 8), obs("u", 9)}, Cursor{"u", 9}); err != nil {
		t.Fatal(err)
	}
	if got := seqs(Cursor{"u", 3}); got != "u8,u9" {
		t.Fatalf("later seqs only: %q", got)
	}
}
