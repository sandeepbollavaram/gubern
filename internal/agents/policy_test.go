package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/llm"
)

func policyRegistry(t *testing.T, cfg LocalFSConfig, p ToolPolicy) (*ToolRegistry, string) {
	t.Helper()
	dir := t.TempDir()
	if cfg.Root == "" {
		cfg.Root = dir
	}
	reg := NewToolRegistry()
	if err := RegisterLocalTools(reg, cfg); err != nil {
		t.Fatal(err)
	}
	if err := reg.ApplyPolicy(p); err != nil {
		t.Fatal(err)
	}
	return reg, cfg.Root
}

func TestPolicy_DenyBlocksAndHidesTheTool(t *testing.T) {
	reg, _ := policyRegistry(t, LocalFSConfig{}, ToolPolicy{Deny: []string{"run_terminal"}})
	tool, err := reg.Get("run_terminal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"command": "echo hi"}); !errors.Is(err, ErrToolDenied) {
		t.Errorf("err = %v, want ErrToolDenied", err)
	}
	// Even an "approved" re-run refuses.
	if _, err := executeApprovedLocal(context.Background(), tool, map[string]any{"command": "echo hi"}); !errors.Is(err, ErrToolDenied) {
		t.Errorf("approved re-run err = %v", err)
	}
	for _, s := range reg.Specs() {
		if s.Name == "run_terminal" {
			t.Error("denied tool offered to the model")
		}
	}
	// In a loop run the model can't call it either.
	m := &scriptedModel{replies: []llm.ToolChatResult{call("c1", "run_terminal", map[string]any{"command": "echo hi"})}}
	audit := &approvalAuditRecorder{}
	res := RunLoop(context.Background(), LoopConfig{Model: m, Tools: reg, Audit: audit,
		Approval: func(context.Context, ApprovalRequest) bool { return true }}, "run it")
	if res.Steps[0].Outcome != "denied" || !strings.Contains(res.Steps[0].Observed, "disabled by policy") {
		t.Errorf("step = %+v", res.Steps[0])
	}
	if len(audit.entries) != 1 || audit.entries[0] != "agent.policy.denied|run_terminal" {
		t.Errorf("audit = %v", audit.entries)
	}
}

func TestPolicy_AskGatesAReadOnlyTool(t *testing.T) {
	reg, dir := policyRegistry(t, LocalFSConfig{}, ToolPolicy{Ask: []string{"read_file"}})
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("s3cret"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := func() *scriptedModel {
		return &scriptedModel{replies: []llm.ToolChatResult{call("c1", "read_file", map[string]any{"path": "secret.txt"}), {Text: "done"}}}
	}
	var asked []ApprovalRequest
	deny := func(_ context.Context, r ApprovalRequest) bool { asked = append(asked, r); return false }
	res := RunLoop(context.Background(), LoopConfig{Model: m(), Tools: reg, Approval: deny}, "read it")
	if res.Steps[0].Outcome != "rejected" || len(asked) != 1 || !strings.Contains(asked[0].Description, "agents.policy requires approval") {
		t.Fatalf("outcome %q, asked %+v", res.Steps[0].Outcome, asked)
	}
	if strings.Contains(res.Steps[0].Observed, "s3cret") {
		t.Error("file content reached the model without approval")
	}
	allow := func(context.Context, ApprovalRequest) bool { return true }
	res = RunLoop(context.Background(), LoopConfig{Model: m(), Tools: reg, Approval: allow}, "read it")
	if res.Steps[0].Outcome != "ok" || !strings.Contains(res.Steps[0].Observed, "s3cret") {
		t.Errorf("approved read: %+v", res.Steps[0])
	}
}

// TestPolicy_AskOnAGatedToolAsksOnce: write_file already needs approval; the
// policy prompt and the tool's own gate must not mean two prompts.
func TestPolicy_AskOnAGatedToolAsksOnce(t *testing.T) {
	reg, dir := policyRegistry(t, LocalFSConfig{RequireApproval: true}, ToolPolicy{Ask: []string{"write_file"}})
	n := 0
	approve := func(context.Context, ApprovalRequest) bool { n++; return true }
	m := &scriptedModel{replies: []llm.ToolChatResult{call("c1", "write_file", map[string]any{"path": "a.txt", "content": "x"}), {Text: "done"}}}
	res := RunLoop(context.Background(), LoopConfig{Model: m, Tools: reg, Approval: approve}, "write")
	if res.Steps[0].Outcome != "ok" || n != 1 {
		t.Errorf("outcome %q after %d approvals", res.Steps[0].Outcome, n)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "x" {
		t.Errorf("file = %q", data)
	}
}

// TestPolicy_AskOverridesAutoMode: Auto Mode would waive an in-scope write,
// but an ask rule still applies.
func TestPolicy_AskOverridesAutoMode(t *testing.T) {
	root := t.TempDir()
	reg, _ := policyRegistry(t, LocalFSConfig{Root: root, TrustedRoot: root, AutoMode: true, RequireApproval: true},
		ToolPolicy{Ask: []string{"write_file"}})
	tool, _ := reg.Get("write_file")
	_, err := tool.Execute(context.Background(), map[string]any{"path": "a.txt", "content": "x"})
	var ae *ApprovalError
	if !errors.As(err, &ae) {
		t.Fatalf("Auto Mode waived an ask rule: err = %v", err)
	}
}

func TestPolicy_Validate(t *testing.T) {
	if err := (ToolPolicy{Deny: []string{"run_terminal"}, Ask: []string{"read_file"}}).Validate(); err != nil {
		t.Errorf("valid policy rejected: %v", err)
	}
	for name, p := range map[string]ToolPolicy{
		"typo in deny": {Deny: []string{"run_terminl"}},
		"typo in ask":  {Ask: []string{"reed_file"}},
		"both lists":   {Deny: []string{"git_commit"}, Ask: []string{"git_commit"}},
	} {
		err := p.Validate()
		if err == nil || !strings.Contains(err.Error(), "known tools:") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	reg := NewToolRegistry()
	if err := reg.ApplyPolicy(ToolPolicy{Deny: []string{"nope"}}); err == nil {
		t.Error("ApplyPolicy accepted an unknown tool")
	}
}

func TestPolicy_WrapperKeepsSpecAndSideEffect(t *testing.T) {
	reg, _ := policyRegistry(t, LocalFSConfig{}, ToolPolicy{Ask: []string{"write_file", "read_file"}})
	w, _ := reg.Get("write_file")
	r, _ := reg.Get("read_file")
	if se, ok := w.(interface{ SideEffecting() bool }); !ok || !se.SideEffecting() {
		t.Error("write_file lost SideEffecting")
	}
	if se, ok := r.(interface{ SideEffecting() bool }); ok && se.SideEffecting() {
		t.Error("read_file became side-effecting")
	}
	if s, ok := SpecFor(w); !ok || s.Name != "write_file" || s.Parameters == nil {
		t.Errorf("spec lost: %+v", s)
	}
}

// TestPolicy_DeniedCommandIsAudited: a chat command for a denied tool is
// refused and recorded in the audit log.
func TestPolicy_DeniedCommandIsAudited(t *testing.T) {
	reg, dir := policyRegistry(t, LocalFSConfig{}, ToolPolicy{Deny: []string{"run_terminal"}})
	rec := &approvalAuditRecorder{}
	c, err := NewCoordinator(CoordinatorConfig{Bus: NewBus(), AIGateway: StubAIGateway{}, LocalTools: reg, WorkingDir: dir, Audit: rec})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	_, err = c.ExecuteLocalTool(context.Background(), "pd1", "run_terminal", map[string]any{"command": "echo hi"}, func(s string) { lines = append(lines, s) })
	if !errors.Is(err, ErrToolDenied) {
		t.Fatalf("err = %v", err)
	}
	if len(rec.entries) != 1 || rec.entries[0] != "agent.policy.denied|run_terminal" {
		t.Errorf("audit = %v", rec.entries)
	}
}

// TestPolicy_CoversSubAgentTools: agents.policy applies to the sub-agent
// tools too, and an approved "ask" tool runs once instead of asking again.
func TestPolicy_CoversSubAgentTools(t *testing.T) {
	if err := (ToolPolicy{Deny: []string{"http_get", "vortex_api"}, Ask: []string{"run_command"}}).Validate(); err != nil {
		t.Fatalf("sub-agent tool names rejected: %v", err)
	}
	reg := NewToolRegistry()
	sandbox := t.TempDir()
	if err := RegisterBuiltins(reg, sandbox, []string{"go"}, nil, "http://127.0.0.1:1", "test"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ApplyPolicy(ToolPolicy{Deny: []string{"http_get"}, Ask: []string{"run_command"}}); err != nil {
		t.Fatal(err)
	}
	sb := NewSandboxedRegistry(reg, sandbox, []string{"go"}, nil)
	ctx := context.Background()
	if _, err := sb.Execute(ctx, "http_get", map[string]any{"url": "https://example.com"}); !errors.Is(err, ErrToolDenied) {
		t.Errorf("denied http_get: err = %v", err)
	}
	if _, err := sb.ExecuteApproved(ctx, "http_get", map[string]any{"url": "https://example.com"}); !errors.Is(err, ErrToolDenied) {
		t.Errorf("approved denied http_get: err = %v", err)
	}
	args := map[string]any{"command": "go", "args": []string{"version"}}
	var ae *ApprovalError
	if _, err := sb.Execute(ctx, "run_command", args); !errors.As(err, &ae) {
		t.Fatalf("ask run_command: err = %v", err)
	}
	res, err := sb.ExecuteApproved(ctx, "run_command", args)
	if err != nil {
		t.Fatalf("approved run_command: %v", err)
	}
	if out, _ := res.(map[string]any); out["exit_code"] != 0 || !strings.Contains(out["stdout"].(string), "go version") {
		t.Errorf("approved run_command result = %v", res)
	}
}

// TestPolicy_CoversTeamSpecialists: the team-mode registry, whose file tools
// skip the approval prompt, still honours agents.policy.
func TestPolicy_CoversTeamSpecialists(t *testing.T) {
	dir := t.TempDir()
	reg, err := NewTrustedToolRegistry(LocalFSConfig{Root: dir, Policy: ToolPolicy{Deny: []string{"write_file"}, Ask: []string{"edit_file"}}})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := reg.Get("write_file")
	if _, err := w.Execute(context.Background(), map[string]any{"path": "a.txt", "content": "x"}); !errors.Is(err, ErrToolDenied) {
		t.Errorf("denied write_file in team mode: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Error("file written despite the deny policy")
	}
	e, _ := reg.Get("edit_file")
	var ae *ApprovalError
	if _, err := e.Execute(context.Background(), map[string]any{"path": "a.txt", "old_str": "x", "new_str": "y"}); !errors.As(err, &ae) {
		t.Errorf("ask edit_file in team mode: err = %v", err)
	}
	if _, err := NewTrustedToolRegistry(LocalFSConfig{Root: dir, Policy: ToolPolicy{Deny: []string{"nope"}}}); err == nil {
		t.Error("unknown policy name accepted")
	}
}
