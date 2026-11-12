package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"pax-connect/daemon/internal/config"
	"pax-connect/daemon/internal/historystore"
)

// trafficPollSpacing is the largest distance between two polls that still proves collection for
// the time between them: the daemon polls every 5 s and a read may take up to 700 ms.
const trafficPollSpacing = 8 * time.Second

// pollMark is the previous poll of the journal stream. It is zero after a gap marker: the next poll
// then only starts a new interval.
type pollMark struct {
	Session    string `json:"s"`
	Collecting bool   `json:"c"`
	AtMs       int64  `json:"a"`
	Valid      bool   `json:"v"`
}

func loadPollMark(ctx context.Context, store trafficStore) (pollMark, error) {
	raw, ok, err := store.Meta(ctx, trafficMetaLastPoll)
	if err != nil || !ok || raw == "" {
		return pollMark{}, err
	}
	var m pollMark
	if json.Unmarshal([]byte(raw), &m) != nil {
		return pollMark{}, nil // an unreadable marker only costs one coverage interval
	}
	return m, nil
}

func (m pollMark) encode() string {
	if !m.Valid {
		return ""
	}
	raw, _ := json.Marshal(m) // plain fields: Marshal cannot fail
	return string(raw)
}

// apply folds one journal batch into the database inside tx and returns the position it reaches. state
// is a copy: it becomes the committed position only after the transaction succeeds. Counters, rows, flow
// marks, coverage and the cursor commit together, so a crash before commit replays the batch exactly.
func (t *historyTraffic) apply(ctx context.Context, tx *historystore.TrafficTx, state trafficState, rows []historystore.Observation) (trafficState, time.Time, error) {
	latest := t.identities
	var err error
	if latest == nil {
		latest, err = t.trafficObservations(ctx, rows)
	}
	if err != nil {
		return state, time.Time{}, err
	}
	batch := newTrafficBatch(tx)
	var observed time.Time
	visible := false // a stored observation may change a query result (R2); poll and gap rows never do
	pollBefore := state.poll
	for _, row := range rows {
		if state.cursor.Session == row.Session && row.Seq <= state.cursor.Seq {
			continue
		}
		cursor := historystore.Cursor{Session: row.Session, Seq: row.Seq}
		switch row.Kind {
		case journalGapKind:
			state.poll = pollMark{}
			if err := tx.SetMeta(ctx, historystore.MetaPartial, "1"); err != nil {
				return state, observed, err
			}
		case journalPollKind:
			var poll journalPoll
			if err := json.Unmarshal(row.Body, &poll); err != nil {
				return state, observed, fmt.Errorf("history traffic: poll: %w", err)
			}
			if err := t.applyPoll(ctx, batch, &state, poll); err != nil {
				return state, observed, err
			}
		default:
			var o config.Observation
			if err := json.Unmarshal(row.Body, &o); err != nil {
				return state, observed, fmt.Errorf("history traffic: observation: %w", err)
			}
			if o.Flow != nil {
				f := trafficIdentity(*o.Flow, latest[o.Flow.Key])
				o.Flow = &f
			}
			if err := t.applyObservation(ctx, batch, o); err != nil {
				return state, observed, err
			}
			state.core = cursor
			visible = visible || o.Flow != nil && !o.At.IsZero()
			if o.At.After(observed) {
				observed = o.At
			}
		}
		state.cursor = cursor
	}
	t.receipts = batch.receipts
	if err := batch.flush(ctx); err != nil {
		return state, observed, err
	}
	if !state.coverage && len(rows) > 0 {
		if err := tx.SetMeta(ctx, trafficMetaCoverageFrom, fmt.Sprint(rows[0].AtMs)); err != nil {
			return state, observed, err
		}
		state.coverage = true
	}
	if state.poll != pollBefore {
		if err := tx.SetMeta(ctx, trafficMetaLastPoll, state.poll.encode()); err != nil {
			return state, observed, err
		}
	}
	if t.down {
		if err := tx.SetMeta(ctx, historystore.MetaErrorCode, ""); err != nil {
			return state, observed, err
		}
	}
	if state.cursor != t.state.cursor {
		if err := tx.SetCursor(ctx, trafficCursorName, state.cursor); err != nil {
			return state, observed, err
		}
	}
	if state.core != t.state.core {
		raw, _ := json.Marshal(state.core)
		if err := tx.SetMeta(ctx, "core_cursor", string(raw)); err != nil {
			return state, observed, err
		}
	}
	if visible {
		if err := tx.SetMeta(ctx, "traffic_revision", fmt.Sprint(state.revision+1)); err != nil {
			return state, observed, err
		}
		state.revision++
	}
	return state, observed, nil
}

// applyPoll sets the collection bits between two consecutive polls of one session that were both
// collecting, at most trafficPollSpacing apart, with no gap marker between them (HS-R5).
func (t *historyTraffic) applyPoll(ctx context.Context, b *trafficBatch, state *trafficState, p journalPoll) error {
	if p.FromMs > 0 {
		// Span form: the writer already proved the whole range; no pairing with a neighbour.
		state.poll = pollMark{}
		if !p.Collecting || p.AtMs <= p.FromMs {
			return nil
		}
		return b.markSeconds(ctx, time.UnixMilli(p.FromMs).UTC(), time.UnixMilli(p.AtMs).UTC())
	}
	prev := state.poll
	state.poll = pollMark{Session: p.Session, Collecting: p.Collecting, AtMs: p.AtMs, Valid: true}
	if !prev.Valid || !prev.Collecting || !p.Collecting || prev.Session != p.Session {
		return nil
	}
	if d := time.Duration(p.AtMs-prev.AtMs) * time.Millisecond; d <= 0 || d > trafficPollSpacing {
		return nil
	}
	return b.markSeconds(ctx, time.UnixMilli(prev.AtMs).UTC(), time.UnixMilli(p.AtMs).UTC())
}

// applyObservation counts one observation from the facts it carries (history-storage §5): bytes go
// into the 5-minute bucket of the record time, the one-shot counters are deduplicated through the
// flow's marks, and every merge is repeated into recent_cuts. Late attribution is not migrated.
func (t *historyTraffic) applyObservation(ctx context.Context, b *trafficBatch, o config.Observation) error {
	if o.Flow == nil || o.At.IsZero() {
		return nil
	}
	t.apps.identifyContext(ctx, o.Flow)
	if t.route != nil {
		t.route(o.Flow)
	}
	f := *o.Flow
	entry, err := b.mark(ctx, f.Key)
	if err != nil {
		return err
	}
	receipt, _, _ := pendingOf(entry.mark, o)
	b.receipts = append(b.receipts, receipt)
	c := countFlow(entry.mark, o, f)
	entry.mark = entry.mark.advance(c, o, f)
	for _, w := range trafficWrites(f, o, c) {
		for table := range 2 {
			if err = b.merge(ctx, table, w, f, o); err != nil {
				return err
			}
		}
	}
	return nil
}
