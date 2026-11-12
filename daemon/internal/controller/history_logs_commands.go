package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"pax-connect/daemon/internal/historystore"
)

const (
	logsCommandCapacity = 16
	logsCommandMaxAge   = 24 * time.Hour
	logsCommandMaxCount = 1024
	logsDrainIterations = 10000
)

type logsCommandKind uint8

const (
	// logsCommandFlush waits until the inbox and journal backlog are processed and
	// committed. stop() uses it to drain the inbox before exiting; RecordEvent and tests
	// use it to wait for their own writes instead of sleeping on the poll timer.
	logsCommandFlush logsCommandKind = iota + 1
	logsCommandClear
	logsCommandSettings
)

// logsCommand is one request executed inside the log writer goroutine. Its result channel is
// buffered so the writer never waits for a caller that already gave up.
type logsCommand struct {
	kind             logsCommandKind
	id, signature    string
	at               time.Time
	hours            int
	expectedRevision uint64
	done             chan logsCommandResult
}

type logsCommandResult struct {
	// deletionGeneration is returned by a clear so the caller can expire the icon cache.
	deletionGeneration uint64
	err                error
}

func (l *historyLogs) reply(cmd logsCommand, result logsCommandResult) {
	select {
	case cmd.done <- result:
	default:
	}
}

// submit enqueues one command and waits for its committed result. A bounded channel keeps
// the request path from piling up behind a busy writer; callers time out and retry.
func (l *historyLogs) submit(ctx context.Context, cmd logsCommand) (logsCommandResult, error) {
	cmd.done = make(chan logsCommandResult, 1)
	select {
	case l.commands <- cmd:
	case <-ctx.Done():
		return logsCommandResult{}, ctx.Err()
	case <-l.done:
		return logsCommandResult{}, errors.New("history_unavailable")
	}
	l.poke()
	select {
	case result := <-cmd.done:
		return result, result.err
	case <-ctx.Done():
		return logsCommandResult{}, ctx.Err()
	case <-l.done:
		// The writer may have answered just before exiting; prefer that result.
		select {
		case result := <-cmd.done:
			return result, result.err
		default:
			return logsCommandResult{}, errors.New("history_unavailable")
		}
	}
}

// Flush returns once every record admitted before the call (inbox events and journal rows)
// is committed to logs.db.
func (l *historyLogs) Flush(ctx context.Context) error {
	_, err := l.submit(ctx, logsCommand{kind: logsCommandFlush})
	return err
}

// runCommands executes everything already queued, in order, after the current batch.
func (l *historyLogs) runCommands(ctx context.Context) {
	if l.executingCommand {
		return
	}
	l.executingCommand = true
	defer func() { l.executingCommand = false }()
	for {
		select {
		case cmd := <-l.commands:
			l.execute(ctx, cmd)
		default:
			return
		}
	}
}

// execute drains any pending writes first, so a command applies to every record admitted
// before it, then performs its own transaction.
func (l *historyLogs) execute(ctx context.Context, cmd logsCommand) {
	if ctx.Err() != nil {
		l.reply(cmd, logsCommandResult{err: ctx.Err()})
		return
	}
	if err := l.drain(ctx); err != nil {
		l.reply(cmd, logsCommandResult{err: err})
		return
	}
	var result logsCommandResult
	switch cmd.kind {
	case logsCommandFlush:
	case logsCommandClear:
		result = l.executeClear(ctx, cmd)
	case logsCommandSettings:
		result = l.executeSettings(ctx, cmd)
	default:
		result.err = errors.New("history_store_failed")
	}
	// Publish before acknowledging: a caller that immediately reads a snapshot must see the
	// generations its command just produced.
	l.publishState(ctx)
	l.reply(cmd, result)
}

// drain consumes the journal and inbox until both are empty. Commands use it to get a
// deterministic point in time; a failed pass returns its error, however long the backoff.
func (l *historyLogs) drain(ctx context.Context) error {
	for i := 0; i < logsDrainIterations; i++ {
		delay, err := l.stepOnce(ctx)
		if err != nil {
			return err
		}
		if delay > 0 {
			return nil
		}
	}
	return errors.New("history_query_busy")
}

// commandReceipt validates the shared idempotency rules and purges expired receipts. It
// runs inside the caller's transaction and returns the current receipt count.
func commandReceipt(ctx context.Context, tx *historystore.LogsTx, cmd logsCommand, now time.Time) (bool, error) {
	previous, exists, err := tx.Command(ctx, cmd.id)
	if err != nil || exists {
		if err == nil && previous.Signature != cmd.signature {
			return true, errors.New("settings_conflict")
		}
		return exists, err
	}
	count, err := tx.PruneCommands(ctx, now.Add(-logsCommandMaxAge).UnixMilli())
	if err != nil {
		return false, err
	}
	if count >= logsCommandMaxCount {
		return false, errors.New("history_capacity_reached")
	}
	return false, nil
}

// executeClear empties the log tables and advances every log generation in one transaction.
func (l *historyLogs) executeClear(ctx context.Context, cmd logsCommand) logsCommandResult {
	now := l.now()
	if _, err := l.refreshCommitted(ctx); err != nil {
		return logsCommandResult{err: err}
	}
	var deletion uint64
	var summary string
	err := l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		exists, err := commandReceipt(ctx, tx, cmd, now)
		if err != nil || exists {
			deletion = l.committed.LogDeletionGeneration
			return err
		}
		if err = tx.ClearLogTables(ctx); err != nil {
			return err
		}
		meta := l.committed
		deletion = meta.LogDeletionGeneration + 1
		summary = historyToken()
		if summary == "" {
			return errors.New("history_unavailable")
		}
		for key, value := range map[string]string{
			historystore.MetaLogClearedAt:          fmt.Sprint(now.UnixMilli()),
			historystore.MetaSummaryGeneration:     summary,
			historystore.MetaLogDeletionGeneration: fmt.Sprint(deletion),
			historystore.MetaLogsRevision:          fmt.Sprint(meta.LogsRevision + 1),
			historystore.MetaClearGeneration:       fmt.Sprint(meta.ClearGeneration + 1),
		} {
			if err = tx.SetMeta(ctx, key, value); err != nil {
				return err
			}
		}
		return tx.PutCommand(ctx, historystore.LogCommand{ID: cmd.id, Signature: cmd.signature, AtMs: cmd.at.UnixMilli()})
	})
	if err == nil {
		l.clearedAt = now
		if summary != "" && l.reads != nil {
			// Tokens die at the commit, not at the next publishState.
			l.reads.observeLogGenerations(deletion, summary, l.committed.JournalGeneration)
		}
	}
	return logsCommandResult{deletionGeneration: deletion, err: err}
}

// executeSettings applies the retention CAS, prunes by the new retention and only then
// records the idempotency receipt together with the new setting. A prune failure therefore
// leaves nothing committed: the same operation id retried later prunes again instead of
// being answered from a receipt for work that never finished.
func (l *historyLogs) executeSettings(ctx context.Context, cmd logsCommand) logsCommandResult {
	now := l.now()
	if _, err := l.refreshCommitted(ctx); err != nil {
		return logsCommandResult{err: err}
	}
	replayed := false
	err := l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		exists, err := commandReceipt(ctx, tx, cmd, now)
		if err != nil || exists {
			replayed = exists
			return err
		}
		if cmd.expectedRevision != l.committed.SettingsRevision {
			return errors.New("settings_conflict")
		}
		return nil
	})
	if err != nil || replayed {
		return logsCommandResult{err: err}
	}
	if err = l.pruneAll(ctx, now.Add(-time.Duration(cmd.hours)*time.Hour)); err != nil {
		return logsCommandResult{err: errors.New(logsErrorStore)}
	}
	err = l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		if _, err := commandReceipt(ctx, tx, cmd, now); err != nil {
			return err
		}
		revision := l.committed.SettingsRevision + 1
		if err := tx.SetMeta(ctx, historystore.MetaRetentionHours, fmt.Sprint(cmd.hours)); err != nil {
			return err
		}
		if err := tx.SetMeta(ctx, historystore.MetaSettingsRevision, fmt.Sprint(revision)); err != nil {
			return err
		}
		return tx.PutCommand(ctx, historystore.LogCommand{ID: cmd.id, Signature: cmd.signature, AtMs: cmd.at.UnixMilli()})
	})
	return logsCommandResult{err: err}
}

// pruneAll deletes every row older than cutoff in bounded transactions and records the
// deletion generations once anything was removed.
func (l *historyLogs) pruneAll(ctx context.Context, cutoff time.Time) error {
	limits := historystore.Limits{Rows: logsMaintainRows, Duration: logsMaintainDuration}
	deleted := false
	for i := 0; i < logsDrainIterations; i++ {
		pruned, err := l.logs.Prune(ctx, cutoff, limits)
		if err != nil {
			return err
		}
		deleted = deleted || pruned.Deleted > 0
		if !pruned.More {
			break
		}
	}
	if deleted {
		return l.bumpDeletion(ctx)
	}
	return nil
}

// bumpDeletion records a retention or capacity deletion: both generations and the revision
// move, invalidating every frozen log token.
func (l *historyLogs) bumpDeletion(ctx context.Context) error {
	metaCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logsMetaTimeout)
	defer cancel()
	next, err := l.refreshCommitted(metaCtx)
	if err != nil {
		return err
	}
	if err := l.logs.Update(metaCtx, func(tx *historystore.LogsTx) error {
		if err := tx.SetMeta(metaCtx, historystore.MetaLogDeletionGeneration, fmt.Sprint(next.LogDeletionGeneration+1)); err != nil {
			return err
		}
		if err := tx.SetMeta(metaCtx, historystore.MetaClearGeneration, fmt.Sprint(next.ClearGeneration+1)); err != nil {
			return err
		}
		return tx.SetMeta(metaCtx, historystore.MetaLogsRevision, fmt.Sprint(next.LogsRevision+1))
	}); err != nil {
		return err
	}
	// Hand the new generation to the read cache at the commit site, not at the next
	// publishState: a query started between the two must not see a stale cache generation.
	if l.reads != nil {
		l.reads.observeLogGenerations(next.LogDeletionGeneration+1, next.SummaryGeneration, next.JournalGeneration, next.LogRemovalBeforeMs)
	}
	return nil
}
