//go:build darwin && cgo

package core

/*
#include <stdlib.h>
#include <string.h>
#include <libproc.h>
#include <sys/proc_info.h>
#include <sys/errno.h>

// scan_pid reports kernel-control sockets of one pid. Returns the number of
// utun_control sockets found (units written to units[], max cap), or -1 when
// the fd table itself cannot be read (EPERM/ESRCH), and sets *denied to the
// number of fds whose socket info could not be read.
static int scan_pid(int pid, unsigned int *units, int cap, int *denied, int *errn) {
	*denied = 0; *errn = 0;
	int bytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, NULL, 0);
	if (bytes <= 0) { *errn = errno; return -1; }
	struct proc_fdinfo *fds = malloc(bytes);
	if (!fds) return -1;
	bytes = proc_pidinfo(pid, PROC_PIDLISTFDS, 0, fds, bytes);
	if (bytes <= 0) { *errn = errno; free(fds); return -1; }
	int n = bytes / sizeof(struct proc_fdinfo), found = 0;
	for (int i = 0; i < n; i++) {
		if (fds[i].proc_fdtype != PROX_FDTYPE_SOCKET) continue;
		struct socket_fdinfo sfi;
		int r = proc_pidfdinfo(pid, fds[i].proc_fd, PROC_PIDFDSOCKETINFO, &sfi, sizeof(sfi));
		if (r != (int)sizeof(sfi)) { (*denied)++; continue; }
		if (sfi.psi.soi_kind != SOCKINFO_KERN_CTL) continue;
		if (strncmp(sfi.psi.soi_proto.pri_kern_ctl.kcsi_name, "com.apple.net.utun_control", MAX_KCTL_NAME) != 0) continue;
		if (found < cap) units[found] = sfi.psi.soi_proto.pri_kern_ctl.kcsi_unit;
		found++;
	}
	free(fds);
	return found;
}
static int list_pids(int *pids, int cap) { return proc_listallpids(pids, cap * sizeof(int)) ; }
static int pid_path(int pid, char *buf, int n) { return proc_pidpath(pid, buf, n); }
static int pid_ppid_uid(int pid, int *ppid, unsigned int *uid) {
	struct proc_bsdinfo bi;
	if (proc_pidinfo(pid, PROC_PIDTBSDINFO, 0, &bi, sizeof(bi)) != (int)sizeof(bi)) return -1;
	*ppid = bi.pbi_ppid; *uid = bi.pbi_uid; return 0;
}
*/
import "C"

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

func scanUTUN(ctx context.Context, unit int) ([]process, error) {
	count := int(C.list_pids(nil, 0))
	if count <= 0 {
		return nil, errors.New("core_process_scan_failed")
	}
	pids := make([]C.int, count+1024)
	n := int(C.list_pids(&pids[0], C.int(len(pids))))
	if n <= 0 || n > len(pids) {
		return nil, errors.New("core_process_scan_failed")
	}
	var result []process
	for _, pid := range pids[:n] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pid <= 0 {
			continue
		}
		units := make([]C.uint, 256)
		var denied, errn C.int
		found := int(C.scan_pid(pid, &units[0], C.int(len(units)), &denied, &errn))
		if found < 0 {
			continue
		}
		if found > len(units) {
			found = len(units)
		}
		for _, u := range units[:found] {
			if int(u) == unit {
				result = append(result, readProcess(int(pid)))
				break
			}
		}
	}
	return result, nil
}
func readProcess(pid int) process {
	var parent C.int
	var uid C.uint
	p := process{PID: pid}
	if C.pid_ppid_uid(C.int(pid), &parent, &uid) == 0 {
		p.Parent = int(parent)
	}
	buf := make([]byte, 4096)
	n := int(C.pid_path(C.int(pid), (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf))))
	if n > 0 && n <= len(buf) {
		p.Path = string(buf[:n])
	}
	return p
}
func scutilText(ctx context.Context, input string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/scutil")
	cmd.Stdin = strings.NewReader(input + "quit\n")
	out, err := cmd.Output()
	return string(out), err
}
func readApp(ctx context.Context, path string) (string, string) {
	plist := filepath.Join(path, "Contents", "Info.plist")
	read := func(key string) string {
		s, e := runCommand(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :"+key, plist)
		if e != nil {
			return ""
		}
		return strings.TrimSpace(s)
	}
	name := read("CFBundleDisplayName")
	if name == "" {
		name = read("CFBundleName")
	}
	return name, read("CFBundleIdentifier")
}
