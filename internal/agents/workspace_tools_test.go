package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/llm"
)

// TestAgentRun_InAnotherWorkspace: a run whose context names another
// workspace resolves relative paths there and sees that workspace's skills.
func TestAgentRun_InAnotherWorkspace(t *testing.T) {
	model := &scriptedModel{replies: []llm.ToolChatResult{
		call("c1", "list_directory", map[string]any{"path": "."}),
		{Text: "listed"},
	}}
	c, defDir := agentCoord(t, toolGateway{model}, nil)
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "only-in-other.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defDir, "only-in-default.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(other, ".agents", "skills", "deploy")
	if err := os.MkdirAll(skill, 0o750); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: deploy\ndescription: Deploy it\n---\nbody\n"), 0o600)
	c.SetWorkspaceTools(func(root string) (*ToolRegistry, error) {
		reg := NewToolRegistry()
		return reg, RegisterLocalTools(reg, LocalFSConfig{Root: root})
	})

	ctx := agentevents.WithWorkspace(context.Background(), other)
	if _, err := c.HandleMessage(ctx, "/agent look around", "ws1"); err != nil {
		t.Fatal(err)
	}
	first := model.seen[0][0].Content
	if !strings.Contains(first, "- deploy:") {
		t.Errorf("the other workspace's skills weren't offered:\n%s", first)
	}
	var listing string
	for _, m := range model.seen[1] {
		if m.ToolCallID == "c1" {
			listing = m.Content
		}
	}
	if !strings.Contains(listing, "only-in-other.txt") || strings.Contains(listing, "only-in-default.txt") {
		t.Errorf("relative path didn't resolve in the chosen workspace: %q", listing)
	}
	if !strings.Contains(model.system, other) {
		t.Error("the system prompt doesn't name the chosen workspace")
	}
}
