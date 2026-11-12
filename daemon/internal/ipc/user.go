package ipc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func withinSharedTemp(path string) bool {
	return path == "/tmp" || strings.HasPrefix(path, "/tmp/") || path == "/private/tmp" || strings.HasPrefix(path, "/private/tmp/")
}

// UserSocketPath is the single path rule used by paxd and by the App's
// --print-user-socket query. Fallback requires a real private per-user temp dir.
// Darwin's sockaddr_un reserves one of its 104 bytes for the terminating NUL.
func UserSocketPath(dataDirectory, temporary string) (string, error) {
	path := filepath.Join(dataDirectory, "run", "paxd.sock")
	if filepath.IsAbs(dataDirectory) && len([]byte(path)) < 104 {
		return path, nil
	}
	temporary = filepath.Clean(temporary)
	canonical, err := filepath.EvalSymlinks(temporary)
	if err != nil || !filepath.IsAbs(temporary) || withinSharedTemp(temporary) || withinSharedTemp(canonical) {
		return "", fmt.Errorf("private temp directory required")
	}
	info, err := os.Lstat(canonical)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return "", fmt.Errorf("private temp directory required")
	}
	path = filepath.Join(canonical, "paxd", "p.sock")
	if len([]byte(path)) >= 104 {
		return "", fmt.Errorf("user socket path too long")
	}
	return path, nil
}
