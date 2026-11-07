package skills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/sandeepbollavaram/vortex/internal/userdir"
)

// The operator's per-skill switches. A disabled skill stays on disk but is
// not offered to the model: it is left out of the catalog every run reads.
// Turning a skill off is an operator decision, so it lives in the user's
// config directory, not in the repository the skill came from.

// stateMu serializes read-modify-write of the state file within a process.
var stateMu sync.Mutex

type stateFile struct {
	Disabled []string `json:"disabled"`
}

// StatePath is where the switches are kept for a user config dir.
func StatePath(userConfigDir string) string {
	return filepath.Join(userdir.App(userConfigDir), "skills-state.json")
}

// Disabled reads the set of disabled skill names. A missing file means none
// are disabled.
func Disabled(path string) (map[string]bool, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is the operator's own config file
	if errors.Is(err, os.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f stateFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make(map[string]bool, len(f.Disabled))
	for _, n := range f.Disabled {
		out[n] = true
	}
	return out, nil
}

// SetEnabled turns a skill on or off. The name is checked against the spec's
// naming rule so the file can only ever hold skill names.
func SetEnabled(path, name string, enabled bool) error {
	if !nameRE.MatchString(name) || len(name) > maxNameLen {
		return fmt.Errorf("invalid skill name %q", name)
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	cur, err := Disabled(path)
	if err != nil {
		return err
	}
	if enabled {
		delete(cur, name)
	} else {
		cur[name] = true
	}
	f := stateFile{Disabled: make([]string, 0, len(cur))}
	for n := range cur {
		f.Disabled = append(f.Disabled, n)
	}
	sort.Strings(f.Disabled)
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Write then rename, so a crash never leaves a half-written file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Enabled returns the skills that are not disabled, in their original order.
func Enabled(all []Skill, disabled map[string]bool) []Skill {
	if len(disabled) == 0 {
		return all
	}
	out := make([]Skill, 0, len(all))
	for _, s := range all {
		if !disabled[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

// Offered returns the skills the model may be shown: not switched off, and
// not a locked install whose files changed since it was approved.
func Offered(found []Skill, statePath, lockPath string) ([]Skill, error) {
	disabled, err := Disabled(statePath)
	if err != nil {
		return nil, err
	}
	lock, err := ReadLock(lockPath)
	if err != nil {
		return nil, err
	}
	for name := range Tampered(found, lock) {
		disabled[name] = true
	}
	return Enabled(found, disabled), nil
}
