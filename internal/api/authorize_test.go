package api

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/config"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// multiRuntime has two sessions, each with a pending approval.
type multiRuntime struct{ stubRuntime }

func (multiRuntime) ListSessions() []SessionSummary {
	return []SessionSummary{{SessionID: "sa"}, {SessionID: "sb"}}
}

func (multiRuntime) Approve(string, bool) (string, bool) { return "✓ done", true }

func (multiRuntime) PendingApprovals() []agentevents.Pending {
	exp := time.Now().Add(time.Minute)
	return []agentevents.Pending{
		{SessionID: "sa", ActionID: "a1", Tool: "run_terminal", ExpiresAt: exp},
		{SessionID: "sb", ActionID: "b1", Tool: "write_file", ExpiresAt: exp},
	}
}

type authzFixture struct {
	s    *Server
	st   *identity.Store
	keys *auth.APIKeyStore
}

func newAuthzFixture(t *testing.T) *authzFixture {
	t.Helper()
	s := New("127.0.0.1:0", config.NewHolder(&config.Config{}), "test", discardLogger())
	keys := auth.NewAPIKeyStore()
	st := wireTestAuth(t, s, keys, auth.NewRBAC())
	s.SetAgentRuntime(multiRuntime{stubRuntime{response: "ok"}})
	s.SetChatProvider(&fakeChatProvider{reply: "ok"})
	s.SetCheckpointProvider(&fakeCheckpointProvider{list: []CheckpointRecord{
		{ID: "cpa", SessionID: "sa", Status: "pending"},
		{ID: "cpb", SessionID: "sb", Status: "pending"},
	}})
	return &authzFixture{s: s, st: st, keys: keys}
}

// principal creates a principal of kind with role's template and returns its
// id and a key bound to it.
func (f *authzFixture) principal(t *testing.T, kind authz.Kind, role string) (string, string) {
	t.Helper()
	p, err := f.st.CreatePrincipal("", kind, string(kind)+" "+role, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.ApplyTemplate(p.ID, role, "test"); err != nil {
		t.Fatal(err)
	}
	k, secret, err := f.keys.Issue(p.ID, "default", []auth.Role{auth.Role(role)}, "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.BindCredential(k.ID, p.ID, "test"); err != nil {
		t.Fatal(err)
	}
	return p.ID, secret
}

func (f *authzFixture) do(method, path, secret, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	req.RemoteAddr = "203.0.113.7:5555"
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	return serve(f.s, req)
}

// TestEveryProtectedRouteHasARule: the registry and the mux agree, and no
// handler is registered outside route() except the documented public ones.
func TestEveryProtectedRouteHasARule(t *testing.T) {
	s := New("127.0.0.1:0", config.NewHolder(&config.Config{}), "test", discardLogger())
	got := append([]string(nil), s.registeredRoutes...)
	sort.Strings(got)
	var want []string
	for p := range routeScopes {
		want = append(want, p)
	}
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("registered routes and routeScopes differ\nregistered:\n%s\nrules:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Source scan: a mux.Handle/HandleFunc call outside route() must be one
	// of the public or self-verifying endpoints.
	public := map[string]bool{
		"GET /health": true, "GET /ready": true, "GET /dashboard/": true,
		"/webhook/": true, "/studio/": true, "/a2a/": true,
	}
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, fn := range files {
		if strings.HasSuffix(fn, "_test.go") || fn == "authorize.go" {
			continue
		}
		src, err := os.ReadFile(fn)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, fn, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "mux" || len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok {
				t.Errorf("%s: mux registration with a non-literal pattern", fset.Position(call.Pos()))
				return true
			}
			pattern := strings.Trim(lit.Value, `"`)
			if !public[pattern] {
				t.Errorf("%s: %q is registered outside route(): add it to routeScopes", fset.Position(call.Pos()), pattern)
			}
			return true
		})
	}
}

// TestNoAuthLayerDenies: without an auth layer a production server refuses
// every protected route (default-deny), while /health stays public.
func TestNoAuthLayerDenies(t *testing.T) {
	unitTestNoAuth = false
	defer func() { unitTestNoAuth = true }()
	s := New("127.0.0.1:0", config.NewHolder(&config.Config{}), "test", discardLogger())
	s.SetAgentRuntime(stubRuntime{response: "x"})
	for _, path := range []string{"/api/status", "/api/agents/status", "/api/keys", "/v1/models"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:1"
		if rec := serve(s, req); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s without auth layer = %d, want 503", path, rec.Code)
		}
	}
	if rec := serve(s, httptest.NewRequest(http.MethodGet, "/health", nil)); rec.Code != http.StatusOK {
		t.Errorf("/health = %d", rec.Code)
	}
	// An auth layer without an identity store also denies.
	keys := auth.NewAPIKeyStore()
	_, secret, _ := keys.Issue("a", "default", []auth.Role{auth.RoleAdmin}, "x", 0)
	s.SetAuth(auth.NewAuthMiddleware(keys, nil, auth.NewRBAC()), keys, auth.NewRBAC())
	req := httptest.NewRequest(http.MethodGet, "/api/agents/status", nil)
	req.Header.Set("X-API-Key", secret)
	if rec := serve(s, req); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("auth without identity store = %d, want 503", rec.Code)
	}
}

// TestN6_RoleMatrix: the routes N6 left open to any key now follow scopes.
func TestN6_RoleMatrix(t *testing.T) {
	f := newAuthzFixture(t)
	_, viewer := f.principal(t, authz.Person, "viewer")
	_, readonly := f.principal(t, authz.Person, "readonly")
	_, operator := f.principal(t, authz.Person, "operator")

	type call struct{ method, path, body string }
	mutating := []call{
		{http.MethodPost, "/api/agents/submit", `{"message":"hi","session_id":"v1"}`},
		{http.MethodPost, "/api/agents/approve", `{"session_id":"sa","approved":true}`},
		{http.MethodPost, "/api/checkpoints/cpa/approve", ""},
		{http.MethodPost, "/api/checkpoints/cpa/edit", `{"files":[{"path":"a","content":"b"}]}`},
		{http.MethodPost, "/api/devops/command", `{"server":"x","command":"uptime"}`},
		{http.MethodPost, "/api/forge/build", `{"message":"app"}`},
		{http.MethodPost, "/api/orchestrate", `{"goal":"g"}`},
		{http.MethodPost, "/api/pipeline/analyze", `{}`},
		{http.MethodPost, "/api/notify", `{"text":"x"}`},
		{http.MethodGet, "/v1/models", ""},
		{http.MethodPost, "/v1/chat/completions", `{"messages":[]}`},
		{http.MethodPost, "/api/keys", `{"roles":["viewer"]}`},
		{http.MethodPost, "/internal/reload", ""},
	}
	for _, c := range mutating {
		for name, key := range map[string]string{"viewer": viewer, "readonly": readonly} {
			if rec := f.do(c.method, c.path, key, c.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s %s = %d, want 403", name, c.method, c.path, rec.Code)
			}
		}
	}
	// readonly can read status but not agent data.
	if rec := f.do(http.MethodGet, "/api/agents/status", readonly, ""); rec.Code != http.StatusForbidden {
		t.Errorf("readonly agents/status = %d, want 403", rec.Code)
	}
	// The operator can run agents on its own session.
	if rec := f.do(http.MethodPost, "/api/agents/submit", operator, `{"message":"hi","session_id":"op1"}`); rec.Code != http.StatusOK {
		t.Errorf("operator submit = %d %s", rec.Code, rec.Body)
	}
	// ... but cannot manage identities or the server.
	for _, c := range []call{{http.MethodPost, "/api/keys", `{"roles":["viewer"]}`}, {http.MethodPost, "/internal/shutdown", ""}, {http.MethodGet, "/api/namespaces", ""}} {
		if rec := f.do(c.method, c.path, operator, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("operator %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
}

// TestApprovals_RequireAPerson: a service principal never decides, even with
// every other admin scope.
func TestApprovals_RequireAPerson(t *testing.T) {
	f := newAuthzFixture(t)
	_, svc := f.principal(t, authz.Service, "admin")
	_, owner := f.principal(t, authz.Person, "owner")
	for _, path := range []string{"/api/agents/approve", "/api/checkpoints/cpa/approve"} {
		body := ""
		if path == "/api/agents/approve" {
			body = `{"session_id":"sa","approved":true}`
		}
		rec := f.do(http.MethodPost, path, svc, body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), authz.ReasonNotPerson) {
			t.Errorf("service admin %s = %d %s, want 403 not-a-person", path, rec.Code, rec.Body)
		}
		if rec := f.do(http.MethodPost, path, owner, body); rec.Code != http.StatusOK {
			t.Errorf("owner %s = %d %s", path, rec.Code, rec.Body)
		}
	}
	// The service admin still administers.
	if rec := f.do(http.MethodGet, "/api/keys", svc, ""); rec.Code != http.StatusOK {
		t.Errorf("service admin GET /api/keys = %d", rec.Code)
	}
}

// TestResourceIsolation: two operators each see and act on only their own
// sessions; the owner sees everything.
func TestResourceIsolation(t *testing.T) {
	f := newAuthzFixture(t)
	a, keyA := f.principal(t, authz.Person, "operator")
	_, keyB := f.principal(t, authz.Person, "operator")
	_, owner := f.principal(t, authz.Person, "owner")

	// A claims sa by submitting to it.
	if rec := f.do(http.MethodPost, "/api/agents/submit", keyA, `{"message":"x","session_id":"sa"}`); rec.Code != http.StatusOK {
		t.Fatalf("A submit sa = %d %s", rec.Code, rec.Body)
	}
	if o, _ := f.st.Owner(resSession, "sa"); o != a {
		t.Fatalf("sa owner = %q, want %q", o, a)
	}
	// B can neither continue, read, nor decide A's session.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/agents/submit", `{"message":"x","session_id":"sa"}`},
		{http.MethodGet, "/api/agents/history/sa", ""},
		{http.MethodPost, "/api/agents/approve", `{"session_id":"sa","action_id":"a1","approved":true}`},
		{http.MethodPost, "/api/checkpoints/cpa/approve", ""},
		{http.MethodPost, "/api/agents/coordinator/chat", `{"message":"x","session_id":"sa"}`},
	} {
		if rec := f.do(c.method, c.path, keyB, c.body); rec.Code != http.StatusForbidden {
			t.Errorf("B %s %s = %d, want 403", c.method, c.path, rec.Code)
		}
	}
	list := func(key, path, field string) []string {
		var body map[string][]map[string]any
		_ = json.Unmarshal(f.do(http.MethodGet, path, key, "").Body.Bytes(), &body)
		var ids []string
		for _, it := range body[field] {
			ids = append(ids, it["session_id"].(string))
		}
		sort.Strings(ids)
		return ids
	}
	for _, c := range []struct{ path, field string }{
		{"/api/agents/pending", "pending"}, {"/api/agents/history", "sessions"},
		{"/api/checkpoints", "checkpoints"}, {"/api/attention", "items"},
	} {
		if got := list(keyA, c.path, c.field); strings.Join(got, ",") != "sa" && !(c.path == "/api/attention" && strings.Join(got, ",") == "sa,sa") {
			t.Errorf("A %s = %v, want only sa", c.path, got)
		}
		if got := list(keyB, c.path, c.field); len(got) != 0 {
			t.Errorf("B %s = %v, want nothing", c.path, got)
		}
		if got := list(owner, c.path, c.field); len(got) < 2 {
			t.Errorf("owner %s = %v, want both sessions", c.path, got)
		}
	}
	// A decides its own approval; the owner decides anyone's.
	if rec := f.do(http.MethodPost, "/api/agents/approve", keyA, `{"session_id":"sa","approved":true}`); rec.Code != http.StatusOK {
		t.Errorf("A approve own = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, "/api/agents/approve", owner, `{"session_id":"sb","approved":false}`); rec.Code != http.StatusOK {
		t.Errorf("owner approve sb = %d %s", rec.Code, rec.Body)
	}
	// An empty session id gets a fresh server-side id, never a shared one.
	rec := f.do(http.MethodPost, "/api/agents/submit", keyB, `{"message":"x"}`)
	var out struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || !strings.HasPrefix(out.SessionID, "s-") {
		t.Errorf("submit without session = %d %+v", rec.Code, out)
	}
}

// TestPermissionChanges: revocation, disabling and key revocation apply to
// the very next request.
func TestPermissionChanges(t *testing.T) {
	f := newAuthzFixture(t)
	pid, key := f.principal(t, authz.Person, "operator")
	ok := func() int { return f.do(http.MethodGet, "/api/agents/status", key, "").Code }
	if c := ok(); c != http.StatusOK {
		t.Fatalf("before = %d", c)
	}
	gs, _ := f.st.Grants(pid)
	for _, g := range gs {
		if g.Scope == authz.AgentsRead {
			if err := f.st.RevokeGrant(g.ID, "test"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if c := ok(); c != http.StatusForbidden {
		t.Errorf("after grant revoke = %d, want 403", c)
	}
	if _, err := f.st.AddGrant(pid, authz.AgentsRead, authz.AnyResource, "test", nil); err != nil {
		t.Fatal(err)
	}
	if c := ok(); c != http.StatusOK {
		t.Errorf("after re-grant = %d", c)
	}
	if err := f.st.SetStatus(pid, identity.StatusDisabled, "test"); err != nil {
		t.Fatal(err)
	}
	if c := ok(); c != http.StatusForbidden {
		t.Errorf("disabled principal = %d, want 403", c)
	}
	_ = f.st.SetStatus(pid, identity.StatusActive, "test")
	past := time.Now().Add(-time.Second)
	if _, err := f.st.AddGrant(pid, authz.ModelsUse, authz.AnyResource, "test", &past); err != nil {
		t.Fatal(err)
	}
	// Revoking the key itself: 401.
	for _, k := range f.keys.All() {
		if k.UserID == pid {
			_ = f.keys.Revoke(k.ID)
		}
	}
	if c := ok(); c != http.StatusUnauthorized {
		t.Errorf("revoked key = %d, want 401", c)
	}
}

// TestIdentityComesOnlyFromTheCredential: headers, query and body fields that
// name a principal are ignored, and an unbound key is no one.
func TestIdentityComesOnlyFromTheCredential(t *testing.T) {
	f := newAuthzFixture(t)
	_, viewer := f.principal(t, authz.Person, "viewer")
	req := httptest.NewRequest(http.MethodPost, "/api/agents/approve?principal_id=owner&role=admin",
		strings.NewReader(`{"session_id":"sa","approved":true,"principal_id":"owner","owner":"owner"}`))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("Authorization", "Bearer "+viewer)
	for _, h := range []string{"X-User-ID", "X-Principal-ID", "X-Gubern-User", "X-Forwarded-User", "X-Remote-User"} {
		req.Header.Set(h, identity.OwnerID)
	}
	if rec := serve(f.s, req); rec.Code != http.StatusForbidden {
		t.Errorf("viewer with spoofed identity = %d, want 403", rec.Code)
	}
	// A valid key bound to nobody authenticates as nobody.
	_, unbound, _ := f.keys.Issue("admin", "default", []auth.Role{auth.RoleAdmin}, "setup key", 0)
	if rec := f.do(http.MethodGet, "/api/status", unbound, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("unbound key = %d, want 401", rec.Code)
	}
}

// TestLoopbackPrincipal: loopback without a credential reaches only the
// control-plane reads and reload/stop, never data, keys or Studio.
func TestLoopbackPrincipal(t *testing.T) {
	f := newAuthzFixture(t)
	f.s.SetStudioHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	local := func(method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Host = "127.0.0.1:9090"
		return serve(f.s, req).Code
	}
	for _, path := range []string{"/api/status", "/api/audit", "/api/secrets/status", "/api/plugins"} {
		if c := local(http.MethodGet, path); c != http.StatusOK {
			t.Errorf("loopback %s = %d, want 200", path, c)
		}
	}
	for _, p := range []struct{ m, path string }{
		{http.MethodPost, "/api/agents/submit"}, {http.MethodGet, "/api/agents/pending"},
		{http.MethodGet, "/api/keys"}, {http.MethodGet, "/v1/models"}, {http.MethodGet, "/studio/x"},
		{http.MethodGet, "/api/logs"}, {http.MethodGet, "/api/ai/cost"}, {http.MethodGet, "/api/healing/status"},
	} {
		if c := local(p.m, p.path); c != http.StatusUnauthorized {
			t.Errorf("loopback %s %s = %d, want 401", p.m, p.path, c)
		}
	}
	f.s.trustLoopback = false
	if c := local(http.MethodGet, "/api/status"); c != http.StatusUnauthorized {
		t.Errorf("loopback with trust off = %d, want 401", c)
	}
}

// TestKeyIssuance_NoEscalation: a key never carries more than its issuer
// holds, and API-issued keys are service principals that cannot approve.
func TestKeyIssuance_NoEscalation(t *testing.T) {
	f := newAuthzFixture(t)
	// A principal that may manage identities but holds little else.
	p, err := f.st.CreatePrincipal("", authz.Person, "key clerk", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, sc := range []authz.Scope{authz.IdentityManage, authz.SystemRead} {
		if _, err := f.st.AddGrant(p.ID, sc, authz.AnyResource, "test", nil); err != nil {
			t.Fatal(err)
		}
	}
	k, clerk, _ := f.keys.Issue(p.ID, "default", []auth.Role{auth.RoleReadonly}, "clerk", 0)
	_ = f.st.BindCredential(k.ID, p.ID, "test")

	if rec := f.do(http.MethodPost, "/api/keys", clerk, `{"roles":["readonly"]}`); rec.Code != http.StatusCreated {
		t.Errorf("clerk issues readonly = %d %s", rec.Code, rec.Body)
	}
	for _, role := range []string{"viewer", "operator", "admin"} {
		if rec := f.do(http.MethodPost, "/api/keys", clerk, `{"roles":["`+role+`"]}`); rec.Code != http.StatusForbidden {
			t.Errorf("clerk issues %s = %d, want 403", role, rec.Code)
		}
	}
	for _, body := range []string{`{"roles":["owner"]}`, `{"roles":["root"]}`, `{"roles":[]}`, `{"roles":["readonly"],"description":"setup key"}`} {
		if rec := f.do(http.MethodPost, "/api/keys", clerk, body); rec.Code != http.StatusBadRequest {
			t.Errorf("clerk %s = %d, want 400", body, rec.Code)
		}
	}

	_, owner := f.principal(t, authz.Person, "owner")
	rec := f.do(http.MethodPost, "/api/keys", owner, `{"roles":["admin"],"description":"ci"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner issues admin = %d %s", rec.Code, rec.Body)
	}
	var created createKeyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	sp, err := f.st.Principal(created.PrincipalID)
	if err != nil || sp.Kind != authz.Service {
		t.Fatalf("issued key principal = %+v, %v", sp, err)
	}
	if rec := f.do(http.MethodPost, "/api/agents/approve", created.Secret, `{"session_id":"sa","approved":true}`); rec.Code != http.StatusForbidden {
		t.Errorf("API-issued admin key approves = %d, want 403", rec.Code)
	}
	// Revoking it unbinds it.
	if rec := f.do(http.MethodDelete, "/api/keys/"+created.ID, owner, ""); rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if _, err := f.st.CredentialPrincipal(created.ID); err == nil {
		t.Error("revoked key still bound")
	}
}

// TestMe_ReportsPrincipalAndScopes: /api/me needs only authentication, and
// reports exactly the caller's grants.
func TestMe_ReportsPrincipalAndScopes(t *testing.T) {
	f := newAuthzFixture(t)
	pid, viewer := f.principal(t, authz.Person, "viewer")
	rec := f.do(http.MethodGet, "/api/me", viewer, "")
	var me struct {
		PrincipalID string   `json:"principal_id"`
		Kind        string   `json:"kind"`
		Scopes      []string `json:"scopes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if rec.Code != http.StatusOK || me.PrincipalID != pid || me.Kind != "person" {
		t.Fatalf("me = %d %s", rec.Code, rec.Body)
	}
	joined := strings.Join(me.Scopes, ",")
	if !strings.Contains(joined, "sessions:read") || strings.Contains(joined, "approvals:decide") || strings.Contains(joined, "agents:run") {
		t.Errorf("viewer scopes = %v", me.Scopes)
	}
	// A principal with no grants still learns who it is (and that it holds nothing).
	p, _ := f.st.CreatePrincipal("", authz.Service, "empty", "test")
	k, secret, _ := f.keys.Issue(p.ID, "default", nil, "empty", 0)
	_ = f.st.BindCredential(k.ID, p.ID, "test")
	if rec := f.do(http.MethodGet, "/api/me", secret, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"scopes":[]`) {
		t.Errorf("no-grant principal = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodGet, "/api/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d", rec.Code)
	}
}
