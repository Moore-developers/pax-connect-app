package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// fakeCore mimics the core's cursor API (core_observations.go, core_baseline.go): replayable
// records, a lost range, and a frozen baseline paged by cursor. Single goroutine only.
type fakeCore struct {
	session    string
	records    []config.Observation
	head       uint64
	lostBefore uint64
	flows      []config.FlowFact
	collecting bool
	forceReset bool // next plain read demands a baseline although nothing was lost
	err        error
	reads      []config.ObservationRead
	baseline   int // flows per baseline page
}

func newFakeCore(session string) *fakeCore {
	return &fakeCore{session: session, collecting: true, baseline: 256}
}

func (f *fakeCore) high() uint64 { return f.head }

// add appends a record with the next sequence number.
func (f *fakeCore) add(at time.Time, kind string, flow *config.FlowFact, up, down uint64) {
	f.head++
	f.records = append(f.records, config.Observation{Session: f.session, Sequence: f.head, At: at, Kind: kind, Flow: flow, Upload: up, Download: down})
}

// overflow drops every record, as a full core buffer would, and reports them lost.
func (f *fakeCore) overflow() {
	f.lostBefore = f.head + 1
	f.records = nil
}

func (f *fakeCore) ReadObservationPage(_ context.Context, q config.ObservationRead) (config.ObservationBatch, error) {
	f.reads = append(f.reads, q)
	if f.err != nil {
		return config.ObservationBatch{}, f.err
	}
	b := config.ObservationBatch{Session: f.session, HighWater: f.high(), LostBefore: f.lostBefore, Collecting: f.collecting}
	reset := q.Session != "" && q.Session != f.session || q.After > f.high() || q.After+1 < f.lostBefore || f.forceReset
	if !reset && q.ResetToken == "" {
		for _, r := range f.records {
			if r.Sequence > q.After && len(b.Records) < 256 {
				b.Records = append(b.Records, r)
			}
		}
		return b, nil
	}
	offset, _ := strconv.Atoi(q.Cursor)
	if q.ResetToken == "" || q.ResetToken == ingestSeedToken {
		offset = 0
	}
	end := min(len(f.flows), offset+f.baseline)
	b.ResetRequired = offset == 0
	b.ResetToken = "baseline"
	b.Baseline = append([]config.FlowFact(nil), f.flows[offset:end]...)
	b.BaselineHighWater = f.high()
	b.BaselineComplete = end == len(f.flows)
	if !b.BaselineComplete {
		b.NextCursor = strconv.Itoa(end)
	}
	f.forceReset = false
	return b, nil
}

// flakyJournal wraps a real journal and fails the next appends on demand.
type flakyJournal struct {
	*historystore.Journal
	fail    int
	appends []int // record count of every Append attempt
}

func (j *flakyJournal) Append(ctx context.Context, at time.Time, batch []historystore.Observation, cursor historystore.Cursor) error {
	j.appends = append(j.appends, len(batch))
	if j.fail > 0 {
		j.fail--
		return errors.New("disk full")
	}
	return j.Journal.Append(ctx, at, batch, cursor)
}

type ingestHarness struct {
	t       *testing.T
	dir     string
	core    *fakeCore
	journal *flakyJournal
	in      *historyIngest
	clock   time.Time
}

var testEpoch = time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)

func historyTestDir(t *testing.T) string {
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

func newIngestHarness(t *testing.T) *ingestHarness {
	t.Helper()
	h := &ingestHarness{t: t, dir: historyTestDir(t), core: newFakeCore("S1"), clock: testEpoch}
	h.open()
	return h
}

// open (re)creates the journal and ingest over the same directory and core, as a daemon restart does.
func (h *ingestHarness) open() {
	h.t.Helper()
	j, err := historystore.OpenJournal(h.dir, uint32(os.Geteuid()))
	if err != nil {
		h.t.Fatal(err)
	}
	h.journal = &flakyJournal{Journal: j}
	h.t.Cleanup(func() { _ = j.Close() })
	h.in = newHistoryIngest(h.journal, h.core)
	h.in.now = func() time.Time { return h.clock }
}

func (h *ingestHarness) restart() {
	h.t.Helper()
	if err := h.journal.Close(); err != nil {
		h.t.Fatal(err)
	}
	h.open()
}

// settle steps until the ingest idles at the poll interval and returns the delays it asked for.
func (h *ingestHarness) settle() []time.Duration {
	h.t.Helper()
	var delays []time.Duration
	for range 50 {
		d := h.in.step(context.Background())
		delays = append(delays, d)
		if d == ingestPollInterval {
			return delays
		}
	}
	h.t.Fatalf("ingest never went idle: %v", delays)
	return nil
}

// allRows is the whole journal, poll rows included.
func (h *ingestHarness) allRows() []historystore.Observation {
	h.t.Helper()
	rows, err := h.journal.ReadAfter(context.Background(), historystore.Cursor{}, 100000)
	if err != nil {
		h.t.Fatal(err)
	}
	return rows
}

// rows is the journal without poll rows: the records and gap markers.
func (h *ingestHarness) rows() []historystore.Observation {
	h.t.Helper()
	var out []historystore.Observation
	for _, r := range h.allRows() {
		if r.Kind != journalPollKind {
			out = append(out, r)
		}
	}
	return out
}

func (h *ingestHarness) gaps() []journalGap {
	h.t.Helper()
	var out []journalGap
	for _, r := range h.rows() {
		if r.Kind == journalGapKind {
			out = append(out, decodeGap(h.t, r))
		}
	}
	return out
}

func flow(key string) *config.FlowFact {
	return &config.FlowFact{Key: key, StartedAt: testEpoch, Direction: "proxy", RouteID: "route-a", Domain: key + ".example.test", Transport: "tcp"}
}
