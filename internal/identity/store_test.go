package identity

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestMigrations_FreshAndReopen(t *testing.T) {
	s, path := openTest(t)
	if v, _ := s.Version(); v != SchemaVersion() {
		t.Fatalf("fresh version = %d, want %d", v, SchemaVersion())
	}
	if _, err := s.CreatePrincipal("", authz.Person, "Ada", "test"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	// Re-opening an up-to-date store applies nothing and keeps the data.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	ps, _ := s2.Principals()
	if len(ps) != 1 {
		t.Fatalf("principals after reopen = %d", len(ps))
	}
}

func TestOpen_FileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	_, path := openTest(t)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("identity.db mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestMigrations_RefuseNewerSchema(t *testing.T) {
	s, path := openTest(t)
	if _, err := s.db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(path); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("Open newer store err = %v, want ErrNewerSchema", err)
	}
}

func TestPrincipalsAndStatus(t *testing.T) {
	s, _ := openTest(t)
	p, err := s.CreatePrincipal("", authz.Person, "Ada", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.ID == "Ada" || p.Status != StatusActive {
		t.Fatalf("principal = %+v", p)
	}
	if _, err := s.CreatePrincipal("", "robot", "x", "owner"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad kind err = %v", err)
	}
	if _, err := s.CreatePrincipal("", authz.Person, "a\nb", "owner"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad name err = %v", err)
	}
	if err := s.SetStatus(p.ID, StatusDisabled, "owner"); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.Subject(p.ID)
	if sub.Active {
		t.Error("disabled principal is active")
	}
	if err := s.SetStatus("nobody", StatusDisabled, "owner"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing principal err = %v", err)
	}
}

func TestGrants_RevokeAndExpiry(t *testing.T) {
	s, _ := openTest(t)
	clock := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	p, _ := s.CreatePrincipal("", authz.Person, "Ada", "owner")

	g, err := s.AddGrant(p.ID, authz.AgentsRun, authz.OwnResource, "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	exp := clock.Add(time.Hour)
	if _, err := s.AddGrant(p.ID, authz.SystemRead, authz.AnyResource, "owner", &exp); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.Subject(p.ID)
	if len(sub.Grants) != 2 {
		t.Fatalf("grants = %+v", sub.Grants)
	}
	// Revocation applies to the very next read.
	if err := s.RevokeGrant(g.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeGrant(g.ID, "owner"); !errors.Is(err, ErrNotFound) {
		t.Errorf("double revoke err = %v", err)
	}
	clock = clock.Add(2 * time.Hour)
	sub, _ = s.Subject(p.ID)
	if len(sub.Grants) != 0 {
		t.Fatalf("revoked/expired grants still live: %+v", sub.Grants)
	}

	// Validation: unknown scope, bad resource, person-only scope on a service.
	if _, err := s.AddGrant(p.ID, "root:all", authz.AnyResource, "owner", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown scope err = %v", err)
	}
	if _, err := s.AddGrant(p.ID, authz.SystemRead, "everything", "owner", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad resource err = %v", err)
	}
	svc, _ := s.CreatePrincipal("", authz.Service, "CI", "owner")
	if _, err := s.AddGrant(svc.ID, authz.ApprovalsDecide, authz.AnyResource, "owner", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("approvals:decide on a service err = %v", err)
	}
}

func TestCredentials_BindRebindUnbind(t *testing.T) {
	s, _ := openTest(t)
	a, _ := s.CreatePrincipal("", authz.Person, "Ada", "owner")
	b, _ := s.CreatePrincipal("", authz.Person, "Bob", "owner")
	if err := s.BindCredential("k1", a.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.CredentialPrincipal("k1"); p.ID != a.ID {
		t.Fatalf("k1 -> %s", p.ID)
	}
	if err := s.BindCredential("k1", b.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.CredentialPrincipal("k1"); p.ID != b.ID {
		t.Fatalf("rebind k1 -> %s", p.ID)
	}
	if err := s.UnbindCredential("k1", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CredentialPrincipal("k1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbound key err = %v", err)
	}
	if err := s.BindCredential("k2", "ghost", "owner"); err == nil {
		t.Error("binding to a missing principal must fail (foreign key)")
	}
}

func TestExternalIdentities(t *testing.T) {
	s, _ := openTest(t)
	a, _ := s.CreatePrincipal("", authz.Person, "Ada", "owner")
	b, _ := s.CreatePrincipal("", authz.Person, "Bob", "owner")
	if _, err := s.LinkExternal(a.ID, "Telegram", "12345", "owner"); err != nil {
		t.Fatal(err)
	}
	// Unique among active links, whoever asks.
	if _, err := s.LinkExternal(b.ID, "telegram", "12345", "owner"); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("duplicate link err = %v", err)
	}
	if p, _ := s.ExternalPrincipal("telegram", "12345"); p.ID != a.ID {
		t.Fatalf("lookup = %s", p.ID)
	}
	if s.ExternalRevoked("telegram", "12345") {
		t.Error("active link reported revoked")
	}
	if s.ExternalRevoked("telegram", "999") {
		t.Error("never-linked subject reported revoked")
	}
	if err := s.RevokeExternal("telegram", "12345", "owner"); err != nil {
		t.Fatal(err)
	}
	if !s.ExternalRevoked("telegram", "12345") {
		t.Error("revoked link not reported revoked")
	}
	if _, err := s.ExternalPrincipal("telegram", "12345"); !errors.Is(err, ErrNotFound) {
		t.Error("revoked link still resolves")
	}
	// After revocation the subject can be linked again (re-pairing).
	if _, err := s.LinkExternal(b.ID, "telegram", "12345", "owner"); err != nil {
		t.Fatalf("relink after revoke: %v", err)
	}
	if s.ExternalRevoked("telegram", "12345") {
		t.Error("re-linked subject still reported revoked")
	}
}

func TestDevices(t *testing.T) {
	s, _ := openTest(t)
	a, _ := s.CreatePrincipal("", authz.Person, "Ada", "owner")
	d, err := s.AddDevice(a.ID, "Ada's phone", "", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(d.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	ds, _ := s.Devices(a.ID)
	if len(ds) != 1 || ds[0].RevokedAt == nil {
		t.Fatalf("devices = %+v", ds)
	}
}

func TestClaim_FirstWinsUnderRace(t *testing.T) {
	s, _ := openTest(t)
	var ids []string
	for _, n := range []string{"A", "B", "C", "D"} {
		p, _ := s.CreatePrincipal("", authz.Person, n, "owner")
		ids = append(ids, p.ID)
	}
	var wg sync.WaitGroup
	owners := make([]string, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			o, err := s.Claim("session", "s1", id)
			if err != nil {
				t.Error(err)
			}
			owners[i] = o
		}(i, id)
	}
	wg.Wait()
	for _, o := range owners {
		if o != owners[0] {
			t.Fatalf("claimants saw different owners: %v", owners)
		}
	}
	if o, _ := s.Owner("session", "s1"); o != owners[0] {
		t.Fatalf("owner = %s, want %s", o, owners[0])
	}
	if o, _ := s.Owner("session", "nope"); o != "" {
		t.Errorf("unclaimed owner = %q", o)
	}
}

func TestAudit_RecordsLifecycle(t *testing.T) {
	s, _ := openTest(t)
	var hooked []string
	s.OnAudit = func(e AuditEvent) { hooked = append(hooked, e.Action) }
	p, _ := s.CreatePrincipal("", authz.Person, "Ada", "owner")
	_, _ = s.AddGrant(p.ID, authz.SystemRead, authz.AnyResource, "owner", nil)
	_ = s.BindCredential("k1", p.ID, "owner")
	_ = s.SetStatus(p.ID, StatusDisabled, "owner")
	evs, _ := s.Audit(10)
	want := []string{"principal.disabled", "credential.bind", "grant.add", "principal.create"}
	if len(evs) != len(want) {
		t.Fatalf("audit = %+v", evs)
	}
	for i, w := range want {
		if evs[i].Action != w || evs[i].Actor != "owner" {
			t.Errorf("audit[%d] = %+v, want %s by owner", i, evs[i], w)
		}
	}
	if len(hooked) != 4 {
		t.Errorf("hook saw %v", hooked)
	}
}

func TestMigrateKeys(t *testing.T) {
	s, _ := openTest(t)
	keys := []LegacyKey{
		{ID: "boot1", UserID: "admin", Description: "auto-created", Roles: []string{"admin"}},
		{ID: "boot2", UserID: "admin", Description: "setup key", Roles: []string{"admin"}},
		{ID: "boot3", UserID: "admin", Description: "tui-setup-key", Roles: []string{"admin"}},
		{ID: "fake1", UserID: "mallory", Description: "setup key", Roles: []string{"admin"}}, // not the bootstrap user
		{ID: "adm", UserID: "ci", Description: "ci deploy", Roles: []string{"admin"}},
		{ID: "view", UserID: "dash", Description: "wallboard", Roles: []string{"viewer"}},
		{ID: "op", UserID: "bot", Description: "ops bot", Roles: []string{"operator"}},
		{ID: "odd", UserID: "x", Description: "weird", Roles: []string{"superuser"}},
		{ID: "none", UserID: "x", Description: "empty"},
		{ID: "gone", UserID: "admin", Description: "setup key", Roles: []string{"admin"}, Revoked: true},
	}
	rep, err := s.MigrateKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(rep.Owner); got != 3 {
		t.Errorf("owner keys = %v", rep.Owner)
	}
	if len(rep.Service) != 4 || len(rep.NoGrants) != 2 {
		t.Errorf("report = %+v", rep)
	}
	if _, err := s.CredentialPrincipal("gone"); !errors.Is(err, ErrNotFound) {
		t.Error("revoked key was migrated")
	}

	now := time.Now()
	sub := func(key string) *authz.Subject {
		p, err := s.CredentialPrincipal(key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		su, _ := s.Subject(p.ID)
		return su
	}
	owner := sub("boot1")
	if owner.ID != OwnerID || owner.Kind != authz.Person || !authz.Decide(owner, authz.ApprovalsDecide, nil, now).Allow {
		t.Errorf("bootstrap key is not the owner person: %+v", owner)
	}
	// No silent promotion: a legacy admin service keeps admin scopes but
	// cannot approve; a viewer cannot run or approve; unknown roles get
	// nothing.
	adm := sub("adm")
	if adm.Kind != authz.Service || authz.Decide(adm, authz.ApprovalsDecide, nil, now).Allow || !authz.Decide(adm, authz.IdentityManage, nil, now).Allow {
		t.Errorf("legacy admin = %+v", adm)
	}
	if sub("fake1").ID == OwnerID {
		t.Error("a non-bootstrap user's key was bound to the owner")
	}
	view := sub("view")
	for _, sc := range []authz.Scope{authz.AgentsRun, authz.ApprovalsDecide, authz.DevopsRun, authz.ModelsUse, authz.RunsWrite} {
		if authz.Decide(view, sc, nil, now).Allow {
			t.Errorf("legacy viewer allowed %s", sc)
		}
	}
	op := sub("op")
	if !authz.Decide(op, authz.AgentsRun, nil, now).Allow || authz.Decide(op, authz.ApprovalsDecide, nil, now).Allow {
		t.Errorf("legacy operator = %+v", op)
	}
	for _, k := range []string{"odd", "none"} {
		if su := sub(k); len(su.Grants) != 0 {
			t.Errorf("%s got grants %+v", k, su.Grants)
		}
	}

	// Idempotent: a second run binds nothing and adds no grants.
	before, _ := s.Grants("legacy-key-adm")
	rep2, err := s.MigrateKeys(keys)
	if err != nil || !rep2.Empty() {
		t.Fatalf("second run = %+v, %v", rep2, err)
	}
	after, _ := s.Grants("legacy-key-adm")
	if len(after) != len(before) {
		t.Errorf("grants grew on re-run: %d -> %d", len(before), len(after))
	}
}

func TestRecoverOwner(t *testing.T) {
	s, _ := openTest(t)
	if _, err := s.EnsureOwner(); err != nil {
		t.Fatal(err)
	}
	// Lock the owner out: disable it and revoke every grant.
	_ = s.SetStatus(OwnerID, StatusDisabled, "attacker")
	gs, _ := s.Grants(OwnerID)
	for _, g := range gs {
		_ = s.RevokeGrant(g.ID, "attacker")
	}
	if err := s.RecoverOwner("newkey"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.CredentialPrincipal("newkey")
	sub, _ := s.Subject(p.ID)
	if sub.ID != OwnerID || !sub.Active || !authz.Decide(sub, authz.IdentityManage, nil, time.Now()).Allow {
		t.Fatalf("recovered owner = %+v", sub)
	}
	// Recovering again does not duplicate grants.
	n := len(sub.Grants)
	_ = s.RecoverOwner("newkey2")
	sub, _ = s.Subject(OwnerID)
	if len(sub.Grants) != n {
		t.Errorf("grants %d -> %d after second recovery", n, len(sub.Grants))
	}
	evs, _ := s.Audit(1)
	if evs[0].Action != "owner.recover" || evs[0].Actor != LocalCLIActor {
		t.Errorf("last audit = %+v", evs[0])
	}
}
