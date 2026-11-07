package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

// TestAgentCommand_ReportsCheckAndOutcomeEvents: a /agent run with a check
// reports verifying, each check attempt, and its outcome, which the event
// log turns into the run's final status (Phase 3).
func TestAgentCommand_ReportsCheckAndOutcomeEvents(t *testing.T) {
	for _, tc := range []struct {
		check, outcome, lastCheck string
		attempts                  int
	}{
		{"exit 0", "completed", "passed", 1},
		{"exit 1", "partial", "failed", 3},
	} {
		c, _ := agentCoord(t, toolGateway{&scriptedModel{}}, func(context.Context, ApprovalRequest) bool { return true })
		var evs []agentevents.Event
		ctx := agentevents.WithSink(context.Background(), func(e agentevents.Event) { evs = append(evs, e) })
		if _, err := c.HandleMessage(ctx, `/agent --check "`+tc.check+`" make it pass`, "out-"+tc.outcome); err != nil {
			t.Fatal(err)
		}
		var verifying, checks int
		var lastCheck, outcome string
		for _, e := range evs {
			switch e.Type {
			case agentevents.RunStatus:
				if e.Status == "verifying" {
					verifying++
				}
			case agentevents.CheckResult:
				checks++
				lastCheck = e.Status
				if e.Label != tc.check {
					t.Errorf("check event label = %q", e.Label)
				}
			case agentevents.RunOutcome:
				outcome = e.Status
			}
		}
		if verifying != tc.attempts || checks != tc.attempts || lastCheck != tc.lastCheck || outcome != tc.outcome {
			t.Errorf("%s: verifying=%d checks=%d last=%s outcome=%s", tc.check, verifying, checks, lastCheck, outcome)
		}
	}
}

func TestRunOutcome(t *testing.T) {
	for _, tc := range []struct {
		res  LoopResult
		want string
	}{
		{LoopResult{Stop: LoopDone, Checked: true, Verified: true}, "completed"},
		{LoopResult{Stop: LoopDone, Checked: true, Checks: 3}, "partial"},
		{LoopResult{Stop: LoopDone}, "done"},
		{LoopResult{Stop: LoopBudgetExceeded}, "partial"},
		{LoopResult{Stop: LoopCancelled}, ""},
		{LoopResult{Stop: LoopModelError, Err: errors.New("boom")}, "failed"},
	} {
		if got, why := runOutcome(tc.res); got != tc.want || (got != "" && why == "") {
			t.Errorf("%+v → %q (%q), want %q", tc.res, got, why, tc.want)
		}
	}
}
