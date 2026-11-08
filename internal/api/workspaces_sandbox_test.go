package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/sandbox"
)

// GET /api/workspaces says which sandbox terminal commands run in, and what
// it does and doesn't enforce, so the Workspaces page can show it honestly.
func TestWorkspacesList_ReportsSandbox(t *testing.T) {
	s := newTestServer(t)
	get := func() map[string]any {
		rec := httptest.NewRecorder()
		s.handleWorkspacesList(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces", nil))
		var body map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("status %d %s", rec.Code, rec.Body.String())
		}
		return body
	}
	if _, ok := get()["sandbox"]; ok {
		t.Error("sandbox reported before it was set")
	}
	s.SetSandboxInfo(sandbox.Info{Adapter: "job-object", Level: sandbox.LevelContained, Platform: "Windows",
		Network: "not enforced", Enforced: []string{"tree killed"}, NotEnforced: []string{"file system fence"}})
	sb, _ := get()["sandbox"].(map[string]any)
	if sb["label"] != "Contained (Windows)" || sb["adapter"] != "job-object" || sb["network"] != "not enforced" {
		t.Errorf("sandbox = %v", sb)
	}
	if ne, _ := sb["not_enforced"].([]any); len(ne) != 1 {
		t.Errorf("not_enforced = %v", sb["not_enforced"])
	}
}
