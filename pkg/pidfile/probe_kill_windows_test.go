//go:build windows

package pidfile

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestProcessAlive_KilledProcessWithAnOpenHandle: like a crash (or kill -9),
// TerminateProcess while another handle stays open.
func TestProcessAlive_KilledProcessWithAnOpenHandle(t *testing.T) {
	cmd := exec.Command("cmd", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	h, err := syscall.OpenProcess(processQueryLimitedInformation|syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = syscall.WaitForSingleObject(h, 5000)
	time.Sleep(50 * time.Millisecond)
	if processAlive(pid) {
		var code uint32
		_ = syscall.GetExitCodeProcess(h, &code)
		t.Errorf("killed process reported running (exit code %d)", code)
	}
}
