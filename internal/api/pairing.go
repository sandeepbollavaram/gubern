package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
)

// Devices and pairing (milestone F, docs/design/pairing.md).

// DefaultDeviceScopes is the ceiling of a device credential when the pairing
// request names none: what a phone needs to follow work and decide approvals.
var DefaultDeviceScopes = []authz.Scope{
	authz.SystemRead, authz.AgentsRead, authz.AgentsRun, authz.SessionsRead,
	authz.ApprovalsRead, authz.ApprovalsDecide, authz.RunsRead, authz.DevicesRead,
}

const resDevice = "device"

// pairingTTL is a code's lifetime: GUBERN_PAIRING_TTL (1s to 1h), else
// identity.PairingTTL (10 minutes).
func pairingTTL() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("GUBERN_PAIRING_TTL"))); err == nil && d >= time.Second && d <= time.Hour {
		return d
	}
	return identity.PairingTTL
}

// touchDevice records a device's last use at most once a minute.
func (s *Server) touchDevice(id string) {
	now := time.Now()
	if v, ok := s.deviceSeen.Load(id); ok && now.Sub(v.(time.Time)) < time.Minute {
		return
	}
	s.deviceSeen.Store(id, now)
	s.identity.TouchDevice(id)
}

// canAccessOwned is canAccess for a resource whose owner is known (devices,
// links: owned by their principal).
func (s *Server) canAccessOwned(r *http.Request, scope authz.Scope, kind, id, owner string) bool {
	sub := subjectFrom(r.Context())
	if sub == nil {
		return false
	}
	return authz.Decide(sub, scope, &authz.Resource{Kind: kind, ID: id, Owner: owner}, time.Now()).Allow
}

type pairingRequest struct {
	Purpose string   `json:"purpose"` // device | telegram | whatsapp | discord
	Label   string   `json:"label,omitempty"`
	Scopes  []string `json:"scopes,omitempty"` // device ceiling; default DefaultDeviceScopes
	// PrincipalID pairs for another person; needs identity:manage.
	PrincipalID string `json:"principal_id,omitempty"`
}

// pairingBaseURL is the address a device should use: GUBERN_PUBLIC_URL, or
// the address this request reached.
func pairingBaseURL(r *http.Request) string {
	if v := strings.TrimRight(os.Getenv("GUBERN_PUBLIC_URL"), "/"); v != "" {
		return v
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// handlePairingCreate serves POST /api/pairing: a single-use code bound to a
// person (the caller, or another person for identity:manage holders) and, for
// a device, to a scope ceiling the caller and the person both hold.
func (s *Server) handlePairingCreate(w http.ResponseWriter, r *http.Request) {
	sub := subjectFrom(r.Context())
	if s.identity == nil || sub == nil {
		http.Error(w, "pairing not available", http.StatusServiceUnavailable)
		return
	}
	var req pairingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	target := sub.ID
	if req.PrincipalID != "" && req.PrincipalID != sub.ID {
		if !authz.Decide(sub, authz.IdentityManage, nil, time.Now()).Allow {
			s.deny(w, r, http.StatusForbidden, authz.IdentityManage, authz.ReasonMissingScope, sub.ID)
			return
		}
		target = req.PrincipalID
	}
	if !s.canAccessOwned(r, authz.DevicesManage, resDevice, "new", target) {
		s.deny(w, r, http.StatusForbidden, authz.DevicesManage, authz.ReasonOutOfBoundary, sub.ID)
		return
	}
	var scopes []authz.Scope
	if req.Purpose == identity.PairDevice {
		if len(req.Scopes) == 0 {
			scopes = DefaultDeviceScopes
		} else {
			for _, sc := range req.Scopes {
				scopes = append(scopes, authz.Scope(sc))
			}
		}
		// The ceiling can't exceed what the caller holds; scopes the person
		// lacks simply never apply (the ceiling only removes).
		var kept []authz.Scope
		now := time.Now()
		for _, sc := range scopes {
			if !authz.Known(sc) {
				http.Error(w, "unknown scope "+string(sc), http.StatusBadRequest)
				return
			}
			if authz.Covers(sub, authz.Grant{Scope: sc, Resource: authz.OwnResource}, now) {
				kept = append(kept, sc)
			}
		}
		if len(kept) == 0 {
			http.Error(w, "none of the requested scopes is yours to give", http.StatusForbidden)
			return
		}
		scopes = kept
	}
	pr, code, err := s.identity.CreatePairing(target, req.Purpose, req.Label, scopes, sub.ID, pairingTTL())
	if err != nil {
		if errors.Is(err, identity.ErrInvalid) || errors.Is(err, identity.ErrNotFound) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "creating the code failed", http.StatusInternalServerError)
		return
	}
	s.audit(principalID(r), "pairing.create", pr.ID, map[string]any{"purpose": pr.Purpose, "principal": pr.PrincipalID})
	body := map[string]any{"pairing": pr, "code": code, "expires_at": pr.ExpiresAt}
	if pr.Purpose == identity.PairDevice {
		q := url.Values{"v": {"1"}, "u": {pairingBaseURL(r)}, "c": {code}}
		if fp := os.Getenv("GUBERN_API_TLS_FINGERPRINT"); fp != "" {
			q.Set("fp", fp)
		}
		body["qr"] = "gubern://pair?" + q.Encode()
	} else {
		body["instructions"] = "Send /pair " + code + " to the Gubern bot from your " + pr.Purpose + " account within 10 minutes."
	}
	s.writeJSON(w, http.StatusCreated, body)
}

type redeemRequest struct {
	Code      string `json:"code"`
	Label     string `json:"label"`
	PublicKey string `json:"public_key"` // base64 Ed25519
}

// handlePairingRedeem serves POST /api/pairing/redeem, the device side of
// pairing. It is unauthenticated (the code is the credential), rate limited
// per address, and answers every bad code the same way.
func (s *Server) handlePairingRedeem(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.pairLimiter != nil && !s.pairLimiter.Allow(ip) {
		if s.identity != nil {
			s.identity.RecordPairingFailure("device", ip, "rate limited")
		}
		w.Header().Set("Retry-After", "60")
		s.writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts; wait a minute"})
		return
	}
	if s.identity == nil || s.keys == nil {
		http.Error(w, "pairing not available", http.StatusServiceUnavailable)
		return
	}
	var req redeemRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	var secret string
	var issued auth.APIKey
	issue := func(pid string) (string, error) {
		k, sec, err := s.keys.Issue(pid, "default", nil, "device "+strings.TrimSpace(req.Label), 0)
		if err != nil {
			return "", err
		}
		issued, secret = k, sec
		return k.ID, nil
	}
	d, pr, _, err := s.identity.RedeemDevice(req.Code, req.Label, req.PublicKey, issue)
	if err != nil {
		if issued.ID != "" {
			_ = s.keys.Revoke(issued.ID) // never leave a key without its device
		}
		if errors.Is(err, identity.ErrPairingInvalid) {
			s.identity.RecordPairingFailure("device", ip, "invalid, used or expired code")
			s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid, used or expired code", "reason": "invalid-code"})
			return
		}
		if errors.Is(err, identity.ErrInvalid) {
			s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "reason": "invalid-request"})
			return
		}
		http.Error(w, "pairing failed", http.StatusInternalServerError)
		return
	}
	s.audit(pr.PrincipalID, "device.paired", d.ID, map[string]any{"label": d.Label, "pairing": pr.ID})
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"device_id": d.ID, "principal_id": d.PrincipalID, "key_id": issued.ID, "secret": secret, "scopes": pr.Scopes,
	})
}

// handleDevicesList serves GET /api/devices: the caller's devices, or all of
// them with a "*" grant. Public keys are included; secrets never exist here.
func (s *Server) handleDevicesList(w http.ResponseWriter, r *http.Request) {
	out := []identity.Device{}
	if s.identity != nil {
		all, err := s.identity.AllDevices()
		if err != nil {
			http.Error(w, "listing devices failed", http.StatusInternalServerError)
			return
		}
		for _, d := range all {
			if s.canAccessOwned(r, authz.DevicesRead, resDevice, d.ID, d.PrincipalID) {
				out = append(out, d)
			}
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) loadDevice(w http.ResponseWriter, r *http.Request) (identity.Device, bool) {
	if s.identity == nil {
		http.Error(w, "devices not available", http.StatusServiceUnavailable)
		return identity.Device{}, false
	}
	d, err := s.identity.Device(r.PathValue("id"))
	if err != nil {
		http.Error(w, "device not found", http.StatusNotFound)
		return identity.Device{}, false
	}
	if !s.canAccessOwned(r, authz.DevicesManage, resDevice, d.ID, d.PrincipalID) {
		s.deny(w, r, http.StatusForbidden, authz.DevicesManage, authz.ReasonOutOfBoundary, principalID(r))
		return identity.Device{}, false
	}
	return d, true
}

// handleDeviceRevoke serves DELETE /api/devices/{id}: the device and every
// credential it holds stop working on the next request.
func (s *Server) handleDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadDevice(w, r)
	if !ok {
		return
	}
	keys, _ := s.identity.DeviceKeys(d.ID)
	for _, k := range keys {
		_ = s.keys.Revoke(k)
	}
	if err := s.identity.RevokeDevice(d.ID, principalID(r)); err != nil && !errors.Is(err, identity.ErrNotFound) {
		http.Error(w, "revoking failed", http.StatusInternalServerError)
		return
	}
	s.audit(principalID(r), "device.revoked", d.ID, map[string]any{"principal": d.PrincipalID, "keys": len(keys)})
	s.writeJSON(w, http.StatusOK, map[string]any{"revoked": d.ID, "keys_revoked": len(keys)})
}

// handleDeviceRotate serves POST /api/devices/{id}/rotate: a new credential
// with the same principal and ceiling; the old one is revoked.
func (s *Server) handleDeviceRotate(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadDevice(w, r)
	if !ok {
		return
	}
	if d.RevokedAt != nil {
		http.Error(w, "device is revoked", http.StatusConflict)
		return
	}
	k, secret, err := s.keys.Issue(d.PrincipalID, "default", nil, "device "+d.Label, 0)
	if err != nil {
		http.Error(w, "issuing failed", http.StatusInternalServerError)
		return
	}
	old, err := s.identity.RebindDeviceKey(d.ID, k.ID, principalID(r))
	if err != nil {
		_ = s.keys.Revoke(k.ID)
		http.Error(w, "rotation failed", http.StatusInternalServerError)
		return
	}
	for _, o := range old {
		_ = s.keys.Revoke(o)
	}
	s.audit(principalID(r), "device.rotated", d.ID, map[string]any{"key": k.ID})
	s.writeJSON(w, http.StatusOK, map[string]any{"device_id": d.ID, "key_id": k.ID, "secret": secret})
}

// handleLinksList serves GET /api/links: channel accounts linked to the
// caller, or all of them with a "*" grant.
func (s *Server) handleLinksList(w http.ResponseWriter, r *http.Request) {
	out := []identity.ExternalIdentity{}
	if s.identity != nil {
		all, err := s.identity.ExternalIdentities("")
		if err != nil {
			http.Error(w, "listing links failed", http.StatusInternalServerError)
			return
		}
		for _, e := range all {
			if s.canAccessOwned(r, authz.DevicesRead, "link", e.Provider+":"+e.Subject, e.PrincipalID) {
				out = append(out, e)
			}
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// handleLinkRevoke serves DELETE /api/links/{provider}/{subject}: the account
// is unlinked and its channel refuses it from then on, allow-list or not.
func (s *Server) handleLinkRevoke(w http.ResponseWriter, r *http.Request) {
	if s.identity == nil {
		http.Error(w, "links not available", http.StatusServiceUnavailable)
		return
	}
	provider, subject := r.PathValue("provider"), r.PathValue("subject")
	p, err := s.identity.ExternalPrincipal(provider, subject)
	if err != nil {
		http.Error(w, "link not found", http.StatusNotFound)
		return
	}
	if !s.canAccessOwned(r, authz.DevicesManage, "link", provider+":"+subject, p.ID) {
		s.deny(w, r, http.StatusForbidden, authz.DevicesManage, authz.ReasonOutOfBoundary, principalID(r))
		return
	}
	if err := s.identity.RevokeExternal(provider, subject, principalID(r)); err != nil {
		http.Error(w, "revoking failed", http.StatusInternalServerError)
		return
	}
	s.audit(principalID(r), "external.revoked", provider+":"+subject, map[string]any{"principal": p.ID})
	s.writeJSON(w, http.StatusOK, map[string]any{"revoked": provider + ":" + subject})
}
