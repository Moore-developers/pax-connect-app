package historystore

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// SQLiteDiskBytes reports filesystem allocation for an active database and its
// SQLite companions. A missing database contributes zero.
func SQLiteDiskBytes(path string, owner uint32) (uint64, error) {
	var bytes uint64
	for _, suffix := range [...]string{"", "-wal", "-shm"} {
		info, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			if suffix == "" {
				return 0, nil
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		if err := checkInfo(info, owner); err != nil {
			return 0, err
		}
		// POSIX st_blocks counts 512-byte units, including on APFS.
		bytes += uint64(info.Sys().(*syscall.Stat_t).Blocks) * 512
	}
	return bytes, nil
}

// JournalDiskBytes counts only active hourly segments, using the same naming
// contract as journal retention.
func JournalDiskBytes(historyDirectory string, owner uint32) (uint64, error) {
	dir := filepath.Join(historyDirectory, journalDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var bytes uint64
	for _, entry := range entries {
		if !hourFile.MatchString(entry.Name()) {
			continue
		}
		segment, err := SQLiteDiskBytes(filepath.Join(dir, entry.Name()), owner)
		if err != nil {
			return 0, err
		}
		bytes += segment
	}
	return bytes, nil
}
