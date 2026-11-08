package agentevents

import "context"

// Run identity in a context (Phase 3). The API sets these when it starts a
// recorded run; they are never read from message text, so a chat message
// can't claim another run's checkpoints.

type runIDKey struct{}
type resumeKey struct{}

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

// CarryRun returns dst with src's event sink, run id and resume request,
// for code that runs a request under a different lifecycle context (the
// agent runtime's) but must keep the request's run identity.
func CarryRun(dst, src context.Context) context.Context {
	dst = WithSink(dst, SinkFrom(src))
	if id := RunIDFrom(src); id != "" {
		dst = WithRunID(dst, id)
	}
	if from := ResumeFrom(src); from != "" {
		dst = WithResume(dst, from)
	}
	return dst
}
