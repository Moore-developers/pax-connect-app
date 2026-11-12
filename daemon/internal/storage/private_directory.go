package storage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// PreparePrivateDirectory creates only the final directory beneath trusted
// parents. Descriptor-based ownership changes never follow a symlink.
func PreparePrivateDirectory(path string, owner uint32) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return ErrPrivateFile
	}
	probe := NewPrivateFile(filepath.Join(path, ".directory-check"), owner, 1)
	if !probe.trustedAncestors(filepath.Dir(path)) {
		return ErrPrivateFile
	}
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return ErrPrivateFile
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ErrPrivateFile
	}
	file := os.NewFile(uintptr(fd), "private-directory")
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return ErrPrivateFile
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner {
		return ErrPrivateFile
	}
	if owner == 0 {
		if err = file.Chown(0, 0); err != nil {
			return ErrPrivateFile
		}
	}
	if err = file.Chmod(0700); err != nil {
		return ErrPrivateFile
	}
	if !probe.validParent() {
		return ErrPrivateFile
	}
	return nil
}
