//go:build windows

package pidfile

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// TestProcessAlive_ExitedProcessWithAnOpenHandle: a process that exited is
// not "running", even while someone still holds a handle to it (the case
// that refused to restart a crashed server).
func TestProcessAlive_ExitedProcessWithAnOpenHandle(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 2 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	h, err := syscall.OpenProcess(processQueryLimitedInformation|syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	if !processAlive(pid) {
		t.Fatal("a running process was reported dead")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if processAlive(pid) {
		t.Error("an exited process (handle still open) was reported running")
	}
	if !processAlive(os.Getpid()) {
		t.Error("this process was reported dead")
	}
}
