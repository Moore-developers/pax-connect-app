package controller

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

type trafficCommandKind uint8

const (
	// trafficCommandFlush waits until the journal backlog is committed to traffic.db; stop() and
	// tests use it instead of sleeping on the 60 s timer.
	trafficCommandFlush trafficCommandKind = iota + 1
	// trafficCommandRestore replays the journal after the committed cursor into a live view.
	trafficCommandRestore
)

// trafficCommand is one request executed inside the traffic goroutine, so it always sees a
// committed position. The result channel is buffered: the writer never waits for a caller that left.
type trafficCommand struct {
	kind trafficCommandKind
	live *liveState
	done chan error
}

func (t *historyTraffic) submit(ctx context.Context, cmd trafficCommand) error {
	cmd.done = make(chan error, 1)
	select {
	case t.commands <- cmd:
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return errors.New("history_unavailable")
	}
	t.poke()
	select {
	case err := <-cmd.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		select {
		case err := <-cmd.done:
			return err
		default:
			return errors.New("history_unavailable")
		}
	}
}

// Flush returns once every journal row appended before the call is committed to traffic.db.
func (t *historyTraffic) Flush(ctx context.Context) error {
	return t.submit(ctx, trafficCommand{kind: trafficCommandFlush})
}

// runCommands executes everything already queued, in order, after the current batch.
func (t *historyTraffic) runCommands(ctx context.Context) {
	if t.executing {
		return
	}
	t.executing = true
	defer func() { t.executing = false }()
	for {
		select {
		case cmd := <-t.commands:
			cmd.done <- t.execute(ctx, cmd)
		default:
			return
		}
	}
}

func (t *historyTraffic) execute(ctx context.Context, cmd trafficCommand) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cmd.kind == trafficCommandRestore {
		// No drain: the point is the backlog the job has not committed yet.
		t.restoreLive = cmd.live
		t.restoreAt = time.Time{}
		t.poke()
		return nil
	}
	return t.drain(ctx)
}

// drain consumes the journal until it is empty, so a command applies to every row admitted before
// it. A failed pass returns its error, however long the backoff: an empty journal is the only success.
func (t *historyTraffic) drain(ctx context.Context) error {
	for range trafficDrainIterations {
		delay, err := t.stepOnce(ctx)
		if err != nil {
			return err
		}
		if delay > 0 {
			return nil
		}
	}
	return errors.New("history_query_busy")
}

// replayPending reads the journal after the committed cursor and the stored marks, derives each record's pending entry and hands them to live.
func (t *historyTraffic) replayPending(ctx context.Context, live *liveState) error {
	stored, err := t.store.FlowMarks(ctx)
	if err != nil {
		return err
	}
	marks := make(map[string]flowMarks, len(stored))
	for _, m := range stored {
		marks[m.FlowKey] = flowMarks{Started: m.Started, Failed: m.Failed, Responded: m.Responded, Closed: m.Closed, LastAt: unixMilliTime(m.LastAtMs)}
	}
	var entries []pendingEntry
	cursor := t.state.cursor
	tail, err := t.journal.Tail(ctx)
	if err != nil {
		return err
	}
	for {
		rows, err := t.journal.ReadAfter(ctx, cursor, trafficBatchRecords)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		reached := false
		for i, row := range rows {
			if row.Session == tail.Session && row.Seq == tail.Seq {
				rows = rows[:i+1]
				reached = true
				break
			}
		}
		for _, row := range rows {
			cursor = historystore.Cursor{Session: row.Session, Seq: row.Seq}
			if row.Kind == journalGapKind || row.Kind == journalPollKind {
				continue
			}
			var o config.Observation
			if err = json.Unmarshal(row.Body, &o); err != nil {
				return err
			}
			if o.Flow == nil || o.At.IsZero() {
				continue
			}
			entry, _, ok := pendingOf(marks[o.Flow.Key], o)
			if ok || entry.fact != nil {
				entries = append(entries, entry)
			}
		}
		if reached {
			break
		}
	}
	live.restorePending(entries, marks)
	return nil
}
