package processidentity

import (
	"errors"
	"golang.org/x/sys/unix"
)

// Identity is a process identity resistant to PID reuse.
type Identity struct {
	PID     int
	UID     uint32
	Seconds int64
	Micros  int32
}

// Read rejects missing and zombie processes.
func Read(pid int) (Identity, error) {
	p, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || p == nil || p.Proc.P_pid != int32(pid) || p.Proc.P_stat == 5 {
		return Identity{}, errors.New("owner_unavailable")
	}
	return Identity{pid, p.Eproc.Ucred.Uid, p.Proc.P_starttime.Sec, p.Proc.P_starttime.Usec}, nil
}

// Alive requires all identity fields to match.
func Alive(want Identity) bool { got, err := Read(want.PID); return err == nil && got == want }
