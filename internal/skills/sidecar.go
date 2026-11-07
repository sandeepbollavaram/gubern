package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The Gubern sidecar (ADR A4, decision D2): <skill>/agents/gubern.yaml holds
// fields only Gubern uses, so SKILL.md stays exactly the open spec and works
// unchanged in other tools. It is parsed with the same restricted YAML subset
// as the frontmatter, and, like the skill, it is knowledge: nothing in it can
// grant a tool or lower a risk.
//
//	display_name: Changelog writer
//	evals: evals.json        # eval tasks for `gubern skills eval`, inside the skill
//	tags: release, docs

// SidecarPath is the sidecar file inside a skill folder.
const SidecarPath = "agents/gubern.yaml"

const maxSidecarBytes = 16 << 10

// Sidecar is the parsed Gubern sidecar.
type Sidecar struct {
	DisplayName string
	Evals       string // slash path relative to the skill folder
	Tags        []string
}

var sidecarKeys = map[string]bool{"display_name": true, "evals": true, "tags": true}

// ReadSidecar loads <dir>/agents/gubern.yaml. A missing file is not an error.
func ReadSidecar(dir string) (Sidecar, bool, error) {
	p := filepath.Join(dir, filepath.FromSlash(SidecarPath))
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return Sidecar{}, false, nil
	}
	if err != nil {
		return Sidecar{}, false, err
	}
	if !fi.Mode().IsRegular() {
		return Sidecar{}, false, fmt.Errorf("%s must be a regular file", SidecarPath)
	}
	if fi.Size() > maxSidecarBytes {
		return Sidecar{}, false, fmt.Errorf("%s is larger than %d bytes", SidecarPath, maxSidecarBytes)
	}
	b, err := os.ReadFile(p) //nolint:gosec // inside a discovered skill folder
	if err != nil {
		return Sidecar{}, false, err
	}
	fields, _, err := parseFrontmatter(string(b))
	if err != nil {
		return Sidecar{}, false, fmt.Errorf("%s: %w", SidecarPath, err)
	}
	for k := range fields {
		if !sidecarKeys[k] {
			return Sidecar{}, false, fmt.Errorf("%s: unknown key %q", SidecarPath, k)
		}
	}
	sc := Sidecar{DisplayName: oneLine(fields["display_name"])}
	if len(sc.DisplayName) > 80 {
		return Sidecar{}, false, fmt.Errorf("%s: display_name is longer than 80 characters", SidecarPath)
	}
	if e := strings.TrimSpace(fields["evals"]); e != "" {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(e)))
		if strings.HasPrefix(e, "/") || filepath.IsAbs(filepath.FromSlash(e)) || filepath.VolumeName(filepath.FromSlash(e)) != "" || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(e, "\\") {
			return Sidecar{}, false, fmt.Errorf("%s: evals must be a path inside the skill", SidecarPath)
		}
		sc.Evals = clean
	}
	for _, t := range strings.Split(fields["tags"], ",") {
		if t = strings.TrimSpace(t); t != "" {
			sc.Tags = append(sc.Tags, t)
		}
	}
	return sc, true, nil
}
