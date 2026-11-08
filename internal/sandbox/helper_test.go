package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The tests run this test binary inside the sandbox as a helper, so they
// don't depend on curl, python or a particular shell being installed.
// GUBERN_SANDBOX_HELPER picks what the helper does.
const helperEnv = "GUBERN_SANDBOX_HELPER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperEnv); mode != "" {
		os.Exit(helper(mode))
	}
	// The helper runs from other working folders; name it absolutely.
	if abs, err := filepath.Abs(os.Args[0]); err == nil {
		os.Args[0] = abs
	}
	os.Exit(m.Run())
}

func helper(mode string) int {
	verb, arg, _ := strings.Cut(mode, ":")
	switch verb {
	case "sleep":
		time.Sleep(60 * time.Second)
	case "spawn": // start N sleeping copies; report how many started
		n, _ := strconv.Atoi(arg)
		started := 0
		var kids []*exec.Cmd
		for range n {
			c := exec.Command(os.Args[0])
			c.Env = append(os.Environ(), helperEnv+"=sleep")
			if c.Start() != nil {
				break
			}
			kids = append(kids, c)
			started++
		}
		fmt.Printf("started=%d\n", started)
		for _, k := range kids {
			_ = k.Process.Kill()
		}
	case "background": // leave a sleeping child behind, write its pid to arg
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := c.Start(); err != nil {
			fmt.Println("start:", err)
			return 1
		}
		_ = os.WriteFile(arg, []byte(strconv.Itoa(c.Process.Pid)), 0o600)
	case "hold": // start a sleeping child, write its pid to arg, then sleep
		c := exec.Command(os.Args[0])
		c.Env = append(os.Environ(), helperEnv+"=sleep")
		if err := c.Start(); err != nil {
			fmt.Println("start:", err)
			return 1
		}
		_ = os.WriteFile(arg, []byte(strconv.Itoa(c.Process.Pid)), 0o600)
		time.Sleep(60 * time.Second)
	case "alloc": // allocate and touch arg MB
		mb, _ := strconv.Atoi(arg)
		buf := make([]byte, mb<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		fmt.Println("allocated", len(buf)>>20)
	case "dial":
		c, err := net.DialTimeout("tcp", arg, 3*time.Second)
		if err != nil {
			fmt.Println("dial failed:", err)
			return 3
		}
		_ = c.Close()
		fmt.Println("connected")
	case "read":
		b, err := os.ReadFile(arg)
		if err != nil {
			fmt.Println("read failed:", err)
			return 3
		}
		fmt.Printf("read=%s\n", b)
	case "env":
		fmt.Printf("%s=%s\n", arg, os.Getenv(arg))
	case "write":
		if err := os.WriteFile(arg, []byte("escaped"), 0o600); err != nil {
			fmt.Println("write failed:", err)
			return 3
		}
		fmt.Println("wrote")
	default:
		return platformHelper(verb, arg)
	}
	return 0
}

// runIn runs the helper in mode under sb and policy p, returning its output
// and exit code.
func runIn(t *testing.T, sb Sandbox, p Policy, mode string, timeout time.Duration) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	cmd.Dir = p.Dir
	proc, err := sb.Prepare(cmd, p)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer proc.Close()
	cmd.Cancel = func() error { proc.Kill(); return nil }
	cmd.WaitDelay = 5 * time.Second
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := proc.Started(cmd); err != nil {
		_ = cmd.Wait()
		t.Fatalf("Started: %v", err)
	}
	err = cmd.Wait()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("Wait: %v (%s)", err, out.String())
	}
	return out.String(), code
}

func startedCount(t *testing.T, out string) int {
	t.Helper()
	_, after, ok := strings.Cut(out, "started=")
	if !ok {
		t.Fatalf("helper didn't report: %q", out)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(after, "\n", 2)[0]))
	return n
}

func TestNew_OffAndInvalid(t *testing.T) {
	sb, info, err := New(Config{Mode: ModeOff})
	if err != nil || sb != nil || info.Level != LevelNone || info.Label() != "Not sandboxed" || info.Reason == "" {
		t.Errorf("off = %v %+v %v", sb, info, err)
	}
	if _, _, err := New(Config{Mode: "strict"}); err == nil {
		t.Error("accepted mode strict")
	}
	if _, _, err := New(Config{Network: "allow"}); err == nil {
		t.Error("accepted network allow")
	}
}

func TestNew_AutoMatchesPlatform(t *testing.T) {
	sb, info, err := New(Config{Mode: ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if sb == nil {
		if info.Level != LevelNone || info.Reason == "" {
			t.Errorf("no adapter but info = %+v", info)
		}
		t.Logf("no adapter here: %s", info.Reason)
		return
	}
	if info.Level == LevelNone || info.Adapter == "none" || len(info.Enforced) == 0 || len(info.NotEnforced) == 0 {
		t.Errorf("adapter info = %+v", info)
	}
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		dir, p string
		in     bool
	}{
		{"/w", "/w", true}, {"/w", "/w/a", true}, {"/w", "/wx", false}, {"/w", "/", false},
		{`C:\w`, `C:\w\a`, true}, {`C:\w`, `C:\wx`, false},
	} {
		if within(c.dir, c.p) != c.in {
			t.Errorf("within(%q, %q) != %v", c.dir, c.p, c.in)
		}
	}
}
