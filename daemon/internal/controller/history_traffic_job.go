package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

const (
	trafficPollInterval     = cadence.HistoryTraffic
	trafficBatchRecords     = 128
	trafficCapacityBytes    = 128 << 20
	trafficCursorName       = "traffic"
	trafficMetaLastPoll     = "last_poll"
	trafficMetaDeletion     = "traffic_deletion_generation"
	trafficMetaCoverageFrom = "coverage_start_ms"
	trafficMetaTimeout      = 2 * time.Second
	trafficStopFlushTimeout = 3 * time.Second
	trafficErrorStore       = "history_store_failed"
	trafficCommandCapacity  = 4
	trafficDrainIterations  = 10000
)

// trafficJournal is the part of *historystore.Journal the traffic job owns: it reads forward from
// its committed cursor and closes the handle with its goroutine.
type trafficJournal interface {
	ReadAfter(ctx context.Context, after historystore.Cursor, limit int) ([]historystore.Observation, error)
	Tail(ctx context.Context) (historystore.Cursor, error)
	Close() error
}

// trafficStore is the part of *historystore.Traffic the job owns. The job is its only writer.
type trafficStore interface {
	Update(ctx context.Context, fn func(*historystore.TrafficTx) error) error
	BeginRead(ctx context.Context) (*historystore.TrafficRead, error)
	Cursor(ctx context.Context, name string) (historystore.Cursor, bool, error)
	Meta(ctx context.Context, key string) (string, bool, error)
	FlowMarks(ctx context.Context) ([]historystore.FlowMark, error)
	Prune(ctx context.Context, cutoffs historystore.TrafficCutoffs, limits historystore.Limits) (historystore.Pruned, error)
	EvictOldest(ctx context.Context, capBytes int64, limits historystore.Limits) (bool, error)
	LiveBytes(ctx context.Context) (int64, error)
	IncrementalVacuum(ctx context.Context, pages int) error
	Checkpoint(ctx context.Context) (bool, error)
	Close() error
}

// trafficState is the durable position of the traffic job. It only advances after a committed
// transaction, so a failed write is retried from the same journal cursor.
type trafficState struct {
	revision uint64
	deletion uint64              // traffic_deletion_generation: moves when stored traffic is deleted
	cursor   historystore.Cursor // last journal row consumed, gap and poll rows included
	core     historystore.Cursor // last core observation consumed: what the live view's pending delta is measured by
	poll     pollMark            // previous poll of the stream; zero after a gap
	coverage bool                // coverage_start_ms is stored
}

// historyTraffic is the single writer of traffic.db (history-storage §4.2). Every 60 s it consumes
// the journal into traffic_5m, recent_cuts, coverage_5m and flow_marks, in transactions that also carry
// its cursor, and runs retention while idle. A failure only degrades traffic.db.
type historyTraffic struct {
	attemptStarted  time.Time
	restoreLive     *liveState
	restoreAt       time.Time
	restoreBackoff  backoff
	receipts        []pendingEntry
	meta            atomic.Pointer[historyMetadata]
	identities      map[string]config.FlowFact
	identityThrough historystore.Cursor
	pendingMu       sync.Mutex
	pendingInput    []pendingEntry
	pendingResolved []pendingEntry
	snapshotMu      sync.RWMutex // pairs a pinned WAL version with its uncommitted memory tail
	store           trafficStore
	journal         trafficJournal
	consumers       *journalConsumers
	now             func() time.Time
	apps            *applicationIdentities
	route           func(*config.FlowFact)
	// live returns the in-memory view told about each commit, or nil while it does not exist yet.
	live    func() *liveState
	changed func()

	commands chan trafficCommand
	wake     chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}

	state      trafficState
	observedAt time.Time // newest observation time consumed: flow mark retention runs on it
	backoff    backoff
	down       bool
	executing  bool
	failure    error // set by fail during the current pass: drain reads it instead of guessing from the delay
	capBytes   int64
	lastVacuum time.Time
}

// newHistoryTraffic restores the committed cursor and poll state before the goroutine starts.
func newHistoryTraffic(ctx context.Context, store trafficStore, journal trafficJournal, consumers *journalConsumers) (*historyTraffic, error) {
	if consumers == nil {
		consumers = newJournalConsumers()
	}
	t := &historyTraffic{store: store, journal: journal, consumers: consumers, now: time.Now, capBytes: trafficCapacityBytes,
		apps: newApplicationIdentities(), backoff: backoff{max: 60 * time.Second}, done: make(chan struct{}),
		commands: make(chan trafficCommand, trafficCommandCapacity), wake: make(chan struct{}, 1)}
	cursor, ok, err := store.Cursor(ctx, trafficCursorName)
	if err != nil {
		return nil, err
	}
	if ok {
		t.state.cursor = cursor
		if isCoreCursor(cursor) {
			t.state.core = cursor
		}
	}
	if raw, _, e := store.Meta(ctx, "core_cursor"); e != nil {
		return nil, e
	} else if raw != "" {
		if e = json.Unmarshal([]byte(raw), &t.state.core); e != nil {
			return nil, e
		}
	}
	if raw, _, e := store.Meta(ctx, "traffic_revision"); e != nil {
		return nil, e
	} else {
		t.state.revision, _ = strconv.ParseUint(raw, 10, 64)
	}
	if raw, _, e := store.Meta(ctx, trafficMetaDeletion); e != nil {
		return nil, e
	} else {
		t.state.deletion, _ = strconv.ParseUint(raw, 10, 64)
	}
	if t.state.poll, err = loadPollMark(ctx, store); err != nil {
		return nil, err
	}
	_, t.state.coverage, err = store.Meta(ctx, trafficMetaCoverageFrom)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// isCoreCursor tells a core observation position from a synthetic gap or poll row.
func isCoreCursor(c historystore.Cursor) bool {
	return !strings.HasPrefix(c.Session, journalGapSession) && !strings.HasPrefix(c.Session, journalPollSession)
}

func (t *historyTraffic) poke() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// start launches the only goroutine that owns the database.
func (t *historyTraffic) start(ctx context.Context) {
	ctx, t.cancel = context.WithCancel(ctx)
	go t.run(ctx)
}

func (t *historyTraffic) run(ctx context.Context) {
	defer close(t.done)
	for {
		if !t.waitOrWake(ctx, t.step(ctx)) {
			return
		}
	}
}

func (t *historyTraffic) waitOrWake(ctx context.Context, d time.Duration) bool {
	return cadence.Wait(ctx, d, t.wake)
}

// stop commits what the journal already holds, cancels the goroutine, waits for it, releases the
// consumer slot and closes both handles.
func (t *historyTraffic) stop(ctx context.Context) error {
	if t.cancel != nil {
		flushCtx, cancel := context.WithTimeout(ctx, trafficStopFlushTimeout)
		_ = t.Flush(flushCtx)
		cancel()
		t.cancel()
		select {
		case <-t.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.consumers.Unregister(HistoryConsumerTraffic)
	return errors.Join(t.journal.Close(), t.store.Close())
}

// step performs one pass and returns the delay before the next: zero drains a full batch, the poll
// interval when idle, and a backoff while the database is degraded.
func (t *historyTraffic) step(ctx context.Context) time.Duration {
	delay, _ := t.stepOnce(ctx)
	t.runCommands(ctx)
	return delay
}

// stepOnce is one pass with an explicit outcome: the delay for the timer and the error when the pass
// failed. The delay alone cannot tell idle from a capped backoff, both are 60 s.
func (t *historyTraffic) stepOnce(ctx context.Context) (time.Duration, error) {
	t.attemptStarted = time.Now()
	t.failure = nil
	if t.restoreLive != nil {
		if t.now().Before(t.restoreAt) {
			return t.restoreAt.Sub(t.now()), t.failure
		}
		if err := t.replayPending(ctx, t.restoreLive); err != nil {
			t.consumers.reportDamage(err)
			slog.Warn("history live restore failed", "code", "history_live_restore_failed", "library", "traffic", "elapsed_ms", time.Since(t.attemptStarted).Milliseconds())
			if t.restoreBackoff.max == 0 {
				t.restoreBackoff.max = time.Minute
			}
			t.restoreAt = t.now().Add(t.restoreBackoff.next())
			delay := t.fail(ctx, "history_live_restore_failed")
			t.publishState(ctx)
			return delay, err
		}
		t.restoreLive = nil
		t.restoreBackoff.reset()
	}

	delay := t.pass(ctx)
	t.publishState(ctx)
	return delay, t.failure
}

func (t *historyTraffic) pass(ctx context.Context) time.Duration {
	rows, err := t.journal.ReadAfter(ctx, t.state.cursor, trafficBatchRecords)
	if err != nil {
		t.consumers.reportDamage(err)
		return t.fail(ctx, "history_traffic_read_failed")
	}
	if len(rows) == 0 {
		if !t.recoverIdle(ctx) {
			t.failure = errors.New(trafficErrorStore) // degraded and unable to clear the marker: not a success for drain
			return t.backoff.next()
		}
		if d := t.maintain(ctx); d > 0 {
			return d
		}
		return trafficPollInterval
	}
	latest, err := t.latestUncommitted(ctx, rows)
	if err != nil {
		return t.fail(ctx, "history_traffic_read_failed")
	}
	t.identities = latest
	for i, row := range rows {
		if row.Session == t.identityThrough.Session && row.Seq == t.identityThrough.Seq {
			rows = rows[:i+1]
			break
		}
	}
	previous := t.state
	state, observed := t.state, t.observedAt
	locked := false
	err = t.store.Update(ctx, func(tx *historystore.TrafficTx) error {
		var applyErr error
		state, observed, applyErr = t.apply(ctx, tx, previous, rows)
		if applyErr == nil {
			t.snapshotMu.Lock()
			locked = true
		}
		return applyErr
	})
	if err != nil {
		if locked {
			t.snapshotMu.Unlock()
		}
		return t.fail(ctx, "history_traffic_write_failed")
	}
	t.state = state
	if observed.After(t.observedAt) {
		t.observedAt = observed
	}
	t.consumers.Advance(HistoryConsumerTraffic, state.cursor)
	if live := t.liveView(); live != nil && state.core != previous.core {
		live.commitTraffic(state.core, t.receipts...)
	}
	if locked {
		t.snapshotMu.Unlock()
	}
	if state.cursor == t.identityThrough {
		t.identities = nil
		t.identityThrough = historystore.Cursor{}
	}
	t.recovered()
	if len(rows) == trafficBatchRecords {
		if state.cursor == previous.cursor {
			return trafficPollInterval // a full page without progress would spin
		}
		return 0
	}
	if d := t.maintain(ctx); d > 0 {
		return d
	}
	return trafficPollInterval
}

func (t *historyTraffic) liveView() *liveState {
	if t.live == nil {
		return nil
	}
	return t.live()
}

// fail degrades only traffic.db and backs off (HS-R4). A failed metadata write is ignored: the next
// attempt carries the same marker.
func (t *historyTraffic) fail(ctx context.Context, reason string) time.Duration {
	if ctx.Err() != nil {
		t.failure = ctx.Err()
		return trafficPollInterval
	}
	t.failure = errors.New(trafficErrorStore)
	if !t.down {
		slog.Warn("history traffic degraded", "code", reason, "library", "traffic", "elapsed_ms", time.Since(t.attemptStarted).Milliseconds())
	}
	t.down = true
	metaCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficMetaTimeout)
	defer cancel()
	_ = t.store.Update(metaCtx, func(tx *historystore.TrafficTx) error {
		if err := tx.SetMeta(metaCtx, historystore.MetaPartial, "1"); err != nil {
			return err
		}
		return tx.SetMeta(metaCtx, historystore.MetaErrorCode, trafficErrorStore)
	})
	return t.backoff.next()
}

// recoverIdle clears a stale error marker after a successful read with nothing to write.
func (t *historyTraffic) recoverIdle(ctx context.Context) bool {
	if !t.down {
		return true
	}
	metaCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficMetaTimeout)
	defer cancel()
	if err := t.store.Update(metaCtx, func(tx *historystore.TrafficTx) error {
		return tx.SetMeta(metaCtx, historystore.MetaErrorCode, "")
	}); err != nil {
		return false
	}
	t.recovered()
	return true
}

func (t *historyTraffic) recovered() {
	if t.down {
		slog.Info("history traffic recovered")
	}
	t.down = false
	t.backoff.reset()
}

// ConfigureHistoryTraffic opens <directory>/history/traffic.db and starts its single writer. Call it
// after ConfigureHistoryConsumers and before ConfigureHistoryIngest.
func (c *Controller) ConfigureHistoryTraffic(ctx context.Context, directory string, owner uint32) error {
	historyDir := filepath.Join(directory, "history")
	started := time.Now()
	store, recovered, err := historystore.OpenTraffic(ctx, historyDir, owner)
	if err != nil {
		return err
	}
	if recovered.Happened() {
		slog.Warn("history library rebuilt", "library", "traffic", "code", "history_library_rebuilt", "elapsed_ms", time.Since(started).Milliseconds())
		if err = store.Update(ctx, func(tx *historystore.TrafficTx) error {
			if e := tx.SetMeta(ctx, historystore.MetaPartial, "1"); e != nil {
				return e
			}
			return tx.SetMeta(ctx, historystore.MetaErrorCode, "history_library_rebuilt")
		}); err != nil {
			_ = store.Close()
			return err
		}
	}

	journal, err := historystore.OpenJournal(historyDir, owner)
	if err != nil {
		_ = store.Close()
		return err
	}
	if c.consumers == nil {
		c.consumers = newJournalConsumers()
	}
	if err = c.consumers.Register(HistoryConsumerTraffic, historystore.Cursor{}); err != nil {
		c.consumers.Unregister(HistoryConsumerTraffic)
		_ = journal.Close()
		_ = store.Close()
		return err
	}
	t, err := newHistoryTraffic(ctx, store, journal, c.consumers)
	if err != nil {
		c.consumers.Unregister(HistoryConsumerTraffic)
		_ = journal.Close()
		_ = store.Close()
		return err
	}
	t.route = c.mapHistoryRoute
	t.live = c.liveView.Load
	t.changed = func() {
		select {
		case c.logsChanged <- struct{}{}:
		default:
		}
	}
	t.start(ctx)
	c.trafficMu.Lock()
	c.traffic = t
	c.trafficMu.Unlock()
	return nil
}

// CloseHistoryTraffic stops the traffic writer and closes traffic.db. It runs after the capture
// goroutine so the journal is no longer growing while the last batch is consumed.
func (c *Controller) CloseHistoryTraffic(ctx context.Context) error {
	c.trafficMu.Lock()
	t := c.traffic
	c.traffic = nil
	c.trafficMu.Unlock()
	if t == nil {
		return nil
	}
	return t.stop(ctx)
}

// trafficJob returns the running writer, or nil.
func (c *Controller) trafficJob() *historyTraffic {
	c.trafficMu.Lock()
	defer c.trafficMu.Unlock()
	return c.traffic
}
