package historystore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"pax-connect/daemon/internal/storage"
)

// ErrJournalRecoveryPending keeps readers behind an unacknowledged durable loss.
var ErrJournalRecoveryPending = errors.New("historystore: journal recovery pending")

const journalLossLimit = 128

type journalLoss struct {
	ID            string
	Hour          time.Time
	Reason        string
	Device, Inode uint64
	Target        string
}
type journalLossState struct {
	Version int
	Pending []journalLoss
}

func (j *Journal) lossFile() *storage.PrivateFile {
	return storage.NewPrivateFile(filepath.Join(j.dir, "losses.json"), j.owner, 128<<10)
}
func (j *Journal) losses() (journalLossState, error) {
	raw, exists, err := j.lossFile().Read()
	if err != nil {
		return journalLossState{}, err
	}
	state := journalLossState{Version: 1}
	if !exists {
		return state, nil
	}
	if json.Unmarshal(raw, &state) != nil || state.Version != 1 || len(state.Pending) > journalLossLimit {
		return state, fmt.Errorf("historystore: invalid journal loss state")
	}
	for _, loss := range state.Pending {
		if loss.ID == "" || strings.ContainsAny(loss.ID, "/\\\x00") || !loss.Hour.Equal(loss.Hour.UTC().Truncate(time.Hour)) || (loss.Reason != "corrupt" && loss.Reason != "schema" && loss.Reason != "evicted") || filepath.Base(loss.Target) != loss.Target || !strings.HasPrefix(loss.Target, loss.Hour.UTC().Format(hourLayout)+".db.") {
			return state, fmt.Errorf("historystore: invalid journal loss intent")
		}
	}
	return state, nil
}
func (j *Journal) saveLosses(ctx context.Context, state journalLossState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return j.lossFile().Write(ctx, raw)
}

// loseLocked records a durable intent before a destructive action. The capture
// owner alone calls it while holding mu. A replay never removes a replacement inode.
func (j *Journal) loseLocked(ctx context.Context, hour time.Time, reason string) (Dropped, error) {
	state, err := j.losses()
	if err != nil {
		return Dropped{}, err
	}
	path := j.path(hour)
	exists, err := guardExisting(path, j.owner)
	if err != nil || !exists {
		return Dropped{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Dropped{}, err
	}
	identity := info.Sys().(*syscall.Stat_t)
	for _, loss := range state.Pending {
		if loss.Hour.Equal(hour) && loss.Device == uint64(identity.Dev) && loss.Inode == uint64(identity.Ino) {
			return j.completeLossLocked(loss)
		}
	}
	if len(state.Pending) >= journalLossLimit {
		return Dropped{}, fmt.Errorf("historystore: journal loss backlog")
	}
	id := rand.Text()
	target := filepath.Base(path) + "." + reason + "-" + time.Now().UTC().Format("20060102T150405Z") + "-" + fmt.Sprint(time.Now().UnixNano())
	loss := journalLoss{ID: id, Hour: hour.UTC(), Reason: reason, Device: uint64(identity.Dev), Inode: uint64(identity.Ino), Target: target}
	state.Pending = append(state.Pending, loss)
	if err = j.saveLosses(ctx, state); err != nil {
		return Dropped{}, err
	}
	return j.completeLossLocked(loss)
}
func (j *Journal) completeLossLocked(loss journalLoss) (Dropped, error) {
	result := Dropped{ID: loss.ID, Hour: loss.Hour, Reason: loss.Reason, Quarantined: filepath.Join(j.dir, loss.Target)}
	path := j.path(loss.Hour)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if err = checkInfo(info, j.owner); err != nil {
		return result, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if uint64(stat.Dev) != loss.Device || uint64(stat.Ino) != loss.Inode {
		return result, nil
	}
	if j.writer != nil && j.writerHour.Equal(loss.Hour) {
		if err = j.closeWriter(); err != nil {
			return result, err
		}
	}
	if loss.Reason == "evicted" {
		if err = j.removeLocked(loss.Hour); err != nil {
			return result, err
		}
	} else {
		for _, suffix := range append(append([]string(nil), sidecars...), "") {
			if err = os.Rename(path+suffix, result.Quarantined+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return result, err
			}
		}
	}
	directory, err := os.Open(j.dir)
	if err != nil {
		return result, err
	}
	defer func() { _ = directory.Close() }()
	return result, directory.Sync()
}

// PendingLosses resumes durable destruction intents before capture reads its
// cursor. It is capture-owner-only; ordinary journal readers never call this.
func (j *Journal) PendingLosses(ctx context.Context) ([]Dropped, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	state, err := j.losses()
	if err != nil {
		return nil, err
	}
	var out []Dropped
	for _, loss := range state.Pending {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		d, err := j.completeLossLocked(loss)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// AppendLoss commits a stable recovery record once across all retained hours,
// then acknowledges its durable intent. A crash between these steps reuses the
// same row, preventing duplicate cursors from skipping intervening observations.
func (j *Journal) AppendLoss(ctx context.Context, at time.Time, id string, row Observation, cursor Cursor) error {
	files, err := j.Files()
	if err != nil {
		return err
	}
	found := false
	for _, file := range files {
		_, exact, _, err := j.findRow(ctx, file.Hour, Cursor{Session: row.Session, Seq: row.Seq})
		if isCorrupt(err) || errors.Is(err, ErrSchemaVersion) {
			return &JournalDamage{Hour: file.Hour, Cause: err}
		}
		if err != nil {
			return err
		}
		found = found || exact
	}
	if !found {
		if err = j.Append(ctx, at, []Observation{row}, cursor); err != nil {
			return err
		}
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	state, err := j.losses()
	if err != nil {
		return err
	}
	kept := state.Pending[:0]
	for _, loss := range state.Pending {
		if loss.ID != id {
			kept = append(kept, loss)
		}
	}
	state.Pending = kept
	return j.saveLosses(ctx, state)
}
