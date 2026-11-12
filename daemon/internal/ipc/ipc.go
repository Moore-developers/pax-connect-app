// Package ipc owns Unix socket paths, peer admission and root-protected registration.
// It does not interpret gRPC requests or control system networking.
package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// ProductionSocket is the only privileged product socket.
const ProductionSocket = "/var/run/pax-connect/pax.sock"

// Listen admits only the current process UID in explicit development paths;
// the production socket uses root-protected single-user registration.
func Listen(path string) (net.Listener, error) { return listen(path, false) }

// ListenUser uses the same per-connection euid admission as development, with
// a private 0700 directory and 0600 socket. It never enters root registration.
func ListenUser(path string) (net.Listener, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || len([]byte(path)) >= 104 || path == ProductionSocket || path == "/private/var/run/pax-connect/pax.sock" {
		return nil, fmt.Errorf("invalid user socket")
	}
	return listen(path, true)
}

func listen(path string, userMode bool) (net.Listener, error) {
	path = filepath.Clean(path)
	if path == "/private/var/run/pax-connect/pax.sock" {
		path = ProductionSocket
	}
	production := path == ProductionSocket
	uid, gid := os.Geteuid(), os.Getegid()
	if production {
		if uid != 0 {
			return nil, fmt.Errorf("production socket requires root")
		}
		var err error
		gid, err = adminGID()
		if err != nil {
			return nil, err
		}
	}
	directory := filepath.Dir(path)
	if directory == "/tmp" || directory == "/private/tmp" || userMode && withinSharedTemp(path) {
		return nil, fmt.Errorf("use a private development directory")
	}
	if err := validateParents(directory); err != nil {
		return nil, err
	}
	mode := os.FileMode(0750)
	if userMode {
		mode = 0700
	}
	if err := os.MkdirAll(directory, mode); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("invalid socket directory")
	}
	owner := info.Sys().(*syscall.Stat_t)
	if int(owner.Uid) != uid {
		return nil, fmt.Errorf("socket directory owner mismatch")
	}
	if userMode && info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("user socket directory must be 0700")
	}
	if production {
		if err := os.Chown(directory, uid, gid); err != nil {
			return nil, err
		}
		if err := os.Chmod(directory, 0750); err != nil {
			return nil, err
		}
	} else if info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
		return nil, fmt.Errorf("development directory writable by others")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 || int(info.Sys().(*syscall.Stat_t).Uid) != uid {
			return nil, fmt.Errorf("refuse non-socket or foreign socket")
		}
		connection, err := net.DialTimeout("unix", path, 200*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil, fmt.Errorf("daemon already listening")
		}
		if !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("cannot confirm stale socket: %w", err)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (net.Listener, error) { _ = listener.Close(); return nil, err }
	socketMode := os.FileMode(0660)
	if userMode {
		socketMode = 0600
	}
	if err := os.Chmod(path, socketMode); err != nil {
		return fail(err)
	}
	if production {
		if err := os.Chown(path, uid, gid); err != nil {
			return fail(err)
		}
	}
	policy := &admission{uid: uid, production: production}
	if production {
		policy.path = "/Library/Application Support/Pax Connect/authorized-user.json"
		if err := prepareRootState(); err != nil {
			return fail(err)
		}
		if err := policy.load(); err != nil {
			return fail(err)
		}
	}
	return &peerListener{Listener: listener, policy: policy}, nil
}

func validateParents(path string) error {
	// /var is Apple's standard alias to /private/var; no other link is accepted.
	canonical := path
	if path == "/var" || len(path) > 5 && path[:5] == "/var/" || path == "/tmp" || len(path) > 5 && path[:5] == "/tmp/" {
		canonical = "/private" + path
	}
	for current := canonical; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe parent %s", current)
		}
	}
	return nil
}
func adminGID() (int, error) {
	group, err := user.LookupGroup("admin")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(group.Gid)
}
