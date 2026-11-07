package identity

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

// Pairing (milestone F, docs/design/pairing.md): a person creates a short
// lived, single-use code that is bound to them and, for a device, to a scope
// ceiling. A device redeems it with its public key and gets its own
// credential; a Telegram or WhatsApp account redeems it with /pair and is
// linked to the person.

// Pairing purposes.
const (
	PairDevice   = "device"
	PairTelegram = "telegram"
	PairWhatsApp = "whatsapp"
	PairDiscord  = "discord"
)

// PairingTTL is how long a code can be redeemed.
const PairingTTL = 10 * time.Minute

// maxPurposeFailures burns a code redeemed on the wrong channel this often.
const maxPurposeFailures = 3

// Pairing errors. ErrPairingInvalid is deliberately the same for an unknown,
// used, expired or burned code, so a guesser learns nothing.
var (
	ErrPairingInvalid   = errors.New("identity: pairing code is invalid, used or expired")
	ErrIdentityConflict = errors.New("identity: that account is already linked to another person")
	ErrAlreadyPaired    = errors.New("identity: that account is already linked to this person")
)

// Pairing is a pairing code's record (the code itself is never stored).
type Pairing struct {
	ID          string        `json:"id"`
	Purpose     string        `json:"purpose"`
	PrincipalID string        `json:"principal_id"`
	Scopes      []authz.Scope `json:"scopes,omitempty"`
	Label       string        `json:"label,omitempty"`
	CreatedBy   string        `json:"created_by"`
	CreatedAt   time.Time     `json:"created_at"`
	ExpiresAt   time.Time     `json:"expires_at"`
	UsedAt      *time.Time    `json:"used_at,omitempty"`
	UsedBy      string        `json:"used_by,omitempty"`
}

// codeAlphabet is Crockford base32 (no I, L, O, U).
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// newPairingCode returns 12 random base32 characters (60 bits) formatted
// XXXX-XXXX-XXXX.
func newPairingCode() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, v := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(codeAlphabet[int(v)%32])
	}
	return sb.String(), nil
}

// NormalizeCode uppercases a typed code, drops separators and maps the
// letters people confuse with digits.
func NormalizeCode(code string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(code) {
		switch r {
		case '-', ' ', '\t':
			continue
		case 'O':
			r = '0'
		case 'I', 'L':
			r = '1'
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func hashCode(code string) string {
	sum := sha256.Sum256([]byte("gubern-pairing\x00" + NormalizeCode(code)))
	return hex.EncodeToString(sum[:])
}

func joinScopes(scopes []authz.Scope) string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = string(s)
	}
	return strings.Join(out, ",")
}

func splitScopes(s string) []authz.Scope {
	if s == "" {
		return nil
	}
	var out []authz.Scope
	for _, p := range strings.Split(s, ",") {
		out = append(out, authz.Scope(p))
	}
	return out
}

// CreatePairing creates a code for purpose bound to principal (a person).
// For a device, scopes is the ceiling of the device's credential (required).
// It returns the record and the code, which is shown once.
func (s *Store) CreatePairing(principalID, purpose, label string, scopes []authz.Scope, by string, ttl time.Duration) (Pairing, string, error) {
	switch purpose {
	case PairDevice, PairTelegram, PairWhatsApp, PairDiscord:
	default:
		return Pairing{}, "", fmt.Errorf("%w: purpose %q", ErrInvalid, purpose)
	}
	p, err := s.Principal(principalID)
	if err != nil {
		return Pairing{}, "", err
	}
	if p.Kind != authz.Person || p.Status != StatusActive {
		return Pairing{}, "", fmt.Errorf("%w: only an active person can be paired", ErrInvalid)
	}
	if purpose == PairDevice {
		if len(scopes) == 0 {
			return Pairing{}, "", fmt.Errorf("%w: a device needs at least one scope", ErrInvalid)
		}
		for _, sc := range scopes {
			if !authz.Known(sc) {
				return Pairing{}, "", fmt.Errorf("%w: scope %q", ErrInvalid, sc)
			}
		}
	} else {
		scopes = nil
	}
	label = strings.TrimSpace(label)
	if len(label) > 80 || strings.ContainsAny(label, "\r\n") {
		return Pairing{}, "", fmt.Errorf("%w: label", ErrInvalid)
	}
	if ttl <= 0 || ttl > time.Hour {
		ttl = PairingTTL
	}
	code, err := newPairingCode()
	if err != nil {
		return Pairing{}, "", err
	}
	id, err := newID("pair_")
	if err != nil {
		return Pairing{}, "", err
	}
	now := s.now()
	pr := Pairing{ID: id, Purpose: purpose, PrincipalID: principalID, Scopes: scopes, Label: label, CreatedBy: by, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO pairing_codes (id, code_hash, purpose, principal_id, scopes, label, created_by, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			id, hashCode(code), purpose, principalID, joinScopes(scopes), label, by, ms(now), ms(pr.ExpiresAt)); err != nil {
			return err
		}
		return s.audit(tx, by, "pairing.create", principalID, purpose+" "+id)
	})
	if err != nil {
		return Pairing{}, "", err
	}
	return pr, code, nil
}

// claimPairing atomically consumes the code for purpose by usedBy. A valid
// code presented for another purpose counts a failure (and is burned after
// maxPurposeFailures) without being consumed.
func (s *Store) claimPairing(tx *sql.Tx, code, purpose, usedBy string) (Pairing, error) {
	var pr Pairing
	var scopes string
	var created, expires int64
	var used sql.NullInt64
	var failures int
	err := tx.QueryRow(`SELECT id, purpose, principal_id, scopes, label, created_by, created_at, expires_at, used_at, failures FROM pairing_codes WHERE code_hash = ?`,
		hashCode(code)).Scan(&pr.ID, &pr.Purpose, &pr.PrincipalID, &scopes, &pr.Label, &pr.CreatedBy, &created, &expires, &used, &failures)
	if errors.Is(err, sql.ErrNoRows) {
		return Pairing{}, ErrPairingInvalid
	}
	if err != nil {
		return Pairing{}, err
	}
	now := s.now()
	if used.Valid || !now.Before(time.UnixMilli(expires)) || failures >= maxPurposeFailures {
		return Pairing{}, ErrPairingInvalid
	}
	if pr.Purpose != purpose {
		// Recorded by the caller after this transaction rolls back.
		return Pairing{}, &wrongChannel{id: pr.ID, principal: pr.PrincipalID, purpose: purpose, by: usedBy}
	}
	res, err := tx.Exec(`UPDATE pairing_codes SET used_at = ?, used_by = ? WHERE id = ? AND used_at IS NULL`, ms(now), usedBy, pr.ID)
	if err != nil {
		return Pairing{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return Pairing{}, ErrPairingInvalid
	}
	p, err := s.principalTx(tx, pr.PrincipalID)
	if err != nil || p.Status != StatusActive {
		return Pairing{}, ErrPairingInvalid
	}
	pr.Scopes, pr.CreatedAt, pr.ExpiresAt, pr.UsedAt, pr.UsedBy = splitScopes(scopes), time.UnixMilli(created), time.UnixMilli(expires), &now, usedBy
	return pr, nil
}

func (s *Store) principalTx(tx *sql.Tx, id string) (Principal, error) {
	var p Principal
	var kind string
	var created int64
	err := tx.QueryRow(`SELECT id, kind, name, status, created_at, created_by FROM principals WHERE id = ?`, id).
		Scan(&p.ID, &kind, &p.Name, &p.Status, &created, &p.CreatedBy)
	if err != nil {
		return Principal{}, err
	}
	p.Kind, p.CreatedAt = authz.Kind(kind), time.UnixMilli(created)
	return p, nil
}

// wrongChannel is a valid code presented on the wrong channel.
type wrongChannel struct{ id, principal, purpose, by string }

func (w *wrongChannel) Error() string { return ErrPairingInvalid.Error() }
func (w *wrongChannel) Unwrap() error { return ErrPairingInvalid }

// settle records a wrong-channel attempt (counted, burning the code at
// maxPurposeFailures) once its transaction has rolled back, and reports the
// uniform invalid-code error.
func (s *Store) settle(err error) error {
	var wc *wrongChannel
	if !errors.As(err, &wc) {
		return err
	}
	_ = s.tx(func(tx *sql.Tx) error {
		if _, e := tx.Exec(`UPDATE pairing_codes SET failures = failures + 1 WHERE id = ?`, wc.id); e != nil {
			return e
		}
		return s.audit(tx, wc.by, "pairing.wrong-channel", wc.principal, wc.id+" presented on "+wc.purpose)
	})
	return ErrPairingInvalid
}

// RecordPairingFailure audits a failed redemption (wrong code, rate limit).
func (s *Store) RecordPairingFailure(channel, who, why string) {
	_ = s.Record(channel+":"+who, "pairing.failed", "pairing", why)
}

// decodeB64 accepts standard or URL-safe base64, padded or not.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}

// parseDeviceKey decodes a device public key: a raw 32-byte Ed25519 key, or
// a P-256 key as SubjectPublicKeyInfo DER (what iOS Secure Enclave and
// Android Keystore keep in hardware).
func parseDeviceKey(k string) (crypto.PublicKey, error) {
	b, err := decodeB64(k)
	if err != nil {
		return nil, err
	}
	if len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	pub, err := x509.ParsePKIXPublicKey(b)
	if err != nil {
		return nil, err
	}
	if ec, ok := pub.(*ecdsa.PublicKey); ok && ec.Curve == elliptic.P256() {
		return ec, nil
	}
	return nil, errors.New("unsupported key type")
}

// ValidPublicKey reports whether k is a supported device public key: base64
// Ed25519 (32 bytes) or base64 P-256 SubjectPublicKeyInfo.
func ValidPublicKey(k string) bool {
	_, err := parseDeviceKey(k)
	return err == nil
}

// VerifyDeviceSignature checks sig (base64) over msg with the device public
// key pub: Ed25519 directly, P-256 as ASN.1 ECDSA over SHA-256(msg).
func VerifyDeviceSignature(pub, msg, sig string) bool {
	key, err := parseDeviceKey(pub)
	if err != nil {
		return false
	}
	s, err := decodeB64(sig)
	if err != nil {
		return false
	}
	switch k := key.(type) {
	case ed25519.PublicKey:
		return ed25519.Verify(k, []byte(msg), s)
	case *ecdsa.PublicKey:
		sum := sha256.Sum256([]byte(msg))
		return ecdsa.VerifyASN1(k, sum[:], s)
	}
	return false
}

// IssueFunc issues an API key for principal and returns its id.
type IssueFunc func(principalID string) (keyID string, err error)

// RedeemDevice redeems a device code: it consumes the code, registers the
// device with its public key, issues its credential with issue, and binds
// the credential to the device with the code's scope ceiling.
func (s *Store) RedeemDevice(code, label, publicKey string, issue IssueFunc) (Device, Pairing, string, error) {
	if !ValidPublicKey(publicKey) {
		return Device{}, Pairing{}, "", fmt.Errorf("%w: public key must be a base64 Ed25519 key", ErrInvalid)
	}
	label = strings.TrimSpace(label)
	if label == "" || len(label) > 80 || strings.ContainsAny(label, "\r\n") {
		return Device{}, Pairing{}, "", fmt.Errorf("%w: device label", ErrInvalid)
	}
	devID, err := newID("dev_")
	if err != nil {
		return Device{}, Pairing{}, "", err
	}
	var pr Pairing
	err = s.tx(func(tx *sql.Tx) error {
		var cerr error
		pr, cerr = s.claimPairing(tx, code, PairDevice, devID)
		return cerr
	})
	if err != nil {
		return Device{}, Pairing{}, "", s.settle(err)
	}
	keyID, err := issue(pr.PrincipalID)
	if err != nil {
		return Device{}, Pairing{}, "", err
	}
	d := Device{ID: devID, PrincipalID: pr.PrincipalID, Label: label, PublicKey: publicKey, CreatedAt: s.now()}
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO devices (id, principal_id, label, public_key, created_at) VALUES (?,?,?,?,?)`,
			d.ID, d.PrincipalID, d.Label, d.PublicKey, ms(d.CreatedAt)); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO credentials (key_id, principal_id, bound_at, bound_by, device_id, scope_ceiling) VALUES (?,?,?,?,?,?)`,
			keyID, pr.PrincipalID, ms(s.now()), "pairing:"+pr.ID, d.ID, joinScopes(pr.Scopes)); err != nil {
			return err
		}
		return s.audit(tx, "pairing:"+pr.ID, "device.paired", pr.PrincipalID, d.ID+" "+d.Label+" key:"+keyID)
	})
	if err != nil {
		return Device{}, Pairing{}, keyID, err
	}
	return d, pr, keyID, nil
}

// RedeemExternal redeems a channel code: the account (provider, subject),
// already verified by the channel (signed webhook), is linked to the code's
// person. An account linked to someone else is refused (and the code is
// kept for its owner); one already linked to the same person reports
// ErrAlreadyPaired.
func (s *Store) RedeemExternal(code, provider, subject string) (Pairing, error) {
	provider, subject = strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(subject)
	if subject == "" {
		return Pairing{}, fmt.Errorf("%w: no account", ErrInvalid)
	}
	var pr Pairing
	err := s.tx(func(tx *sql.Tx) error {
		// Check the account before consuming the code: a conflict must not
		// burn a code that its person can still use from the right account.
		var linked string
		lerr := tx.QueryRow(`SELECT principal_id FROM external_identities WHERE provider = ? AND subject = ? AND revoked_at IS NULL`, provider, subject).Scan(&linked)
		var target string
		_ = tx.QueryRow(`SELECT principal_id FROM pairing_codes WHERE code_hash = ?`, hashCode(code)).Scan(&target)
		if lerr == nil && target != "" {
			if linked == target {
				return ErrAlreadyPaired
			}
			_ = s.audit(tx, provider+":"+subject, "pairing.conflict", target, "account linked to "+linked)
			return ErrIdentityConflict
		}
		var cerr error
		pr, cerr = s.claimPairing(tx, code, provider, provider+":"+subject)
		if cerr != nil {
			return cerr
		}
		if _, err := tx.Exec(`INSERT INTO external_identities (principal_id, provider, subject, verified_at, linked_by) VALUES (?,?,?,?,?)`,
			pr.PrincipalID, provider, subject, ms(s.now()), "pairing:"+pr.ID); err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrIdentityConflict
			}
			return err
		}
		return s.audit(tx, "pairing:"+pr.ID, "external.link", pr.PrincipalID, provider+":"+subject)
	})
	return pr, s.settle(err)
}

// CredentialInfo is what a credential is bound to.
type CredentialInfo struct {
	PrincipalID string
	DeviceID    string
	// Ceiling, when non-empty, limits the credential to these scopes.
	Ceiling []authz.Scope
}

// Credential returns the binding of key keyID.
func (s *Store) Credential(keyID string) (CredentialInfo, error) {
	var ci CredentialInfo
	var ceiling string
	err := s.db.QueryRow(`SELECT principal_id, device_id, scope_ceiling FROM credentials WHERE key_id = ?`, keyID).Scan(&ci.PrincipalID, &ci.DeviceID, &ceiling)
	if errors.Is(err, sql.ErrNoRows) {
		return ci, ErrNotFound
	}
	ci.Ceiling = splitScopes(ceiling)
	return ci, err
}

// ApplyCeiling keeps only the grants of sub whose scope is in ceiling (a nil
// ceiling keeps everything).
func ApplyCeiling(sub *authz.Subject, ceiling []authz.Scope) {
	if len(ceiling) == 0 {
		return
	}
	allow := map[authz.Scope]bool{}
	for _, c := range ceiling {
		allow[c] = true
	}
	kept := sub.Grants[:0]
	for _, g := range sub.Grants {
		if allow[g.Scope] {
			kept = append(kept, g)
		}
	}
	sub.Grants = kept
}

// Device returns device id.
func (s *Store) Device(id string) (Device, error) {
	d := Device{ID: id}
	var created int64
	var seen, revoked sql.NullInt64
	err := s.db.QueryRow(`SELECT principal_id, label, COALESCE(public_key,''), created_at, last_seen_at, revoked_at FROM devices WHERE id = ?`, id).
		Scan(&d.PrincipalID, &d.Label, &d.PublicKey, &created, &seen, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, err
	}
	d.CreatedAt, d.LastSeenAt, d.RevokedAt = time.UnixMilli(created), fromMS(seen), fromMS(revoked)
	return d, nil
}

// AllDevices lists every device, newest first.
func (s *Store) AllDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id FROM devices ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	out := make([]Device, 0, len(ids))
	for _, id := range ids {
		if d, err := s.Device(id); err == nil {
			out = append(out, d)
		}
	}
	return out, nil
}

// TouchDevice records that device id was just used.
func (s *Store) TouchDevice(id string) {
	_, _ = s.db.Exec(`UPDATE devices SET last_seen_at = ? WHERE id = ?`, ms(s.now()), id)
}

// DeviceKeys returns the API key ids bound to device id.
func (s *Store) DeviceKeys(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT key_id FROM credentials WHERE device_id = ?`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RebindDeviceKey replaces device id's credential with newKeyID (rotation),
// keeping its principal and ceiling, and returns the old key ids.
func (s *Store) RebindDeviceKey(id, newKeyID, by string) ([]string, error) {
	old, err := s.DeviceKeys(id)
	if err != nil || len(old) == 0 {
		return nil, ErrNotFound
	}
	ci, err := s.Credential(old[0])
	if err != nil {
		return nil, err
	}
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM credentials WHERE device_id = ?`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO credentials (key_id, principal_id, bound_at, bound_by, device_id, scope_ceiling) VALUES (?,?,?,?,?,?)`,
			newKeyID, ci.PrincipalID, ms(s.now()), by, id, joinScopes(ci.Ceiling)); err != nil {
			return err
		}
		return s.audit(tx, by, "device.rotated", ci.PrincipalID, id+" key:"+newKeyID)
	})
	return old, err
}

// ExternalIdentities lists links, newest first: principal's, or all with "".
func (s *Store) ExternalIdentities(principalID string) ([]ExternalIdentity, error) {
	q := `SELECT id, principal_id, provider, subject, verified_at, revoked_at FROM external_identities`
	var args []any
	if principalID != "" {
		q += ` WHERE principal_id = ?`
		args = append(args, principalID)
	}
	q += ` ORDER BY id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ExternalIdentity
	for rows.Next() {
		var e ExternalIdentity
		var verified int64
		var revoked sql.NullInt64
		if err := rows.Scan(&e.ID, &e.PrincipalID, &e.Provider, &e.Subject, &verified, &revoked); err != nil {
			return nil, err
		}
		e.VerifiedAt, e.RevokedAt = time.UnixMilli(verified), fromMS(revoked)
		out = append(out, e)
	}
	return out, rows.Err()
}
