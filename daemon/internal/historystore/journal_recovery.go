package historystore

import (
	"context"
	"errors"
	"os"
	"time"
)

// JournalDamage identifies a corrupt or unsupported hour without mutating it.
// The reader keeps its cursor; only the capture owner calls RepairHour.
type JournalDamage struct {
	Hour  time.Time
	Cause error
}

func (e *JournalDamage) Error() string { return "historystore: journal damage" }
func (e *JournalDamage) Unwrap() error { return e.Cause }

// RepairHour is capture-owner-only. Concurrent duplicate requests are harmless;
// a healthy replacement is never quarantined in response to an old request.
func (j *Journal) RepairHour(ctx context.Context, hour time.Time) error {
	_, _, err := j.fileCursor(ctx, hour)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if !isCorrupt(err) && !errors.Is(err, ErrSchemaVersion) {
		return err
	}
	reason := "corrupt"
	if errors.Is(err, ErrSchemaVersion) {
		reason = "schema"
	}
	return j.drop(ctx, hour, reason)
}
