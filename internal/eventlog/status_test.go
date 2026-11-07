package eventlog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

func statusOf(t *testing.T, l *Log, id string) string {
	t.Helper()
	r, err := l.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return r.Status
}

// TestStateMachine: a run moves through waiting_approval and verifying as
// its events arrive, and ends with the outcome it reported.
func TestStateMachine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	l, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	run, _, _, err := l.StartRun(context.Background(), "s1", "p", "")
	if err != nil {
		t.Fatal(err)
	}
	step := func(ev agentevents.Event, want string) {
		t.Helper()
		if _, err := l.Append(run.ID, ev); err != nil {
			t.Fatal(err)
		}
		if got := statusOf(t, l, run.ID); got != want {
			t.Errorf("after %s/%s: status %q, want %q", ev.Type, ev.Status, got, want)
		}
	}
	step(agentevents.Event{Type: agentevents.ToolStart, Tool: "read_file"}, StatusRunning)
	step(agentevents.Event{Type: agentevents.ApprovalRequired, Approval: &agentevents.Pending{ActionID: "a1"}}, StatusWaitingApproval)
	step(agentevents.Event{Type: agentevents.ApprovalDecided, Status: "approved", Approval: &agentevents.Pending{ActionID: "a1"}}, StatusRunning)
	step(agentevents.Event{Type: agentevents.RunStatus, Status: StatusVerifying}, StatusVerifying)
	step(agentevents.Event{Type: agentevents.CheckResult, Status: "passed", Label: "go test"}, StatusRunning)
	step(agentevents.Event{Type: agentevents.RunStatus, Status: "completed"}, StatusRunning) // not a live status: ignored
	step(agentevents.Event{Type: agentevents.RunOutcome, Status: StatusCompleted}, StatusRunning)
	l.Finish(run.ID, StatusDone)
	if got := statusOf(t, l, run.ID); got != StatusCompleted {
		t.Errorf("final status %q, want completed (the run's own outcome)", got)
	}

	// A cancelled run stays cancelled whatever it reported.
	run2, _, _, _ := l.StartRun(context.Background(), "s2", "p", "")
	_, _ = l.Append(run2.ID, agentevents.Event{Type: agentevents.RunOutcome, Status: StatusPartial})
	l.Finish(run2.ID, StatusCancelled)
	if got := statusOf(t, l, run2.ID); got != StatusCancelled {
		t.Errorf("cancelled run ended %q", got)
	}

	// A run waiting on an approval when the server stops is interrupted.
	run3, _, _, _ := l.StartRun(context.Background(), "s3", "p", "")
	_, _ = l.Append(run3.ID, agentevents.Event{Type: agentevents.ApprovalRequired, Approval: &agentevents.Pending{ActionID: "a2"}})
	_ = l.Close()
	l2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	if got := statusOf(t, l2, run3.ID); got != StatusInterrupted {
		t.Errorf("after restart: %q, want interrupted", got)
	}
}
