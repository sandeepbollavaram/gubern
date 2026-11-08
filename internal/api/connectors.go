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
	"github.com/sandeepbollavaram/vortex/internal/connectors"
)

// MCP connectors (Phase 5, Milestone N). Listing needs agents:read; every
// change needs system:manage, and enabling a connector (which starts a
// program or opens a connection) also needs an approval, decided like any
// other in the dashboard, the app or a paired channel. Credentials are
// write-only. See docs/design/mcp-connector-architecture.md.

// ConnectorView is a connector with its live state and which of its
// credentials are set (never their values).
type ConnectorView struct {
	connectors.Connector
	Status     connectors.Status `json:"status"`
	SecretsSet map[string]bool   `json:"secrets_set"`
}

// ConnectorsBackend manages connectors.
type ConnectorsBackend interface {
	List(ctx context.Context) ([]ConnectorView, error)
	Get(ctx context.Context, id string) (ConnectorView, error)
	Add(ctx context.Context, c connectors.Connector) (ConnectorView, error)
	// Update replaces the editable fields; a changed transport setting
	// disables the connector (it must be approved again).
	Update(ctx context.Context, id string, c connectors.Connector) (ConnectorView, error)
	Remove(ctx context.Context, id string) error
	// PrepareEnable opens an approval; the connector is enabled and
	// connected once it is approved.
	PrepareEnable(ctx context.Context, id, sessionID, requestedBy string) (approvalID string, err error)
	Disable(ctx context.Context, id string) error
	Reconnect(ctx context.Context, id string) error
	SetToolPolicy(ctx context.Context, id, tool, policy string) error
	SetSecret(ctx context.Context, id, name, value string) error
}

// SetConnectors wires MCP connectors.
func (s *Server) SetConnectors(b ConnectorsBackend) { s.connectors = b }

func (s *Server) connectorsOr503(w http.ResponseWriter) bool {
	if s.connectors == nil {
		http.Error(w, "MCP connectors not available", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// connectorError maps backend errors: input problems are 400 with a safe
// message, a missing connector 404.
func (s *Server) connectorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, connectors.ErrNotFound):
		http.Error(w, "connector not found", http.StatusNotFound)
	case errors.Is(err, connectors.ErrInput):
		s.writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	default:
		http.Error(w, "the connector request failed", http.StatusInternalServerError)
	}
}

func (s *Server) handleConnectorsList(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	list, err := s.connectors.List(r.Context())
	if err != nil {
		s.connectorError(w, err)
		return
	}
	if list == nil {
		list = []ConnectorView{}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"connectors": list})
}

func (s *Server) handleConnectorGet(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	v, err := s.connectors.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.connectorError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, v)
}

func decodeConnector(w http.ResponseWriter, r *http.Request) (connectors.Connector, bool) {
	var c connectors.Connector
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		http.Error(w, "invalid connector JSON: "+err.Error(), http.StatusBadRequest)
		return c, false
	}
	return c, true
}

func (s *Server) handleConnectorAdd(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	c, ok := decodeConnector(w, r)
	if !ok {
		return
	}
	v, err := s.connectors.Add(r.Context(), c)
	if err != nil {
		s.connectorError(w, err)
		return
	}
	s.audit(principalID(r), "mcp.connector_added", v.ID, map[string]any{"transport": v.Transport, "command": v.Command, "url": v.URL})
	s.writeJSON(w, http.StatusCreated, v)
}

func (s *Server) handleConnectorUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	c, ok := decodeConnector(w, r)
	if !ok {
		return
	}
	v, err := s.connectors.Update(r.Context(), r.PathValue("id"), c)
	if err != nil {
		s.connectorError(w, err)
		return
	}
	s.audit(principalID(r), "mcp.connector_updated", v.ID, map[string]any{"version": v.Version, "enabled": v.Enabled})
	s.writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleConnectorRemove(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.connectors.Remove(r.Context(), id); err != nil {
		s.connectorError(w, err)
		return
	}
	s.audit(principalID(r), "mcp.connector_removed", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnectorEnable(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	id := r.PathValue("id")
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	sessionID := "mcp-enable-" + hex.EncodeToString(b)
	// The requester owns the approval's session, so they (and their paired
	// devices) can decide it.
	if !s.claim(w, r, authz.ApprovalsRead, resSession, sessionID) {
		return
	}
	approvalID, err := s.connectors.PrepareEnable(r.Context(), id, sessionID, principalID(r))
	if err != nil {
		s.connectorError(w, err)
		return
	}
	s.audit(principalID(r), "mcp.enable_requested", id, map[string]any{"approval": approvalID})
	s.writeJSON(w, http.StatusAccepted, map[string]any{"approval_id": approvalID, "session_id": sessionID})
}

func (s *Server) handleConnectorDisable(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.connectors.Disable(r.Context(), id); err != nil {
		s.connectorError(w, err)
		return
	}
	s.audit(principalID(r), "mcp.connector_disabled", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnectorReconnect(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	id := r.PathValue("id")
	err := s.connectors.Reconnect(r.Context(), id)
	if errors.Is(err, connectors.ErrNotFound) || errors.Is(err, connectors.ErrInput) {
		s.connectorError(w, err)
		return
	}
	v, _ := s.connectors.Get(r.Context(), id)
	if err != nil {
		// The connection failed; that is the connector's state, not a bad request.
		s.writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "connector": v})
		return
	}
	s.writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleConnectorToolPolicy(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	var req struct {
		Policy string `json:"policy"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		http.Error(w, `body must be {"policy": "ask" | "allow" | "deny"}`, http.StatusBadRequest)
		return
	}
	id, tool := r.PathValue("id"), r.PathValue("tool")
	if err := s.connectors.SetToolPolicy(r.Context(), id, tool, req.Policy); err != nil {
		s.connectorError(w, err)
		return
	}
	s.audit(principalID(r), "mcp.tool_policy_set", id, map[string]any{"tool": tool, "policy": req.Policy})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleConnectorSecret(w http.ResponseWriter, r *http.Request) {
	if !s.connectorsOr503(w) {
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || strings.TrimSpace(req.Value) == "" {
		http.Error(w, `body must be {"value": "<secret>"}`, http.StatusBadRequest)
		return
	}
	id, name := r.PathValue("id"), r.PathValue("name")
	if err := s.connectors.SetSecret(r.Context(), id, name, req.Value); err != nil {
		s.connectorError(w, err)
		return
	}
	// The value is never logged or returned.
	s.audit(principalID(r), "mcp.secret_set", id, map[string]any{"name": name})
	w.WriteHeader(http.StatusNoContent)
}
