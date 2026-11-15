package historystore

import "context"

// ReclaimUpgrade returns one bounded slice of freed v3 pages. The log owner
// interleaves slices with normal collection. The phase survives process exit.
func (l *Logs) ReclaimUpgrade(ctx context.Context) (bool, error) {
	state, err := l.UpgradeState(ctx)
	if err != nil || state != "ready" {
		return true, err
	}
	phase, _, err := l.Meta(ctx, "column_upgrade_phase")
	if err != nil || phase == "reclaimed" {
		return err == nil, err
	}
	if err = l.IncrementalVacuum(ctx, 2000); err != nil {
		return false, err
	}
	if _, err = l.Checkpoint(ctx); err != nil {
		return false, err
	}
	var free int
	if err = l.w.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil {
		return false, err
	}
	if free != 0 {
		return false, nil
	}
	err = l.Update(ctx, func(tx *LogsTx) error {
		return tx.SetMeta(ctx, "column_upgrade_phase", "reclaimed")
	})
	return err == nil, err
}
