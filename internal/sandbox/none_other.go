//go:build !linux && !windows

package sandbox

import (
	"fmt"
	"runtime"
)

// macOS and the BSDs have no adapter yet: commands run unsandboxed, with the
// protections every platform gets (approval, path checks, a scrubbed
// environment, a process-group kill). Info says so.

func platformName() string {
	if runtime.GOOS == "darwin" {
		return "macOS"
	}
	return runtime.GOOS
}

func newPlatform(Config) (Sandbox, error) {
	return nil, fmt.Errorf("%w: no sandbox adapter for %s yet", ErrUnavailable, platformName())
}
