package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// ConfigureHistoryFixture wires the real journal/traffic/log jobs used by RPC tests.
// It is compiled only by go test; production has no fixture branch.
func ConfigureHistoryFixture(t testing.TB, c *Controller, ctx context.Context, dir string, owner uint32) error {
	t.Helper()
	if err := c.ConfigureHistory(ctx, dir, owner); err != nil {
		return err
	}
	if err := c.ConfigureHistoryConsumers(HistoryConsumerLogs, HistoryConsumerTraffic); err != nil {
		return err
	}
	if err := c.ConfigureHistoryLogs(ctx, dir, owner); err != nil {
		return err
	}
	if err := c.ConfigureHistoryTraffic(ctx, dir, owner); err != nil {
		return err
	}
	live := newLiveState()
	live.seeded = true
	c.liveView.Store(live)
	t.Cleanup(func() { _ = c.CloseHistoryTraffic(context.Background()); _ = c.CloseHistoryLogs(context.Background()) })
	return nil
}

// FeedHistoryFixture appends a core batch through the journal and waits on both real
// processors. All fixture writes use the same SQLite pipeline as production.
func FeedHistoryFixture(t testing.TB, c *Controller, ctx context.Context, b config.ObservationBatch) error {
	t.Helper()

	job := c.trafficJob()
	if job == nil {
		return nil
	}
	journal := job.journal.(*historystore.Journal)
	cursor, _, err := journal.Cursor(ctx)
	if err != nil {
		return err
	}
	next := cursor
	if next.Session != b.Session {
		next = historystore.Cursor{Session: b.Session}
	}
	var rows []historystore.Observation
	for _, o := range b.Records {
		if o.Session != b.Session || o.Sequence <= next.Seq {
			continue
		}
		rows = append(rows, journalRecord(o))
		next.Seq = o.Sequence
	}
	now := time.Now()
	rows = append(rows, journalPoll{Session: b.Session, Collecting: b.Collecting, AtMs: now.UnixMilli()}.observation())
	if err = journal.Append(ctx, now, rows, next); err != nil {
		return err
	}
	if live := c.liveView.Load(); live != nil {
		live.apply(b, now)
	}
	if err = job.Flush(ctx); err != nil {
		return err
	}
	if logs, e := c.logJob(); e == nil {
		if err = logs.Flush(ctx); err != nil {
			return err
		}
	}
	select {
	case c.logsChanged <- struct{}{}:
	default:
	}
	return nil
}

// newTrafficRPCFixture is the public QueryTraffic seam over journal inputs.
func newTrafficRPCFixture(t testing.TB) (*Controller, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := New("test", "test")
	if err = ConfigureHistoryFixture(t, c, context.Background(), dir, uint32(os.Geteuid())); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseHistory(context.Background()) })
	return c, dir
}

// CloseHistoryFixture stops the processors before closing state publication.
func CloseHistoryFixture(c *Controller, ctx context.Context) error {
	if err := c.CloseHistoryTraffic(ctx); err != nil {
		return err
	}
	if err := c.CloseHistoryLogs(ctx); err != nil {
		return err
	}
	return c.CloseHistory(ctx)
}

// SetHistoryFixtureClock pins the read-side clock to local noon of a fixed day and
// returns that instant, so "today" windows do not depend on the wall clock.
func SetHistoryFixtureClock(c *Controller) time.Time {
	at := time.Date(2026, 1, 15, 12, 0, 0, 0, time.Local)
	clock := func() time.Time { return at }
	c.history.clock = clock
	c.readTokens.now = clock
	if job := c.trafficJob(); job != nil {
		job.now = clock
	}
	if job, err := c.logJob(); err == nil {
		job.now = clock
	}
	return at
}
