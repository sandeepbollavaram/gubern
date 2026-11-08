//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// The Linux adapter runs the command under bubblewrap (bwrap), an
// unprivileged sandbox built on user, mount, PID and network namespaces.
//
// What the command sees:
//   - the system folders (/usr, /bin, /lib*, /sbin, /etc) read-only;
//   - the workspace read-write, at its real path, as the only writable place;
//   - a fresh /tmp, /proc and /dev; no home directory, so the server's state
//     and the user's keys and dotfiles don't exist for it;
//   - its own PID namespace (it can't see or signal the server), and with
//     network "off" its own network namespace with only a loopback device;
//   - a cap on processes (RLIMIT_NPROC, counted inside its user namespace),
//     applied with prlimit before the command runs.
//
// bwrap is the process the server starts; --die-with-parent and the
// process-group kill take the whole namespace down with it.

func platformName() string { return "Linux" }

var systemDirs = []string{"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/etc"}

type bwrap struct {
	path    string // bwrap
	prlimit string // prlimit, or "" when not installed
	cfg     Config
}

func newPlatform(cfg Config) (Sandbox, error) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("%w: bubblewrap (bwrap) is not installed", ErrUnavailable)
	}
	b := &bwrap{path: path, cfg: cfg}
	if p, err := exec.LookPath("prlimit"); err == nil {
		b.prlimit = p
	}
	// Namespaces can be switched off (or restricted by AppArmor); try one.
	if out, err := exec.Command(path, b.base(NetworkOff)...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%w: bubblewrap can't create a sandbox here: %v %s", ErrUnavailable, err, firstLine(out))
	}
	return b, nil
}

func (b *bwrap) Info() Info {
	in := Info{
		Adapter: "bubblewrap", Level: LevelIsolated, Platform: "Linux", Mode: b.cfg.Mode,
		Network: string(b.cfg.Network),
		Enforced: []string{
			"only the workspace is writable",
			"home folder and server state are not visible (commands get a private home)",
			"system folders are read-only",
			"separate process namespace; the tree dies with the command",
		},
		NotEnforced: []string{"memory cap (needs cgroups)", "system call filter (seccomp)"},
	}
	if b.cfg.Network == NetworkOff {
		in.Enforced = append(in.Enforced, "no network (loopback only)")
	}
	if b.prlimit != "" {
		in.Enforced = append(in.Enforced, "at most "+strconv.Itoa(b.cfg.MaxProcesses)+" processes")
	} else {
		in.NotEnforced = append(in.NotEnforced, "process cap (prlimit is not installed)")
	}
	return in
}

// base is the fixed part of the bwrap command line.
func (b *bwrap) base(net Network) []string {
	args := []string{"--die-with-parent", "--new-session", "--unshare-all", "--hostname", "gubern-sandbox"}
	if net == NetworkOn {
		args = append(args, "--share-net")
	}
	for _, d := range systemDirs {
		fi, err := os.Lstat(d)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if target, err := os.Readlink(d); err == nil {
				args = append(args, "--symlink", target, d)
			}
			continue
		}
		args = append(args, "--ro-bind", d, d)
	}
	return append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--setenv", "HOME", "/tmp", "--", "/bin/true")
}

func (b *bwrap) Prepare(cmd *exec.Cmd, p Policy) (Process, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	ws, err := realDir(p.Writable)
	if err != nil {
		return nil, fmt.Errorf("sandbox: workspace %q: %w", p.Writable, err)
	}
	dir := ws
	if p.Dir != "" {
		if dir, err = realDir(p.Dir); err != nil {
			return nil, fmt.Errorf("sandbox: working folder %q: %w", p.Dir, err)
		}
		if !within(ws, dir) {
			return nil, fmt.Errorf("sandbox: working folder %s is outside the workspace %s", dir, ws)
		}
	}
	args := b.base(p.Network)
	args = args[:len(args)-2] // drop "-- /bin/true"
	for _, r := range p.ReadPaths {
		if rd, err := realDir(r); err == nil && !within(ws, rd) {
			args = append(args, "--ro-bind", rd, rd)
		}
	}
	if p.Home != "" && !within(ws, p.Home) {
		if err := os.MkdirAll(p.Home, 0o700); err != nil {
			return nil, fmt.Errorf("sandbox: home folder: %w", err)
		}
		if home, err := realDir(p.Home); err == nil {
			args = append(args, "--bind", home, home, "--setenv", "HOME", home)
		}
	}
	args = append(args, "--bind", ws, ws, "--chdir", dir, "--")
	if b.prlimit != "" && p.MaxProcesses > 0 {
		n := strconv.Itoa(p.MaxProcesses)
		args = append(args, b.prlimit, "--nproc="+n+":"+n, "--")
	}
	path := cmd.Path
	if !filepath.IsAbs(path) { // exec evaluates a relative Path against Dir
		base := cmd.Dir
		if base == "" {
			base, _ = os.Getwd()
		}
		path = filepath.Join(base, path)
	}
	args = append(args, path)
	args = append(args, cmd.Args[1:]...)
	cmd.Path = b.path
	cmd.Args = append([]string{"bwrap"}, args...)
	cmd.Dir = dir
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	return &groupProcess{}, nil
}

// groupProcess kills bwrap's process group; the PID namespace goes with it.
type groupProcess struct{ cmd *exec.Cmd }

func (g *groupProcess) Started(cmd *exec.Cmd) error { g.cmd = cmd; return nil }

func (g *groupProcess) Kill() {
	if g.cmd == nil || g.cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = g.cmd.Process.Kill()
	}
}

func (g *groupProcess) Close() {}

func realDir(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("not a folder")
	}
	return resolved, nil
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}
