package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// createKeyRequest is the POST /api/keys body.
type createKeyRequest struct {
	UserID      string      `json:"user_id"`
	OrgID       string      `json:"org_id"`
	Roles       []auth.Role `json:"roles"`
	Description string      `json:"description"`
	TTLSeconds  int64       `json:"ttl_seconds"` // 0 = never expires
	// RateLimitRPM sets a custom per-minute request budget for this key's
	// identity: 0 keeps the server default, negative makes it unlimited.
	RateLimitRPM int `json:"rate_limit_rpm"`
}

// createKeyResponse returns the new key's public fields plus the one-time
// plaintext secret.
type createKeyResponse struct {
	ID        string      `json:"id"`
	Secret    string      `json:"secret"` // shown once; never retrievable again
	UserID    string      `json:"user_id"`
	OrgID     string      `json:"org_id"`
	Roles     []auth.Role `json:"roles"`
	ExpiresAt time.Time   `json:"expires_at,omitempty"`
	// PrincipalID is the service principal the key authenticates as.
	PrincipalID string `json:"principal_id,omitempty"`
}

// handleListKeys returns the API keys for the org named in the ?org= query
// parameter (defaulting to "default"). Hashes are never included.
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "key store not configured"})
		return
	}
	org := r.URL.Query().Get("org")
	if org == "" {
		org = "default"
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"keys": s.keys.List(org)})
}

// handleCreateKey issues a new API key. The plaintext secret is returned exactly
// once in the response and never stored.
func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "key store not configured"})
		return
	}
	var req createKeyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.OrgID == "" {
		req.OrgID = "default"
	}
	// Milestone A: a new key gets its own service principal whose grants come
	// from the requested role templates, and the caller must already hold
	// every one of them (no escalation). Persons get credentials only through
	// bootstrap, owner recovery or the local bind-key command, never here.
	msg := s.checkKeyRoles(r, req.Roles)
	if msg == "" && identity.IsBootstrapDescription(strings.TrimSpace(req.Description)) {
		msg = "description " + strconv.Quote(req.Description) + " is reserved for keys created by gubern setup"
	}
	if msg != "" {
		status := http.StatusBadRequest
		if strings.HasPrefix(msg, "cannot grant") {
			status = http.StatusForbidden
		}
		s.writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	key, secret, err := s.keys.Issue(req.UserID, req.OrgID, req.Roles, req.Description, ttl)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "issuing key: " + err.Error()})
		return
	}
	principal, err := s.bindNewKey(r, key, req.Roles)
	if err != nil {
		// Never leave a usable key without its principal.
		_ = s.keys.Revoke(key.ID)
		s.log.Error("binding new key failed; key revoked", "key_id", key.ID, "err", err)
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "binding key to a principal failed"})
		return
	}
	if req.RateLimitRPM != 0 && s.keyLimiter != nil {
		s.keyLimiter.SetKeyLimit(key.UserID, req.RateLimitRPM)
	}
	s.audit(clientIP(r), "apikey.create", key.ID, map[string]any{
		"org": key.OrgID, "user": key.UserID, "principal": principal, "by": principalID(r),
	})
	s.writeJSON(w, http.StatusCreated, createKeyResponse{
		ID:          key.ID,
		Secret:      secret,
		UserID:      key.UserID,
		OrgID:       key.OrgID,
		Roles:       key.Roles,
		ExpiresAt:   key.ExpiresAt,
		PrincipalID: principal,
	})
}

// handleRevokeKey revokes the API key whose ID is in the path.
func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "key store not configured"})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing key id"})
		return
	}
	if err := s.keys.Revoke(id); err != nil {
		s.writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "revoking key: " + err.Error()})
		return
	}
	if s.identity != nil {
		if err := s.identity.UnbindCredential(id, principalID(r)); err != nil {
			s.log.Error("unbinding revoked key failed", "key_id", id, "err", err)
		}
	}
	s.audit(clientIP(r), "apikey.revoke", id, map[string]any{"by": principalID(r)})
	s.writeJSON(w, http.StatusOK, map[string]string{"revoked": id})
}

// checkKeyRoles expands roles into the service grants a new key would carry
// and checks the caller holds each one. It returns a non-empty message on
// refusal.
func (s *Server) checkKeyRoles(r *http.Request, roles []auth.Role) string {
	if len(roles) == 0 {
		return "at least one role is required (admin, operator, viewer, readonly)"
	}
	var grants []authz.Grant
	for _, role := range roles {
		gs := authz.Template(string(role), authz.Service)
		if gs == nil || role == "owner" {
			return "unknown role " + strconv.Quote(string(role)) + " (admin, operator, viewer, readonly)"
		}
		grants = append(grants, gs...)
	}
	sub := subjectFrom(r.Context())
	if sub == nil {
		if unitTestNoAuth && s.authMW == nil {
			return ""
		}
		return "cannot grant: unauthenticated"
	}
	now := time.Now()
	for _, g := range grants {
		if !authz.Covers(sub, g, now) {
			return "cannot grant " + string(g.Scope) + " on " + g.Resource + ": the caller does not hold it"
		}
	}
	return ""
}

// bindNewKey creates the key's service principal, applies its role
// templates and binds the key. It returns the principal id ("" when no
// identity store is wired, which only unit tests do).
func (s *Server) bindNewKey(r *http.Request, key auth.APIKey, roles []auth.Role) (string, error) {
	if s.identity == nil {
		return "", nil
	}
	by := principalID(r)
	name := strings.TrimSpace(key.Description)
	if name == "" {
		name = "API key " + key.ID
	}
	if len(name) > 100 {
		name = name[:100]
	}
	name = strings.Map(func(c rune) rune {
		if c < 0x20 || c == 0x7f {
			return -1
		}
		return c
	}, name)
	if strings.TrimSpace(name) == "" {
		name = "API key " + key.ID
	}
	p, err := s.identity.CreatePrincipal("", authz.Service, name, by)
	if err != nil {
		return "", err
	}
	for _, role := range roles {
		if err := s.identity.ApplyTemplate(p.ID, string(role), by); err != nil {
			_ = s.identity.SetStatus(p.ID, "disabled", by)
			return "", err
		}
	}
	if err := s.identity.BindCredential(key.ID, p.ID, by); err != nil {
		_ = s.identity.SetStatus(p.ID, "disabled", by)
		return "", err
	}
	return p.ID, nil
}
