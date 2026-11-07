package api

import (
	"errors"
	"net/http"
)

// Skills API (agent-platform Phase 2, docs/design/agent-platform-phases-2-5.md
// §2). Skills are knowledge, never authority: listing or reading one runs
// nothing, and turning one off only removes it from the catalog the model
// sees. Reads need agents:read; switching a skill on or off is an operator
// action (system:manage) and is audited.

// SkillInfo is one skill as the API shows it.
type SkillInfo struct {
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Scope         string   `json:"scope"`
	Path          string   `json:"path"`
	Hash          string   `json:"hash"`
	License       string   `json:"license,omitempty"`
	Compatibility string   `json:"compatibility,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty"` // requested, never granted
	Enabled       bool     `json:"enabled"`
}

// SkillProblem is a skill that failed to load.
type SkillProblem struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// ErrSkillNotFound is returned by a SkillsBackend for an unknown name.
var ErrSkillNotFound = errors.New("skill not found")

// SkillsBackend supplies skills to the API.
type SkillsBackend interface {
	List() ([]SkillInfo, []SkillProblem, error)
	// Get returns one skill and its SKILL.md body.
	Get(name string) (SkillInfo, string, error)
	SetEnabled(name string, enabled bool) error
}

// SetSkills wires the skills API. Without it the list is empty and the
// switches answer 503.
func (s *Server) SetSkills(b SkillsBackend) { s.skills = b }

func (s *Server) handleSkillsList(w http.ResponseWriter, _ *http.Request) {
	if s.skills == nil {
		s.writeJSON(w, http.StatusOK, map[string]any{"skills": []SkillInfo{}, "problems": []SkillProblem{}})
		return
	}
	list, problems, err := s.skills.List()
	if err != nil {
		http.Error(w, "listing skills failed", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []SkillInfo{}
	}
	if problems == nil {
		problems = []SkillProblem{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"skills": list, "problems": problems})
}

func (s *Server) handleSkillGet(w http.ResponseWriter, r *http.Request) {
	if s.skills == nil {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}
	info, body, err := s.skills.Get(r.PathValue("name"))
	if errors.Is(err, ErrSkillNotFound) {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "reading skill failed", http.StatusInternalServerError)
		return
	}
	// The body is repository text: served as data for display, never run.
	s.writeJSON(w, http.StatusOK, map[string]any{"skill": info, "body": body})
}

func (s *Server) handleSkillEnable(w http.ResponseWriter, r *http.Request) {
	s.setSkillEnabled(w, r, true)
}

func (s *Server) handleSkillDisable(w http.ResponseWriter, r *http.Request) {
	s.setSkillEnabled(w, r, false)
}

func (s *Server) setSkillEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if s.skills == nil {
		http.Error(w, "skills not available", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	info, _, err := s.skills.Get(name)
	if errors.Is(err, ErrSkillNotFound) {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "reading skill failed", http.StatusInternalServerError)
		return
	}
	if err := s.skills.SetEnabled(name, enabled); err != nil {
		http.Error(w, "updating skill failed", http.StatusInternalServerError)
		return
	}
	action := "skill.disabled"
	if enabled {
		action = "skill.enabled"
	}
	s.audit(principalID(r), action, name, map[string]any{"hash": info.Hash, "scope": info.Scope})
	info.Enabled = enabled
	s.writeJSON(w, http.StatusOK, map[string]any{"skill": info})
}
