// Package identity is Gubern's identity store: principals (persons and
// service principals), the credentials bound to them, their permission grants,
// external identities (Telegram, WhatsApp, Slack ...), devices, resource
// ownership, and an audit trail of every change. It lives in one SQLite
// database beside the API key store; the key store stays the credential
// store (hashes only), and this store says which principal each key belongs to.
//
// Design: docs/design/identity-authorization.md.
package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

// Well-known principal ids.
const (
	// OwnerID is the local owner person, created at bootstrap.
	OwnerID = "owner"
	// LoopbackID is the built-in service principal for trusted loopback
	// requests. It is not stored; the API constructs it (design §6).
	LoopbackID = "local-loopback"
	// SystemActor records changes the server makes on its own (bootstrap,
	// migration) in the audit trail.
	SystemActor = "system"
	// LocalCLIActor records changes made by the local recovery CLI.
	LocalCLIActor = "local-cli"
)

// Principal status values.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// Errors.
var (
	ErrNotFound      = errors.New("identity: not found")
	ErrInvalid       = errors.New("identity: invalid value")
	ErrAlreadyLinked = errors.New("identity: external identity already linked")
	ErrNewerSchema   = errors.New("identity: store was written by a newer Gubern; refusing to run with an older schema")
)

// Principal is whoever acts: a person or a service.
type Principal struct {
	ID        string     `json:"id"`
	Kind      authz.Kind `json:"kind"`
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
}

// Grant is a stored permission grant.
type Grant struct {
	ID          int64       `json:"id"`
	PrincipalID string      `json:"principal_id"`
	Scope       authz.Scope `json:"scope"`
	Resource    string      `json:"resource"`
	GrantedBy   string      `json:"granted_by"`
	CreatedAt   time.Time   `json:"created_at"`
	ExpiresAt   *time.Time  `json:"expires_at,omitempty"`
	RevokedAt   *time.Time  `json:"revoked_at,omitempty"`
}

// ExternalIdentity links a channel account to a principal.
type ExternalIdentity struct {
	ID          int64      `json:"id"`
	PrincipalID string     `json:"principal_id"`
	Provider    string     `json:"provider"`
	Subject     string     `json:"subject"`
	VerifiedAt  time.Time  `json:"verified_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// Device is a paired phone or computer (behaviour arrives in milestones F, H).
type Device struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principal_id"`
	Label       string     `json:"label"`
	PublicKey   string     `json:"public_key,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	LastSeenAt  *time.Time `json:"last_seen_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// AuditEvent is one identity lifecycle change.
type AuditEvent struct {
	ID     int64     `json:"id"`
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"`
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail,omitempty"`
}

// Store is the identity database.
type Store struct {
	db  *sql.DB
	now func() time.Time
	// OnAudit, when set, also receives every audit event (the server wires
	// its HMAC-chained audit log here).
	OnAudit func(AuditEvent)
}

// migrations are applied in order; the database's PRAGMA user_version is the
// number already applied. Never edit a released migration: add a new one.
var migrations = []string{
	// 1: initial identity schema (milestone A).
	`
CREATE TABLE principals (
	id         TEXT PRIMARY KEY,
	kind       TEXT NOT NULL CHECK (kind IN ('person','service')),
	name       TEXT NOT NULL,
	status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
	created_at INTEGER NOT NULL,
	created_by TEXT NOT NULL
);
CREATE TABLE credentials (
	key_id       TEXT PRIMARY KEY,
	principal_id TEXT NOT NULL REFERENCES principals(id),
	kind         TEXT NOT NULL DEFAULT 'api_key',
	bound_at     INTEGER NOT NULL,
	bound_by     TEXT NOT NULL
);
CREATE INDEX credentials_principal ON credentials(principal_id);
CREATE TABLE grants (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	principal_id TEXT NOT NULL REFERENCES principals(id),
	scope        TEXT NOT NULL,
	resource     TEXT NOT NULL,
	granted_by   TEXT NOT NULL,
	created_at   INTEGER NOT NULL,
	expires_at   INTEGER,
	revoked_at   INTEGER,
	revoked_by   TEXT
);
CREATE INDEX grants_principal ON grants(principal_id);
CREATE TABLE external_identities (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	principal_id TEXT NOT NULL REFERENCES principals(id),
	provider     TEXT NOT NULL,
	subject      TEXT NOT NULL,
	verified_at  INTEGER NOT NULL,
	linked_by    TEXT NOT NULL,
	revoked_at   INTEGER,
	revoked_by   TEXT
);
CREATE UNIQUE INDEX external_identities_active ON external_identities(provider, subject) WHERE revoked_at IS NULL;
CREATE TABLE devices (
	id           TEXT PRIMARY KEY,
	principal_id TEXT NOT NULL REFERENCES principals(id),
	label        TEXT NOT NULL,
	public_key   TEXT,
	created_at   INTEGER NOT NULL,
	last_seen_at INTEGER,
	revoked_at   INTEGER
);
CREATE TABLE resource_owners (
	kind         TEXT NOT NULL,
	id           TEXT NOT NULL,
	principal_id TEXT NOT NULL REFERENCES principals(id),
	claimed_at   INTEGER NOT NULL,
	PRIMARY KEY (kind, id)
);
CREATE TABLE identity_audit (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	at     INTEGER NOT NULL,
	actor  TEXT NOT NULL,
	action TEXT NOT NULL,
	target TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT ''
);
`,
	// 2: pairing (milestone F). A credential can belong to a device and carry
	// a scope ceiling; pairing codes are stored only as hashes.
	`
ALTER TABLE credentials ADD COLUMN device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE credentials ADD COLUMN scope_ceiling TEXT NOT NULL DEFAULT '';
CREATE TABLE pairing_codes (
	id           TEXT PRIMARY KEY,
	code_hash    TEXT NOT NULL UNIQUE,
	purpose      TEXT NOT NULL CHECK (purpose IN ('device','telegram','whatsapp','discord')),
	principal_id TEXT NOT NULL REFERENCES principals(id),
	scopes       TEXT NOT NULL DEFAULT '',
	label        TEXT NOT NULL DEFAULT '',
	created_by   TEXT NOT NULL,
	created_at   INTEGER NOT NULL,
	expires_at   INTEGER NOT NULL,
	used_at      INTEGER,
	used_by      TEXT NOT NULL DEFAULT '',
	failures     INTEGER NOT NULL DEFAULT 0
);
`,
}

// SchemaVersion is the schema version this binary writes.
func SchemaVersion() int { return len(migrations) }

// Open opens (creating if needed) the identity database at path and applies
// pending migrations.
func Open(path string) (*Store, error) {
	// Create the file owner-only before SQLite does (it would use the
	// process umask); SQLite gives its -wal and -shm files the same mode.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("identity: open: %w", err)
	}
	_ = f.Close()
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("identity: open: %w", err)
	}
	// One writer keeps SQLite's locking simple; reads are fast enough.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("identity: read schema version: %w", err)
	}
	if v > len(migrations) {
		return fmt.Errorf("%w (store v%d, binary v%d)", ErrNewerSchema, v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("identity: migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("identity: migration %d version: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("identity: migration %d commit: %w", v+1, err)
		}
	}
	return nil
}

// Version returns the schema version of the open database.
func (s *Store) Version() (int, error) {
	var v int
	err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMilli(v.Int64)
	return &t
}

func newID(prefix string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// audit records an event inside tx.
func (s *Store) audit(tx *sql.Tx, actor, action, target, detail string) error {
	at := s.now()
	res, err := tx.Exec(`INSERT INTO identity_audit (at, actor, action, target, detail) VALUES (?,?,?,?,?)`,
		ms(at), actor, action, target, detail)
	if err != nil {
		return err
	}
	if s.OnAudit != nil {
		// The hook only appends to a log; a change that later rolls back is
		// still worth seeing as attempted.
		id, _ := res.LastInsertId()
		s.OnAudit(AuditEvent{ID: id, At: at, Actor: actor, Action: action, Target: target, Detail: detail})
	}
	return nil
}

// tx runs fn in a transaction.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func validName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && len(name) <= 128 && !strings.ContainsAny(name, "\r\n")
}

// --- principals --------------------------------------------------------------

// CreatePrincipal adds a principal. id == "" generates one. Principal ids are
// random, never an email or phone number.
func (s *Store) CreatePrincipal(id string, kind authz.Kind, name, by string) (Principal, error) {
	if kind != authz.Person && kind != authz.Service {
		return Principal{}, fmt.Errorf("%w: kind %q", ErrInvalid, kind)
	}
	if !validName(name) {
		return Principal{}, fmt.Errorf("%w: name", ErrInvalid)
	}
	if id == "" {
		var err error
		prefix := "p_"
		if kind == authz.Service {
			prefix = "svc_"
		}
		if id, err = newID(prefix); err != nil {
			return Principal{}, err
		}
	}
	p := Principal{ID: id, Kind: kind, Name: strings.TrimSpace(name), Status: StatusActive, CreatedAt: s.now(), CreatedBy: by}
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO principals (id, kind, name, status, created_at, created_by) VALUES (?,?,?,?,?,?)`,
			p.ID, string(p.Kind), p.Name, p.Status, ms(p.CreatedAt), by); err != nil {
			return err
		}
		return s.audit(tx, by, "principal.create", p.ID, string(kind)+" "+p.Name)
	})
	if err != nil {
		return Principal{}, fmt.Errorf("identity: create principal: %w", err)
	}
	return p, nil
}

// Principal returns the principal with id.
func (s *Store) Principal(id string) (Principal, error) {
	var p Principal
	var kind string
	var created int64
	err := s.db.QueryRow(`SELECT id, kind, name, status, created_at, created_by FROM principals WHERE id = ?`, id).
		Scan(&p.ID, &kind, &p.Name, &p.Status, &created, &p.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrNotFound
	}
	if err != nil {
		return Principal{}, err
	}
	p.Kind, p.CreatedAt = authz.Kind(kind), time.UnixMilli(created)
	return p, nil
}

// Principals lists every principal, oldest first.
func (s *Store) Principals() ([]Principal, error) {
	rows, err := s.db.Query(`SELECT id, kind, name, status, created_at, created_by FROM principals ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Principal
	for rows.Next() {
		var p Principal
		var kind string
		var created int64
		if err := rows.Scan(&p.ID, &kind, &p.Name, &p.Status, &created, &p.CreatedBy); err != nil {
			return nil, err
		}
		p.Kind, p.CreatedAt = authz.Kind(kind), time.UnixMilli(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetStatus enables or disables a principal. Disabling stops every
// credential it owns on the next request.
func (s *Store) SetStatus(id, status, by string) error {
	if status != StatusActive && status != StatusDisabled {
		return fmt.Errorf("%w: status %q", ErrInvalid, status)
	}
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE principals SET status = ? WHERE id = ?`, status, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return s.audit(tx, by, "principal."+status, id, "")
	})
}

// --- credentials -------------------------------------------------------------

// BindCredential binds API key keyID to principal. Rebinding an already bound
// key moves it (an explicit, audited act, e.g. `gubern identity bind-key`).
func (s *Store) BindCredential(keyID, principalID, by string) error {
	if keyID == "" {
		return fmt.Errorf("%w: key id", ErrInvalid)
	}
	return s.tx(func(tx *sql.Tx) error {
		var prev sql.NullString
		_ = tx.QueryRow(`SELECT principal_id FROM credentials WHERE key_id = ?`, keyID).Scan(&prev)
		if _, err := tx.Exec(`INSERT INTO credentials (key_id, principal_id, bound_at, bound_by) VALUES (?,?,?,?)
			ON CONFLICT(key_id) DO UPDATE SET principal_id = excluded.principal_id, bound_at = excluded.bound_at, bound_by = excluded.bound_by`,
			keyID, principalID, ms(s.now()), by); err != nil {
			return err
		}
		detail := "to " + principalID
		if prev.Valid && prev.String != principalID {
			detail = "from " + prev.String + " to " + principalID
		}
		return s.audit(tx, by, "credential.bind", "key:"+keyID, detail)
	})
}

// UnbindCredential removes a key's binding (the key itself is revoked in the
// key store; an unbound key authenticates as nobody).
func (s *Store) UnbindCredential(keyID, by string) error {
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM credentials WHERE key_id = ?`, keyID); err != nil {
			return err
		}
		return s.audit(tx, by, "credential.unbind", "key:"+keyID, "")
	})
}

// CredentialPrincipal returns the principal key keyID is bound to.
func (s *Store) CredentialPrincipal(keyID string) (Principal, error) {
	var pid string
	err := s.db.QueryRow(`SELECT principal_id FROM credentials WHERE key_id = ?`, keyID).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrNotFound
	}
	if err != nil {
		return Principal{}, err
	}
	return s.Principal(pid)
}

// BoundKeys returns every bound key id.
func (s *Store) BoundKeys() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key_id, principal_id FROM credentials`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, p string
		if err := rows.Scan(&k, &p); err != nil {
			return nil, err
		}
		out[k] = p
	}
	return out, rows.Err()
}

// --- grants ------------------------------------------------------------------

// AddGrant gives principal scope on resource. expires == nil never expires.
func (s *Store) AddGrant(principalID string, scope authz.Scope, resource, by string, expires *time.Time) (Grant, error) {
	p, err := s.Principal(principalID)
	if err != nil {
		return Grant{}, err
	}
	var g Grant
	err = s.tx(func(tx *sql.Tx) error {
		var err error
		g, err = s.addGrantTx(tx, p, scope, resource, by, expires)
		return err
	})
	if err != nil {
		return Grant{}, fmt.Errorf("identity: add grant: %w", err)
	}
	return g, nil
}

func (s *Store) addGrantTx(tx *sql.Tx, p Principal, scope authz.Scope, resource, by string, expires *time.Time) (Grant, error) {
	if !authz.Known(scope) {
		return Grant{}, fmt.Errorf("%w: scope %q", ErrInvalid, scope)
	}
	if !authz.ValidResource(resource) {
		return Grant{}, fmt.Errorf("%w: resource %q", ErrInvalid, resource)
	}
	if authz.PersonOnly(scope) && p.Kind != authz.Person {
		return Grant{}, fmt.Errorf("%w: %s can only be granted to a person", ErrInvalid, scope)
	}
	g := Grant{PrincipalID: p.ID, Scope: scope, Resource: resource, GrantedBy: by, CreatedAt: s.now(), ExpiresAt: expires}
	var exp any
	if expires != nil {
		exp = ms(*expires)
	}
	res, err := tx.Exec(`INSERT INTO grants (principal_id, scope, resource, granted_by, created_at, expires_at) VALUES (?,?,?,?,?,?)`,
		p.ID, string(scope), resource, by, ms(g.CreatedAt), exp)
	if err != nil {
		return Grant{}, err
	}
	g.ID, _ = res.LastInsertId()
	return g, s.audit(tx, by, "grant.add", p.ID, string(scope)+" on "+resource)
}

// ApplyTemplate adds, in one transaction, the grants role expands to
// (authz.Template) for the principal's kind. An unknown role adds nothing and
// reports ErrInvalid.
func (s *Store) ApplyTemplate(principalID, role, by string) error {
	p, err := s.Principal(principalID)
	if err != nil {
		return err
	}
	gs := authz.Template(role, p.Kind)
	if gs == nil {
		return fmt.Errorf("%w: role %q", ErrInvalid, role)
	}
	return s.tx(func(tx *sql.Tx) error {
		for _, g := range gs {
			if _, err := s.addGrantTx(tx, p, g.Scope, g.Resource, by, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// RevokeGrant revokes grant id. It takes effect on the next request.
func (s *Store) RevokeGrant(id int64, by string) error {
	return s.tx(func(tx *sql.Tx) error {
		var pid, scope, resource string
		err := tx.QueryRow(`SELECT principal_id, scope, resource FROM grants WHERE id = ? AND revoked_at IS NULL`, id).Scan(&pid, &scope, &resource)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE grants SET revoked_at = ?, revoked_by = ? WHERE id = ?`, ms(s.now()), by, id); err != nil {
			return err
		}
		return s.audit(tx, by, "grant.revoke", pid, scope+" on "+resource)
	})
}

// Grants returns principal's grants that are neither revoked nor expired.
func (s *Store) Grants(principalID string) ([]Grant, error) {
	rows, err := s.db.Query(`SELECT id, scope, resource, granted_by, created_at, expires_at FROM grants
		WHERE principal_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?) ORDER BY id`,
		principalID, ms(s.now()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Grant
	for rows.Next() {
		g := Grant{PrincipalID: principalID}
		var scope string
		var created int64
		var exp sql.NullInt64
		if err := rows.Scan(&g.ID, &scope, &g.Resource, &g.GrantedBy, &created, &exp); err != nil {
			return nil, err
		}
		g.Scope, g.CreatedAt, g.ExpiresAt = authz.Scope(scope), time.UnixMilli(created), fromMS(exp)
		out = append(out, g)
	}
	return out, rows.Err()
}

// Subject loads principal id with its live grants, ready for authz.Decide.
// It reads the store every time: a revocation applies to the next request.
func (s *Store) Subject(id string) (*authz.Subject, error) {
	p, err := s.Principal(id)
	if err != nil {
		return nil, err
	}
	gs, err := s.Grants(id)
	if err != nil {
		return nil, err
	}
	sub := &authz.Subject{ID: p.ID, Kind: p.Kind, Active: p.Status == StatusActive}
	for _, g := range gs {
		sub.Grants = append(sub.Grants, authz.Grant{Scope: g.Scope, Resource: g.Resource, ExpiresAt: g.ExpiresAt})
	}
	return sub, nil
}

// --- external identities -----------------------------------------------------

// LinkExternal links (provider, subject) to principal. Callers must have
// verified the link (pairing, milestone F); this store never infers one from a
// matching name or number. An active link to anyone is ErrAlreadyLinked.
func (s *Store) LinkExternal(principalID, provider, subject, by string) (ExternalIdentity, error) {
	provider, subject = strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(subject)
	if provider == "" || subject == "" || len(subject) > 256 {
		return ExternalIdentity{}, fmt.Errorf("%w: provider and subject are required", ErrInvalid)
	}
	if _, err := s.Principal(principalID); err != nil {
		return ExternalIdentity{}, err
	}
	e := ExternalIdentity{PrincipalID: principalID, Provider: provider, Subject: subject, VerifiedAt: s.now()}
	err := s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO external_identities (principal_id, provider, subject, verified_at, linked_by) VALUES (?,?,?,?,?)`,
			principalID, provider, subject, ms(e.VerifiedAt), by)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrAlreadyLinked
			}
			return err
		}
		e.ID, _ = res.LastInsertId()
		return s.audit(tx, by, "external.link", principalID, provider+":"+subject)
	})
	if err != nil {
		return ExternalIdentity{}, err
	}
	return e, nil
}

// RevokeExternal revokes the active link for (provider, subject).
func (s *Store) RevokeExternal(provider, subject, by string) error {
	provider, subject = strings.ToLower(strings.TrimSpace(provider)), strings.TrimSpace(subject)
	return s.tx(func(tx *sql.Tx) error {
		var pid string
		err := tx.QueryRow(`SELECT principal_id FROM external_identities WHERE provider = ? AND subject = ? AND revoked_at IS NULL`,
			provider, subject).Scan(&pid)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE external_identities SET revoked_at = ?, revoked_by = ? WHERE provider = ? AND subject = ? AND revoked_at IS NULL`,
			ms(s.now()), by, provider, subject); err != nil {
			return err
		}
		return s.audit(tx, by, "external.revoke", pid, provider+":"+subject)
	})
}

// ExternalPrincipal returns the principal (provider, subject) is actively
// linked to.
func (s *Store) ExternalPrincipal(provider, subject string) (Principal, error) {
	var pid string
	err := s.db.QueryRow(`SELECT principal_id FROM external_identities WHERE provider = ? AND subject = ? AND revoked_at IS NULL`,
		strings.ToLower(provider), subject).Scan(&pid)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrNotFound
	}
	if err != nil {
		return Principal{}, err
	}
	return s.Principal(pid)
}

// ExternalRevoked reports whether (provider, subject) was linked and every
// link to it is revoked. Channels refuse such a sender even when it is still
// in an environment allow-list: revocation wins (design §8).
func (s *Store) ExternalRevoked(provider, subject string) bool {
	var active, revoked int
	err := s.db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN revoked_at IS NULL THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN revoked_at IS NOT NULL THEN 1 ELSE 0 END), 0)
		FROM external_identities WHERE provider = ? AND subject = ?`, strings.ToLower(provider), subject).Scan(&active, &revoked)
	if err != nil {
		// Fail closed only for a confirmed revocation; a read error is
		// logged by the caller and the allow-list still applies.
		return false
	}
	return active == 0 && revoked > 0
}

// --- devices -----------------------------------------------------------------

// AddDevice records a device for principal (pairing arrives in milestone F).
func (s *Store) AddDevice(principalID, label, publicKey, by string) (Device, error) {
	if !validName(label) {
		return Device{}, fmt.Errorf("%w: label", ErrInvalid)
	}
	if _, err := s.Principal(principalID); err != nil {
		return Device{}, err
	}
	id, err := newID("dev_")
	if err != nil {
		return Device{}, err
	}
	d := Device{ID: id, PrincipalID: principalID, Label: strings.TrimSpace(label), PublicKey: publicKey, CreatedAt: s.now()}
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO devices (id, principal_id, label, public_key, created_at) VALUES (?,?,?,?,?)`,
			d.ID, principalID, d.Label, publicKey, ms(d.CreatedAt)); err != nil {
			return err
		}
		return s.audit(tx, by, "device.add", principalID, d.ID+" "+d.Label)
	})
	if err != nil {
		return Device{}, err
	}
	return d, nil
}

// RevokeDevice revokes device id.
func (s *Store) RevokeDevice(id, by string) error {
	return s.tx(func(tx *sql.Tx) error {
		var pid string
		err := tx.QueryRow(`SELECT principal_id FROM devices WHERE id = ? AND revoked_at IS NULL`, id).Scan(&pid)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE devices SET revoked_at = ? WHERE id = ?`, ms(s.now()), id); err != nil {
			return err
		}
		return s.audit(tx, by, "device.revoke", pid, id)
	})
}

// Devices lists principal's devices.
func (s *Store) Devices(principalID string) ([]Device, error) {
	rows, err := s.db.Query(`SELECT id, label, COALESCE(public_key,''), created_at, last_seen_at, revoked_at FROM devices WHERE principal_id = ? ORDER BY created_at`, principalID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Device
	for rows.Next() {
		d := Device{PrincipalID: principalID}
		var created int64
		var seen, revoked sql.NullInt64
		if err := rows.Scan(&d.ID, &d.Label, &d.PublicKey, &created, &seen, &revoked); err != nil {
			return nil, err
		}
		d.CreatedAt, d.LastSeenAt, d.RevokedAt = time.UnixMilli(created), fromMS(seen), fromMS(revoked)
		out = append(out, d)
	}
	return out, rows.Err()
}

// --- resource ownership ------------------------------------------------------

// Claim records principal as the owner of (kind, id) unless someone already
// owns it, atomically. It returns the owner after the call; owner ==
// principalID means the caller owns it (newly or already).
func (s *Store) Claim(kind, id, principalID string) (owner string, err error) {
	if kind == "" || id == "" {
		return "", fmt.Errorf("%w: resource", ErrInvalid)
	}
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO resource_owners (kind, id, principal_id, claimed_at) VALUES (?,?,?,?) ON CONFLICT(kind, id) DO NOTHING`,
			kind, id, principalID, ms(s.now())); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT principal_id FROM resource_owners WHERE kind = ? AND id = ?`, kind, id).Scan(&owner)
	})
	return owner, err
}

// Owner returns the owner of (kind, id), or "" when unclaimed.
func (s *Store) Owner(kind, id string) (string, error) {
	var owner string
	err := s.db.QueryRow(`SELECT principal_id FROM resource_owners WHERE kind = ? AND id = ?`, kind, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return owner, err
}

// --- audit -------------------------------------------------------------------

// Audit returns the latest limit identity audit events, newest first.
func (s *Store) Audit(limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, at, actor, action, target, detail FROM identity_audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Record writes an audit event that is not a store change (e.g. owner
// recovery issuing a credential).
func (s *Store) Record(actor, action, target, detail string) error {
	return s.tx(func(tx *sql.Tx) error { return s.audit(tx, actor, action, target, detail) })
}
