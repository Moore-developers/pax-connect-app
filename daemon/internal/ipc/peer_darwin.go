package ipc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"pax-connect/daemon/internal/storage"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type peerListener struct {
	net.Listener
	policy *admission
}

func (l *peerListener) Accept() (net.Conn, error) {
	for {
		connection, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(connection)
		if err == nil && l.policy.allow(uid) {
			return connection, nil
		}
		_ = connection.Close()
	}
}
func peerUID(connection net.Conn) (int, error) {
	socket, ok := connection.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("not unix connection")
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid int
	var result error
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		result = err
		if err == nil {
			uid = int(cred.Uid)
		}
	})
	if err != nil {
		return 0, err
	}
	return uid, result
}

type admission struct {
	mu         sync.Mutex
	uid        int
	production bool
	registered bool
	path       string
}

func (a *admission) load() error {
	info, err := os.Lstat(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !safeRootFile(info) {
		return fmt.Errorf("unsafe registration file")
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		return err
	}
	var record struct {
		UID int `json:"uid"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	if record.UID <= 0 {
		return fmt.Errorf("invalid registered uid")
	}
	a.uid = record.UID
	a.registered = true
	return nil
}
func (a *admission) allow(uid int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.production {
		return uid == a.uid
	}
	if a.registered {
		return uid != 0 && uid == a.uid
	}
	console, err := os.Stat("/dev/console")
	if err != nil {
		return false
	}
	consoleUID := int(console.Sys().(*syscall.Stat_t).Uid)
	if !eligible(uid, consoleUID, isAdmin(uid)) {
		return false
	}
	if err := atomicRootJSON(a.path, map[string]int{"uid": uid}); err != nil {
		return false
	}
	a.uid = uid
	a.registered = true
	return true
}
func eligible(uid, console int, admin bool) bool { return uid > 0 && uid == console && admin }
func isAdmin(uid int) bool {
	account, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		return false
	}
	groups, err := account.GroupIds()
	if err != nil {
		return false
	}
	gid, err := adminGID()
	if err != nil {
		return false
	}
	for _, group := range groups {
		if group == strconv.Itoa(gid) {
			return true
		}
	}
	return false
}
func safeRootFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && info.Sys().(*syscall.Stat_t).Uid == 0 && info.Sys().(*syscall.Stat_t).Gid == 0
}
func prepareRootState() error {
	return storage.PreparePrivateDirectory("/Library/Application Support/Pax Connect", 0)
}

func atomicRootJSON(path string, value any) error {
	if info, err := os.Lstat(path); err == nil {
		if !safeRootFile(info) {
			return fmt.Errorf("unsafe state file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if err := file.Chown(0, 0); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// BeginRun records previous normal/abnormal termination without touching product state.
func BeginRun() (string, func() error, error) {
	if err := prepareRootState(); err != nil {
		return "", nil, err
	}
	path := "/Library/Application Support/Pax Connect/daemon-run.json"
	previous := "unknown"
	if info, err := os.Lstat(path); err == nil {
		if !safeRootFile(info) {
			return "", nil, fmt.Errorf("unsafe exit record")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		var record struct {
			Clean bool `json:"clean"`
		}
		if err := json.Unmarshal(data, &record); err != nil {
			return "", nil, err
		}
		previous = "abnormal"
		if record.Clean {
			previous = "normal"
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	if err := atomicRootJSON(path, map[string]bool{"clean": false}); err != nil {
		return "", nil, err
	}
	return previous, func() error { return atomicRootJSON(path, map[string]bool{"clean": true}) }, nil
}
