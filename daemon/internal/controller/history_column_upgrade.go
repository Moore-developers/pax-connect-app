package controller

import (
	"context"
	"errors"
	"log/slog"

	"pax-connect/daemon/internal/historystore"
)

// The existing log owner advances one bounded import/projection slice between
// normal journal batches. A failed conversion is reported once, never retried
// on each query or ordinary commit; restart resumes its persisted checkpoint.
type columnUpgradeJob struct {
	store                                        *historystore.Logs
	owner                                        uint32
	prepared, factsDone, finalized, done, failed bool
	view                                         int
}

func (l *historyLogs) advanceColumnUpgrade(ctx context.Context) bool {
	u := l.columnUpgrade
	if u == nil || u.done || u.failed {
		return false
	}
	err := u.step(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return false
		}
		u.failed = true
		if u.finalized {
			slog.Warn("history upgrade reclaim stopped", "library", "logs", "code", "history_reclaim_failed")
			return false
		}
		slog.Warn("history column upgrade stopped", "library", "logs", "code", "history_store_failed")
		_ = u.store.DisableColumnProjection(context.WithoutCancel(ctx), "history_store_failed")
		_ = u.store.Update(context.WithoutCancel(ctx), func(tx *historystore.LogsTx) error {
			return tx.SetMeta(context.WithoutCancel(ctx), historystore.MetaErrorCode, "history_store_failed")
		})
		return false
	}
	return !u.done
}
func (u *columnUpgradeJob) step(ctx context.Context) error {
	if u.finalized {
		var err error
		u.done, err = u.store.ReclaimUpgrade(ctx)
		return err
	}
	if !u.prepared {
		needed, err := u.store.PrepareUpgrade(ctx, u.owner)
		if err != nil {
			return err
		}
		u.prepared = true
		if !needed {
			u.factsDone = true
			u.view = 4
		}
	}
	if !u.factsDone {
		done, err := u.store.UpgradeBatch(ctx, indexColumnBatch)
		if err != nil {
			return err
		}
		u.factsDone = done
		return nil
	}
	c := u.store.Columns()
	views := []string{"activity", "access", "problems", "operations"}
	if u.view < len(views) {
		view := views[u.view]
		name := "groups/" + view
		after, err := c.ProjectionProgress(ctx, name)
		if err != nil {
			return err
		}
		keys, err := c.GroupKeys(ctx, view, after, 64)
		if err != nil {
			return err
		}
		if len(keys) == 0 {
			u.view++
			return nil
		}
		return c.Update(ctx, func(tx *historystore.ColumnsTx) error {
			for _, key := range keys {
				g, err := projectColumnGroup(ctx, tx, view, key)
				if err != nil {
					return err
				}
				if err = tx.PutGroup(ctx, g); err != nil {
					return err
				}
			}
			return tx.SetProjectionProgress(ctx, name, keys[len(keys)-1])
		})
	}
	if ready, err := advanceColumnTrafficIndex(ctx, u.store.Columns()); err != nil {
		return err
	} else if !ready {
		return nil
	}
	if err := u.store.UpgradeFinalize(ctx); err != nil {
		return err
	}
	u.finalized = true
	return nil
}

func (l *historyLogs) columnStore(ctx context.Context) (*historystore.Columns, error) {
	source, ok := l.logs.(interface{ Columns() *historystore.Columns })
	if !ok {
		return nil, errors.New("history_unavailable")
	}
	if ready, ok := l.logs.(interface{ Ready(context.Context) error }); ok {
		if err := ready.Ready(ctx); err != nil {
			return nil, err
		}
	}
	if state, ok := l.logs.(interface {
		UpgradeState(context.Context) (string, error)
	}); ok {
		value, err := state.UpgradeState(ctx)
		if err != nil {
			return nil, err
		}
		if value == "building" {
			return nil, errors.New("history_converting")
		}
		if value == "failed" {
			return nil, errors.New("history_store_failed")
		}
	}
	return source.Columns(), nil
}

// logColumnSnapshot keeps queries from seeing partially imported evidence.
func (l *historyLogs) logColumnSnapshot(ctx context.Context) (*historystore.ColumnSnapshot, error) {
	c, err := l.columnStore(ctx)
	if err != nil || c == nil {
		return nil, err
	}
	return c.Snapshot(ctx)
}
