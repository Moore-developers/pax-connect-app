package controller

import (
	"context"
	"errors"
	"log/slog"
	"pax-connect/daemon/internal/historystore"
	"time"
)

// reportDamage only queues bounded repair intent. Capture alone mutates hour files.
func (r *journalConsumers) reportDamage(err error) {
	var damage *historystore.JournalDamage
	if !errors.As(err, &damage) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.repairs == nil {
		r.repairs = map[int64]time.Time{}
	}
	if len(r.repairs) < journalMaxFiles {
		r.repairs[damage.Hour.Unix()] = damage.Hour
	}
}

func (in *historyIngest) repairJournal(ctx context.Context) {
	owner, ok := in.journal.(interface {
		RepairHour(context.Context, time.Time) error
	})
	if !ok {
		return
	}
	in.consumers.mu.Lock()
	repairs := in.consumers.repairs
	in.consumers.repairs = nil
	in.consumers.mu.Unlock()
	for _, hour := range repairs {
		started := time.Now()
		if err := owner.RepairHour(ctx, hour); err != nil {
			in.consumers.reportDamage(&historystore.JournalDamage{Hour: hour, Cause: err})
			in.journalFailed("history_journal_repair_failed")
		}
		for _, dropped := range in.journal.TakeDropped() {
			slog.Warn("history journal quarantined", "library", "journal", "code", "history_journal_rebuilt", "elapsed_ms", time.Since(started).Milliseconds())
			if dropped.ID == "" {
				in.extra = append(in.extra, evictionGap(gapDropped, dropped.Hour))
			}
		}
	}
}

// flushJournalLosses uses storage's durable intent/ack path, separate from core
// gaps. It preserves the committed core cursor and replays once across hours.
func (in *historyIngest) flushJournalLosses(ctx context.Context) error {
	owner, ok := in.journal.(interface {
		PendingLosses(context.Context) ([]historystore.Dropped, error)
		AppendLoss(context.Context, time.Time, string, historystore.Observation, historystore.Cursor) error
	})
	if !ok {
		return nil
	}
	losses, err := owner.PendingLosses(ctx)
	if err != nil {
		return err
	}
	for _, loss := range losses {
		reason := gapDropped
		if loss.Reason == "evicted" {
			reason = gapEvicted
		}
		row := evictionGap(reason, loss.Hour).observation(in.now())
		row.Session = journalGapSession + "loss:" + loss.ID
		if err = owner.AppendLoss(ctx, in.now(), loss.ID, row, in.cursor); err != nil {
			in.consumers.reportDamage(err)
			return err
		}
	}
	return nil
}
