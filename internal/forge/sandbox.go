package forge

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sandeepbollavaram/vortex/internal/sandbox"
)

// Forge builds in the sandbox (Phase 4). A build compiles generated code
// (Gradle scripts run during flutter build, cgo during go build), so it gets
// the same boundary as an agent's commands, with the network on: builds
// download their dependencies.
//
//   - Contained (Windows job): every build; the whole tree is killed with
//     it and processes and memory are capped.
//   - Isolated (Linux bubblewrap): go builds, with the Go toolchain mounted
//     read-only and the module and build caches in the sandbox's private
//     home. Flutter writes into its own SDK folder, which an isolated build
//     can't, so flutter builds stay unsandboxed there, and say so.

// sandboxSet is the box Forge builds use.
type sandboxSet struct {
	box sandbox.Sandbox
	cfg sandbox.Config
}

// SetSandbox makes Forge run its builds in box (nil: none).
func (f *Forge) SetSandbox(box sandbox.Sandbox, cfg sandbox.Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.box = sandboxSet{box: box, cfg: cfg}
}

// SetSandbox makes the managed Forge run its builds in box.
func (jm *JobManager) SetSandbox(box sandbox.Sandbox, cfg sandbox.Config) {
	jm.forge.SetSandbox(box, cfg)
}

func (f *Forge) sandbox() sandboxSet {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.box
}

// plan decides how command runs in dir: the box and policy to use (nil box:
// unsandboxed), extra environment, and the label recorded with the result.
func (s sandboxSet) plan(command, dir string) (sandbox.Sandbox, sandbox.Policy, []string, string) {
	if s.box == nil {
		return nil, sandbox.Policy{}, nil, "none"
	}
	info := s.box.Info()
	p := sandbox.Policy{Writable: dir, Dir: dir, Network: sandbox.NetworkOn, Limits: s.cfg.Limits}
	if info.Level != sandbox.LevelIsolated {
		return s.box, p, nil, info.Adapter
	}
	if command != "go" {
		return nil, sandbox.Policy{}, nil, "none (" + command + " writes into its own SDK; not isolated on " + info.Platform + ")"
	}
	// The toolchain is read-only; caches live in the private home.
	if goBin, err := exec.LookPath("go"); err == nil {
		if resolved, err := filepath.EvalSymlinks(goBin); err == nil {
			p.ReadPaths = append(p.ReadPaths, filepath.Dir(filepath.Dir(resolved)))
		}
	}
	var env []string
	if home := p.Home; home != "" {
		_ = os.MkdirAll(home, 0o700)
		env = []string{
			"GOPATH=" + filepath.Join(home, "go"),
			"GOMODCACHE=" + filepath.Join(home, "go", "pkg", "mod"),
			"GOCACHE=" + filepath.Join(home, ".cache", "go-build"),
		}
	}
	return s.box, p, env, info.Adapter
}
