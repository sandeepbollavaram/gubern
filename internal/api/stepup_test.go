package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/approvals"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// brokerRuntime decides through a real approval service.
type brokerRuntime struct {
	stubRuntime
	svc *approvals.Service
}

func (b brokerRuntime) DecideApproval(ctx context.Context, _, id string, approve bool, hash string, d approvals.Decider) (string, approvals.Approval, error) {
	a, err := b.svc.Decide(ctx, id, approvals.Decision{Approve: approve, ActionHash: hash, Decider: d})
	return "ok", a, err
}

// signer is a device key: Ed25519 or P-256 (hardware-backed on phones).
type signer struct {
	pub  string
	sign func(msg string) string
}

func edSigner(_ *testing.T) signer {
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	return signer{base64.StdEncoding.EncodeToString(pk), func(m string) string {
		return base64.StdEncoding.EncodeToString(ed25519.Sign(sk, []byte(m)))
	}}
}

func p256Signer(_ *testing.T) signer {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	return signer{base64.StdEncoding.EncodeToString(der), func(m string) string {
		sum := sha256.Sum256([]byte(m))
		sig, _ := ecdsa.SignASN1(rand.Reader, k, sum[:])
		return base64.StdEncoding.EncodeToString(sig)
	}}
}

type stepFixture struct {
	*authzFixture
	svc *approvals.Service
	ada string
}

func newStepFixture(t *testing.T) *stepFixture {
	f := newAuthzFixture(t)
	svc, _, err := approvals.Open(filepath.Join(t.TempDir(), "approvals.db"), approvals.Options{
		Authorizer: func(_ context.Context, _ approvals.Approval, d approvals.Decider) (approvals.Decider, error) {
			return d, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	f.s.SetApprovals(svc)
	f.s.SetAgentRuntime(brokerRuntime{svc: svc})
	ada, _ := f.principal(t, authz.Person, "operator")
	if _, err := f.st.Claim(resSession, "sa", ada); err != nil {
		t.Fatal(err)
	}
	return &stepFixture{authzFixture: f, svc: svc, ada: ada}
}

// device pairs a device for Ada with key k and returns its credential.
func (f *stepFixture) device(t *testing.T, k signer) (string, string) {
	t.Helper()
	_, code, err := f.st.CreatePairing(f.ada, identity.PairDevice, "", DefaultDeviceScopes, f.ada, 0)
	if err != nil {
		t.Fatal(err)
	}
	var secret string
	d, _, _, err := f.st.RedeemDevice(code, "phone", k.pub, func(pid string) (string, error) {
		key, sec, err := f.keys.Issue(pid, "default", nil, "device", 0)
		secret = sec
		return key.ID, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID, secret
}

func (f *stepFixture) approval(t *testing.T) approvals.Approval {
	a, err := f.svc.Create(context.Background(), approvals.CreateRequest{SessionID: "sa", Risk: "HIGH RISK",
		Action: approvals.Action{Tool: "run_terminal", Params: map[string]any{"command": "rm -rf build"}}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type challenge struct {
	ID    string `json:"challenge_id"`
	Nonce string `json:"nonce"`
}

func (f *stepFixture) challenge(t *testing.T, secret, approvalID string) challenge {
	t.Helper()
	rec := f.do(http.MethodPost, "/api/stepup/challenge", secret, `{"approval_id":"`+approvalID+`"}`)
	var c challenge
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	if rec.Code != http.StatusOK || c.ID == "" {
		t.Fatalf("challenge = %d %s", rec.Code, rec.Body)
	}
	return c
}

func (f *stepFixture) decide(secret string, a approvals.Approval, approve bool, proof string) (int, string) {
	rec := f.do(http.MethodPost, "/api/approvals/"+a.ID+"/decision", secret,
		fmt.Sprintf(`{"approved":%v,"action_hash":%q%s}`, approve, a.ActionHash, proof))
	return rec.Code, rec.Body.String()
}

func proof(c challenge, sig string) string {
	return fmt.Sprintf(`,"stepup":{"challenge_id":%q,"signature":%q}`, c.ID, sig)
}

func TestStepUp_DeviceApprovesOnlyWithValidSignature(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) signer{"ed25519": edSigner, "p256": p256Signer} {
		t.Run(name, func(t *testing.T) {
			f := newStepFixture(t)
			k := mk(t)
			_, secret := f.device(t, k)

			a := f.approval(t)
			if code, body := f.decide(secret, a, true, ""); code != http.StatusForbidden || !strings.Contains(body, "stepup-required") {
				t.Fatalf("no step-up = %d %s", code, body)
			}
			// A signature over "reject" can't approve.
			c := f.challenge(t, secret, a.ID)
			if code, body := f.decide(secret, a, true, proof(c, k.sign(StepUpMessage(c.ID, c.Nonce, a.ID, a.ActionHash, false)))); code != http.StatusForbidden || !strings.Contains(body, "stepup-invalid") {
				t.Fatalf("reject signature used to approve = %d %s", code, body)
			}
			// That challenge is spent, even though the attempt failed.
			if code, _ := f.decide(secret, a, true, proof(c, k.sign(StepUpMessage(c.ID, c.Nonce, a.ID, a.ActionHash, true)))); code != http.StatusForbidden {
				t.Fatalf("reused challenge = %d", code)
			}
			// A fresh, correct proof approves, recorded as mobile.
			c = f.challenge(t, secret, a.ID)
			code, body := f.decide(secret, a, true, proof(c, k.sign(StepUpMessage(c.ID, c.Nonce, a.ID, a.ActionHash, true))))
			if code != http.StatusOK || !strings.Contains(body, `"decided_via":"mobile"`) {
				t.Fatalf("valid step-up = %d %s", code, body)
			}
			// Replaying it on a new approval does nothing.
			b := f.approval(t)
			if code, _ := f.decide(secret, b, true, proof(c, k.sign(StepUpMessage(c.ID, c.Nonce, b.ID, b.ActionHash, true)))); code != http.StatusForbidden {
				t.Fatalf("replayed challenge on another approval = %d", code)
			}
		})
	}
}

func TestStepUp_BindingAndExpiry(t *testing.T) {
	f := newStepFixture(t)
	k := edSigner(t)
	_, secret := f.device(t, k)
	a, b := f.approval(t), f.approval(t)

	// A challenge for approval a can't decide approval b.
	c := f.challenge(t, secret, a.ID)
	if code, _ := f.decide(secret, b, true, proof(c, k.sign(StepUpMessage(c.ID, c.Nonce, b.ID, b.ActionHash, true)))); code != http.StatusForbidden {
		t.Errorf("cross-approval challenge = %d", code)
	}
	// Another device's key can't answer this device's challenge.
	other := edSigner(t)
	c = f.challenge(t, secret, b.ID)
	if code, _ := f.decide(secret, b, true, proof(c, other.sign(StepUpMessage(c.ID, c.Nonce, b.ID, b.ActionHash, true)))); code != http.StatusForbidden {
		t.Errorf("foreign key = %d", code)
	}
	// An expired challenge is refused.
	c = f.challenge(t, secret, b.ID)
	f.s.stepUp.mu.Lock()
	ch := f.s.stepUp.m[c.ID]
	ch.expires = time.Now().Add(-time.Second)
	f.s.stepUp.m[c.ID] = ch
	f.s.stepUp.mu.Unlock()
	if code, _ := f.decide(secret, b, true, proof(c, k.sign(StepUpMessage(c.ID, c.Nonce, b.ID, b.ActionHash, true)))); code != http.StatusForbidden {
		t.Errorf("expired challenge = %d", code)
	}
	// Rejecting needs no step-up; the legacy approve route refuses devices.
	if code, body := f.decide(secret, b, false, ""); code != http.StatusOK {
		t.Errorf("reject = %d %s", code, body)
	}
	if rec := f.do(http.MethodPost, "/api/agents/approve", secret, `{"session_id":"sa","action_id":"`+a.ID+`","approved":true}`); rec.Code != http.StatusForbidden {
		t.Errorf("legacy approve from a device = %d", rec.Code)
	}
	// Only devices take challenges.
	_, person := f.principal(t, authz.Person, "owner")
	if rec := f.do(http.MethodPost, "/api/stepup/challenge", person, `{"approval_id":"`+a.ID+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("challenge for a non-device = %d", rec.Code)
	}
}

// TestStepUpVector pins the signing format the mobile app shares
// (mobile/test/fixtures/stepup_vector.json): a fixed Ed25519 key and inputs
// must give this exact message and signature on both sides.
func TestStepUpVector(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = 7
	}
	sk := ed25519.NewKeyFromSeed(seed)
	hash := strings.Repeat("ab", 32)
	msg := StepUpMessage("su_000000000000000000000000", "00112233", "0123456789abcdef", hash, true)
	want := map[string]string{
		"seed_hex":     strings.Repeat("07", 32),
		"public_key":   base64.StdEncoding.EncodeToString(sk.Public().(ed25519.PublicKey)),
		"challenge_id": "su_000000000000000000000000",
		"nonce":        "00112233",
		"approval_id":  "0123456789abcdef",
		"action_hash":  hash,
		"decision":     "approve",
		"message":      msg,
		"signature":    base64.StdEncoding.EncodeToString(ed25519.Sign(sk, []byte(msg))),
	}
	path := filepath.Join("..", "..", "mobile", "test", "fixtures", "stepup_vector.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.Getenv("GUBERN_WRITE_VECTOR") == "1" {
			b, _ := json.MarshalIndent(want, "", "  ")
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			if werr := os.WriteFile(path, append(b, '\n'), 0o644); werr != nil {
				t.Fatal(werr)
			}
			return
		}
		t.Fatalf("read vector: %v (run with GUBERN_WRITE_VECTOR=1 to create it)", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("vector %s = %q, want %q", k, got[k], v)
		}
	}
	if !identity.VerifyDeviceSignature(want["public_key"], msg, want["signature"]) {
		t.Error("vector signature does not verify")
	}
}
