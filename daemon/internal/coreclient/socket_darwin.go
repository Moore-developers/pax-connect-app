package coreclient

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"syscall"
)

// The kernel-authenticated server UID is independent of socket-file ownership
// (core deliberately chowns its socket to the authorized user).
func dialRoot(ctx context.Context, _ string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", Socket)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, ErrRequired
		}
		return nil, ErrUnavailable
	}
	u, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, ErrIdentity
	}
	raw, err := u.SyscallConn()
	if err != nil {
		_ = c.Close()
		return nil, ErrIdentity
	}
	var authErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil || cred.Uid != 0 {
			authErr = ErrIdentity
		}
	})
	if err != nil || authErr != nil {
		_ = c.Close()
		return nil, ErrIdentity
	}
	return c, nil
}
