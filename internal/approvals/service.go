// Package approvals is Gubern's persistent approval service (milestone B):
// one store and one decision path for every channel that can approve an
// agent action (dashboard and API, TUI, Telegram, WhatsApp, Slack, later
// Discord and the mobile app).
//
// An approval records exactly what would run (a full SHA-256 of the canonical
// action) and is decided once: the first valid decision wins atomically, and
// every later one is refused. Approvals expire, record who decided and
// through which channel, and are cancelled when the server restarts (the run
// that was waiting no longer exists, so nothing may execute on a decision
// made afterwards).
//
// Design: docs/design/approval-service.md.
package approvals

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// Status of an approval.
type Status string

// Statuses. Only pending is open; every other status is final.
const (
	Pending   Status = "pending"
	Approved  Status = "approved"
	Rejected  Status = "rejected"
	Expired   Status = "expired"
	Cancelled Status = "cancelled"
)

// DefaultTTL is how long an approval stays decidable.
const DefaultTTL = 15 * time.Minute

// Errors returned by Decide.
var (
	ErrNotFound       = errors.New("approvals: no such approval")
	ErrNotPending     = errors.New("approvals: approval is no longer pending")
	ErrExpired        = errors.New("approvals: approval expired")
	ErrActionMismatch = errors.New("approvals: action hash does not match the approval")
	ErrForbidden      = errors.New("approvals: decider may not decide this approval")
	ErrNewerSchema    = errors.New("approvals: store was written by a newer Gubern")
)

// Action is what an approval authorizes. Its hash is computed over a
// canonical JSON encoding (object keys sorted), so the same action always
// hashes the same and any change to a parameter changes the hash.
type Action struct {
	Tool        string         `json:"tool"`
	Params      map[string]any `json:"params"`
	Command     string         `json:"command,omitempty"`
	Description string         `json:"description,omitempty"`
	Preview     string         `json:"preview,omitempty"`
}

// HashAction returns the full SHA-256 (hex) of a's canonical encoding.
// encoding/json sorts map keys at every depth, which is the canonical form.
func HashAction(a Action) string {
	b, err := json.Marshal(a)
	if err != nil {
		// Unencodable parameters: hash the visible parts plus a marker so
		// the result is still deterministic and never collides with a
		// valid encoding.
		b = []byte("unencodable\x00" + a.Tool + "\x00" + a.Command + "\x00" + a.Description + "\x00" + a.Preview + "\x00" + fmt.Sprint(a.Params))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Approval is one approval record.
type Approval struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"session_id"`
	Tool        string     `json:"tool"`
	Risk        string     `json:"risk"`
	Description string     `json:"description"`
	Command     string     `json:"command,omitempty"`
	Preview     string     `json:"preview,omitempty"`
	Workspace   string     `json:"workspace,omitempty"`
	ActionHash  string     `json:"action_hash"`
	Status      Status     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	DecidedBy   string     `json:"decided_by,omitempty"`
	DecidedVia  string     `json:"decided_via,omitempty"`
	Reason      string     `json:"reason,omitempty"`
}

// Decider is who decides and through which channel. PrincipalID is set when
// the channel authenticated a principal (API key, linked channel identity).
// Channel users not yet linked to a principal carry only Channel+External.
type Decider struct {
	PrincipalID string `json:"principal_id,omitempty"`
	Channel     string `json:"channel"`            // api, tui, telegram, whatsapp, slack, discord, mobile
	External    string `json:"external,omitempty"` // channel subject, e.g. a Telegram user id
}

// Label is how the decider is recorded.
func (d Decider) Label() string {
	if d.PrincipalID != "" {
		return d.PrincipalID
	}
	if d.External != "" {
		return d.Channel + ":" + d.External
	}
	return d.Channel
}

// Decision is one attempt to decide an approval.
type Decision struct {
	Approve bool
	// ActionHash, when set, must equal the approval's hash: the decider
	// confirms the exact action it was shown.
	ActionHash string
	Decider    Decider
	Reason     string
}

// CreateRequest opens an approval.
type CreateRequest struct {
	SessionID string
	Action    Action
	Risk      string
	Workspace string
	// TTL overrides the service default when > 0.
	TTL time.Duration
}

// Authorizer decides whether d may decide a. It returns the decider to
// record (for example with the principal a channel identity is linked to) or
// ErrForbidden (or an error wrapping it) to refuse. A nil Authorizer refuses
// every decision, so a misconfigured server never accepts one.
type Authorizer func(ctx context.Context, a Approval, d Decider) (Decider, error)

// Options configures a Service.
type Options struct {
	TTL        time.Duration
	Authorizer Authorizer
	// Audit, when set, receives every lifecycle event.
	Audit func(action string, a Approval)
}

// Service is the approval store and decision engine.
type Service struct {
	db   *sql.DB
	now  func() time.Time
	opts Options

	mu      sync.Mutex
	waiters map[string][]chan Approval
	hooks   []func(Approval)
}

var migrations = []string{
	`
CREATE TABLE approvals (
	id          TEXT PRIMARY KEY,
	session_id  TEXT NOT NULL,
	tool        TEXT NOT NULL,
	risk        TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	command     TEXT NOT NULL DEFAULT '',
	preview     TEXT NOT NULL DEFAULT '',
	workspace   TEXT NOT NULL DEFAULT '',
	action_hash TEXT NOT NULL,
	status      TEXT NOT NULL CHECK (status IN ('pending','approved','rejected','expired','cancelled')),
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER NOT NULL,
	decided_at  INTEGER,
	decided_by  TEXT NOT NULL DEFAULT '',
	decided_via TEXT NOT NULL DEFAULT '',
	reason      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX approvals_status ON approvals(status, created_at);
CREATE INDEX approvals_session ON approvals(session_id, created_at);
`,
}

// Open opens (creating if needed) the store at path, applies migrations, and
// cancels every approval left pending by a previous process. It returns how
// many were cancelled.
func Open(path string, opts Options) (*Service, int, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("approvals: open: %w", err)
	}
	_ = f.Close()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, 0, fmt.Errorf("approvals: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	s := &Service{db: db, now: time.Now, opts: opts, waiters: map[string][]chan Approval{}}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, 0, err
	}
	n, err := s.cancelStale()
	if err != nil {
		_ = db.Close()
		return nil, 0, err
	}
	return s, n, nil
}

// Close closes the store.
func (s *Service) Close() error { return s.db.Close() }

// TTL returns the default approval lifetime.
func (s *Service) TTL() time.Duration { return s.opts.TTL }

func (s *Service) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
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
			return fmt.Errorf("approvals: migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// cancelStale cancels approvals a previous process left pending: their runs
// are gone, so a decision on them must never execute anything.
func (s *Service) cancelStale() (int, error) {
	rows, err := s.db.Query(`SELECT ` + cols + ` FROM approvals WHERE status = 'pending'`)
	if err != nil {
		return 0, err
	}
	stale, err := scanAll(rows)
	if err != nil {
		return 0, err
	}
	for _, a := range stale {
		if _, err := s.db.Exec(`UPDATE approvals SET status = 'cancelled', decided_at = ?, decided_via = 'system', reason = 'server restarted' WHERE id = ? AND status = 'pending'`,
			s.now().UnixMilli(), a.ID); err != nil {
			return 0, err
		}
		a.Status, a.Reason = Cancelled, "server restarted"
		s.audit("approval.cancelled", a)
	}
	return len(stale), nil
}

// OnChange registers fn to receive every approval when it is created or
// reaches a final status (notifications to channels, event streams).
func (s *Service) OnChange(fn func(Approval)) {
	s.mu.Lock()
	s.hooks = append(s.hooks, fn)
	s.mu.Unlock()
}

func (s *Service) audit(action string, a Approval) {
	if s.opts.Audit != nil {
		s.opts.Audit(action, a)
	}
}

func (s *Service) changed(a Approval) {
	s.mu.Lock()
	hooks := make([]func(Approval), len(s.hooks))
	copy(hooks, s.hooks)
	var ws []chan Approval
	if a.Status != Pending {
		ws = s.waiters[a.ID]
		delete(s.waiters, a.ID)
	}
	s.mu.Unlock()
	for _, w := range ws {
		w <- a // buffered, one value each
	}
	for _, h := range hooks {
		h(a)
	}
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

const cols = `id, session_id, tool, risk, description, command, preview, workspace, action_hash, status, created_at, expires_at, decided_at, decided_by, decided_via, reason`

func scanAll(rows *sql.Rows) ([]Approval, error) {
	defer func() { _ = rows.Close() }()
	var out []Approval
	for rows.Next() {
		var a Approval
		var status string
		var created, expires int64
		var decided sql.NullInt64
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Tool, &a.Risk, &a.Description, &a.Command, &a.Preview,
			&a.Workspace, &a.ActionHash, &status, &created, &expires, &decided, &a.DecidedBy, &a.DecidedVia, &a.Reason); err != nil {
			return nil, err
		}
		a.Status, a.CreatedAt, a.ExpiresAt = Status(status), time.UnixMilli(created), time.UnixMilli(expires)
		if decided.Valid {
			t := time.UnixMilli(decided.Int64)
			a.DecidedAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Create opens a pending approval for req.
func (s *Service) Create(_ context.Context, req CreateRequest) (Approval, error) {
	if req.SessionID == "" || req.Action.Tool == "" {
		return Approval{}, errors.New("approvals: session and tool are required")
	}
	id, err := newID()
	if err != nil {
		return Approval{}, err
	}
	ttl := s.opts.TTL
	if req.TTL > 0 {
		ttl = req.TTL
	}
	now := s.now()
	a := Approval{
		ID: id, SessionID: req.SessionID, Tool: req.Action.Tool, Risk: req.Risk,
		Description: req.Action.Description, Command: req.Action.Command, Preview: req.Action.Preview,
		Workspace: req.Workspace, ActionHash: HashAction(req.Action), Status: Pending,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	if _, err := s.db.Exec(`INSERT INTO approvals (`+cols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NULL,'','','')`,
		a.ID, a.SessionID, a.Tool, a.Risk, a.Description, a.Command, a.Preview, a.Workspace, a.ActionHash,
		string(a.Status), now.UnixMilli(), a.ExpiresAt.UnixMilli()); err != nil {
		return Approval{}, fmt.Errorf("approvals: create: %w", err)
	}
	s.audit("approval.requested", a)
	s.changed(a)
	return a, nil
}

// Get returns approval id.
func (s *Service) Get(id string) (Approval, error) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM approvals WHERE id = ?`, id)
	if err != nil {
		return Approval{}, err
	}
	all, err := scanAll(rows)
	if err != nil {
		return Approval{}, err
	}
	if len(all) == 0 {
		return Approval{}, ErrNotFound
	}
	return all[0], nil
}

// Filter narrows List.
type Filter struct {
	Status    Status
	SessionID string
	Limit     int
}

// List returns approvals, newest first. Pending approvals past their expiry
// are reported (and stored) as expired.
func (s *Service) List(f Filter) ([]Approval, error) {
	s.expireDue()
	q := `SELECT ` + cols + ` FROM approvals WHERE 1=1`
	var args []any
	if f.Status != "" {
		q += ` AND status = ?`
		args = append(args, string(f.Status))
	}
	if f.SessionID != "" {
		q += ` AND session_id = ?`
		args = append(args, f.SessionID)
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanAll(rows)
}

// expireDue marks every pending approval past its expiry as expired.
func (s *Service) expireDue() {
	rows, err := s.db.Query(`SELECT `+cols+` FROM approvals WHERE status = 'pending' AND expires_at <= ?`, s.now().UnixMilli())
	if err != nil {
		return
	}
	due, err := scanAll(rows)
	if err != nil {
		return
	}
	for _, a := range due {
		s.finish(a, Expired, Decider{Channel: "system"}, "expired")
	}
}

// finish moves a pending approval to a final status if it is still pending,
// and reports whether this call did it.
func (s *Service) finish(a Approval, st Status, d Decider, reason string) (Approval, bool) {
	now := s.now()
	res, err := s.db.Exec(`UPDATE approvals SET status = ?, decided_at = ?, decided_by = ?, decided_via = ?, reason = ? WHERE id = ? AND status = 'pending'`,
		string(st), now.UnixMilli(), d.Label(), d.Channel, reason, a.ID)
	if err != nil {
		return a, false
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return a, false
	}
	a.Status, a.DecidedAt, a.DecidedBy, a.DecidedVia, a.Reason = st, &now, d.Label(), d.Channel, reason
	s.audit("approval."+string(st), a)
	s.changed(a)
	return a, true
}

// Decide applies d to approval id. Exactly one decision on a pending,
// unexpired approval succeeds; it is authorized, and when d names an action
// hash it must match. Every other attempt returns an error and changes
// nothing.
func (s *Service) Decide(ctx context.Context, id string, d Decision) (Approval, error) {
	a, err := s.Get(id)
	if err != nil {
		return Approval{}, err
	}
	if a.Status != Pending {
		return a, fmt.Errorf("%w (%s)", ErrNotPending, a.Status)
	}
	if !s.now().Before(a.ExpiresAt) {
		a, _ = s.finish(a, Expired, Decider{Channel: "system"}, "expired")
		return a, ErrExpired
	}
	if d.ActionHash != "" && !strings.EqualFold(d.ActionHash, a.ActionHash) {
		s.audit("approval.mismatch", a)
		return a, ErrActionMismatch
	}
	if d.Decider.Channel == "" {
		return a, fmt.Errorf("%w: no channel", ErrForbidden)
	}
	if s.opts.Authorizer == nil {
		return a, fmt.Errorf("%w: no authorizer configured", ErrForbidden)
	}
	who, err := s.opts.Authorizer(ctx, a, d.Decider)
	if err != nil {
		s.audit("approval.refused", a)
		if errors.Is(err, ErrForbidden) {
			return a, err
		}
		return a, fmt.Errorf("%w: %v", ErrForbidden, err)
	}
	if who.Channel == "" {
		who.Channel = d.Decider.Channel
	}
	st := Rejected
	if d.Approve {
		st = Approved
	}
	reason := d.Reason
	if len(reason) > 500 {
		reason = reason[:500]
	}
	done, ok := s.finish(a, st, who, reason)
	if !ok {
		cur, gerr := s.Get(id)
		if gerr != nil {
			return a, gerr
		}
		return cur, fmt.Errorf("%w (%s)", ErrNotPending, cur.Status)
	}
	return done, nil
}

// Cancel ends a pending approval without a decision (the run stopped).
func (s *Service) Cancel(id, reason string) {
	a, err := s.Get(id)
	if err != nil || a.Status != Pending {
		return
	}
	s.finish(a, Cancelled, Decider{Channel: "system"}, reason)
}

// Wait blocks until approval id reaches a final status and returns it. It
// expires the approval at its deadline, and cancels it when ctx ends.
func (s *Service) Wait(ctx context.Context, id string) (Approval, error) {
	ch := make(chan Approval, 1)
	s.mu.Lock()
	s.waiters[id] = append(s.waiters[id], ch)
	s.mu.Unlock()

	a, err := s.Get(id)
	if err != nil {
		s.dropWaiter(id, ch)
		return Approval{}, err
	}
	if a.Status != Pending {
		s.dropWaiter(id, ch)
		return a, nil
	}
	timer := time.NewTimer(time.Until(a.ExpiresAt))
	defer timer.Stop()
	select {
	case done := <-ch:
		return done, nil
	case <-timer.C:
		s.expireDue()
	case <-ctx.Done():
		s.Cancel(id, "run ended before a decision")
	}
	// The final status was just written by expireDue/Cancel or by a decision
	// that raced them; either way it is in the store now.
	select {
	case done := <-ch:
		return done, nil
	default:
	}
	s.dropWaiter(id, ch)
	return s.Get(id)
}

func (s *Service) dropWaiter(id string, ch chan Approval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.waiters[id]
	for i, w := range ws {
		if w == ch {
			s.waiters[id] = append(ws[:i], ws[i+1:]...)
			break
		}
	}
	if len(s.waiters[id]) == 0 {
		delete(s.waiters, id)
	}
}
