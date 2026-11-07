package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/llm"
)

func resumeRegistry(t *testing.T) (*ToolRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	reg := NewToolRegistry()
	if err := RegisterLocalTools(reg, LocalFSConfig{Root: dir}); err != nil {
		t.Fatal(err)
	}
	return reg, dir
}

// TestLoop_CheckpointsAreWriteAhead: the model's tool calls are saved before
// they run, then each result after it.
func TestLoop_CheckpointsAreWriteAhead(t *testing.T) {
	reg, _ := resumeRegistry(t)
	m := &scriptedModel{replies: []llm.ToolChatResult{
		call("c1", "list_directory", map[string]any{"path": "."}),
		{Text: "done"},
	}}
	var states []LoopState
	RunLoop(context.Background(), LoopConfig{Model: m, Tools: reg, Checkpoint: func(s LoopState) { states = append(states, s) }}, "look")
	if len(states) != 3 {
		t.Fatalf("checkpoints = %d, want 3 (start, call asked, result)", len(states))
	}
	ahead := states[1].Messages[len(states[1].Messages)-1]
	if ahead.Role != "assistant" || len(ahead.ToolCalls) != 1 || states[1].Turns != 1 {
		t.Errorf("second checkpoint should end with the asked-for call: %+v", states[1])
	}
	if last := states[2].Messages[len(states[2].Messages)-1]; last.Role != "tool" || last.ToolCallID != "c1" {
		t.Errorf("third checkpoint should end with the result: %+v", last)
	}
}

// TestLoop_ResumeNeverRepeatsASideEffect: a write that was in flight when the
// run stopped is not run again; the model is told its outcome is unknown.
// A read that was in flight runs again, and recorded results are reused.
func TestLoop_ResumeNeverRepeatsASideEffect(t *testing.T) {
	reg, dir := resumeRegistry(t)
	if err := os.WriteFile(filepath.Join(dir, "seen.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved := LoopState{
		Turns: 2, Tokens: 40,
		Messages: []llm.ChatMessage{
			{Role: "user", Content: "do it"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a", Name: "read_file", Arguments: map[string]any{"path": "seen.txt"}}}},
			{Role: "tool", ToolCallID: "a", ToolName: "read_file", Content: "x (recorded)"},
			{Role: "assistant", ToolCalls: []llm.ToolCall{
				{ID: "b", Name: "write_file", Arguments: map[string]any{"path": "once.txt", "content": "1"}},
				{ID: "c", Name: "list_directory", Arguments: map[string]any{"path": "."}},
			}},
		},
	}
	approvals := 0
	m := &scriptedModel{replies: []llm.ToolChatResult{{Text: "checked; all good"}}}
	res := RunLoop(context.Background(), LoopConfig{
		Model: m, Tools: reg, Resume: &saved,
		Approval: func(context.Context, ApprovalRequest) bool { approvals++; return true },
	}, "ignored on resume")

	if _, err := os.Stat(filepath.Join(dir, "once.txt")); !os.IsNotExist(err) {
		t.Fatal("the in-flight write ran again")
	}
	if approvals != 0 {
		t.Errorf("approvals asked on resume = %d; nothing with a side effect may run", approvals)
	}
	if res.Turns != 3 || res.Answer != "checked; all good" {
		t.Errorf("res = %+v; turns should continue from 2", res)
	}
	sent := m.seen[0]
	var b, c string
	for _, msg := range sent {
		switch msg.ToolCallID {
		case "b":
			b = msg.Content
		case "c":
			c = msg.Content
		}
	}
	if !strings.Contains(b, "outcome unknown") {
		t.Errorf("write result = %q, want outcome unknown", b)
	}
	if !strings.Contains(c, "seen.txt") {
		t.Errorf("in-flight read should run again; got %q", c)
	}
	if sent[2].Content != "x (recorded)" {
		t.Error("a recorded result was replaced")
	}
}
