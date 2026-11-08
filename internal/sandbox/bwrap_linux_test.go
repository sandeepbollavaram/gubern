//go:build linux

package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The Linux escape suite (Phase 4 gate). Each test tries one way out of the
// sandbox and checks, on the host, that it didn't work; each has a control
// showing the same action works where it is allowed, so a pass isn't vacuous.
//
// It needs bubblewrap and unprivileged user namespaces. Set
// GUBERN_SANDBOX_REQUIRED=1 (CI does) to fail instead of skipping when they
// are missing.

func platformHelper(verb, arg string) int {
	if verb == "signal" { // can the command see / signal host process arg?
		pid, _ := strconv.Atoi(arg)
		if err := syscall.Kill(pid, 0); err != nil {
			fmt.Println("signal failed:", err)
			return 3
		}
		fmt.Println("signalled")
		return 0
	}
	return 2
}

func bwrapBox(t *testing.T, net Network, limits Limits) (Sandbox, Policy) {
	t.Helper()
	sb, info, err := New(Config{Mode: ModeAuto, Network: net, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if sb == nil {
		if os.Getenv("GUBERN_SANDBOX_REQUIRED") == "1" {
			t.Fatalf("no sandbox: %s", info.Reason)
		}
		t.Skipf("no sandbox: %s", info.Reason)
	}
	if info.Adapter != "bubblewrap" || info.Label() != "Isolated (Linux)" {
		t.Errorf("info = %+v", info)
	}
	ws := t.TempDir()
	// The helper is this test binary: let the sandbox read it.
	limits.ReadPaths = append(limits.ReadPaths, filepath.Dir(os.Args[0]))
	return sb, Policy{Writable: ws, Dir: ws, Network: net, Limits: limits}
}

func newHelperCmd(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	return cmd
}

func outsideDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gubern-outside-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// ran fails the test unless the helper itself ran and reported.
func ran(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "wrote") && !strings.Contains(out, "failed") {
		t.Fatalf("the helper didn't run: %q", out)
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestEscape_WriteOutsideWorkspace(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	out := outsideDir(t)
	for _, target := range []string{
		filepath.Join(out, "escape.txt"),
		filepath.Join(p.Writable, "..", "escape.txt"), // path traversal
		"/usr/escape.txt",
		"/etc/escape.txt",
	} {
		// A write may land in the sandbox's own scratch /tmp; what counts is
		// that nothing reaches the host.
		o, code := runIn(t, sb, p, "write:"+target, 30*time.Second)
		ran(t, o)
		if exists(target) {
			t.Errorf("wrote %s on the host: exit %d %q", target, code, o)
		}
	}
	// Control: the workspace is writable.
	in := filepath.Join(p.Writable, "ok.txt")
	if o, code := runIn(t, sb, p, "write:"+in, 30*time.Second); code != 0 || !exists(in) {
		t.Errorf("couldn't write in the workspace: exit %d %q", code, o)
	}
}

func TestEscape_ReadSecretsOutsideWorkspace(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	secret := filepath.Join(outsideDir(t), "secret.txt")
	if err := os.WriteFile(secret, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if o, code := runIn(t, sb, p, "read:"+secret, 30*time.Second); code == 0 || strings.Contains(o, "hunter2") || !strings.Contains(o, "read failed") {
		t.Errorf("read the secret: exit %d %q", code, o)
	}
	if home, err := os.UserHomeDir(); err == nil && exists(filepath.Join(home, ".bashrc")) {
		if o, code := runIn(t, sb, p, "read:"+filepath.Join(home, ".bashrc"), 30*time.Second); code == 0 || !strings.Contains(o, "read failed") {
			t.Errorf("read a file in the home folder: %q", o)
		}
	}
	// Control: the same read works inside the workspace.
	in := filepath.Join(p.Writable, "visible.txt")
	_ = os.WriteFile(in, []byte("visible"), 0o600)
	if o, code := runIn(t, sb, p, "read:"+in, 30*time.Second); code != 0 || !strings.Contains(o, "read=visible") {
		t.Errorf("couldn't read in the workspace: exit %d %q", code, o)
	}
}

func TestEscape_SymlinkOut(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	out := outsideDir(t)
	secret := filepath.Join(out, "secret.txt")
	_ = os.WriteFile(secret, []byte("hunter2"), 0o600)
	if err := os.Symlink(secret, filepath.Join(p.Writable, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(p.Writable, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if o, code := runIn(t, sb, p, "read:"+filepath.Join(p.Writable, "link"), 30*time.Second); code == 0 || strings.Contains(o, "hunter2") || !strings.Contains(o, "read failed") {
		t.Errorf("read through a symlink: exit %d %q", code, o)
	}
	target := filepath.Join(p.Writable, "dirlink", "planted.txt")
	if o, code := runIn(t, sb, p, "write:"+target, 30*time.Second); code == 0 || exists(filepath.Join(out, "planted.txt")) || !strings.Contains(o, "write failed") {
		t.Errorf("wrote through a symlinked folder: exit %d %q", code, o)
	}
}

func TestEscape_WorkingFolderOutsideWorkspace(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	p.Dir = outsideDir(t)
	cmdPolicy := p
	if _, err := sb.Prepare(newHelperCmd("sleep"), cmdPolicy); err == nil {
		t.Error("accepted a working folder outside the workspace")
	}
}

func TestEscape_Network(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	addr := ln.Addr().String()
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	if o, code := runIn(t, sb, p, "dial:"+addr, 30*time.Second); code == 0 || strings.Contains(o, "connected") || !strings.Contains(o, "dial failed") {
		t.Errorf("reached the host's network with network off: exit %d %q", code, o)
	}
	// Control: with network on, the same dial connects.
	sbOn, pOn := bwrapBox(t, NetworkOn, Limits{MaxProcesses: 64})
	if o, code := runIn(t, sbOn, pOn, "dial:"+addr, 30*time.Second); code != 0 || !strings.Contains(o, "connected") {
		t.Errorf("network on couldn't connect: exit %d %q", code, o)
	}
}

func TestEscape_ForkBomb(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 16})
	if !strings.Contains(strings.Join(sb.Info().Enforced, ";"), "processes") {
		t.Skip("prlimit is not installed; the process cap isn't enforced")
	}
	o, _ := runIn(t, sb, p, "spawn:200", 60*time.Second)
	if n := startedCount(t, o); n >= 16 {
		t.Errorf("started %d processes under a cap of 16", n)
	}
	// Control: a higher cap lets them start.
	sb2, p2 := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 512})
	if o, _ := runIn(t, sb2, p2, "spawn:40", 60*time.Second); startedCount(t, o) != 40 {
		t.Errorf("a cap of 512 stopped 40 processes: %q", o)
	}
}

func TestEscape_SignalTheServer(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	o, code := runIn(t, sb, p, "signal:"+strconv.Itoa(os.Getpid()), 30*time.Second)
	if code == 0 || strings.Contains(o, "signalled") || !strings.Contains(o, "signal failed") {
		t.Errorf("signalled the server's process: exit %d %q", code, o)
	}
}

func TestEscape_TimeoutKillsTheSandbox(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	start := time.Now()
	_, code := runIn(t, sb, p, "hold:"+filepath.Join(p.Writable, "pid"), 3*time.Second)
	if took := time.Since(start); code == 0 || took > 15*time.Second {
		t.Errorf("exit %d after %s", code, took)
	}
	if !exists(filepath.Join(p.Writable, "pid")) {
		t.Fatal("the helper didn't start its child; the check below would be vacuous")
	}
	// The held child was in the sandbox's PID namespace, which is gone; no
	// process still runs the helper's sleep.
	time.Sleep(500 * time.Millisecond)
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		env, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if strings.Contains(string(env), helperEnv+"=sleep") && strings.Contains(string(env), p.Writable) {
			t.Errorf("process %s from the sandbox is still running", e.Name())
		}
	}
}

// A private home persists between commands (tool caches), and is the
// command's HOME; the real home stays hidden.
func TestSandbox_PrivateHome(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64})
	p.Home = filepath.Join(outsideDir(t), "home")
	cache := filepath.Join(p.Home, "cache.txt")
	if o, code := runIn(t, sb, p, "write:"+cache, 30*time.Second); code != 0 || !exists(cache) {
		t.Fatalf("couldn't write the private home: exit %d %q", code, o)
	}
	if o, code := runIn(t, sb, p, "read:"+cache, 30*time.Second); code != 0 || !strings.Contains(o, "read=escaped") {
		t.Errorf("the private home didn't persist: exit %d %q", code, o)
	}
	if o, _ := runIn(t, sb, p, "env:HOME", 30*time.Second); !strings.Contains(o, "HOME="+p.Home) {
		t.Errorf("HOME inside = %q, want %s", o, p.Home)
	}
}

// With a systemd scope, the cgroup caps the whole tree's memory (and its
// processes, see TestEscape_ForkBomb); a control shows the same helper
// fits under a larger cap.
func TestSandbox_CgroupMemoryCap(t *testing.T) {
	sb, p := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64, MemoryMB: 64})
	if !strings.Contains(strings.Join(sb.Info().Enforced, ";"), "MB of memory (cgroup)") {
		t.Skip("memory isn't capped on this host (no systemd scope, or no cgroup v2 memory controller)")
	}
	if o, code := runIn(t, sb, p, "alloc:512", 60*time.Second); code == 0 || strings.Contains(o, "allocated 512") {
		t.Errorf("allocated 512 MB under a 64 MB cap: exit %d %q", code, o)
	}
	sb2, p2 := bwrapBox(t, NetworkOff, Limits{MaxProcesses: 64, MemoryMB: 1024})
	if o, code := runIn(t, sb2, p2, "alloc:128", 60*time.Second); code != 0 {
		t.Errorf("128 MB under a 1024 MB cap failed: exit %d %q", code, o)
	}
}
