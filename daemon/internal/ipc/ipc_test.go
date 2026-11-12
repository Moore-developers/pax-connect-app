package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pax-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
func TestListenRejectsFileAndLiveSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "p.sock")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if l, err := Listen(path); err == nil {
		_ = l.Close()
		t.Fatal("regular file overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve" {
		t.Fatal("file changed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	first, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if second, err := Listen(path); err == nil {
		_ = second.Close()
		t.Fatal("active socket overwritten")
	}
}
func TestListenRejectsSymlinkAndReclaimsDeadSocket(t *testing.T) {
	dir := shortTempDir(t)
	path := filepath.Join(dir, "p.sock")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if l, err := Listen(path); err == nil {
		_ = l.Close()
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	old, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	old.SetUnlinkOnClose(false)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("socket mode %o", info.Mode().Perm())
	}
}

func TestRegistrationEligibility(t *testing.T) {
	for _, test := range []struct {
		uid, console int
		admin, want  bool
	}{
		{501, 501, true, true}, {0, 0, true, false}, {501, 502, true, false}, {501, 501, false, false},
	} {
		if eligible(test.uid, test.console, test.admin) != test.want {
			t.Fatalf("unexpected eligibility for %+v", test)
		}
	}
}
func TestDevelopmentPeerAdmission(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "p.sock")
	listener, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	result := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err == nil {
			_ = peer.Close()
		}
		result <- err
	}()
	connection, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	policy := &admission{uid: os.Geteuid()}
	if policy.allow(os.Geteuid() + 1) {
		t.Fatal("foreign uid admitted")
	}
}

func TestEquivalentProductionPathCannotUseDevelopmentPolicy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("non-root assertion")
	}
	for _, path := range []string{ProductionSocket, "/var/run/pax-connect/../pax-connect/pax.sock", "/private/var/run/pax-connect/pax.sock"} {
		if listener, err := Listen(path); err == nil {
			_ = listener.Close()
			t.Fatal("production alias admitted non-root")
		}
	}
}
