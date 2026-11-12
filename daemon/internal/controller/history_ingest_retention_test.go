package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/historystore"
)

// fill writes one row per hour starting at testEpoch, each with its own cursor, and returns the cursors.
func (h *ingestHarness) fill(hours int) []historystore.Cursor {
	h.t.Helper()
	var cursors []historystore.Cursor
	for i := range hours {
		c := historystore.Cursor{Session: "S1", Seq: uint64(i + 1)}
		row := historystore.Observation{Session: "S1", Seq: c.Seq, AtMs: 1, Kind: "started", Body: []byte("{}")}
		if err := h.journal.Journal.Append(context.Background(), testEpoch.Add(time.Duration(i)*time.Hour), []historystore.Observation{row}, c); err != nil {
			h.t.Fatal(err)
		}
		cursors = append(cursors, c)
	}
	h.in.started, h.in.cursor = true, cursors[len(cursors)-1]
	return cursors
}

func (h *ingestHarness) hours() []int {
	h.t.Helper()
	files, err := h.journal.Files()
	if err != nil {
		h.t.Fatal(err)
	}
	var out []int
	for _, f := range files {
		out = append(out, int(f.Hour.Sub(testEpoch)/time.Hour))
	}
	return out
}

func (h *ingestHarness) register(name string, c historystore.Cursor) {
	h.t.Helper()
	if err := h.in.consumers.Register(name, c); err != nil {
		h.t.Fatal(err)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRetentionDeletesByAgeOnlyAfterEveryConsumerPassed(t *testing.T) {
	h := newIngestHarness(t)
	cursors := h.fill(5) // hour files 0..4
	maintain := func(at time.Duration) {
		h.clock = testEpoch.Add(at)
		h.in.lastMaintain = time.Time{}
		h.in.maintain(context.Background())
	}

	maintain(48 * time.Hour)
	if got := h.hours(); !equalInts(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("with no consumer nothing may be deleted by age: %v", got)
	}

	h.register("logs", cursors[2])               // standing in file 2
	h.register("traffic", historystore.Cursor{}) // has consumed nothing
	maintain(48 * time.Hour)
	if got := h.hours(); !equalInts(got, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("a consumer that read nothing must hold every file: %v", got)
	}

	h.in.consumers.Advance("traffic", cursors[4])
	maintain(48 * time.Hour)
	if got := h.hours(); !equalInts(got, []int{2, 3, 4}) {
		t.Fatalf("files before the slowest consumer (file 2) must go: %v", got)
	}

	h.in.consumers.Advance("logs", cursors[4])
	maintain(2*time.Hour + 30*time.Minute) // file 2 ended 0.5 h ago... but is younger than 2 h
	if got := h.hours(); !equalInts(got, []int{2, 3, 4}) {
		t.Fatalf("files younger than 2 h after their hour ended must stay: %v", got)
	}
	maintain(5 * time.Hour) // files 0..3 ended; 2 and 3 have been ended for 2 h and 1 h
	if got := h.hours(); !equalInts(got, []int{3, 4}) {
		t.Fatalf("file 2 ended 2 h ago and is consumed: %v", got)
	}
	maintain(48 * time.Hour)
	if got := h.hours(); !equalInts(got, []int{4}) {
		t.Fatalf("the newest file always stays, it holds the resume cursor: %v", got)
	}
	if len(h.gaps()) != 0 {
		t.Fatalf("consumed files leave no gap: %+v", h.gaps())
	}
}

func TestRetentionHardCapEvictsAndMarksUnconsumedRanges(t *testing.T) {
	h := newIngestHarness(t)
	cursors := h.fill(8) // two more files than the cap of 6
	h.register("logs", cursors[0])
	h.register("traffic", cursors[1])
	h.clock = testEpoch.Add(7*time.Hour + 30*time.Minute) // inside the newest file's hour; files 0 and 1 go by the cap, not by age
	h.in.maintain(context.Background())
	if got := h.hours(); !equalInts(got, []int{2, 3, 4, 5, 6, 7}) {
		t.Fatalf("cap of %d files not enforced: %v", journalMaxFiles, got)
	}
	gaps := h.gaps()
	if len(gaps) != 2 {
		t.Fatalf("want a marker per unconsumed evicted file, got %+v", gaps)
	}
	for i, g := range gaps {
		hour := testEpoch.Add(time.Duration(i) * time.Hour)
		if g.Reason != gapEvicted || g.FromMs != hour.UnixMilli() || g.ToMs != hour.Add(time.Hour).UnixMilli() {
			t.Fatalf("gap %d %+v", i, g)
		}
	}
	cursor, _, err := h.journal.Cursor(context.Background())
	if err != nil || cursor != cursors[7] {
		t.Fatalf("a gap marker must carry the committed cursor, got %+v %v", cursor, err)
	}
}

func TestRetentionEvictionWithoutUnconsumedRangeLeavesNoMarker(t *testing.T) {
	h := newIngestHarness(t)
	cursors := h.fill(8)
	h.register("logs", cursors[7])
	h.clock = testEpoch.Add(7*time.Hour + 30*time.Minute)
	h.in.maintain(context.Background())
	if len(h.gaps()) != 0 {
		t.Fatalf("evicting consumed files must not report a gap: %+v", h.gaps())
	}
}

func TestRetentionRecordsGapForQuarantinedJournalFile(t *testing.T) {
	h := newIngestHarness(t)
	h.fill(2)
	h.restart()
	newest := filepath.Join(h.dir, "journal", testEpoch.Add(time.Hour).Format("2006010215")+".db")
	if err := os.WriteFile(newest, []byte("this is not a database this is not a database"), 0600); err != nil {
		t.Fatal(err)
	}
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	h.clock = testEpoch.Add(2 * time.Hour)
	h.settle() // Cursor() quarantines the corrupt newest file and falls back to hour 0
	h.in.maintain(context.Background())
	gaps := h.gaps()
	found := false
	for _, g := range gaps {
		found = found || g.Reason == gapDropped && g.FromMs == testEpoch.Add(time.Hour).UnixMilli()
	}
	if !found {
		t.Fatalf("quarantined file left no gap marker: %+v", gaps)
	}
}

func TestConsumerRegistrationIsSealedByStart(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := New("test", "test")
	c.ConfigureCoreSession(pagerSession{newFakeCore("S1")})
	if err = c.ConfigureHistoryConsumers(HistoryConsumerLogs, HistoryConsumerTraffic); err != nil {
		t.Fatal(err)
	}
	if err = c.ConfigureHistoryIngest(context.Background(), dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	t.Cleanup(func() { _ = c.CloseHistoryIngest(closeCtx) })
	if err = c.ingest.consumers.Register("late", historystore.Cursor{}); !errors.Is(err, errConsumersSealed) {
		t.Fatalf("late Register: %v", err)
	}
	if err = c.ConfigureHistoryConsumers("late"); !errors.Is(err, errConsumersSealed) {
		t.Fatalf("late ConfigureHistoryConsumers: %v", err)
	}
	got := c.ingest.consumers.snapshot()
	if len(got) != 2 {
		t.Fatalf("pre-registered consumers not used by ingest: %v", got)
	}
	c.ingest.consumers.Advance(HistoryConsumerLogs, historystore.Cursor{Session: "S1", Seq: 1})
	c.ingest.consumers.Unregister(HistoryConsumerTraffic)
	if got = c.ingest.consumers.snapshot(); len(got) != 1 {
		t.Fatalf("Advance/Unregister must keep working after sealing: %v", got)
	}
}

func TestPreRegisteredZeroConsumersBlockAgeDeletionAndMarkEviction(t *testing.T) {
	h := newIngestHarness(t)
	cursors := h.fill(8)
	h.register(HistoryConsumerLogs, historystore.Cursor{})
	h.register(HistoryConsumerTraffic, historystore.Cursor{})
	h.clock = testEpoch.Add(7*time.Hour + 30*time.Minute) // files 2..4 are old enough for age deletion
	h.in.maintain(context.Background())
	if got := h.hours(); !equalInts(got, []int{2, 3, 4, 5, 6, 7}) {
		t.Fatalf("only the hard cap may delete while consumers are at zero: %v", got)
	}
	if gaps := h.gaps(); len(gaps) != 2 || gaps[0].Reason != gapEvicted {
		t.Fatalf("unconsumed evicted hours need markers: %+v", gaps)
	}
	if cursor, _, _ := h.journal.Cursor(context.Background()); cursor != cursors[7] {
		t.Fatalf("cursor %+v", cursor)
	}
}
