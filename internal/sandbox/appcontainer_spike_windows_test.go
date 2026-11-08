//go:build windows

package sandbox

// AppContainer feasibility spike (Milestone M). Opt-in: these experiments
// create a temporary AppContainer profile, grant it access to disposable
// folders only, and delete the profile at the end. They never touch the
// user's own files. Run with:
//
//	GUBERN_APPCONTAINER_SPIKE=1 go test -run AppContainerSpike -v ./internal/sandbox/
//
// The approach under test needs no custom process launcher: derive an
// AppContainer ("lowbox") token from the server's own token with
// NtCreateLowBoxToken, and hand it to os/exec through SysProcAttr.Token
// (Go then calls CreateProcessAsUser, still with its explicit inherited-
// handle list). See docs/design/windows-appcontainer-sandbox.md.

import (
	"context"
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
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	userenv                           = windows.NewLazySystemDLL("userenv.dll")
	procCreateAppContainerProfile     = userenv.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile     = userenv.NewProc("DeleteAppContainerProfile")
	procDeriveAppContainerSidFromName = userenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	procNtCreateLowBoxToken           = ntdll.NewProc("NtCreateLowBoxToken")
	procGetHandleInformation          = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
)

const (
	sidInternetClient             = "S-1-15-3-1"
	sidPrivateNetworkClientServer = "S-1-15-3-3"
	tokenIsAppContainerClass      = 29 // TOKEN_INFORMATION_CLASS TokenIsAppContainer
)

// appContainerSID creates (or finds) the profile and returns its SID.
func appContainerSID(t *testing.T, name string) *windows.SID {
	t.Helper()
	n, _ := windows.UTF16PtrFromString(name)
	var sid *windows.SID
	hr, _, _ := procCreateAppContainerProfile.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(n)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if hr == 0x800700B7 { // already exists
		hr, _, _ = procDeriveAppContainerSidFromName.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&sid)))
	}
	if hr != 0 {
		t.Fatalf("CreateAppContainerProfile/Derive: HRESULT 0x%x", hr)
	}
	t.Cleanup(func() {
		r, _, _ := procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(n)))
		t.Logf("DeleteAppContainerProfile(%s): HRESULT 0x%x", name, r)
	})
	return sid
}

// appContainerSIDWithCaps creates a profile that declares capabilities.
func appContainerSIDWithCaps(t *testing.T, name string, caps ...string) *windows.SID {
	t.Helper()
	var capList []windows.SIDAndAttributes
	for _, c := range caps {
		s, _ := windows.StringToSid(c)
		capList = append(capList, windows.SIDAndAttributes{Sid: s, Attributes: windows.SE_GROUP_ENABLED})
	}
	n, _ := windows.UTF16PtrFromString(name)
	var sid *windows.SID
	hr, _, _ := procCreateAppContainerProfile.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(n)),
		uintptr(unsafe.Pointer(&capList[0])), uintptr(len(capList)), uintptr(unsafe.Pointer(&sid)))
	if hr == 0x800700B7 {
		hr, _, _ = procDeriveAppContainerSidFromName.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&sid)))
	}
	if hr != 0 {
		t.Fatalf("CreateAppContainerProfile(%s): HRESULT 0x%x", name, hr)
	}
	t.Cleanup(func() {
		r, _, _ := procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(n)))
		t.Logf("DeleteAppContainerProfile(%s): HRESULT 0x%x", name, r)
	})
	return sid
}

// lowboxToken derives an AppContainer token from this process's token.
func lowboxToken(t *testing.T, pkg *windows.SID, caps ...string) windows.Token {
	t.Helper()
	var self windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ALL_ACCESS, &self); err != nil {
		t.Fatalf("OpenProcessToken: %v", err)
	}
	defer self.Close()
	var capList []windows.SIDAndAttributes
	for _, c := range caps {
		s, err := windows.StringToSid(c)
		if err != nil {
			t.Fatal(err)
		}
		capList = append(capList, windows.SIDAndAttributes{Sid: s, Attributes: windows.SE_GROUP_ENABLED})
	}
	var capPtr uintptr
	if len(capList) > 0 {
		capPtr = uintptr(unsafe.Pointer(&capList[0]))
	}
	var lowbox windows.Token
	st, _, _ := procNtCreateLowBoxToken.Call(uintptr(unsafe.Pointer(&lowbox)), uintptr(self), uintptr(windows.TOKEN_ALL_ACCESS), 0,
		uintptr(unsafe.Pointer(pkg)), uintptr(len(capList)), capPtr, 0, 0)
	if st != 0 {
		t.Fatalf("NtCreateLowBoxToken: NTSTATUS 0x%x", st)
	}
	t.Cleanup(func() { lowbox.Close() })
	return lowbox
}

// grant gives the AppContainer SID access to dir with icacls (disposable
// folders only). rights is "RX" or "M".
func grant(t *testing.T, sid *windows.SID, dir, rights string) {
	t.Helper()
	out, err := exec.Command("icacls", dir, "/grant", "*"+sid.String()+":(OI)(CI)"+rights, "/q").CombinedOutput()
	if err != nil {
		t.Fatalf("icacls %s: %v %s", dir, err, out)
	}
}

// inBox runs the helper (this test binary) in mode inside the AppContainer.
func inBox(t *testing.T, tok windows.Token, mode string, env []string, timeout time.Duration) (string, int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(append(os.Environ(), env...), helperEnv+"="+mode)
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(tok)}
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code, err = ee.ExitCode(), nil
	}
	return string(out), code, err
}

func record(t *testing.T, id, expected, actual string, pass bool) {
	t.Helper()
	verdict := "PASS"
	if !pass {
		verdict = "FAIL"
		t.Fail()
	}
	a := strings.Join(strings.Fields(actual), " ")
	if len(a) > 240 {
		a = a[:240] + "…"
	}
	t.Logf("EXPERIMENT %s | expected: %s | actual: %s | %s", id, expected, a, verdict)
}

func acHelper(verb, arg string) (int, bool) {
	switch verb {
	case "ac": // is this process in an AppContainer?
		var tok windows.Token
		if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok); err != nil {
			fmt.Println("OpenProcessToken:", err)
			return 1, true
		}
		var is uint32
		var n uint32
		if err := windows.GetTokenInformation(tok, tokenIsAppContainerClass, (*byte)(unsafe.Pointer(&is)), 4, &n); err != nil {
			fmt.Println("GetTokenInformation:", err)
			return 1, true
		}
		fmt.Printf("appcontainer=%v\n", is != 0)
		return 0, true
	case "lookup":
		addrs, err := net.LookupHost(arg)
		if err != nil {
			fmt.Println("lookup failed:", err)
			return 3, true
		}
		fmt.Println("resolved", len(addrs))
		return 0, true
	case "lookupvia": // resolve arg's host through a given DNS server: "server|host"
		server, host, _ := strings.Cut(arg, "|")
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", server)
		}}
		addrs, err := r.LookupHost(context.Background(), host)
		if err != nil {
			fmt.Println("lookup failed:", err)
			return 3, true
		}
		fmt.Println("resolved", len(addrs))
		return 0, true
	case "handle": // is an inherited handle value usable here?
		v, _ := strconv.ParseUint(arg, 10, 64)
		var flags uint32
		if r, _, err := procGetHandleInformation.Call(uintptr(v), uintptr(unsafe.Pointer(&flags))); r == 0 {
			fmt.Println("handle not inherited:", err)
			return 3, true
		}
		fmt.Println("handle inherited")
		return 0, true
	case "listdir":
		ents, err := os.ReadDir(arg)
		if err != nil {
			fmt.Println("list failed:", err)
			return 3, true
		}
		fmt.Println("listed", len(ents))
		return 0, true
	}
	return 0, false
}

func TestAppContainerSpike(t *testing.T) {
	if os.Getenv("GUBERN_APPCONTAINER_SPIKE") != "1" {
		t.Skip("opt-in: set GUBERN_APPCONTAINER_SPIKE=1")
	}
	if err := procNtCreateLowBoxToken.Find(); err != nil {
		t.Skipf("NtCreateLowBoxToken not available: %v", err)
	}
	ver := windows.RtlGetVersion()
	t.Logf("Windows %d.%d build %d", ver.MajorVersion, ver.MinorVersion, ver.BuildNumber)

	name := fmt.Sprintf("gubern.spike.%d", os.Getpid())
	sid := appContainerSID(t, name)
	t.Logf("AppContainer SID: %s", sid.String())
	record(t, "E1 profile", "a profile and an S-1-15-2 SID", sid.String(), strings.HasPrefix(sid.String(), "S-1-15-2-"))

	// The helper binary must be readable and executable inside the box.
	binDir := filepath.Dir(os.Args[0])
	grant(t, sid, binDir, "RX")

	base := t.TempDir()
	ws, out, cache, other := filepath.Join(base, "ws"), filepath.Join(base, "out"), filepath.Join(base, "cache"), filepath.Join(base, "other")
	for _, d := range []string{ws, out, cache, other} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(ws, "src.txt"), []byte("project file"), 0o600)
	_ = os.WriteFile(filepath.Join(other, "secret.txt"), []byte("hunter2"), 0o600)
	grant(t, sid, ws, "RX")
	grant(t, sid, out, "M")
	grant(t, sid, cache, "M")

	none := lowboxToken(t, sid)
	inet := lowboxToken(t, sid, sidInternetClient)

	// E2: a process starts with the lowbox token through os/exec.
	o, code, err := inBox(t, none, "ac", nil, 30*time.Second)
	record(t, "E2 launch via os/exec + SysProcAttr.Token", "runs, reports appcontainer=true", fmt.Sprintf("exit %d err %v: %s", code, err, o), err == nil && code == 0 && strings.Contains(o, "appcontainer=true"))
	if err != nil || code != 0 {
		t.Fatalf("can't launch in the AppContainer; remaining experiments would be vacuous")
	}

	o, code, _ = inBox(t, none, "read:"+filepath.Join(ws, "src.txt"), nil, 30*time.Second)
	record(t, "E3 read approved workspace", "read=project file", o, code == 0 && strings.Contains(o, "read=project file"))

	o, code, _ = inBox(t, none, "write:"+filepath.Join(out, "artifact.bin"), nil, 30*time.Second)
	record(t, "E4 write output dir", "wrote", o, code == 0 && strings.Contains(o, "wrote"))

	o, code, _ = inBox(t, none, "write:"+filepath.Join(ws, "tamper.txt"), nil, 30*time.Second)
	_, statErr := os.Stat(filepath.Join(ws, "tamper.txt"))
	record(t, "E4b write to read-only workspace", "write failed, no file", o, code != 0 && os.IsNotExist(statErr))

	o, code, _ = inBox(t, none, "read:"+filepath.Join(other, "secret.txt"), nil, 30*time.Second)
	record(t, "E5 read unrelated host file", "read failed (access denied)", o, code != 0 && !strings.Contains(o, "hunter2"))

	o, code, _ = inBox(t, none, "write:"+filepath.Join(other, "planted.txt"), nil, 30*time.Second)
	_, statErr = os.Stat(filepath.Join(other, "planted.txt"))
	record(t, "E5b write unrelated host folder", "write failed", o, code != 0 && os.IsNotExist(statErr))

	o, code, _ = inBox(t, none, "listdir:"+base, nil, 30*time.Second)
	record(t, "E5c list the parent of the granted folders", "list failed", o, code != 0)

	userTemp := os.Getenv("TEMP")
	o, code, _ = inBox(t, none, "write:"+filepath.Join(userTemp, "gubern-spike-planted.txt"), nil, 30*time.Second)
	planted := filepath.Join(userTemp, "gubern-spike-planted.txt")
	_, statErr = os.Stat(planted)
	_ = os.Remove(planted)
	record(t, "E5d write the user's TEMP", "write failed", o, code != 0 && os.IsNotExist(statErr))

	o, code, _ = inBox(t, none, "write:"+filepath.Join(binDir, "planted.exe"), nil, 30*time.Second)
	_, statErr = os.Stat(filepath.Join(binDir, "planted.exe"))
	record(t, "E6 write into read-only toolchain dir", "write failed", o, code != 0 && os.IsNotExist(statErr))

	o, code, _ = inBox(t, none, "write:"+filepath.Join(cache, "entry"), nil, 30*time.Second)
	record(t, "E7 private cache writable", "wrote", o, code == 0 && strings.Contains(o, "wrote"))
	var o2 string

	// Network. A host listener on loopback, and an external host.
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
	o, code, _ = inBox(t, none, "dial:example.com:443", nil, 40*time.Second)
	record(t, "E9 no capability: internet", "dial failed", o, code != 0)
	o, code, _ = inBox(t, inet, "dial:example.com:443", nil, 40*time.Second)
	record(t, "E8 internetClient: internet", "connected", o, code == 0 && strings.Contains(o, "connected"))
	o, code, _ = inBox(t, inet, "dial:1.1.1.1:443", nil, 40*time.Second)
	record(t, "E8b internetClient: dial a public IP (no DNS)", "connected", o, code == 0 && strings.Contains(o, "connected"))
	o, code, _ = inBox(t, none, "dial:1.1.1.1:443", nil, 40*time.Second)
	record(t, "E9b no capability: dial a public IP", "dial failed", o, code != 0)
	// The same with a profile that declares the capability itself.
	sidNet := appContainerSIDWithCaps(t, name+".net", sidInternetClient)
	grant(t, sidNet, binDir, "RX")
	inetP := lowboxToken(t, sidNet, sidInternetClient)
	o, code, _ = inBox(t, inetP, "dial:1.1.1.1:443", nil, 40*time.Second)
	record(t, "E8c profile+token internetClient: dial a public IP", "connected", o, code == 0 && strings.Contains(o, "connected"))
	o, code, _ = inBox(t, inetP, "lookup:example.com", nil, 40*time.Second)
	record(t, "E10d profile+token internetClient: DNS", "resolved", o, code == 0 && strings.Contains(o, "resolved"))
	both := lowboxToken(t, sid, sidInternetClient, sidPrivateNetworkClientServer)
	o, code, _ = inBox(t, both, "lookup:example.com", nil, 40*time.Second)
	record(t, "E10e internetClient + privateNetworkClientServer: DNS", "resolved", o, code == 0 && strings.Contains(o, "resolved"))
	o, code, _ = inBox(t, both, "dial:example.com:443", nil, 40*time.Second)
	record(t, "E8d internetClient + privateNetworkClientServer: dial by name", "connected", o, code == 0 && strings.Contains(o, "connected"))
	o, code, _ = inBox(t, inet, "lookupvia:1.1.1.1:53|example.com", nil, 40*time.Second)
	record(t, "E10f internetClient: DNS through a public resolver (UDP 1.1.1.1:53)", "resolved", o, code == 0 && strings.Contains(o, "resolved"))
	o, code, _ = inBox(t, none, "lookupvia:1.1.1.1:53|example.com", nil, 40*time.Second)
	record(t, "E10g no capability: DNS through a public resolver", "lookup failed", o, code != 0)
	o, code, _ = inBox(t, none, "lookup:example.com", nil, 40*time.Second)
	t.Logf("EXPERIMENT E10a DNS without capability | observed: exit %d %s", code, strings.TrimSpace(o))
	o, code, _ = inBox(t, inet, "lookup:example.com", nil, 40*time.Second)
	record(t, "E10b DNS with internetClient", "resolved", o, code == 0 && strings.Contains(o, "resolved"))
	o, code, _ = inBox(t, inet, "dial:"+ln.Addr().String(), nil, 40*time.Second)
	record(t, "E10c loopback to a host service (internetClient)", "dial failed (loopback isolation)", o, code != 0)

	// Children inherit the box.
	o, _, _ = inBox(t, none, "spawn:5", nil, 60*time.Second)
	record(t, "E11 child processes", "started=5", o, strings.Contains(o, "started=5"))

	// Job object on top: the Windows adapter's Prepare/Started with the token.
	sb, info, err := New(Config{Mode: ModeRequired, Limits: Limits{MaxProcesses: 8, MemoryMB: 64}})
	if err != nil {
		t.Fatal(err)
	}
	_ = info
	runJob := func(mode string, limits Limits, timeout time.Duration) (string, int) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = append(os.Environ(), helperEnv+"="+mode)
		cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(none)}
		proc, err := sb.Prepare(cmd, Policy{Writable: out, Dir: out, Limits: limits})
		if err != nil {
			t.Fatalf("Prepare: %v", err)
		}
		defer proc.Close()
		cmd.Cancel = func() error { proc.Kill(); return nil }
		cmd.WaitDelay = 5 * time.Second
		var b strings.Builder
		cmd.Stdout, cmd.Stderr = &b, &b
		if err := cmd.Start(); err != nil {
			return "start: " + err.Error(), -1
		}
		if err := proc.Started(cmd); err != nil {
			_ = cmd.Wait()
			return "started: " + err.Error(), -1
		}
		err = cmd.Wait()
		if ee, ok := err.(*exec.ExitError); ok {
			return b.String(), ee.ExitCode()
		}
		return b.String(), 0
	}
	o, _ = runJob("injob", Limits{MaxProcesses: 8}, 30*time.Second)
	o2, code2 := runJob("ac", Limits{MaxProcesses: 8}, 30*time.Second)
	o2 = fmt.Sprintf("[ac exit %d] %s", code2, o2)
	record(t, "E12 job assignment of an AppContainer process", "injob=true and appcontainer=true", o+" "+o2, strings.Contains(o, "injob=true") && strings.Contains(o2, "appcontainer=true"))

	pidFile := filepath.Join(out, "pid")
	start := time.Now()
	_, code = runJob("hold:"+pidFile, Limits{MaxProcesses: 8}, 3*time.Second)
	b, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(string(b))
	time.Sleep(300 * time.Millisecond)
	record(t, "E13 tree termination on timeout", "child dead", fmt.Sprintf("exit %d after %s, child %d alive=%v", code, time.Since(start).Round(time.Millisecond), pid, pid != 0 && alive(pid)), pid != 0 && !alive(pid))

	o, _ = runJob("spawn:40", Limits{MaxProcesses: 8}, 60*time.Second)
	record(t, "E14a process cap", "started < 8", o, strings.Contains(o, "started=") && startedN(o) < 8)
	o, code = runJob("alloc:512", Limits{MaxProcesses: 8, MemoryMB: 64}, 60*time.Second)
	record(t, "E14b memory cap", "allocation fails", fmt.Sprintf("exit %d %s", code, o), code != 0 && !strings.Contains(o, "allocated 512"))

	// Handle inheritance: an inheritable handle in the parent is not passed.
	name16, _ := windows.UTF16PtrFromString(filepath.Join(other, "secret.txt"))
	h, err := windows.CreateFile(name16, windows.GENERIC_READ, windows.FILE_SHARE_READ, &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	o, code, _ = inBox(t, none, "handle:"+strconv.FormatUint(uint64(h), 10), nil, 30*time.Second)
	// STATUS_INVALID_HANDLE (0xc0000008) in the child also means the value
	// names no handle there (AppContainer processes get strict handle checks).
	record(t, "E16 inheritable handle in the parent", "not inherited", o, code != 0 && (strings.Contains(o, "not inherited") || strings.Contains(o, "0xc0000008")))

	// E18: a real go build inside the AppContainer and the job. The toolchain
	// in Program Files is readable by all AppContainers by default; the
	// module, caches and temp are private, granted folders.
	goExe := filepath.Join(os.Getenv("ProgramFiles"), "Go", "bin", "go.exe")
	if _, err := os.Stat(goExe); err != nil {
		t.Logf("EXPERIMENT E18 go build | UNVERIFIED: no Go in Program Files")
	} else {
		mod := filepath.Join(base, "mod")
		priv := filepath.Join(base, "priv")
		for _, d := range []string{mod, filepath.Join(priv, "tmp"), filepath.Join(priv, "gocache"), filepath.Join(priv, "gopath")} {
			_ = os.MkdirAll(d, 0o700)
		}
		_ = os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/hello\n\ngo 1.21\n"), 0o600)
		_ = os.WriteFile(filepath.Join(mod, "main.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o600)
		grant(t, sid, mod, "M")
		grant(t, sid, priv, "M")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, goExe, "build", "-o", filepath.Join(mod, "hello.exe"), ".")
		cmd.Dir = mod
		cmd.Env = []string{
			"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "PATH=" + filepath.Dir(goExe),
			"GOTOOLCHAIN=local", "GOENV=off", "GOTELEMETRY=off", "GOFLAGS=-mod=mod", "GOPROXY=off",
			"GOCACHE=" + filepath.Join(priv, "gocache"), "GOPATH=" + filepath.Join(priv, "gopath"),
			"TMP=" + filepath.Join(priv, "tmp"), "TEMP=" + filepath.Join(priv, "tmp"),
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(none)}
		proc, err := sb.Prepare(cmd, Policy{Writable: mod, Dir: mod, Limits: Limits{MaxProcesses: 64, MemoryMB: 4096}})
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		cmd.Stdout, cmd.Stderr = &b, &b
		start := time.Now()
		err = cmd.Start()
		if err == nil {
			if err = proc.Started(cmd); err == nil {
				err = cmd.Wait()
			}
		}
		proc.Close()
		_, exeErr := os.Stat(filepath.Join(mod, "hello.exe"))
		record(t, "E18 go build in AppContainer + job", "builds hello.exe", fmt.Sprintf("err %v after %s; %s", err, time.Since(start).Round(time.Millisecond), b.String()), err == nil && exeErr == nil)
		if exeErr == nil {
			out, err := exec.Command(filepath.Join(mod, "hello.exe")).CombinedOutput()
			record(t, "E18b built binary runs", "hi", fmt.Sprintf("%v %s", err, out), err == nil && strings.Contains(string(out), "hi"))
		}
	}

	// Failure cleanup: launching a missing binary with the token fails cleanly.
	cmd := exec.Command(filepath.Join(out, "missing.exe"))
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(none)}
	err = cmd.Run()
	record(t, "E17 launch failure", "an error, no process", fmt.Sprint(err), err != nil)
}

func startedN(o string) int {
	_, after, _ := strings.Cut(o, "started=")
	n, _ := strconv.Atoi(strings.TrimSpace(strings.SplitN(after, "\n", 2)[0]))
	return n
}
