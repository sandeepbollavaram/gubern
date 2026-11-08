package agents

import (
	"context"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

// Network policy per run (Phase 4, M.4). The server's sandbox has a default
// network (agents.local_tools.sandbox.network). A run may ask for "off"
// (always allowed: it only narrows) or "on". Widening the default from off to
// on is an approval. Where the sandbox can't enforce network policy
// (Windows, or no sandbox) nothing is asked and the run is told plainly.

// SetNetworkPolicy records the sandbox's default network ("on" or "off") and
// whether this server can enforce it.
func (c *Coordinator) SetNetworkPolicy(def string, enforced bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.netDefault, c.netEnforced = def, enforced
}

// runNetwork settles a run's network: resumed is the network an interrupted
// run was granted ("" for a new run).
func (c *Coordinator) runNetwork(ctx context.Context, _, resumed string, approver ApprovalFunc, emit func(string)) string {
	c.mu.Lock()
	def, enforced := c.netDefault, c.netEnforced
	c.mu.Unlock()
	if def == "" {
		def = "on"
	}
	if resumed != "" {
		return resumed // granted when the run started
	}
	want := agentevents.NetworkRequestFrom(ctx)
	if want != "on" && want != "off" {
		want = def
	}
	switch {
	case !enforced:
		if want != def || want == "off" {
			emit("ℹ Network " + want + " was asked for, but this server's sandbox can't enforce network policy; commands reach the network as the server does.")
		}
		return want
	case want == def:
		return def
	case want == "off":
		emit("ℹ Network off for this run: commands get loopback only.")
		return "off"
	}
	ok := approver != nil && approver(ctx, ApprovalRequest{
		Tool:        "network_access",
		Description: "let this run's commands reach the network",
		Preview:     "The server's default is no network for agent commands. Approve to let this run's commands (after their own approvals) reach the internet and the local network.",
		Params:      map[string]any{"network": "on"},
	})
	if ok {
		emit("ℹ Network on for this run (approved).")
		return "on"
	}
	emit("ℹ Network stays off for this run: it wasn't approved.")
	return "off"
}
