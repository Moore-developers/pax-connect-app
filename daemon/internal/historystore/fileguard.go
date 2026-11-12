package historystore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"syscall"
	"time"
)

// sidecars are the SQLite companion files that must follow the main file.
var sidecars = []string{"-wal", "-shm"}

// guardExisting enforces the private-file contract on path before SQLite opens
// it (SQLite itself follows links): companions are never symlinks, and the main
// file is a regular file owned by owner with mode 0600. It reports whether the
// main file exists.
func guardExisting(path string, owner uint32) (bool, error) {
	for _, suffix := range append([]string{"-journal"}, sidecars...) {
		if info, err := os.Lstat(path + suffix); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("%w: companion is a symlink", ErrUnsafeFile)
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: stat: %v", ErrUnsafeFile, err)
	}
	return true, checkInfo(info, owner)
}

func checkInfo(info os.FileInfo, owner uint32) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != owner {
		return fmt.Errorf("%w: type, mode or owner", ErrUnsafeFile)
	}
	return nil
}

// guardFile is guardExisting for writers: a missing main file is created
// exclusively with O_NOFOLLOW and mode 0600 so SQLite never creates it.
func guardFile(path string, owner uint32) error {
	exists, err := guardExisting(path, owner)
	if err != nil || exists {
		return err
	}
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return fmt.Errorf("%w: create: %v", ErrUnsafeFile, err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	if err = file.Chmod(0600); err != nil {
		return fmt.Errorf("%w: chmod: %v", ErrUnsafeFile, err)
	}
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat: %v", ErrUnsafeFile, err)
	}
	return checkInfo(info, owner)
}

// quarantine renames path and its companions to <name>.<tag>-<UTC timestamp>
// and returns the new main-file name. Companions that do not exist are skipped.
func quarantine(path, tag string) (string, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	target := fmt.Sprintf("%s.%s-%s", path, tag, stamp)
	for n := 1; ; n++ {
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		target = fmt.Sprintf("%s.%s-%s-%d", path, tag, stamp, n)
	}
	for _, suffix := range sidecars {
		if err := os.Rename(path+suffix, target+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("historystore: quarantine %s: %w", filepath.Base(path), err)
		}
	}
	if err := os.Rename(path, target); err != nil {
		return "", fmt.Errorf("historystore: quarantine %s: %w", filepath.Base(path), err)
	}
	return target, nil
}

// keepQuarantined is how many quarantined files are retained per store kind.
const keepQuarantined = 3

var quarantineName = regexp.MustCompile(`\.(?:corrupt|schema)-(\d{8}T\d{6}Z)(?:-(\d+))?$`)

// pruneQuarantine keeps the keepQuarantined newest quarantined main files in dir whose
// base name matches kind (a regexp for the part before the quarantine suffix) and
// deletes older ones with their -wal/-shm companions. It is best-effort: a failure
// leaves old files behind and must not block opening the store.
func pruneQuarantine(dir string, kind *regexp.Regexp) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name, stamp string
		n           int
	}
	var found []item
	for _, e := range entries {
		m := quarantineName.FindStringSubmatchIndex(e.Name())
		if m == nil || !kind.MatchString(e.Name()[:m[0]]) {
			continue
		}
		n := 0
		if m[4] >= 0 {
			n, _ = strconv.Atoi(e.Name()[m[4]:m[5]])
		}
		found = append(found, item{e.Name(), e.Name()[m[2]:m[3]], n})
	}
	sort.Slice(found, func(a, b int) bool {
		if found[a].stamp != found[b].stamp {
			return found[a].stamp > found[b].stamp
		}
		return found[a].n > found[b].n
	})
	for i := keepQuarantined; i < len(found); i++ {
		for _, suffix := range append([]string{""}, sidecars...) {
			_ = os.Remove(filepath.Join(dir, found[i].name+suffix))
		}
	}
}
