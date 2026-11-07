package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/sandeepbollavaram/vortex/internal/approvals"
	"github.com/sandeepbollavaram/vortex/internal/authz"
)

// ApprovalStore reads the persistent approval service (milestone B).
type ApprovalStore interface {
	List(f approvals.Filter) ([]approvals.Approval, error)
	Get(id string) (approvals.Approval, error)
}

// SetApprovals wires the approval store behind GET /api/approvals.
func (s *Server) SetApprovals(st ApprovalStore) { s.approvalStore = st }

// approvalDecider is implemented by runtimes backed by the approval service:
// every decision goes through it, with the decider's identity.
type approvalDecider interface {
	DecideApproval(ctx context.Context, sessionID, approvalID string, approve bool, actionHash string, d approvals.Decider) (string, approvals.Approval, error)
}

// Errors an approvalDecider returns besides the approvals package's own.
var (
	ErrNoPendingApproval = errors.New("api: no pending approval for this session")
	ErrWrongSession      = errors.New("api: approval belongs to another session")
)

// clientChannels are the channel labels an API caller may report. They only
// label the decision; the identity is the authenticated principal.
var clientChannels = map[string]bool{"api": true, "dashboard": true, "mobile": true, "tui": true}

// writeApprovalError maps a decision error to a status and reason.
func (s *Server) writeApprovalError(w http.ResponseWriter, err error, a approvals.Approval) {
	status, reason := http.StatusInternalServerError, "error"
	switch {
	case errors.Is(err, approvals.ErrNotFound), errors.Is(err, ErrNoPendingApproval):
		status, reason = http.StatusNotFound, "not-found"
	case errors.Is(err, approvals.ErrNotPending):
		status, reason = http.StatusConflict, "not-pending"
	case errors.Is(err, approvals.ErrExpired):
		status, reason = http.StatusConflict, "expired"
	case errors.Is(err, approvals.ErrActionMismatch):
		status, reason = http.StatusConflict, "action-mismatch"
	case errors.Is(err, ErrWrongSession):
		status, reason = http.StatusConflict, "wrong-session"
	case errors.Is(err, approvals.ErrForbidden):
		status, reason = http.StatusForbidden, "forbidden"
	}
	body := map[string]any{"error": err.Error(), "reason": reason}
	if a.ID != "" {
		body["status"] = a.Status
	}
	s.writeJSON(w, status, body)
}

// handleApprovalsList serves GET /api/approvals?status=&session_id=&limit=.
func (s *Server) handleApprovalsList(w http.ResponseWriter, r *http.Request) {
	out := []approvals.Approval{}
	if s.approvalStore == nil {
		s.writeJSON(w, http.StatusOK, map[string]any{"approvals": out})
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	list, err := s.approvalStore.List(approvals.Filter{
		Status: approvals.Status(q.Get("status")), SessionID: q.Get("session_id"), Limit: limit,
	})
	if err != nil {
		http.Error(w, "listing approvals failed", http.StatusInternalServerError)
		return
	}
	for _, a := range list {
		if s.canAccess(r, authz.ApprovalsRead, resSession, a.SessionID) {
			out = append(out, a)
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"approvals": out})
}

// handleApprovalGet serves GET /api/approvals/{id}.
func (s *Server) handleApprovalGet(w http.ResponseWriter, r *http.Request) {
	if s.approvalStore == nil {
		http.Error(w, "approvals not configured", http.StatusServiceUnavailable)
		return
	}
	a, err := s.approvalStore.Get(r.PathValue("id"))
	if err != nil {
		s.writeApprovalError(w, err, approvals.Approval{})
		return
	}
	if !s.requireAccess(w, r, authz.ApprovalsRead, resSession, a.SessionID) {
		return
	}
	s.writeJSON(w, http.StatusOK, a)
}

// approvalDecisionRequest is the POST /api/approvals/{id}/decision body.
type approvalDecisionRequest struct {
	Approved bool `json:"approved"`
	// ActionHash is required: the client confirms the exact action (the
	// approval's action_hash) it showed the person.
	ActionHash string `json:"action_hash"`
	Reason     string `json:"reason,omitempty"`
	// Via labels the client (api, dashboard, mobile, tui).
	Via string `json:"via,omitempty"`
}

// handleApprovalDecision serves POST /api/approvals/{id}/decision.
func (s *Server) handleApprovalDecision(w http.ResponseWriter, r *http.Request) {
	dec, ok := s.agentRuntime.(approvalDecider)
	if !ok || s.agentRuntime == nil || s.approvalStore == nil {
		http.Error(w, "approvals not configured", http.StatusServiceUnavailable)
		return
	}
	allowLongResponse(w) // an approved action runs before the reply
	var req approvalDecisionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(req.ActionHash) != 64 {
		http.Error(w, "action_hash (the approval's 64-hex-digit action hash) is required", http.StatusBadRequest)
		return
	}
	if req.Via == "" {
		req.Via = "api"
	}
	if !clientChannels[req.Via] {
		http.Error(w, "via must be one of api, dashboard, mobile, tui", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	cur, err := s.approvalStore.Get(id)
	if err != nil {
		s.writeApprovalError(w, err, approvals.Approval{})
		return
	}
	if !s.requireAccess(w, r, authz.ApprovalsDecide, resSession, cur.SessionID) {
		return
	}
	tr, a, err := dec.DecideApproval(r.Context(), "", id, req.Approved, req.ActionHash,
		approvals.Decider{PrincipalID: principalID(r), Channel: req.Via})
	if err != nil {
		s.writeApprovalError(w, err, a)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"approval": a, "result": tr})
}
