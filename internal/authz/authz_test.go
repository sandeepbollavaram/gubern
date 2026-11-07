package authz

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func subject(kind Kind, gs ...Grant) *Subject {
	return &Subject{ID: "p1", Kind: kind, Active: true, Grants: gs}
}

func TestDecide_DefaultDeny(t *testing.T) {
	cases := []struct {
		name   string
		sub    *Subject
		scope  Scope
		res    *Resource
		reason string
	}{
		{"no subject", nil, SystemRead, nil, ReasonUnauthenticated},
		{"empty id", &Subject{Active: true}, SystemRead, nil, ReasonUnauthenticated},
		{"disabled", &Subject{ID: "p1", Kind: Person, Grants: []Grant{{Scope: SystemRead, Resource: AnyResource}}}, SystemRead, nil, ReasonInactive},
		{"no grants", subject(Person), SystemRead, nil, ReasonMissingScope},
		{"unknown scope", subject(Person, Grant{Scope: "x:y", Resource: AnyResource}), "x:y", nil, ReasonUnknownScope},
		{"other scope only", subject(Person, Grant{Scope: ApprovalsRead, Resource: AnyResource}), ApprovalsDecide, nil, ReasonMissingScope},
		{"service cannot decide", subject(Service, Grant{Scope: ApprovalsDecide, Resource: AnyResource}), ApprovalsDecide, nil, ReasonNotPerson},
		{"own but someone else's", subject(Person, Grant{Scope: SessionsRead, Resource: OwnResource}), SessionsRead, &Resource{Kind: "session", ID: "s1", Owner: "p2"}, ReasonOutOfBoundary},
		{"own but unclaimed", subject(Person, Grant{Scope: SessionsRead, Resource: OwnResource}), SessionsRead, &Resource{Kind: "session", ID: "s1"}, ReasonOutOfBoundary},
		{"specific other id", subject(Person, Grant{Scope: SessionsRead, Resource: "session:s2"}), SessionsRead, &Resource{Kind: "session", ID: "s1", Owner: "p2"}, ReasonOutOfBoundary},
		{"expired", subject(Person, Grant{Scope: SystemRead, Resource: AnyResource, ExpiresAt: ptr(now)}), SystemRead, nil, ReasonMissingScope},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(c.sub, c.scope, c.res, now)
			if d.Allow || d.Reason != c.reason {
				t.Fatalf("Decide = %+v, want deny %s", d, c.reason)
			}
		})
	}
}

func TestDecide_Allows(t *testing.T) {
	session := &Resource{Kind: "session", ID: "s1", Owner: "p1"}
	cases := []struct {
		name  string
		sub   *Subject
		scope Scope
		res   *Resource
	}{
		{"route level", subject(Service, Grant{Scope: SystemRead, Resource: AnyResource}), SystemRead, nil},
		{"own resource", subject(Person, Grant{Scope: SessionsRead, Resource: OwnResource}), SessionsRead, session},
		{"any resource", subject(Service, Grant{Scope: SessionsRead, Resource: AnyResource}), SessionsRead, &Resource{Kind: "session", ID: "x", Owner: "p9"}},
		{"exact resource", subject(Person, Grant{Scope: SessionsRead, Resource: "session:s1"}), SessionsRead, &Resource{Kind: "session", ID: "s1", Owner: "p9"}},
		{"person decides own", subject(Person, Grant{Scope: ApprovalsDecide, Resource: OwnResource}), ApprovalsDecide, session},
		{"not yet expired", subject(Person, Grant{Scope: SystemRead, Resource: AnyResource, ExpiresAt: ptr(now.Add(time.Second))}), SystemRead, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if d := Decide(c.sub, c.scope, c.res, now); !d.Allow {
				t.Fatalf("Decide = %+v, want allow", d)
			}
		})
	}
}

// No scope implies another: holding everything but X never allows X.
func TestDecide_NoImplication(t *testing.T) {
	for _, target := range Active {
		var gs []Grant
		for _, s := range append(append([]Scope{}, Active...), Reserved...) {
			if s != target {
				gs = append(gs, Grant{Scope: s, Resource: AnyResource})
			}
		}
		if d := Decide(subject(Person, gs...), target, nil, now); d.Allow {
			t.Errorf("%s allowed without its own grant", target)
		}
	}
}

func TestTemplates(t *testing.T) {
	has := func(gs []Grant, s Scope, res string) bool {
		for _, g := range gs {
			if g.Scope == s && g.Resource == res {
				return true
			}
		}
		return false
	}
	if gs := Template("viewer", Person); has(gs, ApprovalsDecide, OwnResource) || has(gs, ApprovalsDecide, AnyResource) || has(gs, AgentsRun, OwnResource) {
		t.Error("viewer must not run agents or decide approvals")
	}
	if gs := Template("operator", Service); has(gs, ApprovalsDecide, OwnResource) {
		t.Error("a service operator must not decide approvals")
	}
	if gs := Template("operator", Person); !has(gs, ApprovalsDecide, OwnResource) || has(gs, ApprovalsDecide, AnyResource) {
		t.Error("a person operator decides only their own approvals")
	}
	if gs := Template("admin", Service); has(gs, ApprovalsDecide, AnyResource) || !has(gs, IdentityManage, AnyResource) {
		t.Error("a service admin keeps admin scopes but never approvals")
	}
	if gs := Template("owner", Person); !has(gs, ApprovalsDecide, AnyResource) {
		t.Error("the owner decides approvals")
	}
	if gs := Template("readonly", Person); len(gs) != 1 || !has(gs, SystemRead, AnyResource) {
		t.Errorf("readonly = %+v", gs)
	}
	for _, role := range []string{"", "root", "Admin", "superuser"} {
		if gs := Template(role, Person); gs != nil {
			t.Errorf("unknown role %q expanded to %d grants", role, len(gs))
		}
	}
	for _, role := range Templates {
		for _, g := range Template(role, Person) {
			if !Known(g.Scope) || !ValidResource(g.Resource) {
				t.Errorf("%s template has invalid grant %+v", role, g)
			}
		}
	}
}

func TestCovers_NoEscalation(t *testing.T) {
	op := subject(Person, Template("operator", Person)...)
	if Covers(op, Grant{Scope: IdentityManage, Resource: AnyResource}, now) {
		t.Error("operator could hand out identity:manage")
	}
	if Covers(op, Grant{Scope: SessionsRead, Resource: AnyResource}, now) {
		t.Error("operator could widen sessions:read from own to *")
	}
	if !Covers(op, Grant{Scope: SessionsRead, Resource: OwnResource}, now) {
		t.Error("operator should be able to pass on its own grant")
	}
	admin := subject(Person, Template("admin", Person)...)
	if !Covers(admin, Grant{Scope: SessionsRead, Resource: "session:x"}, now) {
		t.Error("a * grant covers a specific resource")
	}
	if Covers(&Subject{ID: "x", Kind: Person, Grants: admin.Grants}, Grant{Scope: SystemRead, Resource: AnyResource}, now) {
		t.Error("an inactive principal covers nothing")
	}
}

func TestValidResource(t *testing.T) {
	for _, ok := range []string{"*", "own", "session:abc", "job:1"} {
		if !ValidResource(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "session", ":x", "session:", "a b:c", "all"} {
		if ValidResource(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func ptr(t time.Time) *time.Time { return &t }
