//go:build integration

package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// Milestone H against a real server: a paired phone (P-256 key, as in the
// Secure Enclave / Android Keystore) approves only with a signed step-up,
// and a revoked phone can do nothing.
func TestStepUp_PhoneApprovesWithHardwareKeySignature(t *testing.T) {
	testutil.StartFakeAI(t)
	owner := seedAdminKey(t)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)

	// Pair a phone holding a P-256 key.
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	sign := func(msg string) string {
		sum := sha256.Sum256([]byte(msg))
		sig, _ := ecdsa.SignASN1(rand.Reader, k, sum[:])
		return base64.StdEncoding.EncodeToString(sig)
	}
	_, raw := apiCall(t, p, http.MethodPost, "/api/pairing", owner, map[string]any{"purpose": "device"})
	var pr struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(raw, &pr)
	code, raw := apiCall(t, p, http.MethodPost, "/api/pairing/redeem", "", map[string]string{
		"code": pr.Code, "label": "Owner phone", "public_key": base64.StdEncoding.EncodeToString(der),
	})
	if code != http.StatusCreated {
		t.Fatalf("redeem = %d %s", code, raw)
	}
	var dev struct {
		Secret   string `json:"secret"`
		DeviceID string `json:"device_id"`
	}
	_ = json.Unmarshal(raw, &dev)

	// A real run pauses on an approval (started from the dashboard key).
	done := submitAsync(t, p, owner, "phone-1", toolRun)
	item := waitPending(t, p, owner, "phone-1")
	a := getApproval(t, p, dev.Secret, item.ActionID)
	path := "/api/approvals/" + a.ID + "/decision"

	// The phone's credential alone cannot approve.
	if code, raw := apiCall(t, p, http.MethodPost, path, dev.Secret, map[string]any{"approved": true, "action_hash": a.ActionHash}); code != http.StatusForbidden || !strings.Contains(string(raw), "stepup-required") {
		t.Fatalf("no step-up = %d %s", code, raw)
	}
	if code, _ := apiCall(t, p, http.MethodPost, "/api/agents/approve", dev.Secret, map[string]any{"session_id": "phone-1", "action_id": a.ID, "approved": true}); code != http.StatusForbidden {
		t.Errorf("legacy approve from the phone = %d", code)
	}
	challenge := func() (string, string) {
		code, raw := apiCall(t, p, http.MethodPost, "/api/stepup/challenge", dev.Secret, map[string]string{"approval_id": a.ID})
		if code != http.StatusOK {
			t.Fatalf("challenge = %d %s", code, raw)
		}
		var c struct {
			ID    string `json:"challenge_id"`
			Nonce string `json:"nonce"`
		}
		_ = json.Unmarshal(raw, &c)
		return c.ID, c.Nonce
	}
	// A forged signature is refused and spends the challenge.
	cid, nonce := challenge()
	forged := map[string]any{"approved": true, "action_hash": a.ActionHash, "stepup": map[string]string{"challenge_id": cid, "signature": base64.StdEncoding.EncodeToString(make([]byte, 64))}}
	if code, _ := apiCall(t, p, http.MethodPost, path, dev.Secret, forged); code != http.StatusForbidden {
		t.Fatalf("forged signature = %d", code)
	}
	good := map[string]any{"approved": true, "action_hash": a.ActionHash, "stepup": map[string]string{"challenge_id": cid, "signature": sign(api.StepUpMessage(cid, nonce, a.ID, a.ActionHash, true))}}
	if code, _ := apiCall(t, p, http.MethodPost, path, dev.Secret, good); code != http.StatusForbidden {
		t.Fatalf("reused challenge = %d", code)
	}
	// A fresh challenge, signed by the phone's key, approves.
	cid, nonce = challenge()
	good["stepup"] = map[string]string{"challenge_id": cid, "signature": sign(api.StepUpMessage(cid, nonce, a.ID, a.ActionHash, true))}
	if code, raw := apiCall(t, p, http.MethodPost, path, dev.Secret, good); code != http.StatusOK {
		t.Fatalf("signed approval = %d %s", code, raw)
	}
	select {
	case res := <-done:
		if !strings.Contains(res.response, "fake-ai: done") {
			t.Errorf("run reply = %q", res.response)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish after the phone approved")
	}
	final := getApproval(t, p, owner, a.ID)
	if final.Status != "approved" || final.DecidedVia != "mobile" || final.DecidedBy != "owner" {
		t.Fatalf("record = %+v", final)
	}

	// Credential revocation: the revoked phone can't read or decide anything.
	if code, _ := apiCall(t, p, http.MethodDelete, "/api/devices/"+dev.DeviceID, owner, nil); code != http.StatusOK {
		t.Fatal("revoke failed")
	}
	for _, path := range []string{"/api/approvals?status=pending", "/api/me"} {
		if code, _ := apiCall(t, p, http.MethodGet, path, dev.Secret, nil); code != http.StatusUnauthorized {
			t.Errorf("revoked phone %s = %d, want 401", path, code)
		}
	}
}
