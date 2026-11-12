package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"pax-connect/daemon/internal/cadence"
	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

const (
	// logsPollInterval is only a safety net: ingest pokes the job as soon as it stores new rows.
	logsPollInterval     = cadence.HistoryLogsFallback
	logsBatchRecords     = 2000
	logsInboxCapacity    = 64
	logsCapacityBytes    = 512 << 20
	logsMetaTimeout      = 2 * time.Second
	logsStopFlushTimeout = 3 * time.Second
	logsRetentionHours   = 24
	logsCursorName       = "logs"
	logsErrorStore       = "history_store_failed"
	logsErrorIngressFull = "history_ingress_overflow"
	logsResetBatch       = 2000
)

var errInvalidLogEvent = errors.New("invalid_event")

// logsJournal is the part of *historystore.Journal the log job owns: it reads the
// committed cursor forward and closes the handle with its goroutine.
type logsJournal interface {
	ReadAfter(ctx context.Context, after historystore.Cursor, limit int) ([]historystore.Observation, error)
	Close() error
}

// logsStore is the part of *historystore.Logs the log job owns. The job is its only writer;
// queries reach the same value through logReads.
type logsStore interface {
	logReads
	Update(ctx context.Context, fn func(*historystore.LogsTx) error) error
	Cursor(ctx context.Context, name string) (historystore.Cursor, bool, error)
	Meta(ctx context.Context, key string) (string, bool, error)
	Prune(ctx context.Context, before time.Time, limits historystore.Limits) (historystore.Pruned, error)
	EvictOldest(ctx context.Context, capBytes int64, limits historystore.Limits) (bool, error)
	LiveBytes(ctx context.Context) (int64, error)
	IncrementalVacuum(ctx context.Context, pages int) error
	Checkpoint(ctx context.Context) (bool, error)
	Close() error
}

// logsState is the durable position of the log job. It only advances after a committed
// transaction, so a failed write is retried from the same journal cursor.
type logsState struct {
	cursor      historystore.Cursor
	lastSession string
	lastSeq     uint64
	pendingGap  bool
	gapSession  string
	gapThrough  uint64
	revision    uint64
}

// historyLogs is the single writer of logs.db (history-storage §4.2). It consumes the
// journal when ingest stores new rows (60 s safety net), drains the bounded Pax inbox, and runs
// retention while idle.
// Other goroutines only enqueue events and set overflow; they never touch the database.
type historyLogs struct {
	model          *historyLogModel
	attemptStarted time.Time
	logs           logsStore
	journal        logsJournal
	consumers      *journalConsumers
	now            func() time.Time
	apps           *applicationIdentities
	route          func(*config.FlowFact)

	inbox chan config.HistoryEvent
	// commands is the only way another goroutine changes the log store: the job is the
	// single writer, so ClearLogs and settings apply where the transactions live.
	commands chan logsCommand
	wake     chan struct{}
	reads    *readTokenCache
	onMeta   func(historystore.LogsMeta)

	overflow atomic.Bool

	// done exists from construction so concurrent submit calls never race with start.
	cancel context.CancelFunc
	done   chan struct{}

	state            logsState
	committed        historystore.LogsMeta
	clearedAt        time.Time
	backoff          backoff
	down             bool
	executingCommand bool
	failure          error // set by fail during the current pass; see stepOnce
	capBytes         int64
	lastVacuum       time.Time
}

// newHistoryLogs restores the committed cursor and log metadata before the goroutine starts.
func newHistoryLogs(ctx context.Context, logs logsStore, journal logsJournal, consumers *journalConsumers) (*historyLogs, error) {
	if consumers == nil {
		consumers = newJournalConsumers()
	}
	l := &historyLogs{logs: logs, journal: journal, consumers: consumers,
		now: time.Now, capBytes: logsCapacityBytes, apps: newApplicationIdentities(),
		backoff: backoff{max: 60 * time.Second}, done: make(chan struct{}), inbox: make(chan config.HistoryEvent, logsInboxCapacity),
		commands: make(chan logsCommand, logsCommandCapacity), wake: make(chan struct{}, 1)}
	cursor, ok, err := logs.Cursor(ctx, logsCursorName)
	if err != nil {
		return nil, err
	}
	if ok {
		l.state.cursor = cursor
		if !strings.HasPrefix(cursor.Session, journalGapSession) {
			l.state.lastSession, l.state.lastSeq = cursor.Session, cursor.Seq
		}
	}
	if l.committed, err = logs.SnapshotMeta(ctx); err != nil {
		return nil, err
	}
	l.state.revision = l.committed.LogsRevision
	l.clearedAt = l.committed.ClearedAt
	if err = l.installReadModel(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

// publishState mirrors the committed meta for readers, invalidates stale log tokens and
// wakes the publisher. It never blocks the writer on subscribers (R2, R5).
func (l *historyLogs) publishState(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	started := time.Now()
	meta, err := l.refreshCommitted(ctx)
	if err != nil {
		if !l.down {
			slog.Warn("history logs metadata read failed", "code", "history_logs_meta_failed", "library", "logs", "elapsed_ms", time.Since(started).Milliseconds())
		}
		return
	}
	if l.reads != nil {
		l.reads.observeLogGenerations(meta.LogDeletionGeneration, meta.SummaryGeneration, meta.JournalGeneration, meta.LogRemovalBeforeMs)
	}
	if l.onMeta != nil {
		l.onMeta(meta)
	}
}

// refreshCommitted re-reads the committed metadata. Batch writes inside a step (including
// maintenance deletions) do not publish state themselves, so command handlers must refresh
// before deriving the next generation from it.
func (l *historyLogs) refreshCommitted(ctx context.Context) (historystore.LogsMeta, error) {
	meta, err := l.logs.SnapshotMeta(ctx)
	if err != nil {
		return historystore.LogsMeta{}, err
	}
	l.committed, l.clearedAt, l.state.revision = meta, meta.ClearedAt, meta.LogsRevision
	return meta, nil
}

func (l *historyLogs) poke() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// start launches the only goroutine that owns the database. Call it before the writer is
// published to other goroutines.
func (l *historyLogs) start(ctx context.Context) {
	ctx, l.cancel = context.WithCancel(ctx)
	go l.run(ctx)
}

func (l *historyLogs) run(ctx context.Context) {
	defer close(l.done)
	for {
		if err := l.initialize(ctx); err == nil {
			break
		}
		if !l.waitOrWake(ctx, l.fail(ctx, "history_logs_startup_failed")) {
			return
		}
	}

	for {
		if !l.waitOrWake(ctx, l.step(ctx)) {
			return
		}
	}
}

// initialize retries retention normalization and restores committed SQLite metadata.
func (l *historyLogs) initialize(ctx context.Context) error {
	l.attemptStarted = time.Now()
	if err := l.normalizeRetention(ctx); err != nil {
		return err
	}
	_, err := l.refreshCommitted(ctx)
	return err
}

// waitOrWake sleeps for d, returning early on a new command or inbox event.
func (l *historyLogs) waitOrWake(ctx context.Context, d time.Duration) bool {
	return cadence.Wait(ctx, d, l.wake)
}

// stop cancels the goroutine, waits for it, releases the consumer slot and closes the database.
func (l *historyLogs) stop(ctx context.Context) error {
	if l.cancel != nil {
		// Drain the inbox first: events enqueued just before shutdown are committed, not lost.
		// A degraded database makes this fail fast; shutdown proceeds regardless.
		flushCtx, cancel := context.WithTimeout(ctx, logsStopFlushTimeout)
		_ = l.Flush(flushCtx)
		cancel()
		l.cancel()
		select {
		case <-l.done:
		case <-ctx.Done():
			// The owner is still inside a transaction; closing underneath it would be worse
			// than letting process exit reap the handle.
			return ctx.Err()
		}
	}
	l.consumers.Unregister(HistoryConsumerLogs)
	journalErr := l.journal.Close()
	return errors.Join(journalErr, l.logs.Close())
}

// enqueue hands a Pax event to the writer without ever blocking the caller. A full inbox
// is recorded as partial plus history_ingress_overflow by the writer's next transaction.
func (l *historyLogs) enqueue(e config.HistoryEvent) {
	select {
	case l.inbox <- e:
		l.poke()
	default:
		l.overflow.Store(true)
	}
}

// requeue returns the drained events to the inbox after a failed transaction, dropping
// anything beyond capacity but never hiding the overflow. Journal rows are not requeued:
// the committed cursor did not move, so the next read returns them again.
func (l *historyLogs) requeue(events []config.HistoryEvent, overflow bool) {
	if overflow {
		l.overflow.Store(true)
	}
	for _, e := range events {
		select {
		case l.inbox <- e:
		default:
			l.overflow.Store(true)
			return
		}
	}
}

func (l *historyLogs) takeInbox() ([]config.HistoryEvent, bool) {
	var out []config.HistoryEvent
	for {
		select {
		case e := <-l.inbox:
			out = append(out, e)
		default:
			return out, l.overflow.Swap(false)
		}
	}
}

// step performs one pass and returns the delay before the next: zero drains a full batch,
// the poll interval when idle, and a backoff while the database is degraded.
func (l *historyLogs) step(ctx context.Context) time.Duration {
	delay, _ := l.stepOnce(ctx)
	l.runCommands(ctx)
	l.publishState(ctx)
	return delay
}

// stepOnce is one pass with an explicit outcome: the delay for the timer and the error when the pass
// failed, because the delay alone cannot tell idle from a backoff that reached the same value.
func (l *historyLogs) stepOnce(ctx context.Context) (time.Duration, error) {
	l.attemptStarted = time.Now()
	l.failure = nil
	delay := l.pass(ctx)
	return delay, l.failure
}

func (l *historyLogs) pass(ctx context.Context) time.Duration {
	rows, err := l.journal.ReadAfter(ctx, l.state.cursor, logsBatchRecords)
	if err != nil {
		l.consumers.reportDamage(err)
		return l.fail(ctx, "history_logs_read_failed")
	}
	events, overflow := l.takeInbox()
	if len(rows) == 0 && len(events) == 0 && !overflow {
		if !l.recoverIdle(ctx) {
			l.failure = errors.New(logsErrorStore) // degraded and unable to clear the marker: not a success for drain
			return l.backoff.next()
		}
		if d := l.maintain(ctx); d > 0 {
			return d
		}
		return logsPollInterval
	}
	previous := l.state
	state := l.state
	err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		var applyErr error
		state, applyErr = l.apply(ctx, tx, state, rows, events, overflow)
		return applyErr
	})
	if err != nil {
		l.requeue(events, overflow)
		return l.fail(ctx, "history_logs_write_failed")
	}
	l.state = state
	l.consumers.Advance(HistoryConsumerLogs, state.cursor)
	l.recovered()
	if len(rows) == logsBatchRecords {
		if state.cursor == previous.cursor {
			// A full page without progress would spin forever; retry at the poll interval.
			return logsPollInterval
		}
		return 0
	}
	if d := l.maintain(ctx); d > 0 {
		return d
	}
	return logsPollInterval
}

// fail degrades only logs.db and backs off (HS-R4). The structured log carries the precise
// reason while the product error code stays the existing history_store_failed. A failed
// metadata write is ignored: the next attempt carries the same marker.
func (l *historyLogs) fail(ctx context.Context, reason string) time.Duration {
	if ctx.Err() != nil {
		l.failure = ctx.Err()
		return logsPollInterval
	}
	l.failure = errors.New(logsErrorStore)
	if !l.down {
		slog.Warn("history logs degraded", "code", reason, "library", "logs", "elapsed_ms", time.Since(l.attemptStarted).Milliseconds())
	}
	l.down = true
	metaCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logsMetaTimeout)
	defer cancel()
	_ = l.logs.Update(metaCtx, func(tx *historystore.LogsTx) error {
		if err := tx.SetMeta(metaCtx, historystore.MetaPartial, "1"); err != nil {
			return err
		}
		return tx.SetMeta(metaCtx, historystore.MetaErrorCode, logsErrorStore)
	})
	return l.backoff.next()
}

// recoverIdle clears a stale error marker after a successful read with nothing to write.
func (l *historyLogs) recoverIdle(ctx context.Context) bool {
	if !l.down {
		return true
	}
	metaCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logsMetaTimeout)
	defer cancel()
	if err := l.logs.Update(metaCtx, func(tx *historystore.LogsTx) error {
		return tx.SetMeta(metaCtx, historystore.MetaErrorCode, "")
	}); err != nil {
		return false
	}
	l.recovered()
	return true
}

func (l *historyLogs) recovered() {
	if l.down {
		slog.Info("history logs recovered")
	}
	l.down = false
	l.backoff.reset()
}

// ConfigureHistoryLogs opens <directory>/history/logs.db and starts its single writer.
// Call it after ConfigureHistoryConsumers and before ConfigureHistoryIngest.
func (c *Controller) ConfigureHistoryLogs(ctx context.Context, directory string, owner uint32) error {
	historyDir := filepath.Join(directory, "history")
	started := time.Now()
	logs, recovered, err := historystore.OpenLogs(ctx, historyDir, owner)
	if err != nil {
		return err
	}
	if err = ensureLogGenerations(ctx, logs); err != nil {
		_ = logs.Close()
		return err
	}
	if recovered.Happened() {
		slog.Warn("history library rebuilt", "library", "logs", "code", "history_library_rebuilt", "elapsed_ms", time.Since(started).Milliseconds())
		if err = logs.Update(ctx, func(tx *historystore.LogsTx) error {
			if e := tx.SetMeta(ctx, historystore.MetaPartial, "1"); e != nil {
				return e
			}
			return tx.SetMeta(ctx, historystore.MetaErrorCode, "history_library_rebuilt")
		}); err != nil {
			_ = logs.Close()
			return err
		}
	}

	journal, err := historystore.OpenJournal(historyDir, owner)
	if err != nil {
		_ = logs.Close()
		return err
	}
	if c.consumers == nil {
		c.consumers = newJournalConsumers()
	}
	if err = c.consumers.Register(HistoryConsumerLogs, historystore.Cursor{}); err != nil {
		c.consumers.Unregister(HistoryConsumerLogs)
		_ = journal.Close()
		_ = logs.Close()
		return err
	}
	l, err := newHistoryLogs(ctx, logs, journal, c.consumers)
	if err == nil {
		l.route = c.mapHistoryRoute
		l.reads = c.readTokens
		l.onMeta = c.acceptLogMeta
	}
	if err != nil {
		c.consumers.Unregister(HistoryConsumerLogs)
		_ = journal.Close()
		_ = logs.Close()
		return err
	}
	if l.model == nil {
		err = l.installReadModel(ctx)
	}
	if err != nil {
		_ = journal.Close()
		_ = logs.Close()
		return err
	}
	l.publishState(ctx)
	// Start before publishing: every goroutine that obtains the writer through logJob sees
	// it fully initialized (the mutex orders the start before the first reader).
	l.start(ctx)
	c.logsMu.Lock()
	c.logs = l
	c.logsMu.Unlock()
	c.syncLogSnapshot()
	return nil
}

// acceptLogMeta publishes one committed metadata snapshot to query readers and the
// Watch publisher without ever blocking the writer on a subscriber.
func (c *Controller) acceptLogMeta(m historystore.LogsMeta) {
	if prev := c.logState.Swap(&m); prev != nil && *prev == m {
		return // nothing a reader could see moved
	}
	select {
	case c.logsChanged <- struct{}{}:
	default:
	}
}

// ensureLogGenerations gives a new log database its journal and summary generations. Both survive
// restarts so incremental cursors stay valid. journal_generation is minted with the database and so
// changes only when logs.db is rebuilt; a clear replaces summary_generation and moves
// log_deletion_generation and clear_generation instead.
func ensureLogGenerations(ctx context.Context, logs *historystore.Logs) error {
	meta, err := logs.SnapshotMeta(ctx)
	if err != nil {
		return err
	}
	values := map[string]string{}
	if meta.JournalGeneration == "" {
		if values[historystore.MetaJournalGeneration] = historyToken(); values[historystore.MetaJournalGeneration] == "" {
			return errHistory
		}
	}
	resetBytes := meta.SummaryGeneration == ""
	if resetBytes {
		if values[historystore.MetaSummaryGeneration] = historyToken(); values[historystore.MetaSummaryGeneration] == "" {
			return errHistory
		}
	}
	if resetBytes {
		// One-time normalization: the generation marker is written only after every summary
		// has been demoted, so a crash simply retries the idempotent walk on next start.
		if err = resetLogSummaryBytes(ctx, logs); err != nil {
			return err
		}
	}
	if len(values) == 0 {
		return nil
	}
	return logs.Update(ctx, func(tx *historystore.LogsTx) error {
		for key, value := range values {
			if err = tx.SetMeta(ctx, key, value); err != nil {
				return err
			}
		}
		return nil
	})
}

// resetLogSummaryBytes drops byte completeness written without a summary generation: a
// summary from a writer that did not track generations cannot prove its counters. The walk
// is batched so no transaction rewrites more than logsResetBatch rows (HS-R2).
func resetLogSummaryBytes(ctx context.Context, logs *historystore.Logs) error {
	after := ""
	for {
		written := 0
		err := logs.Update(ctx, func(tx *historystore.LogsTx) error {
			rows, err := tx.ConnectionsPage(ctx, after, logsResetBatch)
			if err != nil {
				return err
			}
			for _, row := range rows {
				var summary config.LogConnectionSummary
				if err = json.Unmarshal(row.Body, &summary); err != nil {
					return err
				}
				summary.Metrics.BytesState = "unknown"
				summary.Metrics.Reason = "legacy_history"
				summary.Metrics.Upload, summary.Metrics.Download = 0, 0
				summary.Metrics.BytesFrom = time.Time{}
				if row.Body, err = json.Marshal(summary); err != nil {
					return err
				}
				if err = tx.PutConnection(ctx, row); err != nil {
					return err
				}
				after = row.FlowKey
				written++
			}
			return nil
		})
		if err != nil {
			return err
		}
		if written < logsResetBatch {
			return nil
		}
	}
}

// CloseHistoryLogs stops the log writer and closes logs.db. It runs after the capture
// goroutine so the journal is no longer growing while the last batch is consumed.
func (c *Controller) CloseHistoryLogs(ctx context.Context) error {
	c.logsMu.Lock()
	l := c.logs
	c.logs = nil
	c.logsMu.Unlock()
	if l == nil {
		return nil
	}
	return l.stop(ctx)
}
