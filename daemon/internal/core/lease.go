package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"pax-connect/daemon/internal/processidentity"
)

// The root-private record authorizes replacement of one exact core process by
// its original App owner. PID alone never authorizes a signal.
type coreRecord struct {
	Owner processidentity.Identity
	Core  processidentity.Identity
}

func replacementIdentity(record coreRecord, owner processidentity.Identity, read func(int) (processidentity.Identity, error)) error {
	if record.Owner != owner || record.Core.PID <= 1 || record.Core.PID == os.Getpid() || record.Core.UID != 0 {
		return errors.New("core_owner_invalid")
	}
	actual, err := read(record.Core.PID)
	if err != nil || actual != record.Core {
		return errors.New("core_identity_changed")
	}
	return nil
}

func rootStateFile(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "core-state")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !rootOwned(info) {
		_ = f.Close()
		return nil, errors.New("core_state_unsafe")
	}
	return f, nil
}

func readCoreRecord(path string) (coreRecord, error) {
	var record coreRecord
	f, err := rootStateFile(path, syscall.O_RDONLY)
	if err != nil {
		return record, err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if err = d.Decode(&record); err != nil {
		return record, errors.New("core_state_unsafe")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return record, errors.New("core_state_unsafe")
	}
	return record, nil
}

func coreLease(ctx context.Context, o Options, owner processidentity.Identity) (func(), error) {
	f, err := rootStateFile(filepath.Join(o.State, "process.lock"), syscall.O_CREAT|syscall.O_RDWR)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = f.Close()
		}
	}()
	path := filepath.Join(o.State, "process.json")
	lock := func() error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
	if err = lock(); err != nil {
		if !o.Replace || !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("core_already_running")
		}
		record, readErr := readCoreRecord(path)
		if readErr != nil {
			return nil, errors.New("core_owner_invalid")
		}
		if err = replacementIdentity(record, owner, processidentity.Read); err != nil {
			return nil, err
		}
		if err = syscall.Kill(record.Core.PID, syscall.SIGTERM); err != nil {
			return nil, errors.New("core_replace_failed")
		}
		deadline := time.Now().Add(4 * time.Second)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		killed := false
		for {
			if err = lock(); err == nil {
				break
			}
			if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
				return nil, errors.New("core_replace_failed")
			}
			if !killed && time.Until(deadline) < time.Second {
				// Re-check the kernel start time immediately before escalation.
				if replacementIdentity(record, owner, processidentity.Read) == nil {
					_ = syscall.Kill(record.Core.PID, syscall.SIGKILL)
				}
				killed = true
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	}
	self, err := processidentity.Read(os.Getpid())
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(coreRecord{Owner: owner, Core: self})
	if err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(o.State, ".process-")
	if err != nil {
		return nil, err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err = os.Rename(name, path); err != nil {
		return nil, err
	}
	failed = false
	// Keep the record for crash recovery; the held lock excludes concurrent starts.
	return func() { _ = f.Close() }, nil
}
