package agents

import "testing"

// TestApprovalAction_CommandFromParams: a local terminal tool's approval
// shows the command it will run, taken from its parameters.
func TestApprovalAction_CommandFromParams(t *testing.T) {
	a := approvalAction("run_terminal", map[string]any{"command": "go test ./..."}, ApprovalRequest{Description: "run: go test ./..."})
	if a.Command != "go test ./..." {
		t.Errorf("Command = %q", a.Command)
	}
	b := approvalAction("", nil, ApprovalRequest{Command: "git", Args: []string{"push"}})
	if b.Command != "git push" || b.Tool != "run_command" {
		t.Errorf("command request = %+v", b)
	}
	c := approvalAction("write_file", map[string]any{"path": "a"}, ApprovalRequest{})
	if c.Command != "" {
		t.Errorf("write_file got a command: %q", c.Command)
	}
}
