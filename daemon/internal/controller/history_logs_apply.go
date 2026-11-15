package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
	"pax-connect/daemon/internal/telemetry"
)

// apply writes one journal batch and the drained Pax inbox in one transaction. state is a
// local copy: it becomes the committed position only after Update succeeds.
func (l *historyLogs) apply(ctx context.Context, tx *historystore.LogsTx, state logsState, rows []historystore.Observation, events []config.HistoryEvent, overflow bool) (logsState, error) {
	insertedEvent := false
	for _, row := range rows {
		if state.cursor.Session == row.Session && row.Seq <= state.cursor.Seq {
			continue
		}
		if row.Kind == journalPollKind {
			// Poll rows are coverage evidence for the traffic job; only the cursor moves.
			state.cursor = historystore.Cursor{Session: row.Session, Seq: row.Seq}
			continue
		}
		if row.Kind == journalGapKind {
			var gap journalGap
			if err := json.Unmarshal(row.Body, &gap); err != nil {
				return state, fmt.Errorf("history logs: gap: %w", err)
			}
			if err := l.markGap(ctx, tx); err != nil {
				return state, err
			}
			state.pendingGap, state.gapSession, state.gapThrough = false, "", 0
			if gap.ThroughSeq > 0 {
				state.gapSession, state.gapThrough = gap.Session, gap.ThroughSeq
			} else if gap.Reason != gapSessionChanged && gap.Reason != gapLost && gap.Reason != gapReset && gap.Reason != gapCursorAhead {
				state.pendingGap = true
			}
			state.cursor = historystore.Cursor{Session: row.Session, Seq: row.Seq}
			continue
		}
		var o config.Observation
		if err := json.Unmarshal(row.Body, &o); err != nil {
			return state, fmt.Errorf("history logs: observation: %w", err)
		}
		through := state.gapThrough > 0 && o.Session == state.gapSession && o.Sequence <= state.gapThrough
		gap := state.pendingGap || through || state.lastSession != "" && (o.Session != state.lastSession || o.Sequence != state.lastSeq+1)
		state.pendingGap = false
		inserted, err := l.applyObservation(ctx, tx, o, gap)
		if err != nil {
			return state, err
		}
		insertedEvent = insertedEvent || inserted
		state.lastSession, state.lastSeq = o.Session, o.Sequence
		state.cursor = historystore.Cursor{Session: row.Session, Seq: row.Seq}
	}
	for _, e := range events {
		inserted, err := l.applyEvent(ctx, tx, e, false)
		if err != nil {
			return state, err
		}
		insertedEvent = insertedEvent || inserted
	}
	if insertedEvent {
		state.revision++
		if err := tx.SetMeta(ctx, historystore.MetaLogsRevision, fmt.Sprint(state.revision)); err != nil {
			return state, err
		}
	}
	if overflow {
		if err := tx.SetMeta(ctx, historystore.MetaPartial, "1"); err != nil {
			return state, err
		}
		if err := tx.SetMeta(ctx, historystore.MetaErrorCode, logsErrorIngressFull); err != nil {
			return state, err
		}
	} else if l.down {
		if err := tx.SetMeta(ctx, historystore.MetaErrorCode, ""); err != nil {
			return state, err
		}
	}
	if state.cursor != l.state.cursor {
		if err := tx.SetCursor(ctx, logsCursorName, state.cursor); err != nil {
			return state, err
		}
	}
	return state, nil
}

// applyObservation updates the connection summary first, then diagnostics and the product
// event, in the same order as the legacy AcceptObservations path.
func (l *historyLogs) applyObservation(ctx context.Context, tx *historystore.LogsTx, o config.Observation, gap bool) (bool, error) {
	if o.Flow == nil {
		e := telemetry.ProductEvent(o)
		if e == nil {
			return false, nil
		}
		return l.applyEvent(ctx, tx, *e, true)
	}
	key := o.Flow.Key
	summary, _, err := tx.Summary(ctx, key)
	if err != nil {
		return false, err
	}
	before := summary.Fact
	l.apps.identifyContext(ctx, o.Flow)
	if l.route != nil {
		l.route(o.Flow)
	}
	o.Flow = telemetry.MergeAcceptedFlow(&summary.Fact, o.Flow)
	summary, _ = telemetry.ApplyLogObservation(summary, o, gap, l.clearedAt)
	if o.Kind == "diagnostic" && o.Raw != "" {
		raw, _, _ := telemetry.ScrubRaw(o.Raw)
		if raw != "" {
			if o.Sequence > math.MaxInt64 {
				return false, fmt.Errorf("history logs: diagnostic sequence out of range")
			}
			if err = tx.PutDiagnostic(ctx, historystore.LogDiagnostic{FlowKey: key, Seq: int64(o.Sequence), AtMs: o.At.UnixMilli(), Text: raw}); err != nil {
				return false, err
			}
			if err = tx.TrimDiagnostics(ctx, key, 8); err != nil {
				return false, err
			}
		}
	}
	e := telemetry.ProductEvent(o)
	if e == nil {
		if err = putLogSummaryTx(ctx, tx, summary); err != nil {
			return false, err
		}
		return !sameRecentResponseFact(before, summary.Fact), nil
	}
	inserted, err := l.applyEventWithSummary(ctx, tx, *e, true, &summary)
	return inserted || !sameRecentResponseFact(before, summary.Fact), err
}

// applyEvent stores one event once, assigning the head sequence and checkpointing the
// connection summary from the saved evidence. strict reports malformed
// observation events as errors; inbox events from other producers are dropped instead.
func (l *historyLogs) applyEvent(ctx context.Context, tx *historystore.LogsTx, e config.HistoryEvent, strict bool) (bool, error) {
	return l.applyEventWithSummary(ctx, tx, e, strict, nil)
}

// Observation folding already owns the current summary. Checkpoint it before
// the single write instead of writing, reading/decoding and rewriting it in
// the same transaction. Inbox-only product events still load their summary.
func (l *historyLogs) applyEventWithSummary(ctx context.Context, tx *historystore.LogsTx, e config.HistoryEvent, strict bool, observationSummary *config.LogConnectionSummary) (bool, error) {
	if validLogEvent(e) != nil {
		if strict {
			return false, errInvalidLogEvent
		}
		return false, nil
	}
	exists, err := tx.EventExists(ctx, e.ID)
	if err != nil || exists {
		if err == nil && observationSummary != nil {
			err = putLogSummaryTx(ctx, tx, *observationSummary)
		}
		return false, err
	}
	seq, err := tx.NextEventSequence(ctx)
	if err != nil {
		return false, err
	}
	e.Sequence = uint64(seq)
	telemetry.LimitEventRaw(&e)
	if e.Flow != nil {
		var summary config.LogConnectionSummary
		ok := observationSummary != nil
		if ok {
			summary = *observationSummary
		} else {
			var err error
			summary, ok, err = tx.Summary(ctx, e.Flow.Key)
			if err != nil {
				return false, err
			}
		}
		if ok {
			summary = telemetry.CheckpointLogEvent(summary, &e)
			if err = putLogSummaryTx(ctx, tx, summary); err != nil {
				return false, err
			}
		}
	}
	inserted, _, err := tx.AppendEvent(ctx, e)
	return inserted, err
}

// validLogEvent applies the legacy event validity rule shared by RecordEvent and the writer.
func validLogEvent(e config.HistoryEvent) error {
	if e.ID == "" || e.At.IsZero() || len(e.ID) > 256 {
		return errInvalidLogEvent
	}
	return nil
}

// markGap rewrites every still-open summary as partial from its last known time. The
// scan completes before any write so the same transaction never updates an open cursor.
func (l *historyLogs) markGap(ctx context.Context, tx *historystore.LogsTx) error {
	if err := tx.MarkImportGap(ctx); err != nil {
		return err
	}
	rows, err := tx.Summaries(ctx)
	if err != nil {
		return err
	}
	for _, summary := range rows {
		if summary.Fact.ClosedAt != nil {
			continue
		}
		summary = config.MarkLogGap(summary)
		if err = putLogSummaryTx(ctx, tx, summary); err != nil {
			return err
		}
	}
	return nil
}

func putLogSummaryTx(ctx context.Context, tx *historystore.LogsTx, s config.LogConnectionSummary) error {
	return tx.PutSummary(ctx, s)
}

// Only new chart facts add a revision when no visible product event was inserted.
// Transfer counters and observation timestamps alone do not create extra refreshes.
func sameRecentResponseFact(a, b config.FlowFact) bool {
	ams, aknown := config.FlowLatency(a)
	bms, bknown := config.FlowLatency(b)
	if aknown != bknown {
		return false
	}
	if !aknown {
		return true
	}
	return ams == bms && a.Key == b.Key && a.StartedAt.Equal(b.StartedAt) && slices.Equal(aggregateIdentities(a), aggregateIdentities(b))
}
