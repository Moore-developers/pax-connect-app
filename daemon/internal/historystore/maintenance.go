package historystore

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

// deleteChunk bounds one DELETE statement so the time budget is checked often.
const deleteChunk = 256

// Limits bounds one maintenance transaction (HS-R2). Both fields must be positive;
// the time bound is checked between chunks of at most 256 rows, so it is soft.
type Limits struct {
	Rows     int
	Duration time.Duration
}

func (l Limits) validate() error {
	if l.Rows <= 0 || l.Duration <= 0 {
		return ErrInvalidLimits
	}
	return nil
}

// Pruned reports one batched deletion: rows removed and whether matching rows remain.
type Pruned struct {
	Deleted int
	More    bool
}

// pruneTarget deletes rows of table whose time column is below before (and match where, if set).
type pruneTarget struct {
	table, column string
	removalMeta   string
	before        int64
	where         string
}

func (t pruneTarget) condition() string {
	cond := t.column + " < ?"
	if t.where != "" {
		cond += " AND " + t.where
	}
	return cond
}

// prune runs the targets in one write transaction within limits. Table and
// column names are package constants, never caller input.
func (s *store) prune(ctx context.Context, targets []pruneTarget, limits Limits) (Pruned, error) {
	if err := limits.validate(); err != nil {
		return Pruned{}, err
	}
	var result Pruned
	err := s.update(ctx, func(tx *sql.Tx) error {
		deadline := time.Now().Add(limits.Duration)
		for _, t := range targets {
			for result.Deleted < limits.Rows && time.Now().Before(deadline) {
				chunk := min(deleteChunk, limits.Rows-result.Deleted)
				res, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %[1]s WHERE rowid IN (SELECT rowid FROM %[1]s WHERE %[2]s LIMIT ?)", t.table, t.condition()), t.before, chunk)
				if err != nil {
					return fmt.Errorf("historystore: prune %s: %w", t.table, err)
				}
				n, err := res.RowsAffected()
				if err != nil {
					return fmt.Errorf("historystore: prune %s: %w", t.table, err)
				}
				result.Deleted += int(n)
				if n > 0 && t.removalMeta != "" {
					// Persist the conservative removal bound in the same transaction as the delete.
					if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?)
      ON CONFLICT(key) DO UPDATE SET value=CAST(max(CAST(value AS INTEGER),CAST(excluded.value AS INTEGER)) AS TEXT)`, t.removalMeta, strconv.FormatInt(t.before, 10)); err != nil {
						return err
					}
				}
				if int(n) < chunk {
					break
				}
			}
		}
		for _, t := range targets {
			var more bool
			if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE %s)", t.table, t.condition()), t.before).Scan(&more); err != nil {
				return fmt.Errorf("historystore: prune %s: %w", t.table, err)
			}
			result.More = result.More || more
		}
		return nil
	})
	if err != nil {
		return Pruned{}, err
	}
	return result, nil
}

// LiveBytes returns (page_count - freelist_count) * page_size: the data the file
// really holds, which drops immediately after deletes even before vacuum.
// Do not call it from inside an Update callback.
func (s *store) LiveBytes(ctx context.Context) (int64, error) {
	var pages, free, size int64
	for _, q := range []struct {
		pragma string
		dest   *int64
	}{{"page_count", &pages}, {"freelist_count", &free}, {"page_size", &size}} {
		if err := s.w.QueryRowContext(ctx, "PRAGMA "+q.pragma).Scan(q.dest); err != nil {
			return 0, fmt.Errorf("historystore: size: %w", err)
		}
	}
	return (pages - free) * size, nil
}

// IncrementalVacuum returns up to pages free pages to the file system; call it while the writer is idle.
func (s *store) IncrementalVacuum(ctx context.Context, pages int) error {
	if pages <= 0 {
		return ErrInvalidLimits
	}
	if _, err := s.w.ExecContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages)); err != nil {
		return fmt.Errorf("historystore: incremental_vacuum: %w", err)
	}
	return nil
}

// Checkpoint runs wal_checkpoint(TRUNCATE) and reports whether the WAL was fully truncated;
// false means readers held it back and the call can simply be retried later.
func (s *store) Checkpoint(ctx context.Context) (bool, error) {
	var busy, logFrames, checkpointed int
	if err := s.w.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return false, fmt.Errorf("historystore: checkpoint: %w", err)
	}
	return busy == 0, nil
}
