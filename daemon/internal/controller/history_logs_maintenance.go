package controller

import (
	"context"
	"strconv"
	"time"

	"pax-connect/daemon/internal/historystore"
)

const (
	logsMaintainRows     = 2000
	logsMaintainDuration = 100 * time.Millisecond
	logsIdleVacuumGap    = time.Minute
	logsVacuumPages      = 64
)

// maintain prunes by retention, evicts past the capacity cap and, only when the backlog is
// clear, returns free pages and truncates the WAL (HS-R1, HS-R2). One pass is bounded by
// logsMaintainRows or logsMaintainDuration; Pruned.More postpones the rest to the next pass.
func (l *historyLogs) maintain(ctx context.Context) time.Duration {
	now := l.now()
	hours, err := l.retentionHours(ctx)
	if err != nil {
		return l.fail(ctx, "history_logs_maintain_failed")
	}
	limits := historystore.Limits{Rows: logsMaintainRows, Duration: logsMaintainDuration}
	pruned, err := l.logs.Prune(ctx, now.Add(-time.Duration(hours)*time.Hour), limits)
	if err != nil {
		return l.fail(ctx, "history_logs_maintain_failed")
	}
	if pruned.Deleted > 0 {
		if err = l.bumpDeletion(ctx); err != nil {
			return l.fail(ctx, "history_logs_maintain_failed")
		}
	}
	if pruned.More {
		return 0
	}
	live, err := l.logs.LiveBytes(ctx)
	if err != nil {
		return l.fail(ctx, "history_logs_maintain_failed")
	}
	if live > l.capBytes {
		evicted, evictErr := l.logs.EvictOldest(ctx, l.capBytes, limits)
		if evictErr != nil {
			return l.fail(ctx, "history_logs_maintain_failed")
		}
		if evicted {
			if err = l.bumpDeletion(ctx); err != nil {
				return l.fail(ctx, "history_logs_maintain_failed")
			}
		}
	}
	l.idleMaintenance(ctx)
	return 0
}

// retentionHours reads the setting written by the logs owner; the default is 24 hours.
func (l *historyLogs) retentionHours(ctx context.Context) (int, error) {
	raw, ok, err := l.logs.Meta(ctx, historystore.MetaRetentionHours)
	if err != nil {
		return 0, err
	}
	if !ok || raw == "" {
		return logsRetentionHours, nil
	}
	hours, err := strconv.Atoi(raw)
	if err != nil {
		return logsRetentionHours, nil
	}
	return normalizedRetentionHours(hours), nil
}

// idleMaintenance runs at most once a minute and is best-effort: a busy checkpoint simply
// retries later, and neither action affects writes.
func (l *historyLogs) idleMaintenance(ctx context.Context) {
	now := l.now()
	if !l.lastVacuum.IsZero() && now.Sub(l.lastVacuum) < logsIdleVacuumGap {
		return
	}
	l.lastVacuum = now
	_ = l.logs.IncrementalVacuum(ctx, logsVacuumPages)
	_, _ = l.logs.Checkpoint(ctx)
}

// normalizeRetention applies the current product limit to a database created by an
// earlier writer, independent of any retired import metadata.
func (l *historyLogs) normalizeRetention(ctx context.Context) error {
	raw, _, err := l.logs.Meta(ctx, historystore.MetaRetentionHours)
	if err != nil {
		return err
	}
	hours, _ := strconv.Atoi(raw)
	if validRetentionHours(hours) {
		return nil
	}
	return l.logs.Update(ctx, func(tx *historystore.LogsTx) error {
		revision, _, e := tx.Meta(ctx, historystore.MetaSettingsRevision)
		if e != nil {
			return e
		}
		n, _ := strconv.ParseUint(revision, 10, 64)
		if e = tx.SetMeta(ctx, historystore.MetaRetentionHours, strconv.Itoa(normalizedRetentionHours(hours))); e != nil {
			return e
		}
		return tx.SetMeta(ctx, historystore.MetaSettingsRevision, strconv.FormatUint(n+1, 10))
	})
}
