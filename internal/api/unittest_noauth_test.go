package api

import (
	"path/filepath"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// Unit tests in this package build servers without an auth layer to test
// handler behaviour, as they did before milestone A. This is the only place
// unitTestNoAuth is set; it is a _test.go file, so production binaries never
// contain it. Authorization itself is tested with a real auth layer and
// identity store in authorize_test.go and the integration suite.
func init() { unitTestNoAuth = true }

// wireTestAuth wires the auth layer and a temporary identity store, binding
// every key already in keys to its own person principal with the grants of
// the key's role. Tests of handler behaviour use it in place of SetAuth; the
// production key migration (persons only for bootstrap keys) is tested in
// internal/identity and the integration suite.
func wireTestAuth(t *testing.T, s *Server, keys *auth.APIKeyStore, rbac *auth.RBAC) *identity.Store {
	t.Helper()
	s.SetAuth(auth.NewAuthMiddleware(keys, nil, rbac), keys, rbac)
	st, err := identity.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, k := range keys.All() {
		bindTestPerson(t, st, k)
	}
	s.SetIdentity(st)
	return st
}

// bindTestPerson binds key k to a new person with k's role templates.
func bindTestPerson(t *testing.T, st *identity.Store, k auth.APIKey) string {
	t.Helper()
	p, err := st.CreatePrincipal("", authz.Person, "test "+k.UserID, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range k.Roles {
		if err := st.ApplyTemplate(p.ID, string(r), "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.BindCredential(k.ID, p.ID, "test"); err != nil {
		t.Fatal(err)
	}
	return p.ID
}
