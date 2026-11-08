package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

// Run changes (Phase 4, M.1): the git checkpoints an /agent run took in its
// workspace, each step's diff, and restoring the workspace to a step. A
// restore only happens after an approval (HIGH risk), which a paired phone
// can decide with step-up.

// RunCheckpointInfo is one checkpoint of a run.
type RunCheckpointInfo struct {
	Step   int      `json:"step"`
	Commit string   `json:"commit"`
	Label  string   `json:"label"`
	At     string   `json:"at"`
	Files  []string `json:"files"`
}

// ErrNoCheckpoints: the run took none (its workspace isn't a git repository).
var ErrNoCheckpoints = errors.New("this run has no checkpoints")

// RunChangesBackend reads and restores runs' git checkpoints.
type RunChangesBackend interface {
	List(ctx context.Context, dir, runID string) ([]RunCheckpointInfo, error)
	Diff(ctx context.Context, dir, runID string, step int) (string, error)
	// PrepareRestore opens an approval to restore dir to step; the restore
	// runs if it is approved.
	PrepareRestore(ctx context.Context, dir, runID, sessionID string, step int, requestedBy string) (approvalID string, files []string, err error)
}

// SetRunChanges wires run checkpoints.
func (s *Server) SetRunChanges(b RunChangesBackend) { s.runChanges = b }

// runWorkspace is the folder a run worked in: the one its checkpoint
// recorded, else the server's default workspace.
func (s *Server) runWorkspace(runID string) string {
	if s.events != nil {
		if data, _, err := s.events.Checkpoint(runID); err == nil {
			var cp resumableCheckpoint
			if json.Unmarshal(data, &cp) == nil && cp.Workspace != "" {
				return cp.Workspace
			}
		}
	}
	if s.workspaces != nil {
		if dir, err := s.workspaces.Resolve(""); err == nil {
			return dir
		}
	}
	return ""
}

func (s *Server) handleRunChanges(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r, authz.SessionsRead)
	if !ok {
		return
	}
	if s.runChanges == nil {
		s.writeJSON(w, http.StatusOK, map[string]any{"checkpoints": []RunCheckpointInfo{}})
		return
	}
	list, err := s.runChanges.List(r.Context(), s.runWorkspace(run.ID), run.ID)
	if err != nil || list == nil {
		list = []RunCheckpointInfo{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"checkpoints": list})
}

func (s *Server) handleRunChangeDiff(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r, authz.SessionsRead)
	if !ok {
		return
	}
	step, err := strconv.Atoi(r.PathValue("step"))
	if err != nil || step < 0 || s.runChanges == nil {
		http.Error(w, "checkpoint not found", http.StatusNotFound)
		return
	}
	diff, err := s.runChanges.Diff(r.Context(), s.runWorkspace(run.ID), run.ID, step)
	if err != nil {
		http.Error(w, "checkpoint not found", http.StatusNotFound)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"step": step, "diff": diff})
}

func (s *Server) handleRunRestore(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r, authz.AgentsRun)
	if !ok {
		return
	}
	if s.runChanges == nil {
		http.Error(w, "checkpoints not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Step *int `json:"step"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Step == nil || *req.Step < 0 {
		http.Error(w, `body must be {"step": <checkpoint step>}`, http.StatusBadRequest)
		return
	}
	id, files, err := s.runChanges.PrepareRestore(r.Context(), s.runWorkspace(run.ID), run.ID, run.SessionID, *req.Step, principalID(r))
	if errors.Is(err, ErrNoCheckpoints) {
		s.writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s.audit(principalID(r), "run.restore_requested", run.ID, map[string]any{"step": *req.Step, "approval": id, "files": len(files)})
	s.writeJSON(w, http.StatusAccepted, map[string]any{"approval_id": id, "session_id": run.SessionID, "files": files})
}
