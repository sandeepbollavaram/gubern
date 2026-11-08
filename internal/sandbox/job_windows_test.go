//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var isProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

func platformHelper(verb, _ string) int {
	if verb == "injob" {
		var in int32
		r, _, err := isProcessInJob.Call(uintptr(windows.CurrentProcess()), 0, uintptr(unsafe.Pointer(&in)))
		if r == 0 {
			fmt.Println("IsProcessInJob:", err)
			return 1
		}
		fmt.Printf("injob=%v\n", in != 0)
		return 0
	}
	return 2
}

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}

func jobBox(t *testing.T, limits Limits) (Sandbox, Policy) {
	t.Helper()
	sb, info, err := New(Config{Mode: ModeRequired, Limits: limits})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if info.Adapter != "job-object" || info.Level != LevelContained || info.Label() != "Contained (Windows)" {
		t.Errorf("info = %+v", info)
	}
	ws := t.TempDir()
	return sb, Policy{Writable: ws, Dir: ws, Network: NetworkOn, Limits: limits}
}

// The command is in the job from its first instruction: it starts
// suspended and only runs once assigned.
func TestJob_CommandIsInTheJob(t *testing.T) {
	sb, p := jobBox(t, Limits{MaxProcesses: 16})
	out, code := runIn(t, sb, p, "injob", 30*time.Second)
	if code != 0 || !strings.Contains(out, "injob=true") {
		t.Errorf("exit %d, %q", code, out)
	}
}

// A fork bomb stops at the process cap.
func TestJob_ProcessCap(t *testing.T) {
	sb, p := jobBox(t, Limits{MaxProcesses: 8})
	out, _ := runIn(t, sb, p, "spawn:40", 60*time.Second)
	if n := startedCount(t, out); n >= 8 || n < 1 {
		t.Errorf("started %d children under a cap of 8 (the helper is the 8th)", n)
	}
}

// Without the job, the same helper starts all of them (the cap above is
// what stopped it).
func TestJob_ProcessCapControl(t *testing.T) {
	sb, p := jobBox(t, Limits{MaxProcesses: 64})
	out, _ := runIn(t, sb, p, "spawn:20", 60*time.Second)
	if n := startedCount(t, out); n != 20 {
		t.Errorf("started %d of 20 under a cap of 64", n)
	}
}

// A process the command leaves running in the background dies when the
// command's job is closed.
func TestJob_BackgroundChildDiesWithTheCommand(t *testing.T) {
	sb, p := jobBox(t, Limits{MaxProcesses: 16})
	pidFile := filepath.Join(p.Writable, "pid")
	out, code := runIn(t, sb, p, "background:"+pidFile, 30*time.Second)
	b, err := os.ReadFile(pidFile)
	if code != 0 || err != nil {
		t.Fatalf("exit %d %q %v", code, out, err)
	}
	pid, _ := strconv.Atoi(string(b))
	deadline := time.Now().Add(10 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("background child %d outlived its command", pid)
	}
}

// A timeout kills the whole job: the command and the child it started.
func TestJob_TimeoutKillsTree(t *testing.T) {
	sb, p := jobBox(t, Limits{MaxProcesses: 16})
	pidFile := filepath.Join(p.Writable, "pid")
	start := time.Now()
	_, code := runIn(t, sb, p, "hold:"+pidFile, 3*time.Second)
	if took := time.Since(start); took > 15*time.Second || code == 0 {
		t.Errorf("hold under a 3s timeout: exit %d after %s", code, took)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(string(b))
	if alive(pid) {
		t.Errorf("child %d outlived the timeout", pid)
	}
}

// The memory cap covers the whole tree.
func TestJob_MemoryCap(t *testing.T) {
	sb, p := jobBox(t, Limits{MaxProcesses: 16, MemoryMB: 64})
	out, code := runIn(t, sb, p, "alloc:512", 60*time.Second)
	if code == 0 || strings.Contains(out, "allocated 512") {
		t.Errorf("allocated 512 MB under a 64 MB cap: exit %d %q", code, out)
	}
	sb2, p2 := jobBox(t, Limits{MaxProcesses: 16, MemoryMB: 1024})
	if out, code := runIn(t, sb2, p2, "alloc:128", 60*time.Second); code != 0 {
		t.Errorf("128 MB under a 1024 MB cap failed: exit %d %q", code, out)
	}
}

// Windows doesn't fence the network or the file system; Info says so.
func TestJob_InfoIsHonest(t *testing.T) {
	_, info, err := New(Config{Mode: ModeAuto, Network: NetworkOff})
	if err != nil {
		t.Fatal(err)
	}
	if info.Network != "not enforced" {
		t.Errorf("network off reported as %q", info.Network)
	}
	joined := strings.Join(info.NotEnforced, "; ")
	if !strings.Contains(joined, "file system") || !strings.Contains(joined, "network") {
		t.Errorf("not_enforced = %q", joined)
	}
}
