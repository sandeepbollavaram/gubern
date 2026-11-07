package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/approvals"
)

// brokeredCoord is a coordinator with local tools and the real approval
// service. Only the "owner" principal and channel users named "ok" may decide.
func brokeredCoord(t *testing.T, ttl time.Duration) (*Coordinator, *approvals.Service, string) {
	t.Helper()
	c, dir := localCoord(t, nil)
	svc, _, err := approvals.Open(filepath.Join(t.TempDir(), "approvals.db"), approvals.Options{
		TTL: ttl,
		Authorizer: func(_ context.Context, _ approvals.Approval, d approvals.Decider) (approvals.Decider, error) {
			if d.PrincipalID == "owner" || d.External == "ok" {
				return d, nil
			}
			return d, approvals.ErrForbidden
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	c.SetApprovals(svc)
	return c, svc, dir
}

var owner = approvals.Decider{PrincipalID: "owner", Channel: "api"}

func park(t *testing.T, c *Coordinator, session, path, content string) string {
	t.Helper()
	var lines []string
	if _, err := c.ExecuteLocalTool(context.Background(), session, "write_file",
		map[string]any{"path": path, "content": content}, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	p := c.PendingApprovals()
	if len(p) == 0 {
		t.Fatalf("nothing pending; lines=%v", lines)
	}
	if len(p[len(p)-1].ActionHash) != 64 || len(p[len(p)-1].ActionID) != 16 {
		t.Fatalf("pending = %+v", p[len(p)-1])
	}
	return p[len(p)-1].ActionID
}

func TestBrokered_DecisionExecutesOnceFromAnyChannel(t *testing.T) {
	c, svc, dir := brokeredCoord(t, time.Minute)
	id := park(t, c, "s1", "a.txt", "hi")
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err == nil {
		t.Fatal("the action ran before any decision")
	}

	// Many channels decide at once; exactly one wins and the write happens once.
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i, ch := range []string{"api", "telegram", "whatsapp", "slack", "telegram", "api"} {
		wg.Add(1)
		go func(i int, ch string) {
			defer wg.Done()
			d := approvals.Decider{Channel: ch, External: "ok"}
			if i == 0 {
				d = owner
			}
			if _, _, err := c.DecideApproval(context.Background(), DecideRequest{ApprovalID: id, Approve: true, Decider: d}); err == nil {
				wins.Add(1)
			}
		}(i, ch)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d decisions won", wins.Load())
	}
	if b, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("approved write: %q %v", b, err)
	}
	a, _ := svc.Get(id)
	if a.Status != approvals.Approved || a.DecidedBy == "" {
		t.Fatalf("record = %+v", a)
	}
	if p := c.PendingApprovals(); len(p) != 0 {
		t.Errorf("still pending: %+v", p)
	}
}

func TestBrokered_UnauthorizedAndUnattributedDecisionsRefused(t *testing.T) {
	c, svc, dir := brokeredCoord(t, time.Minute)
	id := park(t, c, "s1", "b.txt", "x")
	_, _, err := c.DecideApproval(context.Background(), DecideRequest{ApprovalID: id, Approve: true, Decider: approvals.Decider{Channel: "telegram", External: "stranger"}})
	if !errors.Is(err, approvals.ErrForbidden) {
		t.Fatalf("stranger err = %v", err)
	}
	// The legacy session-only call names no decider: refused.
	if _, matched, err := c.ApproveActionID("s1", id, true); !matched || !errors.Is(err, approvals.ErrForbidden) {
		t.Fatalf("unattributed approve: matched=%v err=%v", matched, err)
	}
	// A decision for another session's approval is refused.
	if _, _, err := c.DecideApproval(context.Background(), DecideRequest{SessionID: "s2", ApprovalID: id, Approve: true, Decider: owner}); !errors.Is(err, ErrWrongSession) {
		t.Fatalf("wrong session err = %v", err)
	}
	if a, _ := svc.Get(id); a.Status != approvals.Pending {
		t.Fatalf("refused decisions changed status to %s", a.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "b.txt")); err == nil {
		t.Fatal("a refused decision ran the action")
	}
	// Session form with the owner still works.
	if tr, _, err := c.DecideApproval(context.Background(), DecideRequest{SessionID: "s1", Approve: false, Decider: owner}); err != nil || tr == "" {
		t.Fatalf("owner reject: %q %v", tr, err)
	}
}

func TestBrokered_ModifiedActionIsNotRun(t *testing.T) {
	c, _, dir := brokeredCoord(t, time.Minute)
	id := park(t, c, "s1", "c.txt", "original")
	// The decider confirms a different action: refused.
	other := approvals.HashAction(approvals.Action{Tool: "write_file", Params: map[string]any{"path": "c.txt", "content": "evil"}})
	if _, _, err := c.DecideApproval(context.Background(), DecideRequest{ApprovalID: id, Approve: true, ActionHash: other, Decider: owner}); !errors.Is(err, approvals.ErrActionMismatch) {
		t.Fatalf("mismatch err = %v", err)
	}
	// The parked parameters change after the request: the approval no longer
	// matches what would run, so nothing runs.
	c.mu.Lock()
	c.pending["s1"].params["content"] = "tampered"
	c.mu.Unlock()
	tr, _, err := c.DecideApproval(context.Background(), DecideRequest{ApprovalID: id, Approve: true, Decider: owner})
	if err != nil {
		t.Fatal(err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "c.txt")); serr == nil {
		t.Fatalf("a tampered action ran (%s)", tr)
	}
}

func TestBrokered_NewActionSupersedesOld(t *testing.T) {
	c, svc, _ := brokeredCoord(t, time.Minute)
	first := park(t, c, "s1", "d.txt", "1")
	second := park(t, c, "s1", "d.txt", "2")
	if first == second {
		t.Fatal("ids repeat")
	}
	if a, _ := svc.Get(first); a.Status != approvals.Cancelled {
		t.Fatalf("old approval = %s, want cancelled", a.Status)
	}
	// Replaying the old id cannot approve the new action.
	if _, _, err := c.DecideApproval(context.Background(), DecideRequest{ApprovalID: first, Approve: true, Decider: owner}); !errors.Is(err, approvals.ErrNotPending) {
		t.Fatalf("replay err = %v", err)
	}
}

func TestBrokered_LoopWaitsForDecisionAndExpiry(t *testing.T) {
	c, svc, _ := brokeredCoord(t, 300*time.Millisecond)
	approve := c.loopApprover("loop", func(string) {})
	req := ApprovalRequest{Tool: "run_terminal", Params: map[string]any{"command": "echo hi"}, Description: "Run echo"}
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if p := c.PendingApprovals(); len(p) == 1 {
				_, _, _ = c.DecideApproval(context.Background(), DecideRequest{ApprovalID: p[0].ActionID, Approve: true, Decider: owner})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	if !approve(context.Background(), req) {
		t.Fatal("loop approval not granted")
	}
	// Nobody decides: the approval expires and the loop is refused.
	start := time.Now()
	if approve(context.Background(), req) {
		t.Fatal("expired approval granted")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("expiry did not end the wait")
	}
	list, _ := svc.List(approvals.Filter{SessionID: "loop"})
	if len(list) != 2 || list[0].Status != approvals.Expired {
		t.Errorf("loop approvals = %+v", list)
	}
}
