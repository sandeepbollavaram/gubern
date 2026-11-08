package agents

import (
	"context"
	"path/filepath"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

// Per-run workspaces. An /agent run works in the server's default folder
// unless the API picked another of its configured workspaces for it; then
// relative paths, the skill catalog, the system prompt and the completion
// check all use that folder (and, when the operator confines the agent to a
// trusted root, the run is confined to that folder instead).

// WorkspaceToolsFunc builds the local tools rooted at a workspace folder.
type WorkspaceToolsFunc func(root string) (*ToolRegistry, error)

// SetWorkspaceTools enables per-run workspaces.
func (c *Coordinator) SetWorkspaceTools(fn WorkspaceToolsFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.workspaceTools = fn
	c.workspaceRegs = map[string]*ToolRegistry{}
}

// localToolsFor returns the local tools and working folder for ctx's run:
// the requested workspace's, or the defaults.
func (c *Coordinator) localToolsFor(ctx context.Context) (*ToolRegistry, string, error) {
	dir := agentevents.WorkspaceFrom(ctx)
	if dir == "" || filepath.Clean(dir) == filepath.Clean(c.WorkingDir()) {
		return c.cfg.LocalTools, c.WorkingDir(), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workspaceTools == nil {
		return c.cfg.LocalTools, c.WorkingDir(), nil
	}
	if reg, ok := c.workspaceRegs[dir]; ok {
		return reg, dir, nil
	}
	reg, err := c.workspaceTools(dir)
	if err != nil {
		return nil, "", err
	}
	c.workspaceRegs[dir] = reg
	return reg, dir, nil
}
