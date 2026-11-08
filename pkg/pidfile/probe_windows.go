//go:build windows

package pidfile

import "syscall"

// stillActive is the exit code Windows reports for a process that hasn't
// exited (STILL_ACTIVE).
const stillActive = 259

// processQueryLimitedInformation is PROCESS_QUERY_LIMITED_INFORMATION, the
// least access that allows GetExitCodeProcess.
const processQueryLimitedInformation = 0x1000

// processAlive reports whether a process with the given PID is running on
// Windows. Opening the process isn't enough: a process that has exited can
// still be opened while anything (a parent, a supervisor) holds a handle to
// it, which made a crashed server's stale pidfile look live and refused the
// restart. Its exit code says whether it is still running.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		// Can't tell: err on the side of "running" so two servers never
		// share one pidfile.
		return true
	}
	return code == stillActive
}
