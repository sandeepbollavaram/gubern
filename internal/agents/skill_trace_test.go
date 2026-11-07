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

// TestAgentCommand_SkillReadIsAudited: when the model reads a catalogued
// SKILL.md, the run records which skill (and which version of it) was used.
func TestAgentCommand_SkillReadIsAudited(t *testing.T) {
	model := &scriptedModel{replies: []llm.ToolChatResult{
		call("c1", "read_file", map[string]any{"path": ".agents/skills/release-notes/SKILL.md"}),
		call("c2", "read_file", map[string]any{"path": "notes.txt"}), // not a skill
		{Text: "Followed the release-notes skill."},
	}}
	c, dir := agentCoord(t, toolGateway{model}, nil)
	rec := &approvalAuditRecorder{}
	c.cfg.Audit = rec
	skillDir := filepath.Join(dir, ".agents", "skills", "release-notes")
	if err := os.MkdirAll(skillDir, 0o750); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: release-notes\ndescription: Draft release notes from git log\n---\nRun git log.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var events []agentevents.Event
	ctx := agentevents.WithSink(context.Background(), func(e agentevents.Event) { events = append(events, e) })
	reply, err := c.HandleMessage(ctx, "/agent write release notes", "sk1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "Using skill: release-notes") {
		t.Errorf("reply doesn't show the skill in use: %q", reply)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var skillEntries []map[string]any
	for i, e := range rec.entries {
		if strings.HasPrefix(e, "agent.skill.read|") {
			skillEntries = append(skillEntries, rec.details[i])
		}
	}
	if len(skillEntries) != 1 {
		t.Fatalf("skill audit entries = %v (all: %v)", skillEntries, rec.entries)
	}
	if skillEntries[0]["skill"] != "release-notes" || len(skillEntries[0]["sha256"].(string)) != 64 {
		t.Errorf("detail = %v", skillEntries[0])
	}
	// The run's event stream says so too, for the dashboard and the app.
	var activated []agentevents.Event
	for _, e := range events {
		if e.Type == agentevents.SkillActivated {
			activated = append(activated, e)
		}
	}
	if len(activated) != 1 || activated[0].Label != "release-notes" || len(activated[0].SkillHash) != 64 || activated[0].SessionID != "sk1" {
		t.Errorf("skill_activated events = %+v", activated)
	}
}

func TestSkillKey_Normalises(t *testing.T) {
	if SkillKey("./a/../.agents/skills/x/SKILL.md") != SkillKey(".agents/skills/x/SKILL.md") {
		t.Error("cleaning differs")
	}
	if SkillKey(filepath.Join(".agents", "skills", "x", "SKILL.md")) != SkillKey(".agents/skills/x/SKILL.md") {
		t.Error("separator differs")
	}
}
