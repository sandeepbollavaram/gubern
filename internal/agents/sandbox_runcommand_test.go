package agents

import (
	"context"
	"os/exec"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/sandbox"
)

// run_command (background sub-agents) runs in the same sandbox as
// run_terminal: its folder is the only writable one, and it gets the
// network its run was granted, not the server default.
func TestRunCommand_UsesTheSandbox(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	dir := t.TempDir()
	rec := &recordingSandbox{}
	tool := RunCommandTool{SandboxDir: dir, AllowedCommands: []string{"go"}, Sandbox: rec,
		SandboxConfig: sandbox.Config{Network: sandbox.NetworkOff}, approved: true}
	res, err := tool.Execute(agentevents.WithNetwork(context.Background(), "on"), map[string]any{"command": "go", "args": []any{"version"}})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["exit_code"] != 0 || m["sandbox"] != "recording" {
		t.Errorf("result = %v", m)
	}
	rec.mu.Lock()
	nets, dirs := append([]string(nil), rec.nets...), append([]string(nil), rec.dirs...)
	rec.mu.Unlock()
	if len(nets) != 1 || nets[0] != "on" || dirs[0] != dir {
		t.Errorf("sandbox saw network %v, writable %v", nets, dirs)
	}

	// Without a granted network it gets the configured default.
	if _, err := tool.Execute(context.Background(), map[string]any{"command": "go", "args": []any{"version"}}); err != nil {
		t.Fatal(err)
	}
	if got := rec.got(); got[len(got)-1] != "off" {
		t.Errorf("default network = %v", got)
	}
}

// With the platform's real adapter the command still runs and says where.
func TestRunCommand_RealSandbox(t *testing.T) {
	box, info, err := sandbox.New(sandbox.Config{Mode: sandbox.ModeAuto, Limits: sandbox.Limits{MaxProcesses: 64}})
	if err != nil || box == nil {
		t.Skipf("no sandbox here: %v %s", err, info.Reason)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	tool := RunCommandTool{SandboxDir: t.TempDir(), AllowedCommands: []string{"go"}, Sandbox: box,
		SandboxConfig: sandbox.Config{Network: sandbox.NetworkOn, Limits: sandbox.Limits{MaxProcesses: 64}}, approved: true}
	res, err := tool.Execute(context.Background(), map[string]any{"command": "go", "args": []any{"version"}})
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]any); m["exit_code"] != 0 || m["sandbox"] != info.Adapter {
		t.Errorf("result = %v", m)
	}
}
