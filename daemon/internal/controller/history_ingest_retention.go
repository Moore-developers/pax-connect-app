package controller

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"pax-connect/daemon/internal/historystore"
)

const (
	// Journal retention (history-storage HS-R1, HS-R3).
	journalMinAge      = 2 * time.Hour // after the hour ended
	journalMaxBytes    = 256 << 20
	journalMaxFiles    = 6
	journalMaintainGap = time.Minute
)

// Names of the two jobs that read the journal (tickets 84 and 85). main pre-registers both so
// age deletion waits for them and capacity eviction records the ranges they have not read.
const (
	HistoryConsumerLogs    = "logs"
	HistoryConsumerTraffic = "traffic"
)

var errConsumersSealed = errors.New("history_consumers_sealed")

// journalConsumers is where the log and traffic jobs (tickets 84, 85) report how far they
// have read the journal. Consumers are registered (Controller.ConfigureHistoryConsumers)
// before the ingest goroutine starts; start seals the registry and a later Register fails with
// errConsumersSealed. Each job calls Advance after every committed batch with the cursor of its last consumed row, and
// Unregister when it stops for good. The ingest goroutine never deletes a journal file by
// age until every registered consumer has read past it; with no consumer registered it never
// deletes by age. Safe for concurrent use.
type journalConsumers struct {
	mu      sync.Mutex
	cursors map[string]historystore.Cursor
	repairs map[int64]time.Time
	sealed  bool
}

func newJournalConsumers() *journalConsumers {
	return &journalConsumers{cursors: map[string]historystore.Cursor{}}
}

// Register adds or replaces the named consumer at cursor (the zero cursor: nothing consumed yet).
// It fails once the registry is sealed, because a consumer unknown to the first retention pass
// could lose files without a gap marker.
func (r *journalConsumers) Register(name string, cursor historystore.Cursor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return errConsumersSealed
	}
	r.cursors[name] = cursor
	return nil
}

func (r *journalConsumers) seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

// Advance moves a registered consumer (also after sealing); unknown names are ignored so a stopped job cannot re-register itself.
func (r *journalConsumers) Advance(name string, cursor historystore.Cursor) {
	r.mu.Lock()
	if _, ok := r.cursors[name]; ok {
		r.cursors[name] = cursor
	}
	r.mu.Unlock()
}

// Unregister removes the consumer; it no longer holds back deletion.
func (r *journalConsumers) Unregister(name string) {
	r.mu.Lock()
	delete(r.cursors, name)
	r.mu.Unlock()
}

func (r *journalConsumers) snapshot() []historystore.Cursor {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]historystore.Cursor, 0, len(r.cursors))
	for _, c := range r.cursors {
		out = append(out, c)
	}
	return out
}

// maintain runs journal retention at most once a minute, between polls. Failures are retried
// at the next pass; they never stop capture.
func (in *historyIngest) maintain(ctx context.Context) {
	in.repairJournal(ctx)
	now := in.now()
	if !in.lastMaintain.IsZero() && now.Sub(in.lastMaintain) < journalMaintainGap {
		return
	}
	in.lastMaintain = now
	for _, d := range in.journal.TakeDropped() {
		slog.Warn("history journal quarantined", "library", "journal", "code", "history_journal_rebuilt", "elapsed_ms", time.Since(in.attemptStarted).Milliseconds())
		if d.ID == "" {
			in.extra = append(in.extra, evictionGap(gapDropped, d.Hour))
		}
	}
	positions, ok := in.consumerHours(ctx)
	if ok {
		in.deleteConsumed(now, positions)
	}
	var evicted []time.Time
	if owner, durable := in.journal.(interface {
		EvictOverConsumed(context.Context, int64, int, []time.Time) ([]time.Time, error)
	}); durable {
		var consumed []time.Time
		if ok {
			files, err := in.journal.Files()
			if err == nil {
				for _, file := range files {
					if passedBy(positions, file.Hour) {
						consumed = append(consumed, file.Hour)
					}
				}
			}
		}
		var err error
		evicted, err = owner.EvictOverConsumed(ctx, journalMaxBytes, journalMaxFiles, consumed)
		if err != nil {
			in.journalFailed("history_journal_eviction_failed")
		}
	} else {
		evicted, _ = in.journal.EvictOver(journalMaxBytes, journalMaxFiles)
	}
	for _, hour := range evicted {
		if _, durable := in.journal.(interface {
			PendingLosses(context.Context) ([]historystore.Dropped, error)
		}); !durable && (!ok || !passedBy(positions, hour)) {
			in.extra = append(in.extra, evictionGap(gapEvicted, hour))
		}
	}
	if err := in.flushJournalLosses(ctx); err != nil {
		in.journalFailed("history_journal_repair_failed")
	}
	in.flushGaps(ctx, now)
}

// consumerHour is where one consumer stands: the hour of its file, or none.
type consumerHour struct {
	hour  time.Time
	known bool
}

// consumerHours locates every registered consumer. ok is false when a lookup failed, in which
// case nothing is deleted by age this round.
func (in *historyIngest) consumerHours(ctx context.Context) ([]consumerHour, bool) {
	cursors := in.consumers.snapshot()
	out := make([]consumerHour, 0, len(cursors))
	for _, c := range cursors {
		hour, known, err := in.journal.HourOf(ctx, c)
		if err != nil {
			in.consumers.reportDamage(err)
			return nil, false
		}
		out = append(out, consumerHour{hour: hour, known: known})
	}
	return out, true
}

// passedBy reports whether every consumer stands in a later file than hour. With no consumers it is true.
func passedBy(positions []consumerHour, hour time.Time) bool {
	for _, p := range positions {
		if !p.known || !p.hour.After(hour) {
			return false
		}
	}
	return true
}

// deleteConsumed removes, oldest first, the files whose hour ended at least journalMinAge ago and
// that every consumer has passed. The newest file is kept: it holds the cursor restart resumes from.
func (in *historyIngest) deleteConsumed(now time.Time, positions []consumerHour) {
	if len(positions) == 0 {
		return
	}
	files, err := in.journal.Files()
	if err != nil || len(files) < 2 {
		return
	}
	for _, f := range files[:len(files)-1] {
		if f.Hour.Add(time.Hour+journalMinAge).After(now) || !passedBy(positions, f.Hour) {
			return
		}
		if in.journal.DeleteHour(f.Hour) != nil {
			return
		}
	}
}

// flushGaps writes queued journal gaps (eviction, quarantine) with the committed cursor. On
// failure they stay queued for the next commit.
func (in *historyIngest) flushGaps(ctx context.Context, now time.Time) {
	if !in.started || len(in.extra) == 0 { // before the first step the committed cursor is unknown: never write a guess
		return
	}
	rows := make([]historystore.Observation, 0, len(in.extra))
	for _, g := range in.extra {
		rows = append(rows, g.observation(now))
	}
	if in.journal.Append(ctx, now, rows, in.cursor) == nil {
		in.extra = nil
	}
}

// ConfigureHistoryConsumers pre-registers journal consumers at the zero cursor. Call it before
// ConfigureHistoryIngest; afterwards the registry is sealed and it returns errConsumersSealed.
func (c *Controller) ConfigureHistoryConsumers(names ...string) error {
	if c.consumers == nil {
		c.consumers = newJournalConsumers()
	}
	for _, name := range names {
		if err := c.consumers.Register(name, historystore.Cursor{}); err != nil {
			return err
		}
	}
	return nil
}
