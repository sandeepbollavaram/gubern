package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// Authorization (milestone A, design: docs/design/identity-authorization.md).
//
// Every protected route is registered through Server.route with a scope from
// routeScopes, the single registry. A request is authenticated (API key),
// resolved to a principal through the identity store, and checked against
// that principal's live grants with authz.Decide before the handler runs.
// Handlers that touch a session, job or report then check the resource
// boundary through canAccess / requireAccess / claimSession. Anything not
// explicitly allowed is denied, including a route without a rule and a server
// without an auth layer.

// routeScopes maps each protected route pattern to the scope it requires.
// A route registered without an entry panics at construction; a test checks
// the table has no stale entries.
var routeScopes = map[string]authz.Scope{
	// Authentication only: any active principal may ask who it is.
	"GET /api/me": scopeAuthenticated,

	"POST /internal/reload":   authz.SystemManage,
	"POST /internal/shutdown": authz.SystemManage,
	"GET /metrics":            authz.SystemRead,
	"GET /api/status":         authz.SystemRead,
	"GET /api/plugins":        authz.SystemRead,
	"GET /api/secrets/status": authz.SecurityRead,
	"GET /api/audit":          authz.AuditRead,
	"POST /api/audit/verify":  authz.AuditRead,

	"GET /api/keys":         authz.IdentityManage,
	"POST /api/keys":        authz.IdentityManage,
	"DELETE /api/keys/{id}": authz.IdentityManage,

	"GET /api/namespaces":            authz.WorkspacesManage,
	"POST /api/namespaces":           authz.WorkspacesManage,
	"DELETE /api/namespaces/{id}":    authz.WorkspacesManage,
	"GET /api/namespaces/{id}/stats": authz.WorkspacesManage,

	"GET /api/ai/cost":                 authz.SystemRead,
	"GET /api/keys/status":             authz.SystemRead,
	"GET /api/healing/status":          authz.SystemRead,
	"GET /api/healing/events":          authz.SystemRead,
	"GET /api/logs":                    authz.SecurityRead,
	"GET /api/research/reports":        authz.RunsRead,
	"GET /api/research/reports/{name}": authz.RunsRead,
	"GET /api/devops/servers":          authz.DevopsRead,
	"POST /api/devops/command":         authz.DevopsRun,
	"POST /api/pipeline/analyze":       authz.RunsWrite,
	"POST /api/orchestrate":            authz.RunsWrite,

	"GET /api/orchestration/runs":            authz.RunsRead,
	"GET /api/orchestration/runs/{id}/tasks": authz.RunsRead,

	"POST /api/agents/submit":            authz.AgentsRun,
	"GET /api/agents/status":             authz.AgentsRead,
	"GET /api/agents/catalog":            authz.AgentsRead,
	"POST /api/agents/approve":           authz.ApprovalsDecide,
	"GET /api/agents/pending":            authz.ApprovalsRead,
	"GET /api/attention":                 authz.ApprovalsRead,
	"GET /api/agents/history":            authz.SessionsRead,
	"GET /api/agents/history/{id}":       authz.SessionsRead,
	"GET /api/team/agents":               authz.AgentsRead,
	"GET /api/team/status":               authz.AgentsRead,
	"GET /api/agents/team/status":        authz.AgentsRead,
	"GET /api/agents/comms":              authz.SessionsRead,
	"GET /api/agents/comms/stream":       authz.SessionsRead,
	"GET /api/agents/team/{id}/messages": authz.SessionsRead,
	"POST /api/agents/{id}/chat":         authz.AgentsRun,

	"POST /api/pairing":                      authz.DevicesManage,
	"GET /api/devices":                       authz.DevicesRead,
	"DELETE /api/devices/{id}":               authz.DevicesManage,
	"POST /api/devices/{id}/rotate":          authz.DevicesManage,
	"GET /api/links":                         authz.DevicesRead,
	"DELETE /api/links/{provider}/{subject}": authz.DevicesManage,
	"GET /api/runs":                          authz.SessionsRead,
	"GET /api/runs/{id}":                     authz.SessionsRead,
	"GET /api/runs/{id}/events":              authz.SessionsRead,
	"POST /api/runs/{id}/cancel":             authz.AgentsRun,
	"GET /api/activity":                      authz.SessionsRead,
	"GET /api/approvals":                     authz.ApprovalsRead,
	"GET /api/approvals/{id}":                authz.ApprovalsRead,
	"POST /api/approvals/{id}/decision":      authz.ApprovalsDecide,
	"GET /api/checkpoints":                   authz.ApprovalsRead,
	"POST /api/checkpoints/{id}/approve":     authz.ApprovalsDecide,
	"POST /api/checkpoints/{id}/reject":      authz.ApprovalsDecide,
	"POST /api/checkpoints/{id}/edit":        authz.ApprovalsDecide,

	"POST /api/forge/build":      authz.RunsWrite,
	"GET /api/forge/status/{id}": authz.RunsRead,
	"GET /api/forge/jobs":        authz.RunsRead,
	"POST /api/notify":           authz.NotifySend,
	"GET /v1/models":             authz.ModelsUse,
	"POST /v1/chat/completions":  authz.ModelsUse,
	"POST /v1/responses":         authz.ModelsUse,
}

// scopeAuthenticated marks a route that needs an authenticated, active
// principal and no particular scope (GET /api/me only).
const scopeAuthenticated authz.Scope = ""

// loopbackRoutes are the routes a trusted loopback caller may use without a
// credential: exactly the ones loopback trust reached before milestone A
// (control-plane reads and reload/stop, design §6). Trust is per route, not
// per scope, so a route sharing one of these scopes (e.g. /api/logs) does not
// widen it.
var loopbackRoutes = map[string]bool{
	"POST /internal/reload": true, "POST /internal/shutdown": true, "GET /metrics": true,
	"GET /api/status": true, "GET /api/plugins": true, "GET /api/secrets/status": true,
	"GET /api/audit": true, "POST /api/audit/verify": true,
}

// loopbackScopes are the scopes of the loopback principal: those the
// loopbackRoutes need.
var loopbackScopes = []authz.Scope{authz.SystemRead, authz.SystemManage, authz.AuditRead, authz.SecurityRead}

func loopbackSubject() *authz.Subject {
	sub := &authz.Subject{ID: identity.LoopbackID, Kind: authz.Service, Active: true}
	for _, sc := range loopbackScopes {
		sub.Grants = append(sub.Grants, authz.Grant{Scope: sc, Resource: authz.AnyResource})
	}
	return sub
}

// unitTestNoAuth lets this package's own unit tests build servers without an
// auth layer, as they did before milestone A, to test handler behaviour. It is
// unexported, has no setter, and is assigned only in a _test.go file, so no
// production binary can turn it on; a production server without an auth layer
// answers 503 on every protected route.
var unitTestNoAuth bool

type subjectCtxKey struct{}

func withSubject(ctx context.Context, sub *authz.Subject) context.Context {
	return context.WithValue(ctx, subjectCtxKey{}, sub)
}

// subjectFrom returns the authorized principal of the request, or nil.
func subjectFrom(ctx context.Context) *authz.Subject {
	sub, _ := ctx.Value(subjectCtxKey{}).(*authz.Subject)
	return sub
}

// SetIdentity wires the identity store that maps credentials to principals
// and holds grants and resource ownership.
func (s *Server) SetIdentity(st *identity.Store) { s.identity = st }

// route registers h at pattern behind the authorization rule for pattern.
func (s *Server) route(mux *http.ServeMux, pattern string, h http.Handler) {
	scope, ok := routeScopes[pattern]
	if !ok {
		panic("api: route " + pattern + " has no authorization rule in routeScopes")
	}
	s.registeredRoutes = append(s.registeredRoutes, pattern)
	mux.Handle(pattern, s.authorizeWith(scope, loopbackRoutes[pattern], h))
}

// authorizeWith is the single enforcement point for protected routes.
// allowLoopback lets a trusted loopback caller without a credential act as
// the loopback principal (only the loopbackRoutes and nothing else).
func (s *Server) authorizeWith(scope authz.Scope, allowLoopback bool, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authMW == nil {
			if unitTestNoAuth {
				h.ServeHTTP(w, r)
				return
			}
			s.deny(w, r, http.StatusServiceUnavailable, scope, "auth-not-configured", "")
			return
		}
		if allowLoopback && s.trustLoopback && !hasCredential(r) && loopbackTrusted(r) {
			h.ServeHTTP(w, r.WithContext(withSubject(r.Context(), loopbackSubject())))
			return
		}
		s.authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sub, status, reason := s.resolveSubject(r)
			if sub == nil {
				s.deny(w, r, status, scope, reason, "")
				return
			}
			if scope == scopeAuthenticated {
				s.keyLimit(h).ServeHTTP(w, r.WithContext(withSubject(r.Context(), sub)))
				return
			}
			if d := authz.Decide(sub, scope, nil, time.Now()); !d.Allow {
				s.deny(w, r, http.StatusForbidden, scope, d.Reason, sub.ID)
				return
			}
			s.keyLimit(h).ServeHTTP(w, r.WithContext(withSubject(r.Context(), sub)))
		})).ServeHTTP(w, r)
	})
}

// hasCredential reports whether the request presents any credential. A
// loopback caller that presents one is authenticated as that credential's
// principal, not as the loopback principal.
func hasCredential(r *http.Request) bool {
	return r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != ""
}

// resolveSubject maps the authenticated credential to its principal and live
// grants. Only the credential decides the principal; no header, query or body
// field is consulted.
func (s *Server) resolveSubject(r *http.Request) (*authz.Subject, int, string) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		return nil, http.StatusUnauthorized, authz.ReasonUnauthenticated
	}
	if s.identity == nil {
		return nil, http.StatusServiceUnavailable, "identity-not-configured"
	}
	if user.KeyID == "" {
		return nil, http.StatusUnauthorized, "unbound-credential"
	}
	ci, err := s.identity.Credential(user.KeyID)
	if errors.Is(err, identity.ErrNotFound) {
		return nil, http.StatusUnauthorized, "unbound-credential"
	}
	if err != nil {
		s.log.Error("resolving principal failed", "key_id", user.KeyID, "err", err)
		return nil, http.StatusServiceUnavailable, "identity-unavailable"
	}
	if ci.DeviceID != "" {
		// A paired device's credential stops with the device.
		d, derr := s.identity.Device(ci.DeviceID)
		if derr != nil || d.RevokedAt != nil {
			return nil, http.StatusUnauthorized, "device-revoked"
		}
		s.touchDevice(ci.DeviceID)
	}
	sub, err := s.identity.Subject(ci.PrincipalID)
	if err != nil {
		s.log.Error("loading grants failed", "principal", ci.PrincipalID, "err", err)
		return nil, http.StatusServiceUnavailable, "identity-unavailable"
	}
	if !sub.Active {
		return nil, http.StatusForbidden, authz.ReasonInactive
	}
	identity.ApplyCeiling(sub, ci.Ceiling)
	return sub, 0, ""
}

// deny writes an authorization failure and logs it (never with a secret).
func (s *Server) deny(w http.ResponseWriter, r *http.Request, status int, scope authz.Scope, reason, principal string) {
	s.log.Warn("request denied", "path", r.URL.Path, "method", r.Method, "scope", string(scope),
		"reason", reason, "principal", principal)
	msg := "forbidden"
	switch status {
	case http.StatusUnauthorized:
		msg = "authentication required"
	case http.StatusServiceUnavailable:
		msg = "authorization unavailable"
	}
	s.writeJSON(w, status, map[string]string{"error": msg, "reason": reason})
}

// canAccess reports whether the request's principal may use scope on the
// resource (kind, id), given its recorded owner.
func (s *Server) canAccess(r *http.Request, scope authz.Scope, kind, id string) bool {
	sub := subjectFrom(r.Context())
	if sub == nil {
		return unitTestNoAuth && s.authMW == nil
	}
	owner := ""
	if s.identity != nil {
		o, err := s.identity.Owner(kind, id)
		if err != nil {
			s.log.Error("reading resource owner failed", "kind", kind, "err", err)
			return false
		}
		owner = o
	}
	return authz.Decide(sub, scope, &authz.Resource{Kind: kind, ID: id, Owner: owner}, time.Now()).Allow
}

// requireAccess is canAccess that writes the 403 itself.
func (s *Server) requireAccess(w http.ResponseWriter, r *http.Request, scope authz.Scope, kind, id string) bool {
	if s.canAccess(r, scope, kind, id) {
		return true
	}
	s.deny(w, r, http.StatusForbidden, scope, authz.ReasonOutOfBoundary, principalID(r))
	return false
}

// claim records the caller as owner of (kind, id) when nobody owns it, then
// checks the caller may use scope on it: its own resource, or any resource
// with a "*" grant. Ownership is never taken from the request body.
func (s *Server) claim(w http.ResponseWriter, r *http.Request, scope authz.Scope, kind, id string) bool {
	sub := subjectFrom(r.Context())
	if sub == nil {
		if unitTestNoAuth && s.authMW == nil {
			return true
		}
		s.deny(w, r, http.StatusUnauthorized, scope, authz.ReasonUnauthenticated, "")
		return false
	}
	if s.identity == nil || sub.ID == identity.LoopbackID {
		s.deny(w, r, http.StatusForbidden, scope, "cannot-own", sub.ID)
		return false
	}
	if _, err := s.identity.Claim(kind, id, sub.ID); err != nil {
		s.log.Error("claiming resource failed", "kind", kind, "err", err)
		s.deny(w, r, http.StatusServiceUnavailable, scope, "identity-unavailable", sub.ID)
		return false
	}
	return s.requireAccess(w, r, scope, kind, id)
}

// requireAll checks scope over every resource of its kind: only a "*" grant
// passes. It guards views that mix many principals' data (the cross-session
// comms feed, research reports, which have no owner yet).
func (s *Server) requireAll(w http.ResponseWriter, r *http.Request, scope authz.Scope) bool {
	return s.requireAccess(w, r, scope, resFeed, "all")
}

// holds reports whether the request's principal holds scope at all (a
// route-level check for mixed views such as /api/attention).
func (s *Server) holds(r *http.Request, scope authz.Scope) bool {
	sub := subjectFrom(r.Context())
	if sub == nil {
		return unitTestNoAuth && s.authMW == nil
	}
	return authz.Decide(sub, scope, nil, time.Now()).Allow
}

// recordOwner records the caller as owner of a resource it just created (a
// Forge job). A failure is logged, not fatal: the resource then has no owner
// and only "*" grants can read it, which fails closed.
func (s *Server) recordOwner(r *http.Request, kind, id string) {
	sub := subjectFrom(r.Context())
	if sub == nil || s.identity == nil || id == "" {
		return
	}
	if _, err := s.identity.Claim(kind, id, sub.ID); err != nil {
		s.log.Error("recording resource owner failed", "kind", kind, "err", err)
	}
}

func principalID(r *http.Request) string {
	if sub := subjectFrom(r.Context()); sub != nil {
		return sub.ID
	}
	return ""
}

// newSessionID generates a server-side session id when the client sends none,
// so every API-started session has an owner and nobody shares the empty id.
func newSessionID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "s-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	}
	return "s-" + hex.EncodeToString(b)
}

// Resource kinds.
const (
	resSession = "session"
	resJob     = "job"
	resReport  = "report"
	resRun     = "orchestration"
	// resFeed is the cross-session comms feed: no one owns it, so only a "*"
	// grant (or an explicit feed:all grant) reads it.
	resFeed = "feed"
)
