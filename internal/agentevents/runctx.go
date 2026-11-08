package agentevents

import "context"

// Run identity in a context (Phase 3). The API sets these when it starts a
// recorded run; they are never read from message text, so a chat message
// can't claim another run's checkpoints.

type runIDKey struct{}
type resumeKey struct{}
type workspaceKey struct{}
type networkRequestKey struct{}
type networkKey struct{}

// WithNetworkRequest returns ctx carrying the network ("on" or "off") the
// client asked for this run. It is a request, not a grant: the coordinator
// decides (turning it on beyond the server's default needs an approval) and
// records the outcome with WithNetwork.
func WithNetworkRequest(ctx context.Context, network string) context.Context {
	return context.WithValue(ctx, networkRequestKey{}, network)
}

// NetworkRequestFrom returns the network the client asked for, or "".
func NetworkRequestFrom(ctx context.Context) string {
	n, _ := ctx.Value(networkRequestKey{}).(string)
	return n
}

// WithNetwork returns ctx carrying the network a run was granted. Only the
// coordinator sets it, after deciding the request; tools read it.
func WithNetwork(ctx context.Context, network string) context.Context {
	return context.WithValue(ctx, networkKey{}, network)
}

// NetworkFrom returns the network a run was granted, or "" (the server's
// default).
func NetworkFrom(ctx context.Context) string {
	n, _ := ctx.Value(networkKey{}).(string)
	return n
}

// WithWorkspace returns ctx asking the run to work in dir, a workspace the
// API resolved from its own list (never a client-supplied path).
func WithWorkspace(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, workspaceKey{}, dir)
}

// WorkspaceFrom returns the run's workspace folder, or "" for the default.
func WorkspaceFrom(ctx context.Context) string {
	d, _ := ctx.Value(workspaceKey{}).(string)
	return d
}

// WithRunID returns ctx carrying the event-log run id of the current run.
func WithRunID(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runIDKey{}, runID)
}

// RunIDFrom returns the current run's id, or "".
func RunIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(runIDKey{}).(string)
	return id
}

// WithResume returns ctx asking the run to continue from the checkpoint of
// the interrupted run fromRunID.
func WithResume(ctx context.Context, fromRunID string) context.Context {
	return context.WithValue(ctx, resumeKey{}, fromRunID)
}

// ResumeFrom returns the run whose checkpoint this run continues, or "".
func ResumeFrom(ctx context.Context) string {
	id, _ := ctx.Value(resumeKey{}).(string)
	return id
}

// CarryRun returns dst with src's event sink, run id, resume request,
// workspace and network request (never a grant), for code that runs a request under a different lifecycle context (the
// agent runtime's) but must keep the request's run identity.
func CarryRun(dst, src context.Context) context.Context {
	dst = WithSink(dst, SinkFrom(src))
	if id := RunIDFrom(src); id != "" {
		dst = WithRunID(dst, id)
	}
	if from := ResumeFrom(src); from != "" {
		dst = WithResume(dst, from)
	}
	if ws := WorkspaceFrom(src); ws != "" {
		dst = WithWorkspace(dst, ws)
	}
	if n := NetworkRequestFrom(src); n != "" {
		dst = WithNetworkRequest(dst, n)
	}
	return dst
}
