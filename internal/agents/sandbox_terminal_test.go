package agents

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/sandbox"
)

// TestRunTerminal_SandboxedBackgroundProcessDies: a command that leaves a
// process running in the background (which a process-tree kill can't follow
// once the command has exited) no longer leaves it behind when the command
// runs in the sandbox. Without the sandbox it survives; that is the control.
func TestRunTerminal_SandboxedBackgroundProcessDies(t *testing.T) {
	box, info, err := sandbox.New(sandbox.Config{Mode: sandbox.ModeAuto, Limits: sandbox.Limits{MaxProcesses: 64}})
	if err != nil || box == nil {
		t.Skipf("no sandbox here: %v %s", err, info.Reason)
	}
	_, alive, cleanup := longRunningChild(t)
	if alive() {
		t.Skip("a matching process is already running; the result could not be attributed")
	}
	t.Cleanup(cleanup)
	command := "sleep 300 > /dev/null 2>&1 &"
	if runtime.GOOS == "windows" {
		command = "start /b ping -n 60 127.0.0.1 > NUL"
	}
	ws := t.TempDir()

	run := func(cfg LocalFSConfig) (map[string]any, error) {
		res, err := RunTerminalTool{cfg: cfg, approved: true}.Execute(context.Background(), map[string]any{"command": command, "timeout": float64(20)})
		m, _ := res.(map[string]any)
		return m, err
	}

	// Control: unsandboxed, the background process outlives the command (and
	// may hold its output open until WaitDelay; that error is the old
	// behaviour, not what this test checks).
	_, _ = run(LocalFSConfig{Root: ws})
	time.Sleep(500 * time.Millisecond)
	if !alive() {
		t.Skip("the background process didn't outlive the command even without a sandbox; nothing to compare")
	}
	cleanup()
	waitGone(t, alive)

	start := time.Now()
	res, err := run(LocalFSConfig{Root: ws, Sandbox: box, SandboxConfig: sandbox.Config{Limits: sandbox.Limits{MaxProcesses: 64}}})
	if err != nil {
		t.Fatalf("sandboxed Execute: %v", err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the sandboxed command took %s: something it left behind held it open", took)
	}
	if res["sandbox"] != info.Adapter {
		t.Errorf("sandboxed run labelled %v, want %s", res["sandbox"], info.Adapter)
	}
	waitGone(t, alive)
}
