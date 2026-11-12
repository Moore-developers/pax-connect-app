package controller

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// logsPipeline wires a real journal and the real log writer into a test controller. Tests
// append data through the journal (the production write path) and use barrier to wait until
// the writer has consumed the backlog, so no assertion depends on timing.
type logsPipeline struct {
	t       testing.TB
	c       *Controller
	journal *historystore.Journal
	cursor  historystore.Cursor
}

// newLogsTestController creates a controller with both stores in dir. The legacy owner is
// configured too because Watch and snapshots still carry its traffic state.
func newLogsTestController(t testing.TB, dir string) (*Controller, *logsPipeline) {
	t.Helper()
	ctx := context.Background()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := New("test", "test")
	if err := c.ConfigureHistory(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.CloseHistoryLogs(closeCtx)
		_ = c.CloseHistory(closeCtx)
	})
	p := attachLogsPipeline(t, c, dir)
	if err := c.ConfigureHistoryTraffic(ctx, dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	live := newLiveState()
	live.seeded = true
	c.liveView.Store(live)
	t.Cleanup(func() { _ = c.CloseHistoryTraffic(context.Background()) })
	return c, p
}

func newLogsTestDir(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// attachLogsPipeline opens logs.db and starts its single writer for c.
func attachLogsPipeline(t testing.TB, c *Controller, dir string) *logsPipeline {
	t.Helper()
	ctx := context.Background()
	uid := uint32(os.Geteuid())
	historyDir := filepath.Join(dir, "history")
	journal, err := historystore.OpenJournal(historyDir, uid)
	if err != nil {
		t.Fatal(err)
	}
	logs, _, err := historystore.OpenLogs(ctx, historyDir, uid)
	if err != nil {
		_ = journal.Close()
		t.Fatal(err)
	}
	if err = ensureLogGenerations(ctx, logs); err != nil {
		_ = journal.Close()
		_ = logs.Close()
		t.Fatal(err)
	}
	consumers := newJournalConsumers()
	if err = consumers.Register(HistoryConsumerLogs, historystore.Cursor{}); err != nil {
		t.Fatal(err)
	}
	l, err := newHistoryLogs(ctx, logs, journal, consumers)
	if err != nil {
		t.Fatal(err)
	}
	l.route = c.mapHistoryRoute
	l.reads = c.readTokens
	l.onMeta = c.acceptLogMeta
	l.publishState(ctx)
	l.start(ctx)
	c.logsMu.Lock()
	c.logs = l
	c.logsMu.Unlock()
	c.syncLogSnapshot()
	cursor, _, err := journal.Cursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := &logsPipeline{t: t, c: c, journal: journal, cursor: cursor}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.CloseHistoryLogs(closeCtx)
	})
	return p
}

// feed appends one core batch to the journal with the same gap and cursor rules as the
// capture goroutine, then waits until the log writer has consumed it.
func (p *logsPipeline) feed(batch config.ObservationBatch) {
	p.t.Helper()
	now := time.Now()
	if batch.Session == "" {
		batch.Session = "fixture"
	}
	gap := detectCoreGap(p.cursor, batch, false)
	next := p.cursor
	if batch.Session != p.cursor.Session {
		next = historystore.Cursor{Session: batch.Session}
	}
	var rows []historystore.Observation
	records := 0
	for _, o := range batch.Records {
		if o.Session != batch.Session || o.Sequence <= next.Seq {
			continue
		}
		rows = append(rows, journalRecord(o))
		next.Seq = o.Sequence
		records++
	}
	if batch.ResetToken != "" && batch.BaselineComplete {
		next.Seq = max(next.Seq, batch.BaselineHighWater)
	}
	if gap != nil {
		if records > 0 {
			gap.ThroughSeq = next.Seq
		}
		rows = append([]historystore.Observation{gap.observation(now)}, rows...)
	}
	if len(rows) == 0 && next == p.cursor {
		return
	}
	if err := p.journal.Append(context.Background(), now, rows, next); err != nil {
		p.t.Fatal(err)
	}
	p.cursor = next
	p.barrier()
}

// observe writes one batch to both stores: log queries read logs.db while traffic queries
// still read the legacy owner, so cross-domain tests need the same batch on both sides.
func (p *logsPipeline) observe(batch config.ObservationBatch) {
	p.t.Helper()
	if err := FeedHistoryFixture(p.t, p.c, context.Background(), batch); err != nil {
		p.t.Fatal(err)
	}
	p.feed(batch)
}

// emit pushes explicit product events through the writer inbox, like enqueueHistoryEvent.
func (p *logsPipeline) emit(events ...config.HistoryEvent) {
	p.t.Helper()
	for len(events) > 0 {
		n := min(len(events), logsInboxCapacity)
		for _, event := range events[:n] {
			p.c.logs.enqueue(event)
		}
		p.barrier()
		events = events[n:]
	}
}

// barrier waits until every write enqueued before the call has been committed (Flush).
func (p *logsPipeline) barrier() {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.c.logs.Flush(ctx); err != nil {
		p.t.Fatalf("log writer barrier: %v", err)
	}
}

// stripLogEventCheckpoints rewrites logs.db the way an older writer left it: event bodies
// carry no summary checkpoint and the summary generation marker is missing. Reopening must
// report the counters as unknown instead of trusting them.
func stripLogEventCheckpoints(t *testing.T, path string) {
	t.Helper()
	editLogsDatabase(t, path, func(db *sql.DB) error {
		rows, err := db.QueryContext(context.Background(), "SELECT seq,body FROM events")
		if err != nil {
			return err
		}
		type eventRow struct {
			seq  int64
			body []byte
		}
		var events []eventRow
		for rows.Next() {
			var row eventRow
			if err = rows.Scan(&row.seq, &row.body); err != nil {
				_ = rows.Close()
				return err
			}
			events = append(events, row)
		}
		_ = rows.Close()
		for _, row := range events {
			var value map[string]any
			if err = json.Unmarshal(row.body, &value); err != nil {
				return err
			}
			delete(value, "Metrics")
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if _, err = db.ExecContext(context.Background(), "UPDATE events SET body=? WHERE seq=?", raw, row.seq); err != nil {
				return err
			}
		}
		_, err = db.ExecContext(context.Background(), "UPDATE meta SET value='' WHERE key=?", historystore.MetaSummaryGeneration)
		return err
	})
}
