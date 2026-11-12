package controller

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

const (
	ingestPollInterval = cadence.HistoryIngest
	// ingestCoverageFlush bounds how long an idle collecting span stays unwritten.
	ingestCoverageFlush = cadence.HistoryTraffic
	ingestReadTimeout   = 700 * time.Millisecond
	// ingestPageRecords is the core page size. One Append carries one page plus a few gap
	// rows, far below the 2000-record bound of HS-R2.
	ingestPageRecords = 256
	// ingestSeedToken asks the core for a baseline even though no reset is forced.
	ingestSeedToken = "restart"
	// ingestMaxSpins bounds consecutive immediate re-reads that move no cursor.
	ingestMaxSpins = 8
)

var (
	errIngestUnavailable  = errors.New("history_ingest_unavailable")
	errObservationInvalid = errors.New("observation_invalid")
)

// observationPager is the non-consuming core cursor API used by the ingest goroutine.
type observationPager interface {
	ReadObservationPage(context.Context, config.ObservationRead) (config.ObservationBatch, error)
}

// ingestJournal is the part of *historystore.Journal the ingest goroutine uses.
type ingestJournal interface {
	Append(ctx context.Context, at time.Time, batch []historystore.Observation, cursor historystore.Cursor) error
	Cursor(ctx context.Context) (historystore.Cursor, bool, error)
	HourOf(ctx context.Context, c historystore.Cursor) (time.Time, bool, error)
	Files() ([]historystore.JournalFile, error)
	DeleteHour(hour time.Time) error
	EvictOver(maxBytes int64, maxFiles int) ([]time.Time, error)
	TakeDropped() []historystore.Dropped
	Close() error
}

// coverageSpan is the run of successful reads not yet proven in the journal: same core session and
// collecting flag, each read within trafficPollSpacing of the last.
type coverageSpan struct {
	valid      bool
	session    string
	collecting bool
	start      time.Time // first instant not yet written
	last       time.Time // newest successful read
}

// historyIngest is the single capture owner (history-storage §4.2): it pulls the core every
// 5 s, appends each non-empty page with its cursor to the journal in one transaction (an empty
// read writes nothing), feeds the live state and runs journal retention between polls. All fields below the injection block are
// owned by the goroutine running run; other goroutines only touch live and consumers, which
// guard themselves.
type historyIngest struct {
	attemptStarted time.Time
	journal        ingestJournal
	core           observationPager
	live           *liveState
	consumers      *journalConsumers
	now            func() time.Time
	wait           func(context.Context, time.Duration) bool
	// onRows runs after a commit that stored observation or gap rows: the log job consumes them now.
	onRows func()

	cancel context.CancelFunc
	done   chan struct{}

	started        bool
	spins          int                 // consecutive zero-delay steps without progress
	cursor         historystore.Cursor // last committed journal cursor; the next pull starts here
	seeding        bool                // live state still needs a baseline
	resetToken     string
	resetCursor    string
	pending        *journalGap  // core gap not yet committed
	extra          []journalGap // journal gaps not yet committed
	coreBackoff    backoff
	journalBackoff backoff
	lastMaintain   time.Time
	coreDown       bool
	journalDown    bool
	span           coverageSpan
	closed         []journalPoll // ended spans waiting for a commit, oldest first
}

func newHistoryIngest(journal ingestJournal, core observationPager) *historyIngest {
	return &historyIngest{journal: journal, core: core, live: newLiveState(), consumers: newJournalConsumers(),
		now: time.Now, wait: func(ctx context.Context, d time.Duration) bool { return cadence.Wait(ctx, d) }, seeding: true,
		coreBackoff: backoff{max: 30 * time.Second}, journalBackoff: backoff{max: 60 * time.Second}}
}

// start seals the consumer registry and launches the goroutine; it exits when ctx ends.
func (in *historyIngest) start(ctx context.Context) {
	in.consumers.seal()
	ctx, in.cancel = context.WithCancel(ctx)
	in.done = make(chan struct{})
	go in.run(ctx)
}

func (in *historyIngest) run(ctx context.Context) {
	defer close(in.done)
	for {
		delay := in.step(ctx)
		if delay > 0 {
			in.maintain(ctx)
		}
		if !in.wait(ctx, delay) {
			return
		}
	}
}

// stop cancels the goroutine, waits for it and closes the journal.
func (in *historyIngest) stop(ctx context.Context) error {
	if in.cancel == nil {
		return in.journal.Close()
	}
	in.cancel()
	select {
	case <-in.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return in.journal.Close()
}

// step performs one poll and returns how long to wait before the next one: zero to drain
// immediately, the poll interval when idle, or a backoff after a failure.
func (in *historyIngest) step(ctx context.Context) time.Duration {
	in.attemptStarted = time.Now()
	if !in.started {
		if owner, ok := in.journal.(interface {
			PendingLosses(context.Context) ([]historystore.Dropped, error)
		}); ok {
			if _, err := owner.PendingLosses(ctx); err != nil {
				return in.journalFailed("history_journal_repair_failed")
			}
		}
		cursor, _, err := in.journal.Cursor(ctx)
		if err != nil {
			return in.journalFailed("history_journal_cursor_failed")
		}
		in.cursor, in.started = cursor, true
	}
	if err := in.flushJournalLosses(ctx); err != nil {
		return in.journalFailed("history_journal_repair_failed")
	}
	batch, err := in.read(ctx)
	if err != nil {
		in.live.disconnect()
		in.resetToken, in.resetCursor = "", ""
		if !in.coreDown {
			slog.Debug("history ingest: core read failed", "reason", "history_core_read_failed")
		}
		in.coreDown = true
		return in.coreBackoff.next()
	}
	in.coreDown = false
	in.coreBackoff.reset()
	if ctx.Err() != nil {
		return ingestPollInterval
	}
	// Live state first: Watch keeps moving while the journal is failing, and its own
	// watermark makes the later re-pull of the same records harmless.
	in.live.apply(batch, in.now())
	before, beforeReset := in.cursor, in.resetCursor
	plan := in.plan(batch)
	in.trackSpan(batch.Session, batch.Collecting, in.now())
	// An empty read commits nothing: it only extends the in-memory coverage span.
	if err = in.commit(ctx, plan); err != nil {
		return in.journalFailed("history_journal_append_failed")
	}
	in.journalRecovered()
	if !plan.more {
		in.spins = 0
		return ingestPollInterval
	}
	// A core that keeps claiming more while nothing moves must not make a hot loop.
	if in.cursor != before || in.resetCursor != beforeReset || plan.seeded {
		in.spins = 0
	} else if in.spins >= ingestMaxSpins {
		in.spins = 0
		return ingestPollInterval
	} else {
		in.spins++
	}
	return 0
}

func (in *historyIngest) read(ctx context.Context) (config.ObservationBatch, error) {
	request, cancel := context.WithTimeout(ctx, ingestReadTimeout)
	defer cancel()
	q := config.ObservationRead{Session: in.cursor.Session, After: in.cursor.Seq, Limit: ingestPageRecords, ResetToken: in.resetToken, Cursor: in.resetCursor}
	if in.seeding && q.ResetToken == "" {
		q.ResetToken = ingestSeedToken
	}
	batch, err := in.core.ReadObservationPage(request, q)
	if err == nil && len(batch.Records) > ingestPageRecords {
		err = errObservationInvalid
	}
	return batch, err
}

// ingestPlan is what one batch asks of the journal.
type ingestPlan struct {
	rows       []historystore.Observation
	next       historystore.Cursor
	more       bool
	seeded     bool
	session    string // core session of the read, for the poll row
	collecting bool
}

// plan turns a batch into journal rows and the cursor to commit with them (HS-R5 gap included).
// The cursor only moves with the records it covers; a baseline moves it to the baseline high
// water at the end, but only when a gap made the skipped range unrecoverable.
func (in *historyIngest) plan(b config.ObservationBatch) ingestPlan {
	if g := detectCoreGap(in.cursor, b, in.seeding); g != nil && in.pending == nil {
		in.pending = g
	}
	next := in.cursor
	if b.Session != "" && b.Session != next.Session {
		next = historystore.Cursor{Session: b.Session}
	}
	p := ingestPlan{next: next, session: b.Session, collecting: b.Collecting}
	if b.ResetToken != "" {
		if !b.BaselineComplete {
			in.resetToken, in.resetCursor = b.ResetToken, b.NextCursor
			p.next, p.more = in.cursor, true
			return p
		}
		in.resetToken, in.resetCursor, in.seeding, p.seeded = "", "", false, true
		if in.pending != nil {
			p.next.Seq = max(p.next.Seq, b.BaselineHighWater)
		}
	}
	for _, o := range b.Records {
		if o.Session != b.Session || o.Sequence <= p.next.Seq {
			continue
		}
		p.rows = append(p.rows, journalRecord(o))
		p.next.Seq = o.Sequence
	}
	p.more = b.NextCursor != "" || len(b.Records) >= ingestPageRecords || p.next.Seq < b.HighWater && (len(b.Records) > 0 || p.seeded)
	return p
}

// trackSpan folds one successful read into the coverage span. A change of session or collecting flag,
// or a pause longer than trafficPollSpacing, ends the span (queued for the next commit when it proved
// collection) and starts a new one at this read.
func (in *historyIngest) trackSpan(session string, collecting bool, now time.Time) {
	s := &in.span
	if s.valid && (s.session != session || s.collecting != collecting || now.Sub(s.last) > trafficPollSpacing) {
		if s.collecting && s.last.After(s.start) {
			in.closed = append(in.closed, journalPoll{Session: s.session, Collecting: true, AtMs: s.last.UnixMilli(), FromMs: s.start.UnixMilli()})
		}
		s.valid = false
	}
	if !s.valid {
		*s = coverageSpan{valid: true, session: session, collecting: collecting, start: now, last: now}
		return
	}
	s.last = now
}

// commit appends the page, any pending gap rows, the poll rows and the cursor in one journal transaction,
// or nothing when there is nothing to record (empty page, no gap, no ended span, flush not due).
// Ended spans come first and the current span's poll row last, so a gap row in the same commit lies
// between the polls proving the time before it and after it.
// Failure leaves cursor, pending gaps, spans and the journal untouched, so the next poll re-pulls.
func (in *historyIngest) commit(ctx context.Context, p ingestPlan) error {
	now := in.now()
	var gaps []journalGap
	if in.pending != nil {
		gap := *in.pending
		if len(p.rows) > 0 {
			last := p.rows[len(p.rows)-1]
			if last.Session == gap.Session {
				gap.ThroughSeq = last.Seq
			}
		}
		gaps = append(gaps, gap)
	}
	gaps = append(gaps, in.extra...)
	data := len(gaps) > 0 || len(p.rows) > 0
	s := in.span
	current := s.collecting && s.last.After(s.start) && (data || now.Sub(s.start) >= ingestCoverageFlush)
	if !data && !current && len(in.closed) == 0 && p.next == in.cursor {
		return nil
	}
	rows := make([]historystore.Observation, 0, len(in.closed)+len(gaps)+len(p.rows)+1)
	for _, c := range in.closed {
		rows = append(rows, c.observation())
	}
	for _, g := range gaps {
		rows = append(rows, g.observation(now))
	}
	rows = append(rows, p.rows...)
	if current {
		rows = append(rows, journalPoll{Session: s.session, Collecting: true, AtMs: s.last.UnixMilli(), FromMs: s.start.UnixMilli()}.observation())
	}
	if err := in.journal.Append(ctx, now, rows, p.next); err != nil {
		return err
	}
	in.cursor, in.pending, in.extra, in.closed = p.next, nil, nil, nil
	if current {
		in.span.start = s.last
	}
	if data && in.onRows != nil {
		in.onRows()
	}
	return nil
}

func (in *historyIngest) journalFailed(reason string) time.Duration {
	if !in.journalDown {
		slog.Warn("history ingest degraded", "code", reason, "library", "journal", "elapsed_ms", time.Since(in.attemptStarted).Milliseconds())
	}
	in.journalDown = true
	return in.journalBackoff.next()
}

func (in *historyIngest) journalRecovered() {
	if in.journalDown {
		slog.Info("history ingest recovered")
	}
	in.journalDown = false
	in.journalBackoff.reset()
}

// backoff doubles from one second up to max; reset after a success.
type backoff struct {
	max time.Duration
	cur time.Duration
}

func (b *backoff) next() time.Duration {
	if b.cur == 0 {
		b.cur = time.Second
	} else {
		b.cur = min(b.cur*2, b.max)
	}
	return b.cur
}

func (b *backoff) reset() { b.cur = 0 }

// ConfigureHistoryIngest opens <directory>/history/journal and starts the capture goroutine.
// The core session must already be configured. It is the sole production observation reader.
func (c *Controller) ConfigureHistoryIngest(ctx context.Context, directory string, owner uint32) error {
	pager, ok := c.coreSession.(observationPager)
	if !ok {
		return errIngestUnavailable
	}
	journal, err := historystore.OpenJournal(filepath.Join(directory, "history"), owner)
	if err != nil {
		return err
	}
	in := newHistoryIngest(journal, pager)
	if c.consumers != nil {
		in.consumers = c.consumers
	}
	c.consumers = in.consumers
	in.onRows = func() {
		if l, err := c.logJob(); err == nil && l != nil {
			l.poke()
		}
	}
	c.ingest = in
	c.liveView.Store(in.live)
	if t := c.trafficJob(); t != nil {
		// Queue recovery behind the writer's bounded SQLite work without waiting: capture and
		// RPC serving must continue during recovery. restorePending deduplicates
		// against records already admitted by the live view.
		select {
		case t.commands <- trafficCommand{kind: trafficCommandRestore, live: in.live, done: make(chan error, 1)}:
			t.poke()
		case <-ctx.Done():
			return ctx.Err()
		case <-t.done:
			return errIngestUnavailable
		}
	}

	in.start(ctx)
	return nil
}

// CloseHistoryIngest stops the capture goroutine and closes the journal.
func (c *Controller) CloseHistoryIngest(ctx context.Context) error {
	if c.ingest == nil {
		return nil
	}
	return c.ingest.stop(ctx)
}
