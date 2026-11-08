package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/connectors"
)

type fakeConnectors struct {
	mu      sync.Mutex
	list    []connectors.Connector
	secrets map[string]string
	enables int
}

func (f *fakeConnectors) List(context.Context) ([]ConnectorView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ConnectorView
	for _, c := range f.list {
		out = append(out, ConnectorView{Connector: c, SecretsSet: map[string]bool{"tok": f.secrets[c.ID+"/tok"] != ""}})
	}
	return out, nil
}
func (f *fakeConnectors) Get(ctx context.Context, id string) (ConnectorView, error) {
	l, _ := f.List(ctx)
	for _, v := range l {
		if v.ID == id {
			return v, nil
		}
	}
	return ConnectorView{}, connectors.ErrNotFound
}
func (f *fakeConnectors) Add(_ context.Context, c connectors.Connector) (ConnectorView, error) {
	if err := c.Validate(); err != nil {
		return ConnectorView{}, err
	}
	f.mu.Lock()
	f.list = append(f.list, c)
	f.mu.Unlock()
	return ConnectorView{Connector: c}, nil
}
func (f *fakeConnectors) Update(_ context.Context, _ string, c connectors.Connector) (ConnectorView, error) {
	return ConnectorView{Connector: c}, nil
}
func (f *fakeConnectors) Remove(context.Context, string) error { return nil }
func (f *fakeConnectors) PrepareEnable(context.Context, string, string, string) (string, error) {
	f.mu.Lock()
	f.enables++
	f.mu.Unlock()
	return "ap-1", nil
}
func (f *fakeConnectors) Disable(context.Context, string) error                       { return nil }
func (f *fakeConnectors) Reconnect(context.Context, string) error                     { return nil }
func (f *fakeConnectors) SetToolPolicy(context.Context, string, string, string) error { return nil }
func (f *fakeConnectors) SetSecret(_ context.Context, id, name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets[id+"/"+name] = value
	return nil
}

// Listing needs agents:read; every change, and enabling, needs
// system:manage. A secret is write-only: no response carries its value.
func TestConnectors_Authorization(t *testing.T) {
	f := newAuthzFixture(t)
	fc := &fakeConnectors{secrets: map[string]string{}}
	f.s.SetConnectors(fc)
	_, viewer := f.principal(t, authz.Person, "viewer")
	_, operator := f.principal(t, authz.Person, "operator")
	_, owner := f.principal(t, authz.Person, "owner")
	add := `{"id":"web","transport":"http","url":"https://example.com/mcp","bearer_secret":"tok"}`

	if rec := f.do(http.MethodGet, "/api/mcp/connectors", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous list = %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/api/mcp/connectors", viewer, ""); rec.Code != http.StatusOK {
		t.Errorf("viewer list = %d", rec.Code)
	}
	for _, who := range []string{viewer, operator} {
		for _, req := range [][3]string{
			{http.MethodPost, "/api/mcp/connectors", add},
			{http.MethodPost, "/api/mcp/connectors/web/enable", ""},
			{http.MethodPut, "/api/mcp/connectors/web/secrets/tok", `{"value":"v"}`},
			{http.MethodPut, "/api/mcp/connectors/web/tools/echo/policy", `{"policy":"allow"}`},
			{http.MethodDelete, "/api/mcp/connectors/web", ""},
		} {
			if rec := f.do(req[0], req[1], who, req[2]); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s without system:manage = %d", req[0], req[1], rec.Code)
			}
		}
	}
	if fc.enables != 0 {
		t.Fatal("an enable got through without system:manage")
	}

	if rec := f.do(http.MethodPost, "/api/mcp/connectors", owner, add); rec.Code != http.StatusCreated {
		t.Fatalf("owner add = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, "/api/mcp/connectors", owner, `{"id":"bad","transport":"http","url":"http://example.com"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid connector = %d", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/api/mcp/connectors", owner, `{"id":"x","transport":"http","url":"https://e.com","surprise":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field = %d", rec.Code)
	}
	if rec := f.do(http.MethodPut, "/api/mcp/connectors/web/secrets/tok", owner, `{"value":"super-secret-value"}`); rec.Code != http.StatusNoContent || strings.Contains(rec.Body.String(), "super-secret") {
		t.Errorf("set secret = %d %s", rec.Code, rec.Body)
	}
	rec := f.do(http.MethodGet, "/api/mcp/connectors/web", viewer, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "super-secret") || !strings.Contains(rec.Body.String(), `"tok":true`) {
		t.Errorf("get = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, "/api/mcp/connectors/web/enable", owner, ""); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "ap-1") {
		t.Errorf("enable = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodGet, "/api/mcp/connectors/nope", viewer, ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing = %d", rec.Code)
	}
}
