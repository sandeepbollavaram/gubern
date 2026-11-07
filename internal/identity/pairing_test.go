package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

func pubKey(t *testing.T) string {
	t.Helper()
	pk, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pk)
}

func person(t *testing.T, s *Store, role string) string {
	t.Helper()
	p, err := s.CreatePrincipal("", authz.Person, role, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyTemplate(p.ID, role, "test"); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func issuer(n *int) IssueFunc {
	return func(string) (string, error) {
		*n++
		return "key" + strings.Repeat("x", *n), nil
	}
}

func TestPairingCode_Shape(t *testing.T) {
	c, err := newPairingCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 14 || c[4] != '-' || c[9] != '-' {
		t.Fatalf("code = %q", c)
	}
	if hashCode(strings.ToLower(c)) != hashCode(c) || hashCode(strings.ReplaceAll(c, "-", " ")) != hashCode(c) {
		t.Error("normalisation changes the hash")
	}
	if NormalizeCode("o1l-I") != "0111" {
		t.Errorf("normalize = %q", NormalizeCode("o1l-I"))
	}
}

func TestRedeemDevice_OnceWithinTTLWithCeiling(t *testing.T) {
	s, _ := openTest(t)
	ada := person(t, s, "operator")
	ceiling := []authz.Scope{authz.ApprovalsRead, authz.ApprovalsDecide}
	_, code, err := s.CreatePairing(ada, PairDevice, "phone", ceiling, ada, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	d, pr, keyID, err := s.RedeemDevice(code, "Ada's phone", pubKey(t), issuer(&n))
	if err != nil {
		t.Fatal(err)
	}
	if d.PrincipalID != ada || pr.PrincipalID != ada || n != 1 {
		t.Fatalf("device = %+v pairing = %+v", d, pr)
	}
	ci, _ := s.Credential(keyID)
	if ci.DeviceID != d.ID || ci.PrincipalID != ada || len(ci.Ceiling) != 2 {
		t.Fatalf("credential = %+v", ci)
	}
	sub, _ := s.Subject(ada)
	ApplyCeiling(sub, ci.Ceiling)
	for _, g := range sub.Grants {
		if g.Scope != authz.ApprovalsRead && g.Scope != authz.ApprovalsDecide {
			t.Errorf("ceiling kept %s", g.Scope)
		}
	}
	if authz.Decide(sub, authz.AgentsRun, nil, time.Now()).Allow {
		t.Error("device credential can run agents beyond its ceiling")
	}

	// Replay: the same code (any spelling) again issues nothing.
	if _, _, _, err := s.RedeemDevice(strings.ToLower(code), "thief", pubKey(t), issuer(&n)); !errors.Is(err, ErrPairingInvalid) || n != 1 {
		t.Fatalf("replay err = %v issued=%d", err, n)
	}
}

func TestRedeemDevice_ExpiredUnknownAndBadKey(t *testing.T) {
	s, _ := openTest(t)
	clock := time.Now()
	s.now = func() time.Time { return clock }
	ada := person(t, s, "operator")
	_, code, _ := s.CreatePairing(ada, PairDevice, "", []authz.Scope{authz.ApprovalsRead}, ada, time.Minute)
	n := 0
	if _, _, _, err := s.RedeemDevice(code, "p", "not-a-key", issuer(&n)); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad key err = %v", err)
	}
	clock = clock.Add(time.Minute)
	if _, _, _, err := s.RedeemDevice(code, "p", pubKey(t), issuer(&n)); !errors.Is(err, ErrPairingInvalid) {
		t.Errorf("expired err = %v", err)
	}
	if _, _, _, err := s.RedeemDevice("AAAA-BBBB-CCCC", "p", pubKey(t), issuer(&n)); !errors.Is(err, ErrPairingInvalid) {
		t.Errorf("unknown err = %v", err)
	}
	if n != 0 {
		t.Errorf("issued %d keys for invalid redemptions", n)
	}
}

func TestCreatePairing_Validation(t *testing.T) {
	s, _ := openTest(t)
	ada := person(t, s, "operator")
	svc, _ := s.CreatePrincipal("", authz.Service, "ci", "test")
	if _, _, err := s.CreatePairing(svc.ID, PairDevice, "", []authz.Scope{authz.ApprovalsRead}, "x", 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("service pairing err = %v", err)
	}
	if _, _, err := s.CreatePairing(ada, PairDevice, "", nil, ada, 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("no ceiling err = %v", err)
	}
	if _, _, err := s.CreatePairing(ada, "email", "", nil, ada, 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad purpose err = %v", err)
	}
	if _, _, err := s.CreatePairing(ada, PairDevice, "", []authz.Scope{"root:all"}, ada, 0); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown scope err = %v", err)
	}
}

func TestRedeemExternal_LinksAndConflicts(t *testing.T) {
	s, _ := openTest(t)
	ada := person(t, s, "operator")
	bob := person(t, s, "operator")

	_, code, _ := s.CreatePairing(ada, PairTelegram, "", nil, ada, 0)
	// The wrong channel can't use it, and three wrong tries burn it.
	for i := 0; i < maxPurposeFailures; i++ {
		if _, err := s.RedeemExternal(code, "whatsapp", "15550001111"); !errors.Is(err, ErrPairingInvalid) {
			t.Fatalf("wrong channel err = %v", err)
		}
	}
	if _, err := s.RedeemExternal(code, "telegram", "100"); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("burned code err = %v", err)
	}

	_, code, _ = s.CreatePairing(ada, PairTelegram, "", nil, ada, 0)
	if _, err := s.RedeemExternal(code, "telegram", "100"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.ExternalPrincipal("telegram", "100"); p.ID != ada {
		t.Fatalf("telegram:100 -> %s", p.ID)
	}
	// The same account can't be taken by another person's code; that code
	// survives for Bob's own account.
	_, bobCode, _ := s.CreatePairing(bob, PairTelegram, "", nil, bob, 0)
	if _, err := s.RedeemExternal(bobCode, "telegram", "100"); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("conflict err = %v", err)
	}
	if _, err := s.RedeemExternal(bobCode, "telegram", "200"); err != nil {
		t.Fatalf("bob's own account: %v", err)
	}
	// Linking the same account to the same person again is reported.
	_, again, _ := s.CreatePairing(ada, PairTelegram, "", nil, ada, 0)
	if _, err := s.RedeemExternal(again, "telegram", "100"); !errors.Is(err, ErrAlreadyPaired) {
		t.Fatalf("re-pair err = %v", err)
	}
	// One person may hold accounts on several channels.
	_, wa, _ := s.CreatePairing(ada, PairWhatsApp, "", nil, ada, 0)
	if _, err := s.RedeemExternal(wa, "whatsapp", "15550001111"); err != nil {
		t.Fatal(err)
	}
	links, _ := s.ExternalIdentities(ada)
	if len(links) != 2 {
		t.Errorf("ada links = %+v", links)
	}
}

func TestDeviceRotationAndRevocation(t *testing.T) {
	s, _ := openTest(t)
	ada := person(t, s, "operator")
	_, code, _ := s.CreatePairing(ada, PairDevice, "", []authz.Scope{authz.ApprovalsRead}, ada, 0)
	n := 0
	d, _, k1, err := s.RedeemDevice(code, "phone", pubKey(t), issuer(&n))
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.RebindDeviceKey(d.ID, "k2", ada)
	if err != nil || len(old) != 1 || old[0] != k1 {
		t.Fatalf("rotate = %v %v", old, err)
	}
	if _, err := s.Credential(k1); !errors.Is(err, ErrNotFound) {
		t.Error("old key still bound after rotation")
	}
	if ci, _ := s.Credential("k2"); ci.DeviceID != d.ID || len(ci.Ceiling) != 1 {
		t.Errorf("new credential = %+v", ci)
	}
	s.TouchDevice(d.ID)
	if got, _ := s.Device(d.ID); got.LastSeenAt == nil {
		t.Error("last seen not recorded")
	}
	if err := s.RevokeDevice(d.ID, ada); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Device(d.ID); got.RevokedAt == nil {
		t.Error("device not revoked")
	}
}
