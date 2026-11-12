package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// trafficEpoch sits 10 s before a 5-minute boundary so fixtures straddle two buckets.
var trafficEpoch = time.Date(2026, 10, 26, 8, 4, 50, 0, time.UTC)

// flakyTraffic fails the next Update calls on demand and counts them.
type flakyTraffic struct {
	*historystore.Traffic
	fail    int
	updates int
}

func (f *flakyTraffic) Update(ctx context.Context, fn func(*historystore.TrafficTx) error) error {
	f.updates++
	if f.fail > 0 {
		f.fail--
		return errors.New("disk full")
	}
	return f.Traffic.Update(ctx, fn)
}

type trafficHarness struct {
	t         *testing.T
	base, dir string
	journal   *historystore.Journal
	store     *flakyTraffic
	tr        *historyTraffic
	consumers *journalConsumers
	clock     time.Time
	seq       uint64
}

func newTrafficHarness(t *testing.T) *trafficHarness {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(parent, "Application Support")
	if err = os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	h := &trafficHarness{t: t, base: base, dir: filepath.Join(base, "history"), clock: trafficEpoch.Add(time.Minute)}
	h.open()
	return h
}

// open (re)creates the journal handle, traffic.db and the job over the same directory, as a daemon restart does.
func (h *trafficHarness) open() {
	h.t.Helper()
	uid := uint32(os.Geteuid())
	j, err := historystore.OpenJournal(h.dir, uid)
	if err != nil {
		h.t.Fatal(err)
	}
	store, _, err := historystore.OpenTraffic(context.Background(), h.dir, uid)
	if err != nil {
		_ = j.Close()
		h.t.Fatal(err)
	}
	h.journal, h.store = j, &flakyTraffic{Traffic: store}
	h.consumers = newJournalConsumers()
	if err = h.consumers.Register(HistoryConsumerTraffic, historystore.Cursor{}); err != nil {
		h.t.Fatal(err)
	}
	h.tr, err = newHistoryTraffic(context.Background(), h.store, j, h.consumers)
	if err != nil {
		h.t.Fatal(err)
	}
	h.tr.now = func() time.Time { return h.clock }
	h.t.Cleanup(func() {
		_ = j.Close()
		_ = store.Close()
	})
}

// restart closes both handles and reopens them: the committed cursor and marks are all that survives.
func (h *trafficHarness) restart() {
	h.t.Helper()
	if err := h.journal.Close(); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.Close(); err != nil {
		h.t.Fatal(err)
	}
	h.open()
}

// obs builds the next core observation of session "s" at offset d from the epoch.
func (h *trafficHarness) obs(d time.Duration, kind string, f *config.FlowFact, up, down uint64) config.Observation {
	h.seq++
	return config.Observation{Session: "s", Sequence: h.seq, At: trafficEpoch.Add(d), Kind: kind, Flow: f, Upload: up, Download: down}
}

// append writes records and their poll row in one journal transaction, as the capture goroutine does.
func (h *trafficHarness) append(pollAt time.Time, collecting bool, records ...config.Observation) {
	h.t.Helper()
	rows := make([]historystore.Observation, 0, len(records)+1)
	cursor := historystore.Cursor{Session: "s", Seq: h.seq}
	for _, o := range records {
		rows = append(rows, journalRecord(o))
	}
	rows = append(rows, journalPoll{Session: "s", Collecting: collecting, AtMs: pollAt.UnixMilli()}.observation())
	if err := h.journal.Append(context.Background(), pollAt, rows, cursor); err != nil {
		h.t.Fatal(err)
	}
}

func (h *trafficHarness) appendGap(at time.Time, g journalGap) {
	h.t.Helper()
	if err := h.journal.Append(context.Background(), at, []historystore.Observation{g.observation(at)}, historystore.Cursor{Session: "s", Seq: h.seq}); err != nil {
		h.t.Fatal(err)
	}
}

// settle steps until the job idles at the poll interval.
func (h *trafficHarness) settle() {
	h.t.Helper()
	for range 100 {
		switch d := h.tr.step(context.Background()); d {
		case trafficPollInterval:
			return
		case 0:
		default:
			h.t.Fatalf("job backed off by %v", d)
		}
	}
	h.t.Fatal("traffic job never went idle")
}

// rows reads one table straight from the database.
func (h *trafficHarness) rows(table string) []historystore.TrafficRow {
	h.t.Helper()
	return readTrafficTable(h.t, h.store.Traffic, table)
}

func (h *trafficHarness) appendPoll(at time.Time, session string, collecting bool) {
	h.t.Helper()
	row := journalPoll{Session: session, Collecting: collecting, AtMs: at.UnixMilli()}.observation()
	if err := h.journal.Append(context.Background(), at, []historystore.Observation{row}, historystore.Cursor{Session: "s", Seq: h.seq}); err != nil {
		h.t.Fatal(err)
	}
}

// flowMarks reads every stored mark by flow key.
func (h *trafficHarness) flowMarks() map[string]historystore.FlowMark {
	h.t.Helper()
	marks, err := h.store.FlowMarks(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	out := map[string]historystore.FlowMark{}
	for _, m := range marks {
		out[m.FlowKey] = m
	}
	return out
}

// counters sums bytes, new connections and failures of one dimension in traffic_5m.
func (h *trafficHarness) counters(dimension string) (up, down, conns, failed int64) {
	h.t.Helper()
	for _, r := range h.rows("traffic_5m") {
		if r.Dimension == dimension {
			up, down, conns, failed = up+r.UpBytes, down+r.DownBytes, conns+r.NewConns, failed+r.Failures
		}
	}
	return
}

func osEuid() int { return os.Geteuid() }
