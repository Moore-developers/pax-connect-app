package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ErrPrivateFile indicates missing trust, invalid bounds or failed atomic I/O.
// ErrCommitUncertain means rename committed but directory sync could not confirm durability.
var ErrCommitUncertain = errors.New("private file durability uncertain")

var ErrPrivateFile = errors.New("private file unavailable")

// PrivateFile is a bounded atomic byte store. Domain schemas belong to callers.
// Directories must already exist, owned by owner with mode0700; symlinks refused.
type PrivateFile struct {
	path  string
	owner uint32
	limit int64
}

func NewPrivateFile(path string, owner uint32, limit int64) *PrivateFile {
	return &PrivateFile{path: path, owner: owner, limit: limit}
}
func (s *PrivateFile) validParent() bool {
	if !filepath.IsAbs(s.path) || s.limit <= 0 {
		return false
	}
	if !s.trustedAncestors(filepath.Dir(s.path)) {
		return false
	}
	info, err := os.Lstat(filepath.Dir(s.path))
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == s.owner && info.Mode().Perm() == 0700 && (s.owner != 0 || stat.Gid == 0)
}
func (s *PrivateFile) Read() (data []byte, exists bool, err error) {
	if !s.validParent() {
		return nil, false, ErrPrivateFile
	}
	fd, err := syscall.Open(s.path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrPrivateFile
	}
	file := os.NewFile(uintptr(fd), "private-state")
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, false, ErrPrivateFile
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != s.owner || s.owner == 0 && stat.Gid != 0 || info.Size() > s.limit {
		return nil, false, ErrPrivateFile
	}
	data, err = io.ReadAll(io.LimitReader(file, s.limit+1))
	if err != nil || int64(len(data)) > s.limit {
		return nil, false, ErrPrivateFile
	}
	return data, true, nil
}
func (s *PrivateFile) Write(ctx context.Context, data []byte) error {
	if ctx.Err() != nil || int64(len(data)) > s.limit || !s.validParent() {
		return ErrPrivateFile
	}
	if _, _, err := s.Read(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".measurements-")
	if err != nil {
		return ErrPrivateFile
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if s.owner == 0 {
		if file.Chown(0, 0) != nil {
			_ = file.Close()
			return ErrPrivateFile
		}
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return ErrPrivateFile
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return ErrPrivateFile
	}
	if err := file.Close(); err != nil {
		return ErrPrivateFile
	}
	if ctx.Err() != nil || !s.validParent() {
		return ErrPrivateFile
	}
	if err := os.Rename(name, s.path); err != nil {
		return ErrPrivateFile
	}
	directory, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return ErrCommitUncertain
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return ErrCommitUncertain
	}
	return nil
}

func (s *PrivateFile) trustedAncestors(path string) bool {
	for p := path; p != "/"; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 && stat.Uid != s.owner || s.owner == 0 && info.Mode().Perm()&0022 != 0 {
			return false
		}
	}
	return true
}
