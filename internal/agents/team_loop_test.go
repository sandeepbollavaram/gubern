package agents

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/llm"
)

// toolsSpy records the tools offered on every model call.
type toolsSpy struct {
	*scriptedModel
	mu      sync.Mutex
	offered [][]string
}

func (s *toolsSpy) ChatWithTools(ctx context.Context, system string, msgs []llm.ChatMessage, tools []llm.ToolDef) (llm.ToolChatResult, error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	s.mu.Lock()
	s.offered = append(s.offered, names)
	s.mu.Unlock()
	return s.scriptedModel.ChatWithTools(ctx, system, msgs, tools)
}

// spyGateway is a gateway whose tool calls go through a toolsSpy.
type spyGateway struct{ *toolsSpy }

func (spyGateway) Complete(context.Context, string, string) (string, error) { return "text", nil }

func teamCoord(t *testing.T, replies []llm.ToolChatResult) (*Coordinator, string, *toolsSpy, *[]string) {
	t.Helper()
	spy := &toolsSpy{scriptedModel: &scriptedModel{replies: replies}}
	var asked []string
	var mu sync.Mutex
	c, dir := agentCoord(t, spyGateway{spy}, func(_ context.Context, req ApprovalRequest) bool {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, req.Tool)
		return true
	})
	return c, dir, spy, &asked
}

// TestTeam_RolesOnTheLoop: coder, tester, reviewer run in turn under the same
// approvals as /agent. The tester can run a command (it goes through an
// approval; the old team path had no handler, T32), can't write, and the
// team ends completed only when the check passes and the reviewer approves.
func TestTeam_RolesOnTheLoop(t *testing.T) {
	c, dir, spy, asked := teamCoord(t, []llm.ToolChatResult{
		call("w1", "write_file", map[string]any{"path": "hello.go", "content": "package main\n"}), // coder
		{Text: "Wrote hello.go."},
		call("t1", "run_terminal", map[string]any{"command": "echo tests ok"}), // tester
		{Text: "Ran the tests.\nRESULT: PASS"},
		{Text: "Looks right.\nVERDICT: APPROVE"}, // reviewer
	})
	var outcome string
	ctx := agentevents.WithSink(context.Background(), func(e agentevents.Event) {
		if e.Type == agentevents.RunOutcome {
			outcome = e.Status
		}
	})
	reply, err := c.HandleMessage(ctx, `/team --check "exit 0" add hello.go`, "team1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hello.go")); err != nil {
		t.Errorf("the coder's write didn't happen: %v", err)
	}
	for _, role := range []string{"── Coder ──", "── Tester ──", "── Reviewer ──"} {
		if !strings.Contains(reply, role) {
			t.Errorf("reply lacks %q:\n%s", role, reply)
		}
	}
	if !slices.Contains(*asked, "run_terminal") {
		t.Errorf("the tester's command didn't go through an approval: %v", *asked)
	}
	if outcome != "completed" {
		t.Errorf("outcome = %q, want completed", outcome)
	}
	// Calls: coder x2, tester x2, reviewer x1.
	if len(spy.offered) != 5 {
		t.Fatalf("model calls = %d", len(spy.offered))
	}
	tester, reviewer := spy.offered[2], spy.offered[4]
	if slices.Contains(tester, "write_file") || !slices.Contains(tester, "run_terminal") {
		t.Errorf("tester tools = %v; want run_terminal and no write_file", tester)
	}
	if slices.Contains(reviewer, "run_terminal") || slices.Contains(reviewer, "write_file") {
		t.Errorf("reviewer tools = %v; want read-only", reviewer)
	}
}

func TestTeam_ChangesRequestedIsPartial(t *testing.T) {
	c, _, _, _ := teamCoord(t, []llm.ToolChatResult{
		{Text: "Done, no edits needed."},
		{Text: "Nothing to run.\nRESULT: PASS"},
		{Text: "Missing error handling in main.go:3.\nVERDICT: CHANGES REQUESTED"},
	})
	var outcome string
	ctx := agentevents.WithSink(context.Background(), func(e agentevents.Event) {
		if e.Type == agentevents.RunOutcome {
			outcome = e.Status
		}
	})
	if _, err := c.HandleMessage(ctx, "/team tidy main.go", "team2"); err != nil {
		t.Fatal(err)
	}
	if outcome != "partial" {
		t.Errorf("outcome = %q, want partial", outcome)
	}
}

func TestTeam_TesterCantWriteEvenIfAsked(t *testing.T) {
	c, dir, _, _ := teamCoord(t, []llm.ToolChatResult{
		{Text: "Nothing to change."},
		call("x", "write_file", map[string]any{"path": "sneaky.txt", "content": "x"}), // tester tries to edit
		{Text: "RESULT: PASS"},
		{Text: "VERDICT: APPROVE"},
	})
	reply, _ := c.HandleMessage(context.Background(), "/team check things", "team3")
	if _, err := os.Stat(filepath.Join(dir, "sneaky.txt")); !os.IsNotExist(err) {
		t.Error("the tester wrote a file")
	}
	if !strings.Contains(reply, "not available to this role") {
		t.Errorf("reply doesn't say why: %q", reply)
	}
}
