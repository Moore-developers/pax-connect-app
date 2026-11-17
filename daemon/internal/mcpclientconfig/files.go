package mcpclientconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"pax-connect/daemon/internal/storage"
)

const fileLimit = 2 << 20

type observation struct {
	path   string
	data   []byte
	exists bool
	info   os.FileInfo
}

func safeParent(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errInvalid
	}
	for dir := filepath.Dir(path); dir != "/"; dir = filepath.Dir(dir) {
		i, e := os.Lstat(dir)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
			return errInvalid
		}
		s, ok := i.Sys().(*syscall.Stat_t)
		if !ok || s.Uid != 0 && s.Uid != uint32(os.Geteuid()) || i.Mode().Perm()&0022 != 0 {
			return errInvalid
		}
	}
	return nil
}
func readFile(path string) (observation, error) {
	f := observation{path: path}
	if e := safeParent(path); e != nil {
		return f, e
	}
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(e, os.ErrNotExist) {
		return f, nil
	}
	if e != nil {
		return f, errInvalid
	}
	file := os.NewFile(uintptr(fd), "mcp-client-config")
	defer func() { _ = file.Close() }()
	i, e := file.Stat()
	if e != nil {
		return f, errInvalid
	}
	s, ok := i.Sys().(*syscall.Stat_t)
	if !ok || !i.Mode().IsRegular() || s.Uid != uint32(os.Geteuid()) || s.Nlink != 1 || i.Mode().Perm()&0022 != 0 || i.Size() > fileLimit {
		return f, errInvalid
	}
	f.data, e = io.ReadAll(io.LimitReader(file, fileLimit+1))
	if e != nil || len(f.data) > fileLimit {
		return f, errInvalid
	}
	f.exists = true
	f.info = i
	return f, nil
}
func sameFile(a, b observation) bool {
	return a.exists == b.exists && bytes.Equal(a.data, b.data) && (!a.exists || os.SameFile(a.info, b.info) && a.info.Mode() == b.info.Mode())
}

func replaceFile(ctx context.Context, home string, before observation, data []byte) error {
	if len(data) > fileLimit {
		return errInvalid
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	current, e := readFile(before.path)
	if e != nil || !sameFile(before, current) {
		return errors.New("configuration_changed")
	}
	// Client parents may be 0755; never change their permissions to suit PrivateFile.
	if e = os.MkdirAll(filepath.Dir(before.path), 0700); e != nil {
		return errInvalid
	}
	if e = safeParent(before.path); e != nil {
		return e
	}
	if before.exists {
		dir := filepath.Join(home, "Library/Application Support/Pax Connect/mcp-backups")
		if e = os.MkdirAll(filepath.Dir(dir), 0700); e != nil {
			return errInvalid
		}
		if e = storage.PreparePrivateDirectory(dir, uint32(os.Geteuid())); e != nil {
			return e
		}
		hash := sha256.Sum256([]byte(before.path))
		name := time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(hash[:8]) + ".backup"
		if e = storage.NewPrivateFile(filepath.Join(dir, name), uint32(os.Geteuid()), fileLimit).Write(ctx, before.data); e != nil {
			return errors.New("configuration_backup_failed")
		}
	}
	f, e := os.CreateTemp(filepath.Dir(before.path), ".pax-mcp-config-")
	if e != nil {
		return errInvalid
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if before.exists {
		e = f.Chmod(before.info.Mode().Perm())
	}
	if e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	current, e = readFile(before.path)
	if e != nil || !sameFile(before, current) {
		return errors.New("configuration_changed")
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	if !before.exists {
		if e = os.Link(f.Name(), before.path); e != nil {
			return errors.New("configuration_changed")
		}
	} else if e = os.Rename(f.Name(), before.path); e != nil {
		return errInvalid
	}
	dir, e := os.Open(filepath.Dir(before.path))
	if e != nil {
		return storage.ErrCommitUncertain
	}
	defer func() { _ = dir.Close() }()
	if dir.Sync() != nil {
		return storage.ErrCommitUncertain
	}
	return nil
}
