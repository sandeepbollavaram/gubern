package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

func devKey(t *testing.T) string {
	t.Helper()
	pk, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pk)
}

type pairResp struct {
	Code    string           `json:"code"`
	QR      string           `json:"qr"`
	Pairing identity.Pairing `json:"pairing"`
}

type redeemResp struct {
	DeviceID string   `json:"device_id"`
	KeyID    string   `json:"key_id"`
	Secret   string   `json:"secret"`
	Scopes   []string `json:"scopes"`
}

func (f *authzFixture) pair(t *testing.T, key, body string) (int, pairResp) {
	t.Helper()
	rec := f.do(http.MethodPost, "/api/pairing", key, body)
	var out pairResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (f *authzFixture) redeem(t *testing.T, code string) (int, redeemResp) {
	t.Helper()
	rec := f.do(http.MethodPost, "/api/pairing/redeem", "", fmt.Sprintf(`{"code":%q,"label":"phone","public_key":%q}`, code, devKey(t)))
	var out redeemResp
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestPairing_DeviceRedeemsOnceWithinItsCeiling(t *testing.T) {
	f := newAuthzFixture(t)
	ada, adaKey := f.principal(t, authz.Person, "operator")
	_, bobKey := f.principal(t, authz.Person, "operator")

	code, p := f.pair(t, adaKey, `{"purpose":"device","label":"Ada's phone","scopes":["approvals:read","approvals:decide","agents:read"]}`)
	if code != http.StatusCreated || p.Code == "" || !strings.HasPrefix(p.QR, "gubern://pair?") || !strings.Contains(p.QR, "c=") {
		t.Fatalf("pair = %d %+v", code, p)
	}
	st, dev := f.redeem(t, p.Code)
	if st != http.StatusCreated || dev.Secret == "" || dev.DeviceID == "" {
		t.Fatalf("redeem = %d %+v", st, dev)
	}
	// The device acts as Ada, limited to its ceiling.
	if rec := f.do(http.MethodGet, "/api/agents/status", dev.Secret, ""); rec.Code != http.StatusOK {
		t.Errorf("device agents:read = %d", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/api/agents/submit", dev.Secret, `{"message":"x","session_id":"d1"}`); rec.Code != http.StatusForbidden {
		t.Errorf("device beyond its ceiling (agents:run) = %d, want 403", rec.Code)
	}
	var me struct {
		PrincipalID string   `json:"principal_id"`
		Scopes      []string `json:"scopes"`
	}
	_ = json.Unmarshal(f.do(http.MethodGet, "/api/me", dev.Secret, "").Body.Bytes(), &me)
	if me.PrincipalID != ada || len(me.Scopes) != 3 {
		t.Errorf("device me = %+v", me)
	}
	// Replay: the code is spent.
	if st, _ := f.redeem(t, p.Code); st != http.StatusBadRequest {
		t.Errorf("replay = %d, want 400", st)
	}
	// Only Ada (and "*" holders) see and manage it.
	if rec := f.do(http.MethodGet, "/api/devices", bobKey, ""); strings.Contains(rec.Body.String(), dev.DeviceID) {
		t.Error("bob sees ada's device")
	}
	if rec := f.do(http.MethodDelete, "/api/devices/"+dev.DeviceID, bobKey, ""); rec.Code != http.StatusForbidden {
		t.Errorf("bob revokes ada's device = %d, want 403", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/api/devices", adaKey, ""); !strings.Contains(rec.Body.String(), dev.DeviceID) {
		t.Error("ada does not see her device")
	}
}

func TestPairing_RotateThenRevoke(t *testing.T) {
	f := newAuthzFixture(t)
	_, adaKey := f.principal(t, authz.Person, "operator")
	_, p := f.pair(t, adaKey, `{"purpose":"device"}`)
	_, dev := f.redeem(t, p.Code)

	rec := f.do(http.MethodPost, "/api/devices/"+dev.DeviceID+"/rotate", adaKey, "")
	var rot redeemResp
	_ = json.Unmarshal(rec.Body.Bytes(), &rot)
	if rec.Code != http.StatusOK || rot.Secret == "" {
		t.Fatalf("rotate = %d %s", rec.Code, rec.Body)
	}
	if c := f.do(http.MethodGet, "/api/me", dev.Secret, "").Code; c != http.StatusUnauthorized {
		t.Errorf("old credential after rotation = %d, want 401", c)
	}
	if c := f.do(http.MethodGet, "/api/me", rot.Secret, "").Code; c != http.StatusOK {
		t.Errorf("new credential = %d", c)
	}
	if c := f.do(http.MethodDelete, "/api/devices/"+dev.DeviceID, adaKey, "").Code; c != http.StatusOK {
		t.Fatalf("revoke = %d", c)
	}
	if c := f.do(http.MethodGet, "/api/me", rot.Secret, "").Code; c != http.StatusUnauthorized {
		t.Errorf("credential of a revoked device = %d, want 401", c)
	}
}

func TestPairing_GuessingIsRateLimited(t *testing.T) {
	f := newAuthzFixture(t)
	limited := false
	for i := 0; i < 30; i++ {
		st, _ := f.redeem(t, fmt.Sprintf("ZZZZ-ZZZZ-%04d", i))
		if st == http.StatusTooManyRequests {
			limited = true
			break
		}
		if st != http.StatusBadRequest {
			t.Fatalf("guess %d = %d", i, st)
		}
	}
	if !limited {
		t.Fatal("30 guesses from one address were never rate limited")
	}
}

func TestPairing_ExpiredCode(t *testing.T) {
	f := newAuthzFixture(t)
	ada, _ := f.principal(t, authz.Person, "operator")
	_, code, err := f.st.CreatePairing(ada, identity.PairDevice, "", []authz.Scope{authz.ApprovalsRead}, ada, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if st, _ := f.redeem(t, code); st != http.StatusBadRequest {
		t.Errorf("expired code = %d, want 400", st)
	}
}

func TestPairing_NoEscalationAndOtherPeople(t *testing.T) {
	f := newAuthzFixture(t)
	_, adaKey := f.principal(t, authz.Person, "operator")
	bob, _ := f.principal(t, authz.Person, "operator")
	_, viewerKey := f.principal(t, authz.Person, "viewer")
	_, owner := f.principal(t, authz.Person, "owner")

	if c, _ := f.pair(t, viewerKey, `{"purpose":"device"}`); c != http.StatusForbidden {
		t.Errorf("viewer pairs a device = %d, want 403", c)
	}
	if c, _ := f.pair(t, adaKey, `{"purpose":"device","scopes":["identity:manage"]}`); c != http.StatusForbidden {
		t.Errorf("operator gives a device identity:manage = %d, want 403", c)
	}
	if c, p := f.pair(t, adaKey, `{"purpose":"device","scopes":["identity:manage","approvals:read"]}`); c != http.StatusCreated || len(p.Pairing.Scopes) != 1 {
		t.Errorf("mixed scopes = %d %+v", c, p.Pairing.Scopes)
	}
	if c, _ := f.pair(t, adaKey, `{"purpose":"telegram","principal_id":"`+bob+`"}`); c != http.StatusForbidden {
		t.Errorf("ada pairs for bob = %d, want 403", c)
	}
	if c, p := f.pair(t, owner, `{"purpose":"telegram","principal_id":"`+bob+`"}`); c != http.StatusCreated || p.Pairing.PrincipalID != bob {
		t.Errorf("owner pairs for bob = %d %+v", c, p)
	}
	if c, _ := f.pair(t, adaKey, `{"purpose":"email"}`); c != http.StatusBadRequest {
		t.Errorf("bad purpose = %d", c)
	}
}

func TestLinks_RevokeIsOwnerScoped(t *testing.T) {
	f := newAuthzFixture(t)
	ada, adaKey := f.principal(t, authz.Person, "operator")
	_, bobKey := f.principal(t, authz.Person, "operator")
	_, code, _ := f.st.CreatePairing(ada, identity.PairTelegram, "", nil, ada, 0)
	if _, err := f.st.RedeemExternal(code, "telegram", "4242"); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(http.MethodGet, "/api/links", adaKey, ""); !strings.Contains(rec.Body.String(), "4242") {
		t.Errorf("ada links = %s", rec.Body)
	}
	if rec := f.do(http.MethodGet, "/api/links", bobKey, ""); strings.Contains(rec.Body.String(), "4242") {
		t.Error("bob sees ada's link")
	}
	if c := f.do(http.MethodDelete, "/api/links/telegram/4242", bobKey, "").Code; c != http.StatusForbidden {
		t.Errorf("bob unlinks ada = %d", c)
	}
	if c := f.do(http.MethodDelete, "/api/links/telegram/4242", adaKey, "").Code; c != http.StatusOK {
		t.Errorf("ada unlinks = %d", c)
	}
	if !f.st.ExternalRevoked("telegram", "4242") {
		t.Error("link not revoked")
	}
}
