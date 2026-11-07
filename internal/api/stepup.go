package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// Step-up for device approvals (milestone H, docs/design/mobile-app.md §4).
// A paired device approves only with a signature from its own key (held in
// hardware on the phone) over a one-time server challenge bound to the
// approval, its action hash and the decision. A stolen device credential
// alone can't approve anything.

// StepUpTTL is how long a challenge can be answered.
const StepUpTTL = 60 * time.Second

type stepUpChallenge struct {
	deviceID, approvalID, actionHash, nonce string
	expires                                 time.Time
}

type stepUpStore struct {
	mu sync.Mutex
	m  map[string]stepUpChallenge
}

// StepUpMessage is the exact text a device signs (UTF-8, "\n"-separated).
// The mobile app builds the same string; both sides share a test vector.
func StepUpMessage(challengeID, nonce, approvalID, actionHash string, approve bool) string {
	decision := "reject"
	if approve {
		decision = "approve"
	}
	return strings.Join([]string{"gubern-stepup-v1", challengeID, nonce, approvalID, strings.ToLower(actionHash), decision}, "\n")
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (st *stepUpStore) issue(deviceID, approvalID, actionHash string) (string, stepUpChallenge) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.m == nil {
		st.m = map[string]stepUpChallenge{}
	}
	now := time.Now()
	for id, c := range st.m { // drop stale challenges
		if now.After(c.expires) {
			delete(st.m, id)
		}
	}
	id := "su_" + randHex(12)
	c := stepUpChallenge{deviceID: deviceID, approvalID: approvalID, actionHash: actionHash, nonce: randHex(24), expires: now.Add(StepUpTTL)}
	st.m[id] = c
	return id, c
}

// take removes and returns challenge id: every challenge is answered at most
// once, whether the answer is right or not.
func (st *stepUpStore) take(id string) (stepUpChallenge, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	c, ok := st.m[id]
	delete(st.m, id)
	return c, ok && time.Now().Before(c.expires)
}

// handleStepUpChallenge serves POST /api/stepup/challenge {approval_id}: only
// for a paired device's credential.
func (s *Server) handleStepUpChallenge(w http.ResponseWriter, r *http.Request) {
	dev := deviceFrom(r.Context())
	if dev == "" {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "step-up is for paired devices", "reason": "not-a-device"})
		return
	}
	if s.approvalStore == nil {
		http.Error(w, "approvals not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		ApprovalID string `json:"approval_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	a, err := s.approvalStore.Get(req.ApprovalID)
	if err != nil {
		http.Error(w, "approval not found", http.StatusNotFound)
		return
	}
	if !s.requireAccess(w, r, authz.ApprovalsDecide, resSession, a.SessionID) {
		return
	}
	id, c := s.stepUp.issue(dev, a.ID, a.ActionHash)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": id, "nonce": c.nonce, "approval_id": a.ID, "action_hash": a.ActionHash,
		"expires_at": c.expires, "format": "gubern-stepup-v1\n<challenge_id>\n<nonce>\n<approval_id>\n<action_hash>\n<approve|reject>",
	})
}

// stepUpProof is the signed answer to a challenge.
type stepUpProof struct {
	ChallengeID string `json:"challenge_id"`
	Signature   string `json:"signature"` // base64
}

// verifyStepUp checks a device's proof for deciding approval id with hash.
// It returns "" when valid, else the refusal reason.
func (s *Server) verifyStepUp(deviceID, approvalID, actionHash string, approve bool, p *stepUpProof) string {
	if p == nil || p.ChallengeID == "" || p.Signature == "" {
		return "stepup-required"
	}
	c, ok := s.stepUp.take(p.ChallengeID)
	if !ok || c.deviceID != deviceID || c.approvalID != approvalID || !strings.EqualFold(c.actionHash, actionHash) {
		return "stepup-invalid"
	}
	d, err := s.identity.Device(deviceID)
	if err != nil || d.RevokedAt != nil || d.PublicKey == "" {
		return "stepup-invalid"
	}
	if !identity.VerifyDeviceSignature(d.PublicKey, StepUpMessage(p.ChallengeID, c.nonce, approvalID, actionHash, approve), p.Signature) {
		return "stepup-invalid"
	}
	return ""
}
