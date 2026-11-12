package ipc

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestUserSocketResolutionAndModes(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// UserSocketPath intentionally rejects shared temporary directories. Keep
	// this fallback fixture under the user's private home, independent of
	// TMPDIR (which is /tmp on some hosts).
	parent, err := os.MkdirTemp(home, ".pax-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := UserSocketPath("/Users/fixture/Library/Application Support/Pax Connect", parent)
	if err != nil || !strings.HasSuffix(path, "/run/paxd.sock") {
		t.Fatalf("%s %v", path, err)
	}
	path, err = UserSocketPath("/Users/"+strings.Repeat("long", 40), parent)
	if err != nil || path != filepath.Join(parent, "paxd", "p.sock") {
		t.Fatalf("%s %v", path, err)
	}
	listener, err := ListenUser(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	for name, mode := range map[string]os.FileMode{filepath.Dir(path): 0700, path: 0600} {
		info, err := os.Lstat(name)
		if err != nil || info.Mode().Perm() != mode || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			t.Fatalf("mode/owner mismatch: %v", err)
		}
	}
	accepted := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if c != nil {
			_ = c.Close()
		}
		accepted <- err
	}()
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if err = <-accepted; err != nil {
		t.Fatal(err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("socket remains after close")
	}
	policy := admission{uid: os.Geteuid()}
	if !policy.allow(os.Geteuid()) || policy.allow(os.Geteuid()+1) {
		t.Fatal("euid admission broken")
	}
}

func TestUserSocketRefusesSharedAndUnsafePaths(t *testing.T) {
	for _, temp := range []string{"/tmp", "/private/tmp", "/tmp/child", "/private/tmp/child", "/missing"} {
		if _, err := UserSocketPath("/"+strings.Repeat("x", 110), temp); err == nil {
			t.Fatal("shared/invalid temp admitted")
		}
	}
	if _, err := ListenUser(ProductionSocket); err == nil {
		t.Fatal("root registration reachable")
	}
	if _, err := ListenUser("/tmp/private/p.sock"); err == nil {
		t.Fatal("shared temp admitted")
	}
}
