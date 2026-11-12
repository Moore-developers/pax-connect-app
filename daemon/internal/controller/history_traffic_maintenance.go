package controller

import (
	"context"
	"fmt"
	"time"

	"pax-connect/daemon/internal/historystore"
)

const (
	trafficRetention       = 30 * 24 * time.Hour
	trafficRecentRetention = 20 * time.Minute
	trafficClosedMarkAge   = 10 * time.Minute
	trafficOpenMarkAge     = 24 * time.Hour
	trafficMaintainRows    = 2000
	trafficMaintainTime    = 100 * time.Millisecond
	trafficIdleVacuumGap   = time.Minute
	trafficVacuumPages     = 64
)

// maintain prunes by retention, evicts past the capacity cap and, only when the backlog is clear,
// returns free pages and truncates the WAL (HS-R1, HS-R2). One pass is bounded by trafficMaintainRows
// or trafficMaintainTime; Pruned.More postpones the rest to the next pass.
//
// Deleting stored traffic (aggregates past retention, capacity eviction) moves
// traffic_deletion_generation and traffic_revision (R3); the routine pruning of the 5-second tail and
// of flow marks does not, it never removes anything a window query would have shown.
//
// Flow marks are judged on observation time: closed ones expire 10 minutes after their last record,
// never-closed ones 24 hours after. Until a record was consumed in this run no mark is pruned.
func (t *historyTraffic) maintain(ctx context.Context) time.Duration {
	now := t.now()
	cutoffs := historystore.TrafficCutoffs{Aggregates: now.Add(-trafficRetention), Recent: now.Add(-trafficRecentRetention)}
	if !t.observedAt.IsZero() {
		cutoffs.ClosedMarks = t.observedAt.Add(-trafficClosedMarkAge)
		cutoffs.OpenMarks = t.observedAt.Add(-trafficOpenMarkAge)
	}
	limits := historystore.Limits{Rows: trafficMaintainRows, Duration: trafficMaintainTime}
	expired, err := t.store.Prune(ctx, historystore.TrafficCutoffs{Aggregates: cutoffs.Aggregates}, limits)
	if err == nil && expired.Deleted > 0 {
		err = t.bumpDeletion(ctx)
	}
	if err != nil {
		return t.fail(ctx, "history_traffic_maintain_failed")
	}
	if expired.More {
		return 0
	}
	cutoffs.Aggregates = time.Time{}
	pruned, err := t.store.Prune(ctx, cutoffs, limits)
	if err != nil {
		return t.fail(ctx, "history_traffic_maintain_failed")
	}
	if pruned.More {
		return 0
	}
	live, err := t.store.LiveBytes(ctx)
	if err != nil {
		return t.fail(ctx, "history_traffic_maintain_failed")
	}
	if live > t.capBytes {
		// EvictOldest stamps capacity_evicted; writes are never refused for capacity (HS-R1).
		evicted, evictErr := t.store.EvictOldest(ctx, t.capBytes, limits)
		if evictErr != nil {
			return t.fail(ctx, "history_traffic_maintain_failed")
		}
		if evicted {
			metaCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficMetaTimeout)
			defer cancel()
			if err = t.store.Update(metaCtx, func(tx *historystore.TrafficTx) error {
				return tx.SetMeta(metaCtx, historystore.MetaPartial, "1")
			}); err == nil {
				err = t.bumpDeletion(ctx)
			}
			if err != nil {
				return t.fail(ctx, "history_traffic_maintain_failed")
			}
		}
	}
	t.idleMaintenance(ctx)
	return 0
}

// idleMaintenance runs at most once a minute and is best-effort: a busy checkpoint simply retries
// later, and neither action affects writes.
func (t *historyTraffic) idleMaintenance(ctx context.Context) {
	now := t.now()
	if !t.lastVacuum.IsZero() && now.Sub(t.lastVacuum) < trafficIdleVacuumGap {
		return
	}
	t.lastVacuum = now
	_ = t.store.IncrementalVacuum(ctx, trafficVacuumPages)
	_, _ = t.store.Checkpoint(ctx)
}

// bumpDeletion records that stored traffic was deleted: the generation and the revision move in one
// transaction, so a client drops its cache and refetches (R3, R4).
func (t *historyTraffic) bumpDeletion(ctx context.Context) error {
	metaCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), trafficMetaTimeout)
	defer cancel()
	deletion, revision := t.state.deletion+1, t.state.revision+1
	if err := t.store.Update(metaCtx, func(tx *historystore.TrafficTx) error {
		if err := tx.SetMeta(metaCtx, trafficMetaDeletion, fmt.Sprint(deletion)); err != nil {
			return err
		}
		return tx.SetMeta(metaCtx, "traffic_revision", fmt.Sprint(revision))
	}); err != nil {
		return err
	}
	t.state.deletion, t.state.revision = deletion, revision
	return nil
}
