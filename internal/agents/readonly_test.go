package agents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/llm"
)

// TestLoop_ReadOnlyOffersOnlyReadTools: a read-only run hides every tool that
// writes, runs commands or changes git, and refuses one the model asks for
// anyway, even when the approver would have said yes.
func TestLoop_ReadOnlyOffersOnlyReadTools(t *testing.T) {
	reg, root := policyRegistry(t, LocalFSConfig{}, ToolPolicy{})
	m := &scriptedModel{replies: []llm.ToolChatResult{
		call("c1", "write_file", map[string]any{"path": "a.txt", "content": "x"}),
		{Text: "done"},
	}}
	audit := &approvalAuditRecorder{}
	approved := false
	res := RunLoop(context.Background(), LoopConfig{Model: m, Tools: reg, Audit: audit, ReadOnly: true,
		Approval: func(context.Context, ApprovalRequest) bool { approved = true; return true }}, "write it")

	if len(m.tools) == 0 {
		t.Fatal("no tools offered at all")
	}
	for _, d := range m.tools {
		s, err := reg.Get(d.Name)
		if err != nil {
			t.Fatal(err)
		}
		spec, _ := SpecFor(s)
		if spec.Risk != RiskRead || spec.SideEffect {
			t.Errorf("read-only run offered %s (risk %v)", d.Name, spec.Risk)
		}
	}
	if res.Steps[0].Outcome != "denied" || !strings.Contains(res.Steps[0].Observed, "read-only") {
		t.Errorf("step = %+v", res.Steps[0])
	}
	if approved {
		t.Error("approver was asked; a read-only run must refuse without asking")
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("file was written: %v", err)
	}
	if len(audit.entries) != 1 || audit.entries[0] != "agent.policy.denied|write_file" {
		t.Errorf("audit = %v", audit.entries)
	}
}

// TestLoop_ReadOnlyStillReads: read tools work as usual.
func TestLoop_ReadOnlyStillReads(t *testing.T) {
	reg, root := policyRegistry(t, LocalFSConfig{}, ToolPolicy{})
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &scriptedModel{replies: []llm.ToolChatResult{
		call("c1", "read_file", map[string]any{"path": "notes.txt"}),
		{Text: "it says hello"},
	}}
	res := RunLoop(context.Background(), LoopConfig{Model: m, Tools: reg, ReadOnly: true}, "read it")
	if res.Steps[0].Outcome != "ok" || !strings.Contains(res.Steps[0].Observed, "hello") {
		t.Errorf("step = %+v", res.Steps[0])
	}
}
