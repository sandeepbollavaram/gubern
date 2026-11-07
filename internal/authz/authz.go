// Package authz is Gubern's authorization core: the scope vocabulary, the role
// templates that expand into grants, and Decide, a pure function that says
// whether a principal may use a scope on a resource. It does no I/O, so every
// rule here is unit-testable on its own; the identity store supplies the
// principal and its grants, and the API applies the decision.
//
// Design: docs/design/identity-authorization.md.
package authz

import (
	"strings"
	"time"
)

// Scope names one permission, "<area>:<verb>".
type Scope string

// Scopes in use. See the design doc §4 for what each one allows.
const (
	SystemRead       Scope = "system:read"
	SystemManage     Scope = "system:manage"
	AuditRead        Scope = "audit:read"
	SecurityRead     Scope = "security:read"
	IdentityManage   Scope = "identity:manage"
	WorkspacesManage Scope = "workspaces:manage"
	AgentsRead       Scope = "agents:read"
	AgentsRun        Scope = "agents:run"
	SessionsRead     Scope = "sessions:read"
	ApprovalsRead    Scope = "approvals:read"
	ApprovalsDecide  Scope = "approvals:decide"
	RunsRead         Scope = "runs:read"
	RunsWrite        Scope = "runs:write"
	DevopsRead       Scope = "devops:read"
	DevopsRun        Scope = "devops:run"
	ModelsUse        Scope = "models:use"
	NotifySend       Scope = "notify:send"

	// Reserved for milestones F and H. They are valid in grants (so a grant
	// made ahead of time survives the upgrade) but no route uses them yet.
	FilesRead          Scope = "files:read"
	FilesWrite         Scope = "files:write"
	DevicesRead        Scope = "devices:read"
	DevicesManage      Scope = "devices:manage"
	IntegrationsRead   Scope = "integrations:read"
	IntegrationsManage Scope = "integrations:manage"
)

// Active lists the scopes routes use today.
var Active = []Scope{
	SystemRead, SystemManage, AuditRead, SecurityRead, IdentityManage, WorkspacesManage,
	AgentsRead, AgentsRun, SessionsRead, ApprovalsRead, ApprovalsDecide,
	RunsRead, RunsWrite, DevopsRead, DevopsRun, ModelsUse, NotifySend,
}

// Reserved lists scopes accepted in grants ahead of the routes that use them.
var Reserved = []Scope{FilesRead, FilesWrite, DevicesRead, DevicesManage, IntegrationsRead, IntegrationsManage}

// Known reports whether s is an active or reserved scope.
func Known(s Scope) bool {
	for _, k := range Active {
		if k == s {
			return true
		}
	}
	for _, k := range Reserved {
		if k == s {
			return true
		}
	}
	return false
}

// PersonOnly reports whether only a person principal may hold s. Deciding an
// approval is a human's act: an automation that could approve would defeat the
// approval step (ADR-A4).
func PersonOnly(s Scope) bool { return s == ApprovalsDecide }

// Resource values on a grant.
const (
	AnyResource = "*"   // every resource
	OwnResource = "own" // resources the principal owns
)

// ValidResource reports whether r is a valid grant resource: "*", "own", or
// "<kind>:<id>" with a non-empty kind and id.
func ValidResource(r string) bool {
	if r == AnyResource || r == OwnResource {
		return true
	}
	kind, id, ok := strings.Cut(r, ":")
	return ok && kind != "" && id != "" && !strings.ContainsAny(r, " \t\r\n")
}

// Kind is a principal's kind.
type Kind string

// Principal kinds.
const (
	Person  Kind = "person"
	Service Kind = "service"
)

// Grant is one permission held by a principal.
type Grant struct {
	Scope    Scope
	Resource string
	// ExpiresAt, when set, ends the grant. Revoked grants never reach
	// Decide; the store filters them.
	ExpiresAt *time.Time
}

// Subject is the authenticated principal a decision is made for.
type Subject struct {
	ID     string
	Kind   Kind
	Active bool
	Grants []Grant
}

// Resource is the object a scoped request touches. Owner is the principal
// that owns it ("" when nobody has claimed it).
type Resource struct {
	Kind  string
	ID    string
	Owner string
}

// Ref is the "<kind>:<id>" form used in grants.
func (r Resource) Ref() string { return r.Kind + ":" + r.ID }

// Decision is the outcome of Decide with a machine-readable reason.
type Decision struct {
	Allow  bool
	Reason string
}

// Reasons.
const (
	ReasonAllowed         = "allowed"
	ReasonUnauthenticated = "unauthenticated"
	ReasonInactive        = "inactive"
	ReasonNotPerson       = "not-a-person"
	ReasonMissingScope    = "missing-scope"
	ReasonOutOfBoundary   = "out-of-boundary"
	ReasonUnknownScope    = "unknown-scope"
)

// Decide reports whether sub may use scope on res at time now. res == nil is a
// route-level check (does the subject hold the scope at all). It is
// default-deny: anything not explicitly allowed is denied.
func Decide(sub *Subject, scope Scope, res *Resource, now time.Time) Decision {
	deny := func(reason string) Decision { return Decision{Reason: reason} }
	switch {
	case sub == nil || sub.ID == "":
		return deny(ReasonUnauthenticated)
	case !sub.Active:
		return deny(ReasonInactive)
	case !Known(scope):
		return deny(ReasonUnknownScope)
	case PersonOnly(scope) && sub.Kind != Person:
		return deny(ReasonNotPerson)
	}
	held := false
	for _, g := range sub.Grants {
		if g.Scope != scope || (g.ExpiresAt != nil && !now.Before(*g.ExpiresAt)) {
			continue
		}
		held = true
		if res == nil {
			return Decision{Allow: true, Reason: ReasonAllowed}
		}
		switch g.Resource {
		case AnyResource:
			return Decision{Allow: true, Reason: ReasonAllowed}
		case OwnResource:
			if res.Owner != "" && res.Owner == sub.ID {
				return Decision{Allow: true, Reason: ReasonAllowed}
			}
		default:
			if g.Resource == res.Ref() {
				return Decision{Allow: true, Reason: ReasonAllowed}
			}
		}
	}
	if !held {
		return deny(ReasonMissingScope)
	}
	return deny(ReasonOutOfBoundary)
}

// Covers reports whether sub could be granted g by someone holding exactly
// sub's grants: the same scope, on a resource at least as wide. It is the
// no-escalation check for issuing keys and grants.
func Covers(sub *Subject, g Grant, now time.Time) bool {
	if sub == nil || !sub.Active {
		return false
	}
	for _, h := range sub.Grants {
		if h.Scope != g.Scope || (h.ExpiresAt != nil && !now.Before(*h.ExpiresAt)) {
			continue
		}
		if h.Resource == AnyResource || h.Resource == g.Resource {
			return true
		}
	}
	return false
}

// Templates are the role names a key or principal can be created with.
var Templates = []string{"owner", "admin", "operator", "viewer", "readonly"}

// Template returns the grants role expands to for a principal of kind. An
// unknown role returns nil: no grants, never a guess. Person-only scopes are
// left out for service principals.
func Template(role string, kind Kind) []Grant {
	var gs []Grant
	add := func(resource string, scopes ...Scope) {
		for _, s := range scopes {
			if PersonOnly(s) && kind != Person {
				continue
			}
			gs = append(gs, Grant{Scope: s, Resource: resource})
		}
	}
	switch role {
	case "owner", "admin":
		add(AnyResource, Active...)
		add(AnyResource, Reserved...)
	case "operator":
		// Everything a viewer reads, plus running work on its own sessions.
		add(AnyResource, SystemRead, AuditRead, SecurityRead, AgentsRead, RunsWrite, DevopsRead, DevopsRun, ModelsUse, NotifySend)
		add(OwnResource, AgentsRun, SessionsRead, ApprovalsRead, ApprovalsDecide, RunsRead)
	case "viewer":
		add(AnyResource, SystemRead, AuditRead, SecurityRead, AgentsRead, DevopsRead)
		add(OwnResource, SessionsRead, ApprovalsRead, RunsRead)
	case "readonly":
		add(AnyResource, SystemRead)
	default:
		return nil
	}
	return gs
}
