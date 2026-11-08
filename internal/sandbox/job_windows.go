//go:build windows

package sandbox

import (
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows adapter puts each command in its own Job Object.
//
// The command is created suspended, assigned to the job, then resumed, so
// nothing it starts can slip out before the job applies. The job:
//   - kills every process in it when the command exits, is stopped, or the
//     job is closed (TerminateJobObject, KILL_ON_JOB_CLOSE), so a timeout
//     ends the whole tree, including processes started in the background;
//   - caps the number of processes at once (ACTIVE_PROCESS), which stops a
//     fork bomb;
//   - caps the memory committed by the whole tree (JOB_MEMORY).
//
// A job does not fence the file system or the network: the command runs as
// the server's user and can reach what that user can. That is containment,
// not isolation, and Info says so. (An AppContainer would fence both; it is
// a separate, later step.)

func platformName() string { return "Windows" }

var (
	ntdll           = windows.NewLazySystemDLL("ntdll.dll")
	ntResumeProcess = ntdll.NewProc("NtResumeProcess")
)

type jobSandbox struct{ cfg Config }

func newPlatform(cfg Config) (Sandbox, error) {
	if err := ntResumeProcess.Find(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// A job can be created here (nested jobs need Windows 8 or later).
	j, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: CreateJobObject: %v", ErrUnavailable, err)
	}
	_ = windows.CloseHandle(j)
	return &jobSandbox{cfg: cfg}, nil
}

func (s *jobSandbox) Info() Info {
	in := Info{
		Adapter: "job-object", Level: LevelContained, Platform: "Windows", Mode: s.cfg.Mode,
		Network: string(s.cfg.Network),
		Enforced: []string{
			"every process the command starts is killed with it",
			"at most " + strconv.Itoa(s.cfg.MaxProcesses) + " processes",
		},
		NotEnforced: []string{
			"file system fence (the command can write wherever the server's user can)",
			"home folder and server state are visible",
		},
	}
	if s.cfg.MemoryMB > 0 {
		in.Enforced = append(in.Enforced, "at most "+strconv.Itoa(s.cfg.MemoryMB)+" MB of memory")
	}
	if s.cfg.Network == NetworkOff {
		in.Network = "not enforced"
		in.NotEnforced = append(in.NotEnforced, "network off (Windows needs an AppContainer or firewall rule for this)")
	} else {
		in.NotEnforced = append(in.NotEnforced, "network policy")
	}
	return in
}

func (s *jobSandbox) Prepare(cmd *exec.Cmd, p Policy) (Process, error) {
	if cmd.Err != nil {
		return nil, cmd.Err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("sandbox: CreateJobObject: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	if p.MaxProcesses > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = uint32(p.MaxProcesses)
	}
	if p.MemoryMB > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_JOB_MEMORY
		info.JobMemoryLimit = uintptr(p.MemoryMB) << 20
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("sandbox: SetInformationJobObject: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return &jobProcess{job: job}, nil
}

type jobProcess struct {
	mu     sync.Mutex
	job    windows.Handle
	closed bool
}

// Started assigns the suspended process to the job, then resumes it.
func (j *jobProcess) Started(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("sandbox: the command did not start")
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE,
		false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("sandbox: OpenProcess: %w", err)
	}
	if err := windows.AssignProcessToJobObject(j.job, h); err != nil {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
		return fmt.Errorf("sandbox: AssignProcessToJobObject: %w", err)
	}
	if r, _, _ := ntResumeProcess.Call(uintptr(h)); r != 0 {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
		return fmt.Errorf("sandbox: NtResumeProcess: status 0x%x", r)
	}
	// The tree dies with the command: once the command's own process exits,
	// end whatever it left running. Otherwise a background process holding
	// the output pipe would keep the call waiting until WaitDelay.
	go func() {
		defer func() { _ = windows.CloseHandle(h) }()
		if ev, _ := windows.WaitForSingleObject(h, windows.INFINITE); ev == windows.WAIT_OBJECT_0 {
			j.Kill()
		}
	}()
	return nil
}

// Kill ends every process in the job.
func (j *jobProcess) Kill() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.closed {
		_ = windows.TerminateJobObject(j.job, 1)
	}
}

// Close closes the job; KILL_ON_JOB_CLOSE ends anything still running.
func (j *jobProcess) Close() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.closed {
		j.closed = true
		_ = windows.CloseHandle(j.job)
	}
}
