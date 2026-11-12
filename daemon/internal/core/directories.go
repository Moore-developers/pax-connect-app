package core

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type directoryHost struct {
	lstat func(string) (os.FileInfo, error)
	mkdir func(string, os.FileMode) error
	chown func(string, int, int) error
	chmod func(string, os.FileMode) error
}

func nativeDirectories() directoryHost { return directoryHost{os.Lstat, os.Mkdir, os.Chown, os.Chmod} }
func protectedDirectory(path string, mode os.FileMode) error {
	return protectDirectory(path, mode, nativeDirectories())
}

// Only this known Apple system directory may have group write permission.
// Require its precise identity/mode; the exception never applies to children.
func trustedParent(path string, info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return path == "/private/var/run" && ok && s.Uid == 0 && s.Gid == 1 && info.Mode() == os.ModeDir|0775
}
func directoryIdentity(info os.FileInfo, mode os.FileMode) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == 0 && s.Gid == 0 && info.Mode() == os.ModeDir|mode
}
func protectDirectory(path string, mode os.FileMode, h directoryHost) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || path == "/" {
		return errors.New("core_directory_unsafe")
	}
	// Walk root-to-leaf. Never follow aliases/symlinks or repair an existing dir.
	var chain []string
	for p := path; ; p = filepath.Dir(p) {
		chain = append(chain, p)
		if p == "/" {
			break
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		expected := os.FileMode(0755)
		if p == path {
			expected = mode
		}
		info, err := h.lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			if err = h.mkdir(p, expected); err != nil {
				return errors.New("core_directory_create_failed")
			}
			// Chown/chmod is legal only after THIS call successfully creates the dir.
			if err = h.chown(p, 0, 0); err != nil {
				return errors.New("core_directory_owner_failed")
			}
			if err = h.chmod(p, expected); err != nil {
				return errors.New("core_directory_mode_failed")
			}
			info, err = h.lstat(p)
			if err != nil || !directoryIdentity(info, expected) {
				return errors.New("core_directory_unsafe")
			}
		} else if err != nil {
			return errors.New("core_directory_stat_failed")
		} else if p == path {
			if !directoryIdentity(info, expected) {
				return errors.New("core_directory_unsafe")
			}
		} else {
			if trustedParent(p, info) {
				continue
			}
			if !info.IsDir() || !rootOwned(info) || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm()&0022 != 0 {
				return errors.New("core_parent_unsafe")
			}
		}
	}
	return nil
}
