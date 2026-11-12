package controller

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

func decodeGap(t *testing.T, row historystore.Observation) journalGap {
	t.Helper()
	var g journalGap
	if err := json.Unmarshal(row.Body, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func TestIngestPollsEveryFiveSecondsAndDrainsFullPagesImmediately(t *testing.T) {
	h := newIngestHarness(t)
	for range 600 {
		h.core.add(testEpoch, "started", flow("a"), 0, 0)
	}
	// Seed baseline, two full pages, the remainder, then idle at the poll interval.
	want := []time.Duration{0, 0, 0, ingestPollInterval}
	if got := h.settle(); !slices.Equal(got, want) {
		t.Fatalf("delays %v want %v", got, want)
	}
	if got := len(h.rows()); got != 600 {
		t.Fatalf("journal holds %d rows, want 600", got)
	}
	for _, n := range h.journal.appends {
		if n > 2000 {
			t.Fatalf("Append of %d records exceeds the 2000 bound (HS-R2)", n)
		}
	}
	// The seed baseline commits its cursor alone; a page carries no poll row while the span is young.
	if pageSizes := h.journal.appends; !slices.Equal(pageSizes, []int{0, 256, 256, 88}) {
		t.Fatalf("append sizes %v", pageSizes)
	}
	// An idle poll does not speed up and writes nothing: an empty read is not a journal event.
	before := len(h.journal.appends)
	if d := h.in.step(context.Background()); d != ingestPollInterval || len(h.journal.appends) != before {
		t.Fatalf("idle poll: delay %v, appends %v", d, h.journal.appends)
	}
	last := h.core.reads[len(h.core.reads)-1]
	if last.Limit != 256 || last.After != 600 || last.Session != "S1" {
		t.Fatalf("pull request %+v", last)
	}
}

func TestIngestRejectsOversizedCorePage(t *testing.T) {
	h := newIngestHarness(t)
	h.core.err = nil
	for range 300 {
		h.core.add(testEpoch, "started", flow("a"), 0, 0)
	}
	h.settle()
	oversized := &oversizedCore{fakeCore: h.core}
	h.in.core = oversized
	appends := len(h.journal.appends)
	if d := h.in.step(context.Background()); d != time.Second || len(h.journal.appends) != appends {
		t.Fatalf("oversized page accepted: delay %v appends %d->%d", d, appends, len(h.journal.appends))
	}
}

type oversizedCore struct{ *fakeCore }

func (o *oversizedCore) ReadObservationPage(context.Context, config.ObservationRead) (config.ObservationBatch, error) {
	b := config.ObservationBatch{Session: o.session, Collecting: true}
	for i := range 3000 {
		b.Records = append(b.Records, config.Observation{Session: o.session, Sequence: uint64(1000 + i), At: testEpoch, Kind: "started"})
	}
	return b, nil
}

func TestIngestCoreFailureBacksOffThenRecovers(t *testing.T) {
	h := newIngestHarness(t)
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	h.settle()
	h.core.err = errors.New("core down")
	var got []time.Duration
	for range 8 {
		got = append(got, h.in.step(context.Background()))
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for i := range want {
		want[i] *= time.Second
	}
	if !slices.Equal(got, want) {
		t.Fatalf("backoff %v want %v", got, want)
	}
	if snap := h.in.live.Snapshot(testEpoch); snap.Known {
		t.Fatal("active count must be unknown while the core cannot be read")
	}
	h.core.err = nil
	h.core.add(testEpoch, "started", flow("b"), 0, 0)
	h.settle()
	h.core.err = errors.New("core down")
	if d := h.in.step(context.Background()); d != time.Second {
		t.Fatalf("backoff did not reset after success: %v", d)
	}
	if got := len(h.rows()); got != 2 {
		t.Fatalf("rows %d", got)
	}
}

func TestIngestJournalFailureKeepsCursorBacksOffAndNeverDoubleCountsLive(t *testing.T) {
	h := newIngestHarness(t)
	a := flow("a")
	h.core.flows = nil
	h.settle()
	h.core.add(testEpoch, "started", a, 0, 0)
	h.core.add(testEpoch.Add(5*time.Second), "sample", a, 100, 1000)
	h.journal.fail = 2
	ctx := context.Background()
	var delays []time.Duration
	for range 3 {
		delays = append(delays, h.in.step(ctx))
	}
	// Two failures back off 1s, 2s; the third pull commits.
	if !slices.Equal(delays[:2], []time.Duration{time.Second, 2 * time.Second}) || delays[2] != ingestPollInterval {
		t.Fatalf("delays %v", delays)
	}
	// Every pull restarted from the last committed journal cursor.
	pulls := h.core.reads[len(h.core.reads)-3:]
	for _, r := range pulls {
		if r.After != 0 {
			t.Fatalf("pull cursor advanced past an uncommitted page: %+v", r)
		}
	}
	if got := len(h.rows()); got != 2 {
		t.Fatalf("journal rows %d, want 2 without duplicates", got)
	}
	live := h.in.live.flows["a"]
	if live == nil || live.Upload != 100 || live.Download != 1000 {
		t.Fatalf("live bytes were counted more than once: %+v", live)
	}
	if cursor, ok, err := h.journal.Cursor(ctx); err != nil || !ok || cursor != (historystore.Cursor{Session: "S1", Seq: 2}) {
		t.Fatalf("committed cursor %+v %v %v", cursor, ok, err)
	}
	// Watch kept moving during the failure: the first failed pull already updated the live state.
	if h.in.live.mark.Seq != 2 {
		t.Fatalf("live watermark %+v", h.in.live.mark)
	}
}

func TestIngestRestartResumesFromJournalCursorWithoutDuplicateRows(t *testing.T) {
	h := newIngestHarness(t)
	h.core.flows = []config.FlowFact{*flow("live")}
	for range 5 {
		h.core.add(testEpoch, "started", flow("a"), 0, 0)
	}
	h.settle()
	h.restart()
	for range 2 {
		h.core.add(testEpoch, "started", flow("b"), 0, 0)
	}
	h.core.reads = nil
	h.settle()
	first := h.core.reads[0]
	if first.Session != "S1" || first.After != 5 {
		t.Fatalf("restart did not resume at the journal cursor: %+v", first)
	}
	rows := h.rows()
	if len(rows) != 7 {
		t.Fatalf("journal rows %d, want 7", len(rows))
	}
	for i, r := range rows {
		if r.Seq != uint64(i+1) {
			t.Fatalf("row %d has seq %d: duplicates or reordering", i, r.Seq)
		}
	}
	if len(h.gaps()) != 0 {
		t.Fatalf("a clean restart must not record a gap: %+v", h.gaps())
	}
	// The restarted live state was reseeded from the baseline, not left empty.
	if snap := h.in.live.Snapshot(testEpoch); !snap.Known || snap.Active != 1 {
		t.Fatalf("live state after restart: %+v", snap)
	}
}

func TestIngestRecordsGapOnLostSessionChangeAndReset(t *testing.T) {
	cases := []struct {
		name   string
		cause  func(*fakeCore)
		reason string
		prev   string
		from   uint64
		to     uint64
	}{
		{"lost range", func(c *fakeCore) {
			c.add(testEpoch, "started", flow("a"), 0, 0)
			c.add(testEpoch, "started", flow("a"), 0, 0)
			c.overflow()
			c.add(testEpoch, "started", flow("n"), 0, 0)
		}, gapLost, "", 4, 5},
		{"session change", func(c *fakeCore) { *c = *newFakeCore("S2"); c.add(testEpoch, "started", flow("n"), 0, 0) }, gapSessionChanged, "S1", 1, 1},
		{"reset required", func(c *fakeCore) { c.forceReset = true; c.add(testEpoch, "started", flow("n"), 0, 0) }, gapReset, "", 4, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newIngestHarness(t)
			for range 3 {
				h.core.add(testEpoch, "started", flow("a"), 0, 0)
			}
			h.settle()
			if len(h.gaps()) != 0 {
				t.Fatalf("gap before the cause: %+v", h.gaps())
			}
			tc.cause(h.core)
			h.core.flows = []config.FlowFact{*flow("n")}
			h.settle()
			h.core.add(testEpoch, "started", flow("tail"), 0, 0)
			h.settle()
			gaps := h.gaps()
			if len(gaps) != 1 {
				t.Fatalf("want exactly one gap marker, got %+v", gaps)
			}
			want := journalGap{Reason: tc.reason, Session: h.core.session, PrevSession: tc.prev, FromSeq: tc.from, ToSeq: tc.to}
			if gaps[0] != want {
				t.Fatalf("gap %+v want %+v", gaps[0], want)
			}
			// The marker is committed with the cursor that jumps over the lost range, right before the next page.
			rows := h.rows()
			if rows[len(rows)-1].FlowKey != "tail" || rows[len(rows)-2].Kind != journalGapKind {
				t.Fatalf("gap not adjacent to the next page: %v", kinds(rows))
			}
			cursor, _, _ := h.journal.Cursor(context.Background())
			if cursor.Session != h.core.session || cursor.Seq != h.core.high() {
				t.Fatalf("cursor after gap %+v", cursor)
			}
		})
	}
}

func kinds(rows []historystore.Observation) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}

func TestIngestJournalFailureDuringGapKeepsTheMarker(t *testing.T) {
	h := newIngestHarness(t)
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	h.settle()
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	h.core.overflow()
	h.core.add(testEpoch, "started", flow("n"), 0, 0)
	h.journal.fail = 1
	h.settle()
	if got := h.gaps(); len(got) != 1 || got[0].Reason != gapLost {
		t.Fatalf("marker lost or duplicated after a failed append: %+v", got)
	}
}

// stuckCore always claims more is pending (a full page of already-seen records) and never moves.
type stuckCore struct{ reads int }

func (s *stuckCore) ReadObservationPage(_ context.Context, q config.ObservationRead) (config.ObservationBatch, error) {
	s.reads++
	b := config.ObservationBatch{Session: "S1", HighWater: 1000, Collecting: true}
	if q.ResetToken != "" {
		b.ResetToken, b.BaselineComplete, b.BaselineHighWater = "t", true, 0
		return b, nil
	}
	for i := range ingestPageRecords {
		b.Records = append(b.Records, config.Observation{Session: "S1", Sequence: uint64(i + 1), At: testEpoch, Kind: "started"})
	}
	return b, nil
}

func TestIngestForcesPollIntervalAfterEightSpinsWithoutProgress(t *testing.T) {
	h := newIngestHarness(t)
	h.in.core = &stuckCore{}
	h.core.add(testEpoch, "started", flow("a"), 0, 0)
	var delays []time.Duration
	for range 14 {
		delays = append(delays, h.in.step(context.Background()))
		if delays[len(delays)-1] == ingestPollInterval {
			break
		}
	}
	zero := 0
	for _, d := range delays {
		if d == 0 {
			zero++
		}
	}
	// The first pass reads 256 fresh records (progress); after that nothing moves.
	if delays[len(delays)-1] != ingestPollInterval || zero > ingestMaxSpins+2 {
		t.Fatalf("hot loop not bounded: %v", delays)
	}
}
