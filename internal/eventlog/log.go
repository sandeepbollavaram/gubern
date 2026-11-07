// Package eventlog is Gubern's persisted, redacted run event log (milestone
// D). Every structured agent event (text, tool steps, approvals, run start
// and end) is redacted, then stored with its run id and a per-run sequence
// number, then delivered to live subscribers. A client that disconnects
// resumes with Last-Event-ID and gets exactly the events it missed: no gap,
// no duplicate.
//
// Design: docs/design/event-log.md.
package eventlog

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

// Run statuses (Phase 3 state machine, docs/design/agent-platform-phases-2-5.md).
//
// Live: running, waiting_approval (an approval is open), verifying (the
// completion check is running). Final: completed (its check passed),
// partial (its check still failed, or it ran out of budget), done (it
// finished and declared no check, so nothing was verified), failed,
// cancelled, and interrupted (the server stopped while it ran).
const (
	StatusRunning         = "running"
	StatusWaitingApproval = "waiting_approval"
	StatusVerifying       = "verifying"
	StatusCompleted       = "completed"
	StatusPartial         = "partial"
	StatusDone            = "done"
	StatusCancelled       = "cancelled"
	StatusFailed          = "failed"
	StatusInterrupted     = "interrupted"
)

// liveStatuses are the statuses of a run still in progress.
var liveStatuses = map[string]bool{StatusRunning: true, StatusWaitingApproval: true, StatusVerifying: true}

// finalOutcomes are the outcomes a run may report for itself.
var finalOutcomes = map[string]bool{StatusCompleted: true, StatusPartial: true, StatusDone: true, StatusFailed: true}

// Errors.
var (
	ErrNotFound    = errors.New("eventlog: no such run")
	ErrNewerSchema = errors.New("eventlog: store was written by a newer Gubern")
)

// Run is one agent run.
type Run struct {
	ID          string     `json:"id"`
	SessionID   string     `json:"session_id"`
	PrincipalID string     `json:"principal_id,omitempty"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	LastSeq     int64      `json:"last_seq"`
}

// Options configures a Log.
type Options struct {
	// Retention prunes events of runs that ended longer ago (default 7 days).
	Retention time.Duration
	// DetachGrace is how long a run keeps going with nobody watching before
	// it is cancelled (default 2 minutes). A client that reconnects within
	// the grace resumes the stream.
	DetachGrace time.Duration
	// SubscriberBuffer is each live subscriber's queue (default 256). A
	// subscriber that falls this far behind is disconnected; it resumes from
	// the store with Last-Event-ID.
	SubscriberBuffer int
}

// Log is the event store and live fan-out.
type Log struct {
	db   *sql.DB
	opts Options
	now  func() time.Time

	mu   sync.Mutex
	live map[string]*liveRun
}

type liveRun struct {
	run    Run
	cancel context.CancelFunc
	subs   map[chan agentevents.Event]struct{}
	detach *time.Timer
	idem   string
	// outcome is the run's own report of how it ended (run_outcome).
	outcome string
	// pending counts open approvals, for waiting_approval.
	pending int
}

var migrations = []string{`
CREATE TABLE runs (
	id           TEXT PRIMARY KEY,
	session_id   TEXT NOT NULL,
	principal_id TEXT NOT NULL DEFAULT '',
	status       TEXT NOT NULL,
	started_at   INTEGER NOT NULL,
	ended_at     INTEGER,
	last_seq     INTEGER NOT NULL DEFAULT 0,
	idem_key     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX runs_session ON runs(session_id, started_at);
CREATE INDEX runs_idem ON runs(principal_id, idem_key, started_at);
CREATE TABLE events (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id  TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	seq     INTEGER NOT NULL,
	type    TEXT NOT NULL,
	at      INTEGER NOT NULL,
	data    TEXT NOT NULL,
	UNIQUE (run_id, seq)
);
CREATE INDEX events_type_at ON events(type, at);
`}

// Open opens (creating if needed) the log at path. Runs a previous process
// left running are marked interrupted, and expired events are pruned.
func Open(path string, opts Options) (*Log, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("eventlog: open: %w", err)
	}
	_ = f.Close()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("eventlog: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if opts.Retention <= 0 {
		opts.Retention = 7 * 24 * time.Hour
	}
	if opts.DetachGrace <= 0 {
		opts.DetachGrace = 2 * time.Minute
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = 256
	}
	l := &Log{db: db, opts: opts, now: time.Now, live: map[string]*liveRun{}}
	if err := l.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	now := l.now().UnixMilli()
	if _, err := db.Exec(`UPDATE runs SET status = ?, ended_at = ? WHERE status IN (?, ?, ?)`, StatusInterrupted, now, StatusRunning, StatusWaitingApproval, StatusVerifying); err != nil {
		_ = db.Close()
		return nil, err
	}
	cutoff := l.now().Add(-opts.Retention).UnixMilli()
	if _, err := db.Exec(`DELETE FROM runs WHERE ended_at IS NOT NULL AND ended_at < ?`, cutoff); err != nil {
		_ = db.Close()
		return nil, err
	}
	return l, nil
}

// Close closes the store.
func (l *Log) Close() error { return l.db.Close() }

func (l *Log) migrate() error {
	var v int
	if err := l.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("%w (store v%d, binary v%d)", ErrNewerSchema, v, len(migrations))
	}
	for ; v < len(migrations); v++ {
		tx, err := l.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("eventlog: migration %d: %w", v+1, err)
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

func newRunID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "r-" + hex.EncodeToString(b), nil
}

// idemWindow is how long an Idempotency-Key maps to the run it started.
const idemWindow = 10 * time.Minute

// StartRun registers a run for session started by principal and returns it
// with the context the run must use: it is cancelled by Cancel, or when
// nobody has watched the run for DetachGrace. A non-empty idemKey that the
// same principal used within the last 10 minutes returns that run instead
// (existing = true) and starts nothing.
func (l *Log) StartRun(parent context.Context, sessionID, principalID, idemKey string) (run Run, ctx context.Context, existing bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if idemKey != "" {
		var id string
		qerr := l.db.QueryRow(`SELECT id FROM runs WHERE principal_id = ? AND idem_key = ? AND started_at > ? ORDER BY started_at DESC LIMIT 1`,
			principalID, idemKey, l.now().Add(-idemWindow).UnixMilli()).Scan(&id)
		if qerr == nil {
			r, gerr := l.getLocked(id)
			return r, nil, true, gerr
		}
	}
	id, err := newRunID()
	if err != nil {
		return Run{}, nil, false, err
	}
	now := l.now()
	run = Run{ID: id, SessionID: sessionID, PrincipalID: principalID, Status: StatusRunning, StartedAt: now}
	if _, err := l.db.Exec(`INSERT INTO runs (id, session_id, principal_id, status, started_at, idem_key) VALUES (?,?,?,?,?,?)`,
		id, sessionID, principalID, StatusRunning, now.UnixMilli(), idemKey); err != nil {
		return Run{}, nil, false, fmt.Errorf("eventlog: start run: %w", err)
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	lr := &liveRun{run: run, cancel: cancel, subs: map[chan agentevents.Event]struct{}{}, idem: idemKey}
	l.live[id] = lr
	// Nobody is subscribed yet: the grace starts now, so a run whose caller
	// never subscribes still ends.
	l.armDetachLocked(lr)
	if _, err := l.appendLocked(lr, agentevents.Event{Type: agentevents.RunStarted, SessionID: sessionID}); err != nil {
		return Run{}, nil, false, err
	}
	return lr.run, ctx, false, nil
}

func (l *Log) armDetachLocked(lr *liveRun) {
	if lr.detach != nil {
		lr.detach.Stop()
	}
	id := lr.run.ID
	lr.detach = time.AfterFunc(l.opts.DetachGrace, func() {
		l.mu.Lock()
		cur, ok := l.live[id]
		idle := ok && len(cur.subs) == 0
		l.mu.Unlock()
		if idle {
			l.Cancel(id)
		}
	})
}

// Append records ev for run (redacted first) and delivers it to live
// subscribers. Events for a run that already finished are dropped.
func (l *Log) Append(runID string, ev agentevents.Event) (agentevents.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lr, ok := l.live[runID]
	if !ok {
		return ev, ErrNotFound
	}
	return l.appendLocked(lr, ev)
}

// redactEvent removes secrets from every free-text field of ev.
func redactEvent(ev *agentevents.Event) {
	redactAll(&ev.Text, &ev.Output, &ev.Label)
	if ev.Approval != nil {
		a := *ev.Approval
		redactAll(&a.Description, &a.Command, &a.Preview)
		ev.Approval = &a
	}
}

func (l *Log) appendLocked(lr *liveRun, ev agentevents.Event) (agentevents.Event, error) {
	redactEvent(&ev)
	ev.RunID = lr.run.ID
	if ev.SessionID == "" {
		ev.SessionID = lr.run.SessionID
	}
	if ev.At == 0 {
		ev.At = l.now().UnixMilli()
	}
	ev.Seq = lr.run.LastSeq + 1
	data, err := json.Marshal(ev)
	if err != nil {
		return ev, err
	}
	if _, err := l.db.Exec(`INSERT INTO events (run_id, seq, type, at, data) VALUES (?,?,?,?,?)`,
		lr.run.ID, ev.Seq, string(ev.Type), ev.At, string(data)); err != nil {
		return ev, fmt.Errorf("eventlog: append: %w", err)
	}
	if _, err := l.db.Exec(`UPDATE runs SET last_seq = ? WHERE id = ?`, ev.Seq, lr.run.ID); err != nil {
		return ev, err
	}
	lr.run.LastSeq = ev.Seq
	l.trackStatusLocked(lr, ev)
	for ch := range lr.subs {
		select {
		case ch <- ev:
		default:
			// Too far behind: disconnect; it resumes from the store.
			delete(lr.subs, ch)
			close(ch)
		}
	}
	if len(lr.subs) == 0 && lr.detach == nil {
		l.armDetachLocked(lr)
	}
	return ev, nil
}

// Finish ends run with status, records a run_finished event, and ends every
// live subscription. Finishing twice is a no-op.
func (l *Log) Finish(runID, status string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lr, ok := l.live[runID]
	if !ok {
		return
	}
	// A run that reported its own outcome ends with it, unless it was
	// cancelled or failed outside its own control.
	if status == StatusDone && lr.outcome != "" {
		status = lr.outcome
	}
	_, _ = l.appendLocked(lr, agentevents.Event{Type: agentevents.RunFinished, Status: status})
	now := l.now()
	_, _ = l.db.Exec(`UPDATE runs SET status = ?, ended_at = ? WHERE id = ?`, status, now.UnixMilli(), runID)
	for ch := range lr.subs {
		close(ch)
	}
	if lr.detach != nil {
		lr.detach.Stop()
	}
	lr.cancel()
	delete(l.live, runID)
}

// trackStatusLocked moves a live run through the state machine as its
// events arrive, and records the outcome it reports.
func (l *Log) trackStatusLocked(lr *liveRun, ev agentevents.Event) {
	next := lr.run.Status
	switch ev.Type {
	case agentevents.ApprovalRequired:
		lr.pending++
		next = StatusWaitingApproval
	case agentevents.ApprovalDecided:
		if lr.pending > 0 {
			lr.pending--
		}
		if lr.pending == 0 {
			next = StatusRunning
		}
	case agentevents.RunStatus:
		if liveStatuses[ev.Status] {
			next = ev.Status
		}
	case agentevents.CheckResult:
		next = StatusRunning
	case agentevents.RunOutcome:
		if finalOutcomes[ev.Status] {
			lr.outcome = ev.Status
		}
		return
	default:
		return
	}
	if next != lr.run.Status && liveStatuses[next] {
		lr.run.Status = next
		_, _ = l.db.Exec(`UPDATE runs SET status = ? WHERE id = ? AND ended_at IS NULL`, next, lr.run.ID)
	}
}

// Cancel stops a live run (its context is cancelled; the caller that runs it
// then finishes it as cancelled).
func (l *Log) Cancel(runID string) bool {
	l.mu.Lock()
	lr, ok := l.live[runID]
	l.mu.Unlock()
	if ok {
		lr.cancel()
	}
	return ok
}

// Subscription is a replay of stored events followed by live ones.
type Subscription struct {
	// Replay holds the stored events after the requested sequence number.
	Replay []agentevents.Event
	// Live delivers later events; nil when the run has already finished. It
	// is closed when the run finishes or the subscriber falls behind.
	Live <-chan agentevents.Event
	// Close ends the subscription.
	Close func()
}

// Subscribe returns the events of run after seq afterSeq and, while the run
// is live, a channel of the events that follow. Reading the store and
// registering for live events happen under one lock, so the two never
// overlap or leave a gap.
func (l *Log) Subscribe(runID string, afterSeq int64) (Subscription, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.getLocked(runID); err != nil {
		return Subscription{}, err
	}
	replay, err := l.eventsLocked(runID, afterSeq)
	if err != nil {
		return Subscription{}, err
	}
	sub := Subscription{Replay: replay, Close: func() {}}
	lr, ok := l.live[runID]
	if !ok {
		return sub, nil
	}
	ch := make(chan agentevents.Event, l.opts.SubscriberBuffer)
	lr.subs[ch] = struct{}{}
	if lr.detach != nil {
		lr.detach.Stop()
		lr.detach = nil
	}
	sub.Live = ch
	var once sync.Once
	sub.Close = func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			cur, ok := l.live[runID]
			if !ok {
				return
			}
			if _, still := cur.subs[ch]; still {
				delete(cur.subs, ch)
				close(ch)
			}
			if len(cur.subs) == 0 {
				l.armDetachLocked(cur)
			}
		})
	}
	return sub, nil
}

func (l *Log) eventsLocked(runID string, afterSeq int64) ([]agentevents.Event, error) {
	rows, err := l.db.Query(`SELECT data FROM events WHERE run_id = ? AND seq > ? ORDER BY seq`, runID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []agentevents.Event
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var ev agentevents.Event
		if json.Unmarshal([]byte(data), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, rows.Err()
}

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var started int64
	var ended sql.NullInt64
	if err := sc.Scan(&r.ID, &r.SessionID, &r.PrincipalID, &r.Status, &started, &ended, &r.LastSeq); err != nil {
		return Run{}, err
	}
	r.StartedAt = time.UnixMilli(started)
	if ended.Valid {
		t := time.UnixMilli(ended.Int64)
		r.EndedAt = &t
	}
	return r, nil
}

const runCols = `id, session_id, principal_id, status, started_at, ended_at, last_seq`

func (l *Log) getLocked(id string) (Run, error) {
	r, err := scanRun(l.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

// Get returns run id.
func (l *Log) Get(id string) (Run, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.getLocked(id)
}

// Runs lists runs, newest first, optionally for one session.
func (l *Log) Runs(sessionID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q, args := `SELECT `+runCols+` FROM runs`, []any{}
	if sessionID != "" {
		q += ` WHERE session_id = ?`
		args = append(args, sessionID)
	}
	q += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Activity returns the latest timeline events (everything except streamed
// text), newest first, optionally for one session.
func (l *Log) Activity(sessionID string, limit int) ([]agentevents.Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT e.data FROM events e JOIN runs r ON r.id = e.run_id WHERE e.type != 'text'`
	var args []any
	if sessionID != "" {
		q += ` AND r.session_id = ?`
		args = append(args, sessionID)
	}
	q += ` ORDER BY e.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := l.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []agentevents.Event
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var ev agentevents.Event
		if json.Unmarshal([]byte(data), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, rows.Err()
}
