package mcpinstall

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"pax-connect/daemon/internal/storage"
)

// Directory returns the stable private entry directory, independent of PATH.
func Directory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("app_unavailable")
	}
	return filepath.Join(home, "Library/Application Support/Pax Connect/bin"), nil
}

func record(dir string) *storage.PrivateFile {
	return storage.NewPrivateFile(filepath.Join(dir, "mcp-install.json"), uint32(os.Geteuid()), 8192)
}

// Resolve treats the registration only as a hint, then verifies the signed App.
func Resolve(ctx context.Context) (Bundle, error) {
	dir, err := Directory()
	if err != nil {
		return Bundle{}, err
	}
	data, exists, err := record(dir).Read()
	var registered Bundle
	if err != nil || !exists || json.Unmarshal(data, &registered) != nil {
		return Bundle{}, errors.New("app_unavailable")
	}
	bundle, err := Verify(ctx, registered.Path)
	if err != nil {
		return Bundle{}, err
	}
	if registered != bundle {
		return Bundle{}, errors.New("app_mismatch")
	}
	return bundle, nil
}

// Install atomically replaces the launcher and registration with a complete build.
// Refresh only updates an existing installation; first launch never installs it.
func Install(ctx context.Context, path string, refresh bool) (string, error) {
	dir, err := Directory()
	if err != nil {
		return "", err
	}
	if refresh {
		// Missing directory is the normal not-yet-installed state. Invalid existing
		// registration is reported instead of silently treating corruption as absence.
		if _, e := os.Lstat(dir); errors.Is(e, os.ErrNotExist) {
			return "", nil
		}
		_, exists, e := record(dir).Read()
		if e != nil {
			return "", e
		}
		if !exists {
			return "", nil
		}
	}
	bundle, err := Verify(ctx, path)
	if err != nil {
		return "", err
	}
	owner := uint32(os.Geteuid())
	// The product root normally exists, but explicit installation also works
	// before the daemon has started. Library/Application Support already exists.
	if err = storage.PreparePrivateDirectory(filepath.Dir(dir), owner); err != nil {
		return "", err
	}
	if err = storage.PreparePrivateDirectory(dir, owner); err != nil {
		return "", err
	}
	source := filepath.Join(bundle.Path, "Contents/MacOS/pax-mcp-launcher")
	data, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "pax-mcp")
	if info, e := os.Lstat(target); e == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || stat.Uid != owner {
			return "", errors.New("installation_invalid")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	file, err := os.CreateTemp(dir, ".pax-mcp-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err = file.Write(data); err == nil {
		err = file.Chmod(0700)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), target); err != nil {
		return "", err
	}
	registration, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	if err = record(dir).Write(ctx, registration); err != nil {
		return "", err
	}
	return target, nil
}

// CheckLauncher rejects replaced or mismatched installed copies before exec.
func (b Bundle) CheckLauncher(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("installation_invalid")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0700 || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("installation_invalid")
	}
	actual, err := fileHash(path)
	expected, e := fileHash(filepath.Join(b.Path, "Contents/MacOS/pax-mcp-launcher"))
	if err != nil || e != nil || actual != expected {
		return errors.New("app_mismatch")
	}
	return nil
}
