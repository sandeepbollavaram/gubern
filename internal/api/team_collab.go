// This file implements the agent-team collaboration API (build plan AG-UI File
// 6): live visibility into agent-to-agent communication, direct chat with a
// named specialist, and human checkpoint review. These back the three-panel
// `gubern code` view and the dashboard.
//
// The handlers depend only on small provider interfaces defined here, so the
// api package stays decoupled from the a2a package (the adapters are wired in
// start.go). All endpoints are data-plane and require an API key.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

// CommsRecord is one agent-communication entry exposed over the API. It mirrors
// a2a.BusMessage without importing it.
type CommsRecord struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
}

// CheckpointRecord is a pending/decided checkpoint exposed over the API.
type CheckpointRecord struct {
	ID          string                 `json:"id"`
	SessionID   string                 `json:"session_id"`
	FromAgent   string                 `json:"from_agent"`
	ToAgent     string                 `json:"to_agent"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Status      string                 `json:"status"`
	Files       []CheckpointFileRecord `json:"files"`
	CreatedAt   time.Time              `json:"created_at"`
}

// CheckpointFileRecord is one file produced at a checkpoint.
type CheckpointFileRecord struct {
	Path  string `json:"path"`
	Lines int    `json:"lines"`
	IsNew bool   `json:"is_new"`
}

// CommsProvider supplies the agent-communication feed: recent history, a live
// subscription for SSE, and per-agent history. Implemented by an adapter over
// *a2a.MessageBus.
type CommsProvider interface {
	History(limit int) []CommsRecord
	Subscribe() (<-chan CommsRecord, func())
	// AgentMessages returns up to limit recent messages to or from agentID.
	AgentMessages(agentID string, limit int) []CommsRecord
}

// ChatProvider routes a direct-chat message to a named specialist and returns
// its reply. Implemented by an adapter over *a2a.AgentServer.
type ChatProvider interface {
	Chat(ctx context.Context, agentID, sessionID, message string) (string, error)
}

// CheckpointFileEdit is one file edited by the user at a checkpoint.
type CheckpointFileEdit struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CheckpointProvider lists and resolves human checkpoints. Implemented by an
// adapter over *a2a.CheckpointManager.
type CheckpointProvider interface {
	List() []CheckpointRecord
	Approve(id string) error
	Reject(id, reason string) error
	Edit(id string, edits []CheckpointFileEdit) error
}

// SetCommsProvider wires the agent-communication feed (GET /api/agents/comms,
// /api/agents/comms/stream). Nil yields an empty feed.
func (s *Server) SetCommsProvider(p CommsProvider) { s.comms = p }

// SetChatProvider wires direct chat (POST /api/agents/{id}/chat). Nil yields 503.
func (s *Server) SetChatProvider(p ChatProvider) { s.agentChat = p }

// SetCheckpointProvider wires checkpoint review (GET/POST /api/checkpoints).
// Nil yields an empty list / 503.
func (s *Server) SetCheckpointProvider(p CheckpointProvider) { s.checkpoints = p }

// registerTeamCollab attaches the collaboration routes to the mux. Called from
// NewServer's route table.
func (s *Server) registerTeamCollab(mux *http.ServeMux) {
	s.route(mux, "GET /api/agents/team/status", http.HandlerFunc(s.handleTeamAgents))
	s.route(mux, "GET /api/agents/comms", http.HandlerFunc(s.handleComms))
	s.route(mux, "GET /api/agents/comms/stream", http.HandlerFunc(s.handleCommsStream))
	s.route(mux, "GET /api/agents/team/{id}/messages", http.HandlerFunc(s.handleAgentMessages))
	s.route(mux, "POST /api/agents/{id}/chat", http.HandlerFunc(s.handleAgentDirectChat))
	s.route(mux, "GET /api/checkpoints", http.HandlerFunc(s.handleCheckpointsList))
	s.route(mux, "POST /api/checkpoints/{id}/approve", http.HandlerFunc(s.handleCheckpointApprove))
	s.route(mux, "POST /api/checkpoints/{id}/reject", http.HandlerFunc(s.handleCheckpointReject))
	s.route(mux, "POST /api/checkpoints/{id}/edit", http.HandlerFunc(s.handleCheckpointEdit))
}

// handleComms returns the recent agent-communication feed as JSON.
func (s *Server) handleComms(w http.ResponseWriter, r *http.Request) {
	// The feed mixes every session's traffic: it needs sessions:read on "*".
	if !s.requireAll(w, r, authz.SessionsRead) {
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	feed := []CommsRecord{}
	if s.comms != nil {
		feed = s.comms.History(limit)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"messages": feed})
}

// handleAgentMessages returns the recent messages to or from one agent — the
// per-agent slice of the comms feed (e.g. a dashboard/TUI drill-down on a
// single specialist). The id path segment names the agent.
func (s *Server) handleAgentMessages(w http.ResponseWriter, r *http.Request) {
	// The feed mixes every session's traffic: it needs sessions:read on "*".
	if !s.requireAll(w, r, authz.SessionsRead) {
		return
	}
	agentID := r.PathValue("id")
	if agentID == "" {
		http.Error(w, "agent id required", http.StatusBadRequest)
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	msgs := []CommsRecord{}
	if s.comms != nil {
		msgs = s.comms.AgentMessages(agentID, limit)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"agent_id": agentID, "messages": msgs})
}

// handleCommsStream streams agent-communication messages as Server-Sent Events.
func (s *Server) handleCommsStream(w http.ResponseWriter, r *http.Request) {
	// The feed mixes every session's traffic: it needs sessions:read on "*".
	if !s.requireAll(w, r, authz.SessionsRead) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	if s.comms == nil {
		http.Error(w, "agent comms not configured", http.StatusServiceUnavailable)
		return
	}
	// Long-lived SSE feed: without this the server's WriteTimeout severs the
	// stream ~60s in, silently dropping events until the client reconnects.
	allowLongResponse(w)
	ch, unsub := s.comms.Subscribe()
	defer unsub()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Replay recent history first so a late subscriber has context.
	for _, rec := range s.comms.History(50) {
		writeCommsEvent(w, rec)
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case rec, open := <-ch:
			if !open {
				return
			}
			writeCommsEvent(w, rec)
			flusher.Flush()
		}
	}
}

// writeCommsEvent writes one comms record as an SSE data frame.
func writeCommsEvent(w http.ResponseWriter, rec CommsRecord) {
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// handleAgentDirectChat routes a direct-chat message to a specialist.
func (s *Server) handleAgentDirectChat(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if s.agentChat == nil {
		http.Error(w, "direct chat not configured", http.StatusServiceUnavailable)
		return
	}
	// Direct chat is an AI generation; it can outlive the 60s WriteTimeout.
	allowLongResponse(w)
	var req struct {
		SessionID string `json:"session_id"`
		Message   string `json:"message"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, "message required", http.StatusBadRequest)
		return
	}
	// Like submit: no shared empty session, and the session is the caller's.
	if req.SessionID == "" {
		req.SessionID = newSessionID()
	} else if !validSessionID(req.SessionID) {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}
	if !s.claim(w, r, authz.AgentsRun, resSession, req.SessionID) {
		return
	}
	reply, err := s.agentChat.Chat(r.Context(), agentID, req.SessionID, req.Message)
	if err != nil {
		http.Error(w, "chat failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"agent_id":   agentID,
		"session_id": req.SessionID,
		"response":   reply,
		"timestamp":  time.Now(),
	})
}

// handleCheckpointsList returns pending/decided checkpoints.
func (s *Server) handleCheckpointsList(w http.ResponseWriter, r *http.Request) {
	list := []CheckpointRecord{}
	if s.checkpoints != nil {
		for _, c := range s.checkpoints.List() {
			if s.canAccess(r, authz.ApprovalsRead, resSession, c.SessionID) {
				list = append(list, c)
			}
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"checkpoints": list})
}

// handleCheckpointApprove approves a pending checkpoint, unblocking the pipeline.
func (s *Server) handleCheckpointApprove(w http.ResponseWriter, r *http.Request) {
	if s.checkpoints == nil {
		http.Error(w, "checkpoints not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if !s.checkpointAccess(w, r, id) {
		return
	}
	if err := s.checkpoints.Approve(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "approved"})
}

// handleCheckpointReject rejects a pending checkpoint with an optional reason.
func (s *Server) handleCheckpointReject(w http.ResponseWriter, r *http.Request) {
	if s.checkpoints == nil {
		http.Error(w, "checkpoints not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if !s.checkpointAccess(w, r, id) {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	_ = json.NewDecoder(r.Body).Decode(&req) // reason is optional
	if err := s.checkpoints.Reject(id, req.Reason); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "rejected"})
}

// handleCheckpointEdit applies user file edits and resolves the checkpoint as
// edited, unblocking the pipeline with the edited content.
func (s *Server) handleCheckpointEdit(w http.ResponseWriter, r *http.Request) {
	if s.checkpoints == nil {
		http.Error(w, "checkpoints not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if !s.checkpointAccess(w, r, id) {
		return
	}
	var req struct {
		Files []CheckpointFileEdit `json:"files"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Files) == 0 {
		http.Error(w, "files required", http.StatusBadRequest)
		return
	}
	if err := s.checkpoints.Edit(id, req.Files); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"id": id, "status": "edited"})
}

// checkpointAccess checks the caller may decide checkpoint id: it follows the
// owner of the checkpoint's session. An unknown id is a 404.
func (s *Server) checkpointAccess(w http.ResponseWriter, r *http.Request, id string) bool {
	for _, c := range s.checkpoints.List() {
		if c.ID == id {
			return s.requireAccess(w, r, authz.ApprovalsDecide, resSession, c.SessionID)
		}
	}
	http.Error(w, "checkpoint not found", http.StatusNotFound)
	return false
}

// parseLimit parses a positive integer query parameter, falling back to def.
func parseLimit(raw string, def int) int {
	if raw == "" {
		return def
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return def
	}
	return n
}
