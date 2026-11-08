package agents

import "context"

// External tools (Phase 5, MCP connectors). They come from outside Gubern,
// so they gate themselves: Execute returns an *ApprovalError until a human
// approves (unless the operator set the tool to "allow"), and the approval
// flow then calls ExecuteApproved with the same parameters. The approval
// service's action hash covers those parameters, so what runs is what was
// approved.

// ApprovalGated is a tool that asks for approval itself.
type ApprovalGated interface {
	Tool
	ExecuteApproved(ctx context.Context, params map[string]any) (any, error)
}

// ApprovalAlwaysRequired reports whether this build ignores approval
// waivers (release builds do; audit I6). External tools honour it too.
func ApprovalAlwaysRequired() bool { return approvalAlwaysRequired }

// SafeEnv is the scrubbed environment agent-run processes get: enough to
// run, and none of the server's secrets.
func SafeEnv() []string { return scrubbedEnv() }

// SetExternalTools gives /agent and /team runs the tools tools() returns at
// the start of each run (MCP connectors). They are never offered to
// read-only runs.
func (c *Coordinator) SetExternalTools(tools func() []Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.externalTools = tools
}

// withExternalTools returns base plus the external tools, as a new registry
// for one run; base is returned unchanged when there are none.
func (c *Coordinator) withExternalTools(base *ToolRegistry) *ToolRegistry {
	c.mu.Lock()
	fn := c.externalTools
	c.mu.Unlock()
	if fn == nil {
		return base
	}
	extra := fn()
	if len(extra) == 0 {
		return base
	}
	reg := NewToolRegistry()
	if base != nil {
		for _, name := range base.List() {
			if t, err := base.Get(name); err == nil {
				_ = reg.Register(t)
			}
		}
	}
	for _, t := range extra {
		// A name already taken (a first-party tool) is never replaced.
		if _, err := reg.Get(t.Name()); err == nil {
			continue
		}
		_ = reg.Register(t)
	}
	return reg
}
