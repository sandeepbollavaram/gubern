package agents

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ToolPolicy is the operator's per-tool policy for the local tools
// (agents.policy in the config): tools that may never run, and tools that
// always need a human approval, even read-only ones and even where Auto Mode
// would waive the prompt. It is applied once, to the registry, so every path
// that runs a local tool (chat commands, the /agent loop, Telegram, the API)
// gets the same decision.
type ToolPolicy struct {
	Deny []string // never run; also hidden from the model in /agent runs
	Ask  []string // always ask before running
}

// ErrToolDenied means a tool is disabled by agents.policy.deny_tools.
var ErrToolDenied = errors.New("agents: tool disabled by agents.policy.deny_tools")

// subAgentToolNames are the sub-agent tools (RegisterBuiltins) that have no
// local counterpart; write_file and read_file share their local names, so a
// policy entry for those covers both.
var subAgentToolNames = []string{"http_get", "run_command", "vortex_api", "send_message", "lsp_diagnostics"}

// KnownToolNames lists the tool names a policy may refer to: the local tools
// and the sub-agent tools.
func KnownToolNames() []string {
	seen := map[string]bool{}
	for n := range builtinSpecs {
		seen[n] = true
	}
	for _, n := range subAgentToolNames {
		seen[n] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Validate reports unknown tool names and tools listed as both denied and
// asked. A typo must fail loudly: a misspelt deny entry would otherwise
// leave the tool enabled.
func (p ToolPolicy) Validate() error {
	known := map[string]bool{}
	for _, n := range KnownToolNames() {
		known[n] = true
	}
	var problems []string
	deny := map[string]bool{}
	for _, n := range p.Deny {
		if !known[n] {
			problems = append(problems, fmt.Sprintf("deny_tools: unknown tool %q", n))
		}
		deny[n] = true
	}
	for _, n := range p.Ask {
		if !known[n] {
			problems = append(problems, fmt.Sprintf("ask_tools: unknown tool %q", n))
		}
		if deny[n] {
			problems = append(problems, fmt.Sprintf("%q is in both deny_tools and ask_tools", n))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("agents.policy: %s (known tools: %s)", strings.Join(problems, "; "), strings.Join(KnownToolNames(), ", "))
	}
	return nil
}

// ApplyPolicy wraps the registry's tools with the policy. It validates first;
// names that are valid but not registered here are ignored.
func (r *ToolRegistry) ApplyPolicy(p ToolPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	deny, ask := toSet(p.Deny), toSet(p.Ask)
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, t := range r.tools {
		if deny[name] || ask[name] {
			r.tools[name] = policyTool{inner: t, deny: deny[name], ask: ask[name]}
		}
	}
	return nil
}

func toSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// toolDenied reports whether name is registered but disabled by the policy.
func toolDenied(r *ToolRegistry, name string) bool {
	t, err := r.Get(name)
	if err != nil {
		return false
	}
	pt, ok := t.(policyTool)
	return ok && pt.deny
}

// policyTool enforces the policy around one tool. An "ask" tool answers with
// the same ApprovalError a write does, so every existing approval path
// (TUI/API pending actions, Telegram, the loop's approver) handles it; once
// approved, executeApprovedLocal unwraps it and runs the tool with its own
// gate cleared too, so the user approves once.
type policyTool struct {
	inner     Tool
	deny, ask bool
}

func (p policyTool) Name() string        { return p.inner.Name() }
func (p policyTool) Description() string { return p.inner.Description() }

// Spec keeps the wrapped tool's schema.
func (p policyTool) Spec() ToolSpec {
	s, _ := SpecFor(p.inner)
	return s
}

// SideEffecting forwards the wrapped tool's answer (effect ledger).
func (p policyTool) SideEffecting() bool {
	se, ok := p.inner.(interface{ SideEffecting() bool })
	return ok && se.SideEffecting()
}

func (p policyTool) Execute(ctx context.Context, params map[string]any) (any, error) {
	if p.deny {
		return nil, fmt.Errorf("%w: %s", ErrToolDenied, p.inner.Name())
	}
	if p.ask {
		return nil, &ApprovalError{Request: ApprovalRequest{
			Tool:        p.inner.Name(),
			Description: "agents.policy requires approval to run " + p.inner.Name() + describeParams(params),
			Params:      params,
		}}
	}
	return p.inner.Execute(ctx, params)
}

// describeParams renders the arguments a person needs to judge the request
// (not session_id, which the runtime adds), sorted and truncated.
func describeParams(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		if k != "session_id" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := fmt.Sprint(params[k])
		if len(v) > 80 {
			v = v[:80] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	return " (" + strings.Join(parts, ", ") + ")"
}
