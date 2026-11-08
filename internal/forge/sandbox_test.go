package forge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/sandbox"
)

type fakeBox struct{ level sandbox.Level }

func (f fakeBox) Info() sandbox.Info {
	return sandbox.Info{Adapter: "fake-" + string(f.level), Level: f.level, Platform: "Testland"}
}
func (fakeBox) Prepare(*exec.Cmd, sandbox.Policy) (sandbox.Process, error) { return nil, nil }

func TestSandboxPlan(t *testing.T) {
	home := t.TempDir()
	cfg := sandbox.Config{Limits: sandbox.Limits{MaxProcesses: 64, Home: home}}
	if box, _, _, label := (sandboxSet{}).plan("go", "/w"); box != nil || label != "none" {
		t.Errorf("no box: %v %q", box, label)
	}
	// Contained: every build, network on, the build folder writable.
	box, p, env, label := sandboxSet{box: fakeBox{sandbox.LevelContained}, cfg: cfg}.plan("flutter", "/w")
	if box == nil || label != "fake-contained" || p.Writable != "/w" || p.Network != sandbox.NetworkOn || len(env) != 0 {
		t.Errorf("contained flutter: %v %+v %v %q", box, p, env, label)
	}
	// Isolated: go builds get their caches in the private home...
	box, p, env, label = sandboxSet{box: fakeBox{sandbox.LevelIsolated}, cfg: cfg}.plan("go", "/w")
	if box == nil || label != "fake-isolated" || !strings.Contains(strings.Join(env, " "), "GOMODCACHE="+filepath.Join(home, "go", "pkg", "mod")) {
		t.Errorf("isolated go: %v %+v %v %q", box, p, env, label)
	}
	// ...flutter builds can't be isolated, and the label says why.
	box, _, _, label = sandboxSet{box: fakeBox{sandbox.LevelIsolated}, cfg: cfg}.plan("flutter", "/w")
	if box != nil || !strings.HasPrefix(label, "none (flutter writes into its own SDK") {
		t.Errorf("isolated flutter: %v %q", box, label)
	}
}

// A real go build under this platform's sandbox succeeds and says where it ran.
func TestBuild_GoInTheRealSandbox(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	box, info, err := sandbox.New(sandbox.Config{Mode: sandbox.ModeAuto, Limits: sandbox.Limits{MaxProcesses: 256}})
	if err != nil || box == nil {
		t.Skipf("no sandbox here: %v %s", err, info.Reason)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":  "module example.com/hello\n\ngo 1.21\n",
		"main.go": "package main\n\nfunc main() { println(\"hi\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := NewBuildAgent(BuildConfig{SandboxDir: dir, Stack: StackChoice{Backend: "go"},
		box: sandboxSet{box: box, cfg: sandbox.Config{Limits: sandbox.Limits{MaxProcesses: 256, Home: t.TempDir()}}}})
	out, err := b.Build(context.Background())
	if err != nil || !out.Success || out.Sandbox != info.Adapter {
		t.Fatalf("build: %+v %v", out, err)
	}
}
