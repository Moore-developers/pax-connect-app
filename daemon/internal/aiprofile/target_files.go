package aiprofile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func (m *Manager) targetDir(cli CLI) string {
	if p := m.directories[cli]; p != "" {
		return p
	}
	switch cli {
	case Codex:
		return filepath.Join(m.home, ".codex")
	case Claude:
		return filepath.Join(m.home, ".claude")
	case DSH:
		return filepath.Join(m.home, ".dsh")
	}
	return ""
}
func (m *Manager) targetNames(cli CLI) []string {
	switch cli {
	case Codex:
		return []string{"config.toml", "auth.json"}
	case Claude:
		return []string{"settings.json"}
	case DSH:
		if m.dshVersion == "0.2.1-rc.3" {
			return []string{"profiles/desktop/cordis.patch.yml", ".credentials.yaml"}
		}
		return []string{"settings.yaml", ".credentials.yaml"}
	}
	return nil
}
func (m *Manager) safeParent(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return Unsafe
	}
	for p := dir; p != "/"; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil {
			return Unsafe
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || st.Uid != 0 && st.Uid != m.owner || info.Mode().Perm()&0022 != 0 {
			return Unsafe
		}
	}
	return nil
}
func (m *Manager) native(cli CLI) ([]nativeFile, error) {
	if cli == DSH && m.usageOnlyDSH {
		return nil, Unsupported
	}
	dir := m.targetDir(cli)
	names := m.targetNames(cli)
	if dir == "" || len(names) == 0 {
		return nil, Unsupported
	}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		if cli != Claude && cli != Codex {
			return nil, Unsupported
		}
		if err := m.safeParent(filepath.Dir(dir)); err != nil {
			return nil, err
		}
		files := make([]nativeFile, 0, len(names))
		for _, name := range names {
			files = append(files, nativeFile{Name: name})
		}
		return files, nil
	}
	if err := m.safeParent(dir); err != nil {
		return nil, err
	}
	files := make([]nativeFile, 0, len(names))
	for _, name := range names {
		if e := m.safeParent(filepath.Dir(filepath.Join(dir, name))); e != nil {
			return nil, e
		}
		f := nativeFile{Name: name}
		fd, err := syscall.Open(filepath.Join(dir, name), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if errors.Is(err, os.ErrNotExist) {
			files = append(files, f)
			continue
		}
		if err != nil {
			return nil, Unsafe
		}
		file := os.NewFile(uintptr(fd), "cli-configuration")
		info, e := file.Stat()
		if e != nil {
			_ = file.Close()
			return nil, Unsafe
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || st.Uid != m.owner || st.Nlink != 1 || info.Size() > 2<<20 || info.Mode().Perm()&0022 != 0 {
			_ = file.Close()
			return nil, Unsafe
		}
		b, e := io.ReadAll(io.LimitReader(file, (2<<20)+1))
		_ = file.Close()
		if e != nil || len(b) > 2<<20 {
			return nil, Unsafe
		}
		f.Exists = true
		f.Identity = fmt.Sprintf("%d:%d:%o", st.Dev, st.Ino, info.Mode().Perm())
		f.Data = b
		files = append(files, f)
	}
	return files, nil
}
func sameFiles(a, b []nativeFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Exists != b[i].Exists || !bytes.Equal(a[i].Data, b[i].Data) {
			return false
		}
	}
	return true
}
func (m *Manager) replaceNative(cli CLI, f nativeFile, expectedExists bool) error {
	if cli == DSH && m.usageOnlyDSH {
		return Unsupported
	}
	dir := m.targetDir(cli)
	if err := m.safeParent(dir); err != nil {
		return err
	}
	allowed := false
	for _, name := range m.targetNames(cli) {
		if f.Name == name {
			allowed = true
		}
	}
	if !allowed {
		return Unsafe
	}
	path := filepath.Join(dir, f.Name)
	dir = filepath.Dir(path)
	if err := m.safeParent(dir); err != nil {
		return err
	}
	if !f.Exists {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Unsafe
		}
		return syncDirectory(dir)
	}
	tmp, err := os.CreateTemp(dir, ".pax-profile-")
	if err != nil {
		return Unsafe
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err = io.Copy(tmp, bytes.NewReader(f.Data)); err != nil {
		_ = tmp.Close()
		return Unsafe
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return Uncertain
	}
	if err = tmp.Close(); err != nil {
		return Unsafe
	}
	_, statErr := os.Lstat(path)
	createOnly := !expectedExists
	if createOnly || errors.Is(statErr, os.ErrNotExist) {
		if err = os.Link(tmpName, path); err != nil {
			return Changed
		}
		if err = os.Remove(tmpName); err != nil {
			return Uncertain
		}
	} else if err = os.Rename(tmpName, path); err != nil {
		return Unsafe
	}
	return syncDirectory(dir)
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return Uncertain
	}
	defer func() { _ = f.Close() }()
	if f.Sync() != nil {
		return Uncertain
	}
	return nil
}

func sameObservation(a, b []nativeFile) bool {
	if !sameFiles(a, b) {
		return false
	}
	for i := range a {
		if a[i].Identity != b[i].Identity {
			return false
		}
	}
	return true
}

func (m *Manager) validNativeSet(cli CLI, files []nativeFile) bool {
	names := m.targetNames(cli)
	if len(files) != len(names) {
		return false
	}
	for i, f := range files {
		if f.Name != names[i] || len(f.Data) > 2<<20 || !f.Exists && len(f.Data) > 0 {
			return false
		}
	}
	return true
}
