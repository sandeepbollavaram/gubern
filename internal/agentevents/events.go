// Package agentevents defines the structured events an agent run emits for
// clients that render it (the dashboard, the mobile app): tool steps,
// approval requests and decisions, questions and status. It has no
// dependencies so both internal/agents (producer) and internal/api (SSE
// transport) can import it without a cycle.
//
// Events travel alongside the existing text stream; they never replace it, so
// the TUI and scripts that parse text lines keep working unchanged.
package agentevents

import (
	"context"
	"time"
	"unicode/utf8"
)

// Type names an event. Values are part of the public SSE contract
// (docs/design/dashboard-mobile-plan.md §2.1); never rename one.
type Type string

const (
	// Text is a chunk of the agent's reply. In events mode it travels on the
	// same ordered channel as the other events.
	Text             Type = "text"
	ToolStart        Type = "tool_start"
	ToolEnd          Type = "tool_end"
	ApprovalRequired Type = "approval_required"
	ApprovalDecided  Type = "approval_decided"
	Status           Type = "status"
)

// MaxOutput caps a tool_end output excerpt so one event stays small; the full
// output remains in the text stream and the session history.
const MaxOutput = 4096

// Pending describes an action waiting for a human decision.
type Pending struct {
	SessionID string `json:"session_id"`
	ActionID  string `json:"action_id"`
	// ActionHash is the full SHA-256 of the canonical action (milestone B).
	// A decision that names it applies only to exactly this action.
	ActionHash  string    `json:"action_hash,omitempty"`
	Tool        string    `json:"tool"`
	Risk        string    `json:"risk"`
	Description string    `json:"description"`
	Command     string    `json:"command,omitempty"`
	Preview     string    `json:"preview,omitempty"`
	Workspace   string    `json:"workspace,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// Event is one structured step of an agent run.
type Event struct {
	Type Type `json:"type"`
	// RunID and Seq identify the event in the persisted log: Seq increases
	// by one per event within a run and is the SSE event id.
	RunID     string `json:"run_id,omitempty"`
	Seq       int64  `json:"seq,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// StepID pairs a tool_start with its tool_end.
	StepID     string   `json:"step_id,omitempty"`
	Tool       string   `json:"tool,omitempty"`
	Label      string   `json:"label,omitempty"`
	Status     string   `json:"status,omitempty"` // tool_end: "ok" | "failed"; approval_decided: "approved" | "rejected"
	DurationMs int64    `json:"duration_ms,omitempty"`
	Output     string   `json:"output,omitempty"`
	Approval   *Pending `json:"approval,omitempty"`
	DecidedVia string   `json:"decided_via,omitempty"`
	DecidedBy  string   `json:"decided_by,omitempty"`
	Text       string   `json:"text,omitempty"`
	At         int64    `json:"at"` // unix milliseconds
}

// Sink receives events. Implementations must not block for long: they run on
// the agent's goroutine.
type Sink func(Event)

type ctxKey struct{}

// WithSink returns ctx carrying sink. A nil sink returns ctx unchanged.
func WithSink(ctx context.Context, sink Sink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, sink)
}

// SinkFrom returns the sink carried by ctx, or nil.
func SinkFrom(ctx context.Context) Sink {
	s, _ := ctx.Value(ctxKey{}).(Sink)
	return s
}

// Emit sends ev to the sink in ctx, stamping the time. It is a no-op when ctx
// carries no sink, so producers can call it unconditionally.
func Emit(ctx context.Context, ev Event) {
	sink := SinkFrom(ctx)
	if sink == nil {
		return
	}
	if ev.At == 0 {
		ev.At = time.Now().UnixMilli()
	}
	ev.Output = Truncate(ev.Output, MaxOutput)
	sink(ev)
}

// Truncate shortens s to at most n bytes without splitting a UTF-8 sequence,
// marking the cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
