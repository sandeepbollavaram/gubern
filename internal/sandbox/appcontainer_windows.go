//go:build windows

package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The AppContainer adapter (Milestone M.8; design and evidence in
// docs/design/windows-appcontainer-sandbox.md). Each command runs with an
// AppContainer ("lowbox") token derived from the server's own token, handed
// to os/exec through SysProcAttr.Token, inside the same Job Object as the
// job adapter. The token is deny-by-default:
//
//   - files: only folders granted to the workspace's AppContainer SID (the
//     workspace and the private home read-write; extra read paths and the
//     program's folder read-only), plus what Windows grants every
//     AppContainer (Program Files and System32, read-only);
//   - network: none, or the public internet when the run's network is on
//     (internetClient); loopback stays closed, private ranges too.
//
// Grants are ACEs added to those folders' DACLs; they are recorded so an
// operator can see and remove them.

var (
	acUserenv              = windows.NewLazySystemDLL("userenv.dll")
	acCreateProfile        = acUserenv.NewProc("CreateAppContainerProfile")
	acDeriveSID            = acUserenv.NewProc("DeriveAppContainerSidFromAppContainerName")
	acNtCreateLowBoxToken  = ntdll.NewProc("NtCreateLowBoxToken")
	acInternetClientSID, _ = windows.StringToSid("S-1-15-3-1")
)

type appContainerSandbox struct {
	job   *jobSandbox
	cfg   Config
	mu    sync.Mutex
	sids  map[string]*windows.SID // profile name → SID
	grant map[string]bool         // "sid|path|rights" already applied
	// grantLog records every grant (one per line) for operators.
	grantLog string
}

func newAppContainer(cfg Config, job *jobSandbox) (Sandbox, error) {
	if err := acNtCreateLowBoxToken.Find(); err != nil {
		return nil, fmt.Errorf("%w: NtCreateLowBoxToken: %v", ErrUnavailable, err)
	}
	if err := acCreateProfile.Find(); err != nil {
		return nil, fmt.Errorf("%w: CreateAppContainerProfile: %v", ErrUnavailable, err)
	}
	if acInternetClientSID == nil {
		return nil, fmt.Errorf("%w: internetClient SID", ErrUnavailable)
	}
	s := &appContainerSandbox{job: job, cfg: cfg, sids: map[string]*windows.SID{}, grant: map[string]bool{}}
	if cfg.Home != "" {
		s.grantLog = filepath.Join(filepath.Dir(cfg.Home), "appcontainer-grants.log")
	}
	return s, nil
}

func (s *appContainerSandbox) Info() Info {
	in := Info{
		Adapter: "appcontainer", Level: LevelIsolated, Platform: "Windows", Mode: s.cfg.Mode,
		Network: string(s.cfg.Network),
		Enforced: []string{
			"only the workspace and a private home are writable",
			"other folders (home, server state, other workspaces) can't be read",
			"no network unless the run's network is on (then the public internet only)",
			"loopback and host services are closed",
			"every process the command starts is killed with it",
			fmt.Sprintf("at most %d processes", s.cfg.MaxProcesses),
		},
		NotEnforced: []string{
			"destination filtering (network on means the whole public internet)",
			"Program Files and System32 are readable (Windows grants that to every AppContainer)",
		},
	}
	if s.cfg.MemoryMB > 0 {
		in.Enforced = append(in.Enforced, fmt.Sprintf("at most %d MB of memory", s.cfg.MemoryMB))
	}
	if loopbackResolver() {
		in.NotEnforced = append(in.NotEnforced, "name lookups: this host's DNS resolver is on loopback (a VPN client?), which AppContainers can't reach")
	}
	return in
}

func (s *appContainerSandbox) Prepare(cmd *exec.Cmd, p Policy) (Process, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	ws, err := filepath.Abs(p.Writable)
	if err != nil {
		return nil, err
	}
	sid, err := s.profile(ws)
	if err != nil {
		return nil, err
	}
	// Grants: the workspace and private home writable, read paths and the
	// program's own folder readable.
	if err := s.ensureGrant(sid, ws, "M"); err != nil {
		return nil, err
	}
	home := p.Home
	if home == "" {
		home = filepath.Join(ws, ".gubern-home")
	}
	tmp := filepath.Join(home, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return nil, fmt.Errorf("sandbox: home folder: %w", err)
	}
	if err := s.ensureGrant(sid, home, "M"); err != nil {
		return nil, err
	}
	for _, r := range p.ReadPaths {
		if fi, err := os.Stat(r); err == nil && fi.IsDir() {
			if err := s.ensureGrant(sid, r, "RX"); err != nil {
				return nil, err
			}
		}
	}
	if prog := filepath.Dir(cmd.Path); filepath.IsAbs(prog) && !systemReadable(prog) {
		if err := s.ensureGrant(sid, prog, "RX"); err != nil {
			return nil, err
		}
	}
	var caps []windows.SIDAndAttributes
	if p.Network == NetworkOn {
		caps = append(caps, windows.SIDAndAttributes{Sid: acInternetClientSID, Attributes: windows.SE_GROUP_ENABLED})
	}
	tok, err := lowbox(sid, caps)
	if err != nil {
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)
	// No console: a process the server starts without one gets a new
	// console, whose host can't start inside the AppContainer (cmd.exe then
	// fails with STATUS_DLL_INIT_FAILED). Commands talk over pipes anyway.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, "TEMP="+tmp, "TMP="+tmp, "USERPROFILE="+home, "HOME="+home)
	jp, err := s.job.Prepare(cmd, p)
	if err != nil {
		_ = tok.Close()
		return nil, err
	}
	return &acProcess{Process: jp, token: tok}, nil
}

// acProcess closes the token once the command is done.
type acProcess struct {
	Process
	token windows.Token
	once  sync.Once
}

func (a *acProcess) Close() {
	a.Process.Close()
	a.once.Do(func() { _ = a.token.Close() })
}

// profile returns the AppContainer SID for a workspace, creating the
// profile on first use.
func (s *appContainerSandbox) profile(ws string) (*windows.SID, error) {
	h := sha256.Sum256([]byte(strings.ToLower(ws)))
	name := "gubern.ws." + hex.EncodeToString(h[:6])
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid := s.sids[name]; sid != nil {
		return sid, nil
	}
	n, _ := windows.UTF16PtrFromString(name)
	var sid *windows.SID
	hr, _, _ := acCreateProfile.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(n)), 0, 0, uintptr(unsafe.Pointer(&sid)))
	if hr == 0x800700B7 { // already exists
		hr, _, _ = acDeriveSID.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&sid)))
	}
	if hr != 0 || sid == nil {
		return nil, fmt.Errorf("sandbox: AppContainer profile %s: HRESULT 0x%x", name, hr)
	}
	copied, err := sid.Copy()
	_ = windows.FreeSid(sid)
	if err != nil {
		return nil, err
	}
	s.sids[name] = copied
	return copied, nil
}

// ensureGrant gives sid rights ("M" or "RX") on dir and its contents, once.
func (s *appContainerSandbox) ensureGrant(sid *windows.SID, dir, rights string) error {
	key := sid.String() + "|" + strings.ToLower(dir) + "|" + rights
	s.mu.Lock()
	done := s.grant[key]
	s.mu.Unlock()
	if done {
		return nil
	}
	out, err := exec.Command("icacls", dir, "/grant", "*"+sid.String()+":(OI)(CI)"+rights, "/q").CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox: granting %s on %s: %v %s", rights, dir, err, strings.TrimSpace(string(out)))
	}
	s.mu.Lock()
	s.grant[key] = true
	s.mu.Unlock()
	if s.grantLog != "" {
		if f, err := os.OpenFile(s.grantLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "%s\t%s\t%s\n", sid.String(), rights, dir)
			_ = f.Close()
		}
	}
	return nil
}

func lowbox(pkg *windows.SID, caps []windows.SIDAndAttributes) (windows.Token, error) {
	var self windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ALL_ACCESS, &self); err != nil {
		return 0, fmt.Errorf("sandbox: OpenProcessToken: %w", err)
	}
	defer self.Close()
	var capPtr uintptr
	if len(caps) > 0 {
		capPtr = uintptr(unsafe.Pointer(&caps[0]))
	}
	var tok windows.Token
	st, _, _ := acNtCreateLowBoxToken.Call(uintptr(unsafe.Pointer(&tok)), uintptr(self), uintptr(windows.TOKEN_ALL_ACCESS), 0,
		uintptr(unsafe.Pointer(pkg)), uintptr(len(caps)), capPtr, 0, 0)
	if st != 0 {
		return 0, fmt.Errorf("sandbox: NtCreateLowBoxToken: NTSTATUS 0x%x", st)
	}
	return tok, nil
}

// systemReadable reports folders every AppContainer can already read.
func systemReadable(dir string) bool {
	d := strings.ToLower(filepath.Clean(dir))
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "SystemRoot"} {
		if v := os.Getenv(env); v != "" && within(strings.ToLower(filepath.Clean(v)), d) {
			return true
		}
	}
	return false
}

// loopbackResolver reports whether a DNS server of this host is on
// loopback, which an AppContainer can't reach.
func loopbackResolver() bool {
	b, err := exec.Command("powershell", "-NoProfile", "-Command",
		"(Get-DnsClientServerAddress -AddressFamily IPv4).ServerAddresses -join ' '").Output()
	if err != nil {
		return false
	}
	for _, f := range strings.Fields(string(b)) {
		if ip := net.ParseIP(f); ip != nil && ip.IsLoopback() {
			return true
		}
	}
	return false
}
