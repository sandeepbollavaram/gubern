package agents

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/llm"
	"github.com/sandeepbollavaram/vortex/internal/sandbox"
)

// recordingSandbox records the network each command was prepared with (the
// command then runs normally).
type recordingSandbox struct {
	mu   sync.Mutex
	nets []string
}

func (r *recordingSandbox) Info() sandbox.Info {
	return sandbox.Info{Adapter: "recording", Level: sandbox.LevelIsolated, Network: "off"}
}

func (r *recordingSandbox) Prepare(_ *exec.Cmd, p sandbox.Policy) (sandbox.Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nets = append(r.nets, string(p.Network))
	return noopProcess{}, nil
}

func (r *recordingSandbox) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.nets...)
}

type noopProcess struct{}

func (noopProcess) Started(*exec.Cmd) error { return nil }
func (noopProcess) Kill()                   {}
func (noopProcess) Close()                  {}

func TestRunNetwork_Rules(t *testing.T) {
	for _, tc := range []struct {
		name             string
		def              string
		enforced         bool
		request, resumed string
		approve          bool
		want             string
		asked            bool
		says             string
	}{
		{name: "default on, nothing asked", def: "on", enforced: true, want: "on"},
		{name: "default off, nothing asked", def: "off", enforced: true, want: "off"},
		{name: "narrowing needs no approval", def: "on", enforced: true, request: "off", want: "off", says: "Network off"},
		{name: "widening approved", def: "off", enforced: true, request: "on", approve: true, want: "on", asked: true, says: "approved"},
		{name: "widening rejected stays off", def: "off", enforced: true, request: "on", want: "off", asked: true, says: "stays off"},
		{name: "not enforced: no approval theatre", def: "off", enforced: false, request: "on", want: "on", says: "can't enforce"},
		{name: "not enforced: off is said to be unenforced", def: "on", enforced: false, request: "off", want: "off", says: "can't enforce"},
		{name: "a resume keeps its grant without asking", def: "off", enforced: true, request: "off", resumed: "on", want: "on"},
		{name: "a junk request means the default", def: "off", enforced: true, request: "maybe", want: "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := agentCoord(t, toolGateway{&scriptedModel{}}, nil)
			c.SetNetworkPolicy(tc.def, tc.enforced)
			asked := false
			approver := func(_ context.Context, req ApprovalRequest) bool {
				asked = true
				if req.Tool != "network_access" || req.Risk() != RiskHigh {
					t.Errorf("approval = %+v (risk %s)", req, req.Risk())
				}
				return tc.approve
			}
			var lines []string
			ctx := context.Background()
			if tc.request != "" {
				ctx = agentevents.WithNetworkRequest(ctx, tc.request)
			}
			got := c.runNetwork(ctx, "s", tc.resumed, approver, func(l string) { lines = append(lines, l) })
			if got != tc.want || asked != tc.asked {
				t.Errorf("got %q asked=%v; want %q asked=%v", got, asked, tc.want, tc.asked)
			}
			if tc.says != "" && !strings.Contains(strings.Join(lines, "\n"), tc.says) {
				t.Errorf("the run wasn't told (%q): %v", tc.says, lines)
			}
		})
	}
}

// TestAgentRun_NetworkReachesTheSandbox: the network an /agent run is
// granted is what its commands get. A request to widen it that isn't
// approved leaves commands without network; the request alone (which the
// API puts in the context) never reaches a command.
func TestAgentRun_NetworkReachesTheSandbox(t *testing.T) {
	for _, tc := range []struct {
		name, def, request string
		approveNetwork     bool
		want               string
	}{
		{"rejected", "off", "on", false, "off"},
		{"approved", "off", "on", true, "on"},
		{"narrowed", "on", "off", false, "off"},
		{"default", "off", "", false, "off"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &scriptedModel{replies: []llm.ToolChatResult{
				call("t", "run_terminal", map[string]any{"command": "echo hi"}),
				{Text: "ran it"},
			}}
			var asked []string
			c, _ := agentCoord(t, toolGateway{model}, func(_ context.Context, req ApprovalRequest) bool {
				asked = append(asked, req.Tool)
				if req.Tool == "network_access" {
					return tc.approveNetwork
				}
				return true
			})
			rec := &recordingSandbox{}
			dir := t.TempDir() // a workspace other than the server's own folder
			c.SetWorkspaceTools(func(root string) (*ToolRegistry, error) {
				reg := NewToolRegistry()
				return reg, RegisterLocalTools(reg, LocalFSConfig{Root: root, Sandbox: rec,
					SandboxConfig: sandbox.Config{Network: sandbox.Network(tc.def)}})
			})
			c.SetNetworkPolicy(tc.def, true)
			store := &memCheckpoints{all: map[string][]RunCheckpoint{}, last: map[string]RunCheckpoint{}}
			c.SetRunCheckpoints(store)
			ctx := agentevents.WithRunID(agentevents.WithWorkspace(context.Background(), dir), "r-net-"+tc.name)
			if tc.request != "" {
				ctx = agentevents.WithNetworkRequest(ctx, tc.request)
			}
			if _, err := c.HandleMessage(ctx, "/agent run it", "net-"+tc.name); err != nil {
				t.Fatal(err)
			}
			if got := rec.got(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("commands got network %v, want [%s] (approvals asked: %v)", got, tc.want, asked)
			}
			if cp := store.last["r-net-"+tc.name]; cp.Network != tc.want {
				t.Errorf("checkpoint network = %q, want %q", cp.Network, tc.want)
			}
		})
	}
}
