package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sandeepbollavaram/vortex/internal/authz"
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
	// Pinned: installed through an approved install and recorded in
	// skills.lock. Tampered: pinned, but its files no longer match the
	// recorded hash; such a skill is not offered to the model.
	Pinned   bool   `json:"pinned,omitempty"`
	Tampered bool   `json:"tampered,omitempty"`
	Source   string `json:"source,omitempty"`
}

// SkillFile is one file of a skill being installed.
type SkillFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// SkillInstall is a fetched skill waiting for a person to approve it.
type SkillInstall struct {
	ApprovalID string      `json:"approval_id"`
	SessionID  string      `json:"session_id"`
	Source     string      `json:"source"`
	TreeHash   string      `json:"tree_hash"`
	Skill      SkillInfo   `json:"skill"`
	Files      []SkillFile `json:"files"`
}

// SkillInstaller is implemented by backends that can install skills: fetch
// into quarantine and open an approval; the install happens only if it is
// approved.
type SkillInstaller interface {
	PrepareInstall(ctx context.Context, source, sessionID, requestedBy string) (SkillInstall, error)
}

// ErrInstallInput marks an install request the caller can fix (bad source,
// invalid skill); its message is safe to show.
var ErrInstallInput = errors.New("skill install")

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

// handleSkillInstall serves POST /api/skills/install {"source": "..."}. The
// skill is fetched and hashed now; installing it needs an approval, which any
// approver (including a paired phone with step-up) can decide.
func (s *Server) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.skills.(SkillInstaller)
	if s.skills == nil || !ok {
		http.Error(w, "skill install not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil || strings.TrimSpace(req.Source) == "" {
		http.Error(w, `body must be {"source": "<folder or https git URL @ commit>"}`, http.StatusBadRequest)
		return
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	sessionID := "skill-install-" + hex.EncodeToString(b)
	// The requester owns the install's session, so they (and their paired
	// devices) can decide its approval.
	if !s.claim(w, r, authz.ApprovalsRead, resSession, sessionID) {
		return
	}
	res, err := inst.PrepareInstall(r.Context(), strings.TrimSpace(req.Source), sessionID, principalID(r))
	if errors.Is(err, ErrInstallInput) {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		http.Error(w, "preparing the install failed", http.StatusInternalServerError)
		return
	}
	s.audit(principalID(r), "skill.install_requested", res.Skill.Name, map[string]any{"source": res.Source, "tree_hash": res.TreeHash, "approval": res.ApprovalID})
	s.writeJSON(w, http.StatusAccepted, res)
}
