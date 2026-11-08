package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sandeepbollavaram/vortex/internal/sandbox"
)

// Workspaces: the folders agent runs can work in. The server's default
// folder is always one; an operator (workspaces:manage) adds others. A
// submit picks one by name, and the API resolves the name to the folder, so
// a client never hands the agent an arbitrary path. Listing needs
// agents:read; changes are audited.

// WorkspaceInfo is one workspace.
type WorkspaceInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Default bool   `json:"default,omitempty"`
	Exists  bool   `json:"exists"`
}

// Errors a WorkspacesBackend returns.
var (
	ErrWorkspaceNotFound = errors.New("workspace not found")
	// ErrWorkspaceInput marks a request the caller can fix; its message is
	// safe to show.
	ErrWorkspaceInput = errors.New("workspace")
)

// WorkspacesBackend keeps the workspace list.
type WorkspacesBackend interface {
	List() ([]WorkspaceInfo, error)
	// Resolve returns the folder of the named workspace ("" = default).
	Resolve(name string) (string, error)
	Add(name, path string) (WorkspaceInfo, error)
	Remove(name string) error
}

// SetWorkspaces wires the workspace list.
func (s *Server) SetWorkspaces(b WorkspacesBackend) { s.workspaces = b }

// SetSandboxInfo records the boundary terminal commands run in (Phase 4), so
// the Workspaces page can say what it does and doesn't do on this server.
func (s *Server) SetSandboxInfo(info sandbox.Info) { s.sandboxInfo = &info }

func (s *Server) handleWorkspacesList(w http.ResponseWriter, _ *http.Request) {
	body := map[string]any{"workspaces": []WorkspaceInfo{}}
	if s.sandboxInfo != nil {
		info := *s.sandboxInfo
		body["sandbox"] = map[string]any{
			"adapter": info.Adapter, "level": info.Level, "platform": info.Platform, "mode": info.Mode,
			"network": info.Network, "enforced": info.Enforced, "not_enforced": info.NotEnforced,
			"reason": info.Reason, "label": info.Label(),
		}
	}
	if s.workspaces == nil {
		s.writeJSON(w, http.StatusOK, body)
		return
	}
	list, err := s.workspaces.List()
	if err != nil {
		http.Error(w, "listing workspaces failed", http.StatusInternalServerError)
		return
	}
	body["workspaces"] = list
	s.writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleWorkspaceAdd(w http.ResponseWriter, r *http.Request) {
	if s.workspaces == nil {
		http.Error(w, "workspaces not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		http.Error(w, `body must be {"path": "<absolute folder>", "name": "<optional>"}`, http.StatusBadRequest)
		return
	}
	ws, err := s.workspaces.Add(strings.TrimSpace(req.Name), strings.TrimSpace(req.Path))
	if errors.Is(err, ErrWorkspaceInput) {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		http.Error(w, "adding the workspace failed", http.StatusInternalServerError)
		return
	}
	s.audit(principalID(r), "workspace.added", ws.Name, map[string]any{"path": ws.Path})
	s.writeJSON(w, http.StatusCreated, map[string]any{"workspace": ws})
}

func (s *Server) handleWorkspaceRemove(w http.ResponseWriter, r *http.Request) {
	if s.workspaces == nil {
		http.Error(w, "workspaces not available", http.StatusServiceUnavailable)
		return
	}
	name := r.PathValue("name")
	err := s.workspaces.Remove(name)
	switch {
	case errors.Is(err, ErrWorkspaceNotFound):
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	case errors.Is(err, ErrWorkspaceInput):
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	case err != nil:
		http.Error(w, "removing the workspace failed", http.StatusInternalServerError)
		return
	}
	s.audit(principalID(r), "workspace.removed", name, nil)
	w.WriteHeader(http.StatusNoContent)
}
