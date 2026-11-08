package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

// Team mode on the one loop (Phase 3, L.4; decision D1). /team runs three
// role profiles in turn — coder, tester, reviewer — each an ordinary RunLoop
// with the same tools, policy, approvals, audit and events as /agent. It
// replaces the separate specialist pipeline, whose tester couldn't run a
// command (it had no approval handler, backlog T32).

// RoleProfile is one team role: what it is told and what it may use.
type RoleProfile struct {
	Name   string
	Prompt string
	// AllowTools, when set, is the only tools the role is offered.
	AllowTools []string
	ReadOnly   bool
}

// Team role profiles.
var (
	CoderProfile = RoleProfile{
		Name: "coder",
		Prompt: "Your role: the coder. Implement the goal in the workspace with small, correct changes. " +
			"Read before you edit. Don't claim tests pass unless you ran them. End with a short summary of " +
			"what you changed and which files.",
	}
	TesterProfile = RoleProfile{
		Name: "tester",
		Prompt: "Your role: the tester. Don't edit files. Check the coder's change by running the project's " +
			"tests or the commands that exercise it, and report what passed and what failed, quoting the " +
			"command and its output as evidence. End with a line RESULT: PASS or RESULT: FAIL.",
		AllowTools: []string{"read_file", "list_directory", "find_files", "search_files", "git_status", "git_diff", "run_terminal"},
	}
	ReviewerProfile = RoleProfile{
		Name: "reviewer",
		Prompt: "Your role: the reviewer. Don't edit files or run commands. Review the change (use git_diff " +
			"and read the files) for correctness, security and clarity. List concrete issues with file and " +
			"line. End with a line VERDICT: APPROVE or VERDICT: CHANGES REQUESTED.",
		ReadOnly: true,
	}
)

// handleTeamLoop runs coder → tester → reviewer on goal. A --check given in
// goal is the tester's completion check: the team ends completed only when
// it passes.
func (c *Coordinator) handleTeamLoop(ctx context.Context, sessionID, goal string, sf *streamFilter) (string, error) {
	userLine := "/team " + goal
	flags, goal, err := parseAgentFlags(goal)
	if err != nil || goal == "" {
		return "Usage: /team [--check \"<command>\"] <goal>", nil
	}
	if flags.readOnly {
		return "/team changes files; use /agent --read-only to only look.", nil
	}
	localTools, workDir, err := c.localToolsFor(ctx)
	if err != nil {
		return "Can't open that workspace: " + err.Error(), nil
	}
	var transcript []string
	emit := func(line string) {
		transcript = append(transcript, line)
		if sf != nil {
			sf.Write(line + "\n")
		}
	}
	prompted := PromptedTools{Model: c.cfg.AIGateway}
	var model ToolChatter = prompted
	if native, ok := c.cfg.AIGateway.(ToolChatter); ok {
		model = &FallbackTools{Native: native, Prompted: prompted}
	}
	runID := newRunID()
	if logID := agentevents.RunIDFrom(ctx); logID != "" {
		runID = logID
	}
	check := flags.check
	c.auditRunStarted(ctx, sessionID, runID, "team: "+goal, check, false)
	catalog, skillFiles := c.skillCatalog(workDir)
	approver := c.loopApprover(sessionID, emit)
	// One network for the whole team run, decided before any command (M.4).
	ctx = agentevents.WithNetwork(ctx, c.runNetwork(ctx, sessionID, "", approver, emit))
	afterStep := c.workspaceCheckpoints(ctx, sessionID, runID, workDir, localTools, emit)

	run := func(p RoleProfile, task string, verify func(context.Context) (bool, string)) LoopResult {
		emit(fmt.Sprintf("── %s ──", strings.ToUpper(p.Name[:1])+p.Name[1:]))
		agentevents.Emit(ctx, agentevents.Event{Type: agentevents.Status, SessionID: sessionID, Label: "team:" + p.Name, Status: "started"})
		res := RunLoop(ctx, LoopConfig{
			Model: model, Tools: localTools, Approval: approver, Audit: c.cfg.Audit,
			System:     DefaultLoopSystemPrompt(workDir) + "\n\n" + p.Prompt,
			Reference:  catalog,
			SkillFiles: skillFiles,
			Verify:     verify,
			ReadOnly:   p.ReadOnly,
			AllowTools: p.AllowTools,
			Session:    sessionID,
			Timeout:    AgentRunTimeout - 5*time.Second,
			MaxTokens:  250_000,
			Emit:       emit,
			AfterStep:  afterStep,
		}, task)
		if res.Stop == LoopDone {
			emit(res.Answer)
		} else {
			emit(fmt.Sprintf("Stopped (%s): %s", res.Stop, errString(res.Err)))
		}
		return res
	}

	finish := func(status, why string) (string, error) {
		if status != "" { // a cancelled run reports nothing; the log records it
			agentevents.Emit(ctx, agentevents.Event{Type: agentevents.RunOutcome, SessionID: sessionID, Status: status, Text: why})
		}
		reply := strings.Join(transcript, "\n")
		c.recordExchange(sessionID, userLine, reply)
		return reply, nil
	}

	coder := run(CoderProfile, goal, nil)
	c.auditRun(ctx, sessionID, runID, "team/coder: "+goal, "", coder)
	if coder.Stop != LoopDone {
		if coder.Stop == LoopCancelled {
			return finish("", "")
		}
		return finish("partial", "the coder didn't finish")
	}

	var verify func(context.Context) (bool, string)
	if check != "" {
		inner := c.loopCheck(localTools, sessionID, check, approver, emit)
		attempt := 0
		verify = func(ctx context.Context) (bool, string) {
			attempt++
			agentevents.Emit(ctx, agentevents.Event{Type: agentevents.RunStatus, SessionID: sessionID, Status: "verifying"})
			ok, out := inner(ctx)
			st := "failed"
			if ok {
				st = "passed"
			}
			agentevents.Emit(ctx, agentevents.Event{Type: agentevents.CheckResult, SessionID: sessionID, Label: check, Status: st,
				Output: excerptOutput(out), Text: fmt.Sprintf("attempt %d", attempt)})
			return ok, out
		}
	}
	tester := run(TesterProfile, "Goal: "+goal+"\n\nThe coder reported:\n"+coder.Answer+
		"\n\nTest the change now.", verify)
	c.auditRun(ctx, sessionID, runID, "team/tester: "+goal, check, tester)
	if tester.Stop == LoopCancelled {
		return finish("", "")
	}

	reviewer := run(ReviewerProfile, "Goal: "+goal+"\n\nThe coder reported:\n"+coder.Answer+
		"\n\nThe tester reported:\n"+tester.Answer+"\n\nReview the change.", nil)
	c.auditRun(ctx, sessionID, runID, "team/reviewer: "+goal, "", reviewer)
	if reviewer.Stop == LoopCancelled {
		return finish("", "")
	}

	changes := strings.Contains(strings.ToUpper(reviewer.Answer), "VERDICT: CHANGES REQUESTED")
	switch {
	case check != "" && tester.Checked && tester.Verified && !changes:
		emit("✓ Verified: " + check + " passed, and the reviewer approved")
		return finish("completed", "the check passed and the reviewer approved")
	case check != "" && tester.Checked && tester.Verified:
		emit("✓ Verified: " + check + " passed; the reviewer requested changes")
		return finish("partial", "the check passed but the reviewer requested changes")
	case check != "":
		emit("✗ Not verified: " + check + " still fails")
		return finish("partial", "the completion check didn't pass")
	case changes:
		return finish("partial", "the reviewer requested changes")
	default:
		return finish("done", "no completion check was declared, so the result is unverified")
	}
}
