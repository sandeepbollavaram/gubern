package api

import (
	"net/http"
	"sort"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

// meGrant is one grant as the UI sees it.
type meGrant struct {
	Scope    authz.Scope `json:"scope"`
	Resource string      `json:"resource"`
}

// handleMe serves GET /api/me: who the caller is and what it may do, so a
// client can hide what its scopes forbid. The server enforces every scope
// regardless of what a client shows.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sub := subjectFrom(r.Context())
	if sub == nil {
		s.writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
		return
	}
	name := sub.ID
	if s.identity != nil {
		if p, err := s.identity.Principal(sub.ID); err == nil {
			name = p.Name
		}
	}
	seen := map[authz.Scope]bool{}
	scopes := []authz.Scope{}
	grants := []meGrant{}
	for _, g := range sub.Grants {
		grants = append(grants, meGrant{Scope: g.Scope, Resource: g.Resource})
		if !seen[g.Scope] {
			seen[g.Scope] = true
			scopes = append(scopes, g.Scope)
		}
	}
	sort.Slice(scopes, func(i, j int) bool { return scopes[i] < scopes[j] })
	s.writeJSON(w, http.StatusOK, map[string]any{
		"principal_id": sub.ID, "kind": sub.Kind, "name": name,
		"scopes": scopes, "grants": grants,
	})
}
