package cmd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/approvals"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

func TestApprovalAuthorizer(t *testing.T) {
	ids, err := identity.Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ids.Close() }()
	if _, err := ids.EnsureOwner(); err != nil {
		t.Fatal(err)
	}
	mk := func(kind authz.Kind, role string) string {
		p, err := ids.CreatePrincipal("", kind, role, "test")
		if err != nil {
			t.Fatal(err)
		}
		if err := ids.ApplyTemplate(p.ID, role, "test"); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	ada := mk(authz.Person, "operator")
	viewer := mk(authz.Person, "viewer")
	svc := mk(authz.Service, "admin")
	if _, err := ids.Claim("session", "ada-1", ada); err != nil {
		t.Fatal(err)
	}
	if _, err := ids.LinkExternal(viewer, "telegram", "200", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := ids.LinkExternal(ada, "telegram", "100", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := ids.LinkExternal(identity.OwnerID, "whatsapp", "15550001111", "test"); err != nil {
		t.Fatal(err)
	}
	if err := ids.RevokeExternal("whatsapp", "15550001111", "test"); err != nil {
		t.Fatal(err)
	}

	a := approvals.Approval{ID: "0123456789abcdef", SessionID: "ada-1"}
	cases := []struct {
		name    string
		d       approvals.Decider
		linked  bool
		ok      bool
		recorde string
	}{
		{"owner over the API", approvals.Decider{PrincipalID: identity.OwnerID, Channel: "dashboard"}, false, true, identity.OwnerID},
		{"session owner over the API", approvals.Decider{PrincipalID: ada, Channel: "api"}, false, true, ada},
		{"viewer over the API", approvals.Decider{PrincipalID: viewer, Channel: "api"}, false, false, ""},
		{"service admin over the API", approvals.Decider{PrincipalID: svc, Channel: "api"}, false, false, ""},
		{"API without principal", approvals.Decider{Channel: "api"}, false, false, ""},
		{"linked person on telegram", approvals.Decider{Channel: "telegram", External: "100"}, false, true, ada},
		{"linked viewer on telegram", approvals.Decider{Channel: "telegram", External: "200"}, false, false, ""},
		{"revoked link on whatsapp", approvals.Decider{Channel: "whatsapp", External: "15550001111"}, false, false, ""},
		{"unlinked telegram user (allow-list)", approvals.Decider{Channel: "telegram", External: "300"}, false, true, ""},
		{"unlinked telegram user, links required", approvals.Decider{Channel: "telegram", External: "300"}, true, false, ""},
		{"channel without identity", approvals.Decider{Channel: "slack"}, false, false, ""},
		{"unknown channel", approvals.Decider{Channel: "unattributed", PrincipalID: identity.OwnerID}, false, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			who, err := approvalAuthorizer(ids, c.linked)(context.Background(), a, c.d)
			if c.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, c.ok)
			}
			if err != nil && !errors.Is(err, approvals.ErrForbidden) {
				t.Fatalf("refusal is not ErrForbidden: %v", err)
			}
			if c.ok && who.PrincipalID != c.recorde {
				t.Errorf("recorded principal = %q, want %q", who.PrincipalID, c.recorde)
			}
		})
	}
}

func TestTelegramAPIBase(t *testing.T) {
	for v, want := range map[string]string{
		"":                          "",
		"https://tg.example.com/":   "https://tg.example.com/botTOKEN",
		"http://127.0.0.1:8081":     "http://127.0.0.1:8081/botTOKEN",
		"http://localhost:8081/api": "http://localhost:8081/api/botTOKEN",
	} {
		t.Setenv("GUBERN_TELEGRAM_API_URL", v)
		got, err := telegramAPIBase("TOKEN")
		if err != nil || got != want {
			t.Errorf("%q → %q, %v; want %q", v, got, err, want)
		}
	}
	for _, bad := range []string{"http://tg.example.com", "ftp://x", "not a url"} {
		t.Setenv("GUBERN_TELEGRAM_API_URL", bad)
		if _, err := telegramAPIBase("TOKEN"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
