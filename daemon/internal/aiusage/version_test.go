package aiusage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func drained(s *Store) {
	select {
	case <-s.Changes():
	default:
	}
}

func signalled(s *Store) bool {
	select {
	case <-s.Changes():
		return true
	default:
		return false
	}
}

func TestEmptyScanKeepsTheVersionNumbersAndStaysQuiet(t *testing.T) {
	s, path := claudeStore(t)
	ctx := context.Background()
	defer func() { _ = s.Close(ctx) }()
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	// Backfill start and end are status, not data: revision and generation stay put.
	if v := s.State(); v.Revision != 0 || v.Generation != 0 || v.Backfilling || v.Error != "" {
		t.Fatalf("empty first scan: %+v", v)
	}
	drained(s)
	if e := s.Scan(ctx); e != nil || signalled(s) || s.State().Revision != 0 {
		t.Fatalf("empty rescan must not signal or move: %v %+v", e, s.State())
	}
	appendTo(t, path, block("10:00:01", "m1", 5))
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	if v := s.State(); v.Revision != 1 || v.Generation != 0 || !signalled(s) {
		t.Fatalf("an insert adds one revision: %+v", v)
	}
	drained(s)
	if e := s.Scan(ctx); e != nil || s.State().Revision != 1 || signalled(s) {
		t.Fatalf("an unchanged file must not move anything: %v %+v", e, s.State())
	}
}

func TestUnchangedFilesAreSkippedWithoutReadingTheDatabase(t *testing.T) {
	s, path := claudeStore(t)
	ctx := context.Background()
	defer func() { _ = s.Close(ctx) }()
	appendTo(t, path, block("10:00:01", "m1", 5))
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	if s.stampOf(path) == (stamp{}) {
		t.Fatal("the ingested file's stat must be remembered")
	}
	// A missing cursor table would fail any database read for this file.
	if _, e := s.w.ExecContext(ctx, "ALTER TABLE cursors RENAME TO cursors_away"); e != nil {
		t.Fatal(e)
	}
	if e := s.Scan(ctx); e != nil {
		t.Fatalf("an unchanged file must not touch the database: %v", e)
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	_ = s.Scan(ctx)
	if s.stampOf(path) != (stamp{}) {
		t.Fatal("a vanished file's stamp must be forgotten")
	}
}

func TestExpiredRowsMoveGenerationAndRevisionTogetherAtMostDailyAndSurviveRestart(t *testing.T) {
	h := home(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(h, ".claude", "projects", "p", "a.jsonl")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s := openAt(t, h, &now, nil)
	if e := s.Scan(ctx); e != nil { // first round prunes (nothing) and starts the daily clock
		t.Fatal(e)
	}
	appendTo(t, path, strings.ReplaceAll(block("10:00:01", "old", 5), "2026-10-28", "2026-06-01"), block("10:00:02", "new", 5))
	now = now.Add(time.Hour)
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	if v := s.State(); v.Revision != 1 || v.Generation != 0 || claudeRows(t, s).All != 2 {
		t.Fatalf("within a day nothing is deleted: %+v", v)
	}
	now = now.Add(24 * time.Hour)
	drained(s)
	if e := s.Scan(ctx); e != nil {
		t.Fatal(e)
	}
	v := s.State()
	if v.Revision != 2 || v.Generation != 1 || claudeRows(t, s).All != 1 || !signalled(s) {
		t.Fatalf("a delete moves both numbers by one: %+v", v)
	}
	if e := s.Scan(ctx); e != nil || s.State() != v {
		t.Fatalf("nothing more to delete: %v %+v", e, s.State())
	}
	if e := s.Close(ctx); e != nil {
		t.Fatal(e)
	}
	again := openAt(t, h, &now, nil)
	defer func() { _ = again.Close(ctx) }()
	if got := again.State(); got.Revision != 2 || got.Generation != 1 {
		t.Fatalf("restart must not roll back: %+v", got)
	}
}

func TestCollectorFollowsForegroundAndWakesWhenAPageOpens(t *testing.T) {
	s, path := claudeStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if s.isForeground() {
		t.Fatal("without a probe the collector is in the background")
	}
	visible := false
	s.SetForeground(func() bool { return visible })
	if s.isForeground() {
		t.Fatal("no visible page means the background cadence")
	}
	visible = true
	if !s.isForeground() {
		t.Fatal("a visible page means the foreground cadence")
	}
	visible = false
	if e := s.Configure(ctx); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close(context.Background()) }()
	time.Sleep(50 * time.Millisecond) // the first round has run; the next is two minutes away
	appendTo(t, path, block("10:00:01", "m1", 5))
	time.Sleep(50 * time.Millisecond)
	if s.State().Revision != 0 {
		t.Fatal("the background cadence must not rescan within two minutes")
	}
	s.Wake()
	for deadline := time.Now().Add(2 * time.Second); s.State().Revision == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if s.State().Revision != 1 {
		t.Fatalf("a wake must scan at once: %+v", s.State())
	}
}
