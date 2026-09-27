package harness

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

// ProcessIdentity matches GJC's processIncarnation, not just a reusable PID.
func ProcessIdentity(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err == unix.ESRCH {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", err
	}
	if info == nil || info.Proc.P_pid != int32(pid) {
		return "", os.ErrNotExist
	}
	return fmt.Sprintf("darwin:%d:%d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}
