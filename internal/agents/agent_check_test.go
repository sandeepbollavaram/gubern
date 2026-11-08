package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/llm"
)

func TestParseAgentFlags(t *testing.T) {
	cases := []struct {
		in, check, rest string
		bad, readOnly   bool
	}{
		{"fix the test", "", "fix the test", false, false},
		{`--check "go test ./..." fix the test`, "go test ./...", "fix the test", false, false},
		{"--check=make fix it", "make", "fix it", false, false},
		{"--check make fix it", "make", "fix it", false, false},
		{`--check "unclosed fix`, "", "", true, false},
		{`--check "" fix`, "", "", true, false},
		{"--read-only explain main.go", "", "explain main.go", false, true},
		{`--read-only --check "go vet ./..." look`, "go vet ./...", "look", false, true},
		{`--check make --read-only look`, "make", "look", false, true},
		{`--check "go vet ./..." --read-only look`, "go vet ./...", "look", false, true}, // as the API's declared check writes it
		{"--read-only", "", "", false, true},
		{"--read-onlyx look", "", "--read-onlyx look", false, false},
		{"--check a --check b x", "", "", true, false},
	}
	for _, tc := range cases {
		f, rest, err := parseAgentFlags(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("%q: want error", tc.in)
			}
			continue
		}
		if err != nil || f.check != tc.check || f.readOnly != tc.readOnly || rest != tc.rest {
			t.Errorf("%q → %+v rest=%q err=%v", tc.in, f, rest, err)
		}
	}
}

// TestLoop_VerifyFailureContinuesTheRun: a failed check sends its output
// back and the run continues until the check passes.
func TestLoop_VerifyFailureContinuesTheRun(t *testing.T) {
	m := &scriptedModel{replies: []llm.ToolChatResult{{Text: "done"}, {Text: "really done"}}}
	calls := 0
	res := RunLoop(context.Background(), LoopConfig{Model: m, Verify: func(context.Context) (bool, string) {
		calls++
		return calls == 2, "FAIL: TestX"
	}}, "fix it")
	if !res.Checked || !res.Verified || res.Checks != 2 || res.Answer != "really done" {
		t.Fatalf("res = %+v", res)
	}
	last := m.seen[1][len(m.seen[1])-1]
	if last.Role != "user" || !strings.Contains(last.Content, "FAIL: TestX") {
		t.Errorf("model wasn't shown the failing check: %+v", last)
	}
}

func TestLoop_VerifyGivesUpAfterMaxAttempts(t *testing.T) {
	m := &scriptedModel{} // always answers "done"
	res := RunLoop(context.Background(), LoopConfig{Model: m, MaxVerify: 2,
		Verify: func(context.Context) (bool, string) { return false, "still failing" }}, "fix it")
	if res.Stop != LoopDone || res.Verified || res.Checks != 2 {
		t.Errorf("res = %+v", res)
	}
}

// TestAgentCommand_CheckRunsThroughApproval: the check is a real command,
// approved like any other, and the run reports verified or not.
func TestAgentCommand_CheckRunsThroughApproval(t *testing.T) {
	for _, tc := range []struct {
		check, want string
	}{
		{"exit 0", "✓ Verified: exit 0 passed"},
		{"exit 1", "✗ Not verified: exit 1 still fails after 3 attempt(s)"},
	} {
		var asked []string
		approve := func(_ context.Context, req ApprovalRequest) bool {
			asked = append(asked, req.Tool)
			return true
		}
		c, _ := agentCoord(t, toolGateway{&scriptedModel{}}, approve)
		reply, err := c.HandleMessage(context.Background(), `/agent --check "`+tc.check+`" make it pass`, "chk")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(reply, tc.want) {
			t.Errorf("%s: reply = %q", tc.check, reply)
		}
		if len(asked) == 0 || asked[0] != "run_terminal" {
			t.Errorf("%s: check didn't go through approval: %v", tc.check, asked)
		}
	}
}

func TestAgentCommand_CheckRejectedIsNotVerified(t *testing.T) {
	c, _ := agentCoord(t, toolGateway{&scriptedModel{}}, func(context.Context, ApprovalRequest) bool { return false })
	reply, _ := c.HandleMessage(context.Background(), `/agent --check "exit 0" do it`, "chk2")
	if !strings.Contains(reply, "Not verified") {
		t.Errorf("a check the user refused must not count as passed: %q", reply)
	}
}

// TestAgentCommand_RunFinishedIsAudited: every /agent run ends with one
// agent.run.finished entry describing the outcome.
func TestAgentCommand_RunFinishedIsAudited(t *testing.T) {
	c, _ := agentCoord(t, toolGateway{writeScript()}, func(context.Context, ApprovalRequest) bool { return true })
	rec := &approvalAuditRecorder{}
	c.cfg.Audit = rec
	if _, err := c.HandleMessage(context.Background(), `/agent --check "exit 0" create hello.txt`, "ra1"); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var d map[string]any
	for i, e := range rec.entries {
		if strings.HasPrefix(e, "agent.run.finished|") {
			if d != nil {
				t.Fatal("more than one run.finished entry")
			}
			d = rec.details[i]
		}
	}
	if d == nil {
		t.Fatalf("no agent.run.finished entry: %v", rec.entries)
	}
	if d["stop"] != "done" || d["verified"] != true || d["tool_calls"] != 1 || d["task"] != "create hello.txt" {
		t.Errorf("detail = %v", d)
	}
}

// TestAgentCommand_RunStartedPairsWithFinished: a run's started and finished
// entries share one run id, so an interrupted run (started, never finished)
// can be found in the audit log.
func TestAgentCommand_RunStartedPairsWithFinished(t *testing.T) {
	c, _ := agentCoord(t, toolGateway{writeScript()}, func(context.Context, ApprovalRequest) bool { return true })
	rec := &approvalAuditRecorder{}
	c.cfg.Audit = rec
	if _, err := c.HandleMessage(context.Background(), `/agent --read-only look around`, "rs1"); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var started, finished map[string]any
	for i, e := range rec.entries {
		switch {
		case strings.HasPrefix(e, "agent.run.started|"):
			started = rec.details[i]
		case strings.HasPrefix(e, "agent.run.finished|"):
			finished = rec.details[i]
		}
	}
	if started == nil || finished == nil {
		t.Fatalf("entries = %v", rec.entries)
	}
	id, _ := started["run"].(string)
	if id == "" || finished["run"] != id {
		t.Errorf("run ids: started %v, finished %v", started["run"], finished["run"])
	}
	if started["read_only"] != true || started["task"] != "look around" || started["session"] != "rs1" {
		t.Errorf("started = %v", started)
	}
}

// TestAgentCommand_RunIsInHistory: an /agent run is recorded in the
// session's conversation history (it used to be missing from /history).
func TestAgentCommand_RunIsInHistory(t *testing.T) {
	c, _ := agentCoord(t, toolGateway{writeScript()}, func(context.Context, ApprovalRequest) bool { return true })
	c.cfg.MemoryStore = t.TempDir()
	if _, err := c.HandleMessage(context.Background(), `/agent --check "exit 0" create hello.txt`, "hist1"); err != nil {
		t.Fatal(err)
	}
	msgs := c.SessionHistory("hist1")
	if len(msgs) != 2 {
		t.Fatalf("history = %+v", msgs)
	}
	if msgs[0].Content != `/agent --check "exit 0" create hello.txt` || !strings.Contains(msgs[1].Content, "Created hello.txt.") {
		t.Errorf("history = %+v", msgs)
	}
}
