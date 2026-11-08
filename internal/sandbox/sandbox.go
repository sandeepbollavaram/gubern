// Package sandbox runs an agent's terminal commands inside an operating
// system boundary (Phase 4, M.2).
//
// The boundary differs by platform, and Info says which one applies in plain
// words, so nothing claims more than it does:
//
//   - Linux: bubblewrap. The command sees a read-only system, the workspace
//     as the only writable folder, no home directory, its own process and
//     network namespaces, and a cap on how many processes it can start.
//     This is isolation.
//   - Windows: a Job Object. Every process the command starts is in the job,
//     so the whole tree is killed together, and the job caps memory and the
//     number of processes. The file system and the network are not fenced:
//     this is containment, not isolation.
//   - Other platforms: none. Commands run as before (approval, path checks,
//     a scrubbed environment and a process-group kill still apply).
package sandbox

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Mode is how the operator asked for commands to be sandboxed.
type Mode string

const (
	// ModeAuto uses the platform's adapter when it is available and runs
	// unsandboxed (saying so) when it isn't.
	ModeAuto Mode = "auto"
	// ModeRequired refuses to start the server when the platform's adapter
	// isn't available.
	ModeRequired Mode = "required"
	// ModeOff runs commands without a sandbox.
	ModeOff Mode = "off"
)

// Network is the network a sandboxed command gets.
type Network string

const (
	NetworkOn  Network = "on"
	NetworkOff Network = "off"
)

// Level is how strong the boundary is.
type Level string

const (
	LevelIsolated  Level = "isolated"
	LevelContained Level = "contained"
	LevelNone      Level = "none"
)

// Limits are the operator's caps for every sandboxed command.
type Limits struct {
	// MaxProcesses caps how many processes a command may have at once.
	MaxProcesses int
	// MemoryMB caps the memory of the whole process tree (0 = no cap).
	MemoryMB int
	// ReadPaths are extra host folders a command may read (toolchains
	// installed outside the system folders, for example).
	ReadPaths []string
	// Home is a private home folder for commands (Linux), kept between
	// commands so tool caches survive; never the user's real home. Empty
	// gives each command a fresh, empty one.
	Home string
}

// Policy is the boundary for one command.
type Policy struct {
	// Writable is the only folder the command may change: the workspace.
	Writable string
	// Dir is the command's working directory, inside Writable.
	Dir     string
	Network Network
	Limits
}

// Info describes the adapter in effect, for the API and the run record.
type Info struct {
	Adapter  string `json:"adapter"`
	Level    Level  `json:"level"`
	Platform string `json:"platform"`
	Mode     Mode   `json:"mode"`
	// Network is what a command's network actually is: "off", "on", or
	// "not enforced" when the policy can't be applied on this platform.
	Network string `json:"network"`
	// Enforced lists what the boundary does, in plain words.
	Enforced []string `json:"enforced"`
	// NotEnforced lists what it does not do on this platform.
	NotEnforced []string `json:"not_enforced"`
	// Reason says why there is no sandbox, when there isn't one.
	Reason string `json:"reason,omitempty"`
}

// Label is the short badge text, e.g. "Isolated (Linux)".
func (i Info) Label() string {
	switch i.Level {
	case LevelIsolated:
		return "Isolated (" + i.Platform + ")"
	case LevelContained:
		return "Contained (" + i.Platform + ")"
	default:
		return "Not sandboxed"
	}
}

// Sandbox wraps commands in the platform's boundary.
type Sandbox interface {
	Info() Info
	// Prepare changes cmd (before Start) so it runs inside the boundary.
	// The caller then starts cmd and calls Started on the result.
	Prepare(cmd *exec.Cmd, p Policy) (Process, error)
}

// Process is one prepared command.
type Process interface {
	// Started finishes setting up the boundary once cmd has started. If it
	// fails, the command has been killed.
	Started(cmd *exec.Cmd) error
	// Kill stops the command and everything it started.
	Kill()
	// Close releases the boundary after the command has exited.
	Close()
}

// ErrUnavailable means the platform's adapter can't run here.
var ErrUnavailable = errors.New("sandbox unavailable")

// Config is the operator's agents.local_tools.sandbox section.
type Config struct {
	Mode    Mode
	Network Network
	// Windows picks the Windows adapter: "job" (default; containment) or
	// "appcontainer" (isolation; see docs/design/windows-appcontainer-sandbox.md).
	Windows string
	Limits
}

// DefaultLimits are used when the operator sets none.
var DefaultLimits = Limits{MaxProcesses: 256, MemoryMB: 8192}

// New returns the sandbox for this platform under cfg, and its Info. With
// ModeOff, or ModeAuto when the adapter is unavailable, the Sandbox is nil
// and Info says why. With ModeRequired and no adapter it returns an error.
func New(cfg Config) (Sandbox, Info, error) {
	if cfg.Mode == "" {
		cfg.Mode = ModeAuto
	}
	if cfg.Network == "" {
		cfg.Network = NetworkOn
	}
	if cfg.MaxProcesses <= 0 {
		cfg.MaxProcesses = DefaultLimits.MaxProcesses
	}
	if cfg.MemoryMB < 0 {
		cfg.MemoryMB = 0
	}
	switch cfg.Mode {
	case ModeAuto, ModeRequired, ModeOff:
	default:
		return nil, Info{}, fmt.Errorf("sandbox mode %q: want auto, required or off", cfg.Mode)
	}
	switch cfg.Network {
	case NetworkOn, NetworkOff:
	default:
		return nil, Info{}, fmt.Errorf("sandbox network %q: want on or off", cfg.Network)
	}
	switch cfg.Windows {
	case "", "job", "appcontainer":
	default:
		return nil, Info{}, fmt.Errorf("sandbox windows %q: want job or appcontainer", cfg.Windows)
	}
	if cfg.Mode == ModeOff {
		return nil, noneInfo(cfg, "turned off in agents.local_tools.sandbox"), nil
	}
	sb, err := newPlatform(cfg)
	if err != nil {
		if cfg.Mode == ModeRequired {
			return nil, Info{}, fmt.Errorf("agents.local_tools.sandbox is required: %w", err)
		}
		return nil, noneInfo(cfg, err.Error()), nil
	}
	return sb, sb.Info(), nil
}

func noneInfo(cfg Config, reason string) Info {
	return Info{
		Adapter: "none", Level: LevelNone, Platform: platformName(), Mode: cfg.Mode,
		Network: "not enforced",
		Enforced: []string{
			"approval before every command",
			"working folder inside the workspace",
			"server secrets removed from the environment",
			"the command's process group is killed on timeout",
		},
		NotEnforced: []string{"file system fence", "network policy", "process and memory caps"},
		Reason:      reason,
	}
}

// within reports whether path is dir or inside it (both cleaned, absolute).
func within(dir, path string) bool {
	if path == dir {
		return true
	}
	sep := "/"
	if strings.Contains(dir, `\`) {
		sep = `\`
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dir, sep)+sep)
}
