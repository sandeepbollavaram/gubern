//go:build windows

package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows escape suite for the AppContainer adapter (M.8). Like the
// Linux suite, every escape attempt is checked on the host, and each has a
// control showing the same action works where it is allowed. Only temporary
// folders are granted to the test profiles, which are deleted afterwards.

func acBox(t *testing.T, net Network) (Sandbox, Policy) {
	t.Helper()
	sb, info, err := New(Config{Mode: ModeAuto, Network: net, Windows: "appcontainer", Limits: Limits{MaxProcesses: 32, MemoryMB: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	if sb == nil || info.Adapter != "appcontainer" {
		t.Skipf("no AppContainer here: %s", info.Reason)
	}
	if info.Level != LevelIsolated || info.Label() != "Isolated (Windows)" {
		t.Errorf("info = %+v", info)
	}
	ws := t.TempDir()
	h := sha256.Sum256([]byte(strings.ToLower(ws)))
	name, _ := windows.UTF16PtrFromString("gubern.ws." + hex.EncodeToString(h[:6]))
	t.Cleanup(func() { _, _, _ = procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(name))) })
	return sb, Policy{Writable: ws, Dir: ws, Network: net, Limits: Limits{MaxProcesses: 32, MemoryMB: 4096, Home: t.TempDir()}}
}

func hostExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestAC_Files(t *testing.T) {
	sb, p := acBox(t, NetworkOff)
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Control: the workspace is writable and readable.
	in := filepath.Join(p.Writable, "ok.txt")
	if o, code := runIn(t, sb, p, "write:"+in, 30*time.Second); code != 0 || !hostExists(in) {
		t.Fatalf("workspace not writable: exit %d %q", code, o)
	}
	if o, code := runIn(t, sb, p, "read:"+in, 30*time.Second); code != 0 || !strings.Contains(o, "read=escaped") {
		t.Errorf("workspace not readable: exit %d %q", code, o)
	}
	for _, target := range []string{
		filepath.Join(outside, "planted.txt"),
		filepath.Join(p.Writable, "..", "escape.txt"),
		filepath.Join(os.Getenv("SystemRoot"), "gubern-planted.txt"),
	} {
		o, code := runIn(t, sb, p, "write:"+target, 30*time.Second)
		if code == 0 || hostExists(target) {
			t.Errorf("wrote %s: exit %d %q", target, code, o)
		}
		_ = os.Remove(target)
	}
	if o, code := runIn(t, sb, p, "read:"+secret, 30*time.Second); code == 0 || strings.Contains(o, "hunter2") || !strings.Contains(o, "read failed") {
		t.Errorf("read the secret: exit %d %q", code, o)
	}
	// A junction inside the workspace pointing out doesn't open the target.
	link := filepath.Join(p.Writable, "out")
	if err := os.Symlink(outside, link); err == nil {
		if o, code := runIn(t, sb, p, "read:"+filepath.Join(link, "secret.txt"), 30*time.Second); code == 0 || strings.Contains(o, "hunter2") {
			t.Errorf("read through a link: exit %d %q", code, o)
		}
	}
}

func TestAC_Network(t *testing.T) {
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
	off, pOff := acBox(t, NetworkOff)
	if o, code := runIn(t, off, pOff, "dial:1.1.1.1:443", 30*time.Second); code == 0 || !strings.Contains(o, "dial failed") {
		t.Errorf("network off reached the internet: exit %d %q", code, o)
	}
	on, pOn := acBox(t, NetworkOn)
	if o, code := runIn(t, on, pOn, "dial:"+ln.Addr().String(), 30*time.Second); code == 0 || !strings.Contains(o, "dial failed") {
		t.Errorf("reached a host service on loopback: exit %d %q", code, o)
	}
	// Control: network on reaches the internet (when the host can).
	if c, err := net.DialTimeout("tcp", "1.1.1.1:443", 5*time.Second); err != nil {
		t.Skipf("this host can't reach 1.1.1.1 itself: %v", err)
	} else {
		_ = c.Close()
	}
	if o, code := runIn(t, on, pOn, "dial:1.1.1.1:443", 30*time.Second); code != 0 || !strings.Contains(o, "connected") {
		t.Errorf("network on couldn't connect: exit %d %q", code, o)
	}
}

func TestAC_InTheJobAndTheBox(t *testing.T) {
	sb, p := acBox(t, NetworkOff)
	if o, _ := runIn(t, sb, p, "injob", 30*time.Second); !strings.Contains(o, "injob=true") {
		t.Errorf("not in the job: %q", o)
	}
	if o, _ := runIn(t, sb, p, "ac", 30*time.Second); !strings.Contains(o, "appcontainer=true") {
		t.Errorf("not in an AppContainer: %q", o)
	}
	if o, _ := runIn(t, sb, p, "spawn:60", 60*time.Second); startedCount(t, o) >= 32 {
		t.Errorf("process cap not applied: %q", o)
	}
	// TEMP points into the private home, which is writable.
	if o, code := runIn(t, sb, p, "env:TEMP", 30*time.Second); code != 0 || !strings.Contains(o, filepath.Clean(p.Home)) {
		t.Errorf("TEMP = %q", o)
	}
}
