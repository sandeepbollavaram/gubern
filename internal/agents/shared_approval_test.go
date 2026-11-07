package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// blockingApprover stands in for Telegram: it waits until its context is
// cancelled (nobody pressed a button) and records that it was cancelled.
func blockingApprover(cancelled chan<- struct{}) ApprovalFunc {
	return func(ctx context.Context, _ ApprovalRequest) bool {
		<-ctx.Done()
		cancelled <- struct{}{}
		return false
	}
}

func waitPendingAction(t *testing.T, c *Coordinator) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if p := c.PendingApprovals(); len(p) == 1 {
			return p[0].ActionID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("action never became pending")
	return ""
}

// With Telegram configured, a dashboard/app decision (the API) still decides
// the action, the Telegram wait is cancelled, and the action runs.
func TestSharedApproval_APIDecisionWinsOverWaitingApprover(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	c, dir := localCoord(t, blockingApprover(cancelled))
	type res struct{ err error }
	done := make(chan res, 1)
	go func() {
		_, err := c.ExecuteLocalTool(context.Background(), "s1", "write_file", map[string]any{"path": "a.txt", "content": "hi"}, func(string) {})
		done <- res{err}
	}()
	id := waitPendingAction(t, c)
	if _, matched, err := c.ApproveActionID("s1", id, true); err != nil || !matched {
		t.Fatalf("API approve: matched=%v err=%v", matched, err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("approved action failed: %v", r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run still waiting after an API approval")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(b) != "hi" {
		t.Errorf("approved write did not happen: %q %v", b, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Error("the messaging approver was not cancelled")
	}
}

// Telegram can still decide first; the entry is then gone, so a late API
// call finds nothing pending.
func TestSharedApproval_ApproverDecisionClearsPending(t *testing.T) {
	c, dir := localCoord(t, func(context.Context, ApprovalRequest) bool { return true })
	if _, err := c.ExecuteLocalTool(context.Background(), "s2", "write_file", map[string]any{"path": "b.txt", "content": "ok"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatalf("approved write missing: %v", err)
	}
	if p := c.PendingApprovals(); len(p) != 0 {
		t.Errorf("pending after decision: %+v", p)
	}
	if _, matched, _ := c.ApproveActionID("s2", "", true); matched {
		t.Error("a late API approve matched an already-decided action")
	}
}

// A rejection from the API reaches the run and nothing is written.
func TestSharedApproval_APIRejectStopsAction(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	c, dir := localCoord(t, blockingApprover(cancelled))
	errCh := make(chan error, 1)
	go func() {
		_, err := c.ExecuteLocalTool(context.Background(), "s3", "write_file", map[string]any{"path": "c.txt", "content": "no"}, func(string) {})
		errCh <- err
	}()
	id := waitPendingAction(t, c)
	if _, matched, err := c.ApproveActionID("s3", id, false); err != nil || !matched {
		t.Fatalf("API reject: matched=%v err=%v", matched, err)
	}
	if err := <-errCh; err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("rejected run returned %v, want a rejection error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "c.txt")); !os.IsNotExist(err) {
		t.Errorf("rejected write happened: %v", err)
	}
}
