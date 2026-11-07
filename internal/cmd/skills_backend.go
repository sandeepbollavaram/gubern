package cmd

import (
	"path/filepath"
	"strings"

	"github.com/sandeepbollavaram/vortex/internal/api"
	agentskills "github.com/sandeepbollavaram/vortex/internal/skills"
)

// skillsBackend serves the workspace's skills to the API: the same set /agent
// offers the model (workspace skills, re-read on every call, so edits show
// without a restart), with the operator's on/off switches.
type skillsBackend struct {
	workspace string
	statePath string
}

func newSkillsBackend(workspace, userConfigDir string) skillsBackend {
	return skillsBackend{workspace: workspace, statePath: agentskills.StatePath(userConfigDir)}
}

func (b skillsBackend) discover() ([]agentskills.Skill, []agentskills.Problem, map[string]bool, error) {
	if b.workspace == "" {
		return nil, nil, map[string]bool{}, nil
	}
	found, problems := agentskills.Discover(agentskills.DefaultRoots(b.workspace, ""))
	disabled, err := agentskills.Disabled(b.statePath)
	return found, problems, disabled, err
}

func (b skillsBackend) info(s agentskills.Skill, disabled map[string]bool) api.SkillInfo {
	return api.SkillInfo{
		Name:          s.Name,
		Description:   s.Description,
		Scope:         s.Scope,
		Path:          b.rel(filepath.Join(s.Dir, "SKILL.md")),
		Hash:          s.Hash,
		License:       s.License,
		Compatibility: s.Compatibility,
		AllowedTools:  s.AllowedTools,
		Enabled:       !disabled[s.Name],
	}
}

// rel shows a path relative to the workspace, so the API doesn't leak the
// server's directory layout for files inside it.
func (b skillsBackend) rel(p string) string {
	if r, err := filepath.Rel(b.workspace, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func (b skillsBackend) List() ([]api.SkillInfo, []api.SkillProblem, error) {
	found, problems, disabled, err := b.discover()
	if err != nil {
		return nil, nil, err
	}
	out := make([]api.SkillInfo, 0, len(found))
	for _, s := range found {
		out = append(out, b.info(s, disabled))
	}
	probs := make([]api.SkillProblem, 0, len(problems))
	for _, p := range problems {
		probs = append(probs, api.SkillProblem{Path: b.rel(p.Path), Error: p.Err.Error()})
	}
	return out, probs, nil
}

func (b skillsBackend) Get(name string) (api.SkillInfo, string, error) {
	found, _, disabled, err := b.discover()
	if err != nil {
		return api.SkillInfo{}, "", err
	}
	for _, s := range found {
		if s.Name != name {
			continue
		}
		body, err := s.Body()
		if err != nil {
			return api.SkillInfo{}, "", err
		}
		return b.info(s, disabled), body, nil
	}
	return api.SkillInfo{}, "", api.ErrSkillNotFound
}

func (b skillsBackend) SetEnabled(name string, enabled bool) error {
	return agentskills.SetEnabled(b.statePath, name, enabled)
}
