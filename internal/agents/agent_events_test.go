package agents

import (
	"context"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

func collect(ctx context.Context) (context.Context, *[]agentevents.Event) {
	var evs []agentevents.Event
	return agentevents.WithSink(ctx, func(e agentevents.Event) { evs = append(evs, e) }), &evs
}

// A tool that runs without approval produces a tool_start/tool_end pair with
// the same step id and an ok status, and no text lines in events mode.
func TestExecuteLocalTool_EmitsToolEvents(t *testing.T) {
	c, _ := localCoord(t, nil)
	ctx, evs := collect(context.Background())
	var text []string
	if _, err := c.ExecuteLocalTool(ctx, "s1", "list_directory", map[string]any{"path": "."}, func(s string) { text = append(text, s) }); err != nil {
		t.Fatal(err)
	}
	if len(*evs) != 2 || (*evs)[0].Type != agentevents.ToolStart || (*evs)[1].Type != agentevents.ToolEnd {
		t.Fatalf("events = %+v, want tool_start then tool_end", *evs)
	}
	start, end := (*evs)[0], (*evs)[1]
	if start.StepID == "" || start.StepID != end.StepID || end.Status != "ok" || end.Tool != "list_directory" || end.SessionID != "s1" {
		t.Errorf("unpaired or wrong events: %+v / %+v", start, end)
	}
	if len(text) != 0 {
		t.Errorf("step text lines repeated in events mode: %q", text)
	}
}

// Without a sink (TUI, Telegram, scripts) the text lines are unchanged.
func TestExecuteLocalTool_NoSinkKeepsTextLines(t *testing.T) {
	c, _ := localCoord(t, nil)
	var text []string
	if _, err := c.ExecuteLocalTool(context.Background(), "s1", "list_directory", map[string]any{"path": "."}, func(s string) { text = append(text, s) }); err != nil {
		t.Fatal(err)
	}
	if len(text) < 2 {
		t.Fatalf("classic text lines missing: %q", text)
	}
}

// An approval-gated action on the async path emits approval_required with
// what the dashboard shows, and is listed by PendingApprovals until decided.
func TestExecuteLocalTool_ApprovalEventAndPendingList(t *testing.T) {
	c, dir := localCoord(t, nil)
	ctx, evs := collect(context.Background())
	if _, err := c.ExecuteLocalTool(ctx, "s9", "write_file", map[string]any{"path": "a.txt", "content": "hello"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	var req *agentevents.Pending
	for _, e := range *evs {
		if e.Type == agentevents.ApprovalRequired {
			req = e.Approval
		}
	}
	if req == nil {
		t.Fatalf("no approval_required event in %+v", *evs)
	}
	if req.SessionID != "s9" || req.Tool != "write_file" || req.ActionID == "" || req.Risk == "" || req.Workspace != dir {
		t.Errorf("approval info incomplete: %+v", req)
	}
	if req.ExpiresAt.Sub(req.CreatedAt) != approvalTTL || time.Until(req.ExpiresAt) <= 0 {
		t.Errorf("expiry = %v after creation, want %v", req.ExpiresAt.Sub(req.CreatedAt), approvalTTL)
	}

	pending := c.PendingApprovals()
	if len(pending) != 1 || pending[0].ActionID != req.ActionID {
		t.Fatalf("PendingApprovals = %+v, want the one action", pending)
	}
	if _, matched, err := c.ApproveActionID("s9", req.ActionID, false); err != nil || !matched {
		t.Fatalf("reject: matched=%v err=%v", matched, err)
	}
	if left := c.PendingApprovals(); len(left) != 0 {
		t.Errorf("decided action still listed: %+v", left)
	}
}

// The runtime carries the caller's sink across its own lifecycle context and
// delivers text on the same ordered channel.
func TestRuntimeSubmit_CarriesSinkAndText(t *testing.T) {
	c := newTestCoordinator(t, StubAIGateway{})
	rt, err := NewRuntime(RuntimeConfig{Bus: NewBus(), Coordinator: c, MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	evCh := make(chan agentevents.Event, 64)
	ctx := agentevents.WithSink(context.Background(), func(e agentevents.Event) { evCh <- e })
	ch, err := rt.Submit(ctx, "hello", "sess-ev")
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	close(evCh)
	var text int
	for e := range evCh {
		if e.Type == agentevents.Text && e.Text != "" {
			text++
		}
	}
	if text == 0 {
		t.Error("no text events delivered through the sink")
	}
}
