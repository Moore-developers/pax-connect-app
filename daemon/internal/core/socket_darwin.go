package core

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"syscall"
	"time"
)

func rootOwned(i os.FileInfo) bool { s, ok := i.Sys().(*syscall.Stat_t); return ok && s.Uid == 0 }
func listenSocket(o Options) (net.Listener, error) {
	return bindSocket(o, func(path string) error { return os.Chown(path, o.OwnerUID, -1) })
}
func bindSocket(o Options, chown func(string) error) (net.Listener, error) {
	if info, err := os.Lstat(o.Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("core_socket_not_socket")
		}
		c, e := net.DialTimeout("unix", o.Socket, 200*time.Millisecond)
		if e == nil {
			_ = c.Close()
			return nil, errors.New("core_already_running")
		}
		if !errors.Is(e, syscall.ECONNREFUSED) {
			return nil, errors.New("core_socket_not_stale")
		}
		if err = os.Remove(o.Socket); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: o.Socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(o.Socket, 0600); err == nil {
		err = chown(o.Socket)
	}
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	return &ownerListener{Listener: l, uid: o.OwnerUID}, nil
}

type ownerListener struct {
	net.Listener
	uid int
}

func (l *ownerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(c)
		if err == nil && uid == l.uid {
			return c, nil
		}
		_ = c.Close()
	}
}
func peerUID(c net.Conn) (int, error) {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("core_peer_not_unix")
	}
	raw, err := u.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid int
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		credErr = e
		if e == nil {
			uid = int(cred.Uid)
		}
	})
	if err != nil {
		return 0, err
	}
	return uid, credErr
}
