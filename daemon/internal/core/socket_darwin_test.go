package core

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"pax-connect/daemon/internal/singbox"
	"testing"
	"time"
)

func TestRootRequired(t *testing.T) {
	r := &fakeRuntime{}
	h := nativeHost()
	h.euid = func() int { return 501 }
	if err := run(context.Background(), Options{}, r, singbox.ValidateCoreOptions, h); err == nil || err.Error() != "core_root_required" {
		t.Fatal(err)
	}
}
func TestSocketProtection(t *testing.T) {
	o := Options{Socket: filepath.Join(t.TempDir(), "core.sock"), OwnerUID: os.Getuid()}
	if err := os.WriteFile(o.Socket, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := bindSocket(o, func(string) error { return nil }); err == nil {
		t.Fatal("removed regular file")
	}
	data, err := os.ReadFile(o.Socket)
	if err != nil || string(data) != "keep" {
		t.Fatal("file changed")
	}
	if err = os.Remove(o.Socket); err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: o.Socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err = stale.Close(); err != nil {
		t.Fatal(err)
	}
	l, err := bindSocket(o, func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	info, err := os.Stat(o.Socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket mode")
	}
	if _, err = bindSocket(o, func(string) error { return nil }); err == nil {
		t.Fatal("unlinked live socket")
	}
	received := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e == nil {
			uid, e2 := peerUID(c)
			if e2 != nil || uid != os.Getuid() {
				received <- os.ErrPermission
				_ = c.Close()
				return
			}
			e = c.Close()
		}
		received <- e
	}()
	c, err := net.DialTimeout("unix", o.Socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	select {
	case err = <-received:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("peer not admitted")
	}
}
