package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/authz"
)

func TestApplyCheck(t *testing.T) {
	for _, tc := range []struct {
		msg, check, want string
		bad              bool
	}{
		{"/agent fix the build", "", "/agent fix the build", false},
		{"/agent fix the build", "go test ./...", `/agent --check "go test ./..." fix the build`, false},
		{"/agent --read-only look", "go vet ./...", `/agent --check "go vet ./..." --read-only look`, false},
		{"hello", "go test", "", true},                               // not an /agent run
		{`/agent --check "make" fix`, "go test", "", true},           // two checks
		{"/agent fix", `echo "hi"`, "", true},                        // quotes
		{"/agent fix", "go test\nrm -rf /", "", true},                // two lines
		{"/agent fix", strings.Repeat("x", maxCheckLen+1), "", true}, // too long
	} {
		got, err := applyCheck(tc.msg, tc.check)
		if (err != nil) != tc.bad || (!tc.bad && got != tc.want) {
			t.Errorf("applyCheck(%q, %q) = %q, %v", tc.msg, tc.check, got, err)
		}
	}
}

type recordingRuntime struct {
	stubRuntime
	mu  sync.Mutex
	got []string
}

func (r *recordingRuntime) Submit(ctx context.Context, msg, sid string) (<-chan string, error) {
	r.mu.Lock()
	r.got = append(r.got, msg)
	r.mu.Unlock()
	return r.stubRuntime.Submit(ctx, msg, sid)
}

func TestSubmit_DeclaredCheck(t *testing.T) {
	f := newAuthzFixture(t)
	rt := &recordingRuntime{stubRuntime: stubRuntime{response: "ok"}}
	f.s.SetAgentRuntime(rt)
	_, operator := f.principal(t, authz.Person, "operator")
	if rec := f.do(http.MethodPost, "/api/agents/submit", operator, `{"message":"/agent fix it","session_id":"chk1","check":"go test ./..."}`); rec.Code != http.StatusOK {
		t.Fatalf("submit = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, "/api/agents/submit", operator, `{"message":"just chat","session_id":"chk2","check":"go test"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("check on a chat message = %d, want 400", rec.Code)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.got) != 1 || rt.got[0] != `/agent --check "go test ./..." fix it` {
		t.Errorf("runtime got %q", rt.got)
	}
}

type fakeWorkspaces struct{}

func (fakeWorkspaces) List() ([]WorkspaceInfo, error) {
	return []WorkspaceInfo{{Name: "default", Path: "/srv/app", Default: true, Exists: true}, {Name: "docs", Path: "/srv/docs", Exists: true}}, nil
}
func (fakeWorkspaces) Resolve(name string) (string, error) {
	switch name {
	case "", "default":
		return "/srv/app", nil
	case "docs":
		return "/srv/docs", nil
	}
	return "", ErrWorkspaceNotFound
}
func (fakeWorkspaces) Add(string, string) (WorkspaceInfo, error) {
	return WorkspaceInfo{Name: "x"}, nil
}
func (fakeWorkspaces) Remove(string) error { return nil }

type wsRuntime struct {
	stubRuntime
	mu  sync.Mutex
	got []string
}

func (r *wsRuntime) Submit(ctx context.Context, msg, sid string) (<-chan string, error) {
	r.mu.Lock()
	r.got = append(r.got, agentevents.WorkspaceFrom(ctx))
	r.mu.Unlock()
	return r.stubRuntime.Submit(ctx, msg, sid)
}

func TestSubmit_Workspace(t *testing.T) {
	f := newAuthzFixture(t)
	rt := &wsRuntime{stubRuntime: stubRuntime{response: "ok"}}
	f.s.SetAgentRuntime(rt)
	f.s.SetWorkspaces(fakeWorkspaces{})
	_, operator := f.principal(t, authz.Person, "operator")
	if rec := f.do(http.MethodPost, "/api/agents/submit", operator, `{"message":"/agent tidy","session_id":"w1","workspace":"docs"}`); rec.Code != http.StatusOK {
		t.Fatalf("submit = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, "/api/agents/submit", operator, `{"message":"/agent tidy","session_id":"w2","workspace":"../etc"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown workspace = %d, want 400", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/api/workspaces", operator, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"docs"`) {
		t.Errorf("list = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, "/api/workspaces", operator, `{"path":"/tmp"}`); rec.Code != http.StatusForbidden {
		t.Errorf("operator add = %d, want 403 (workspaces:manage)", rec.Code)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if len(rt.got) != 1 || rt.got[0] != "/srv/docs" {
		t.Errorf("runtime got workspaces %q", rt.got)
	}
}
