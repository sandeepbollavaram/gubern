package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agents"
	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/approvals"
	"github.com/sandeepbollavaram/vortex/internal/audit"
	"github.com/sandeepbollavaram/vortex/internal/config"
	"github.com/sandeepbollavaram/vortex/internal/connectors"
	"github.com/sandeepbollavaram/vortex/internal/secrets"
	"github.com/sandeepbollavaram/vortex/internal/userdir"
	"github.com/sandeepbollavaram/vortex/pkg/lifecycle"
)

// connectorsBackend serves /api/mcp/connectors (Phase 5, Milestone N).
type connectorsBackend struct {
	store     *connectors.Store
	mgr       *connectors.Manager
	secrets   secrets.Adapter    // nil: credentials can't be set
	approvals *approvals.Service // nil: connectors can't be enabled
	audit     *audit.Log
	sandbox   string // the adapter local servers run in, for the approval text
	log       *slog.Logger
}

func (b *connectorsBackend) record(actor, action, id string, detail map[string]any) {
	if b.audit == nil {
		return
	}
	if err := b.audit.Append(context.Background(), actor, action, id, detail); err != nil {
		b.log.Warn("audit append failed", "action", action, "error", err)
	}
}

// secretRefs are the credential names a connector refers to.
func secretRefs(c connectors.Connector) []string {
	var out []string
	for _, ref := range c.SecretEnv {
		out = append(out, ref)
	}
	if c.BearerSecret != "" {
		out = append(out, c.BearerSecret)
	}
	return out
}

func (b *connectorsBackend) view(ctx context.Context, c connectors.Connector) api.ConnectorView {
	v := api.ConnectorView{Connector: c, Status: b.mgr.Status(c.ID), SecretsSet: map[string]bool{}}
	for _, ref := range secretRefs(c) {
		set := false
		if b.secrets != nil {
			_, err := b.secrets.Get(ctx, connectors.SecretName(c.ID, ref))
			set = err == nil
		}
		v.SecretsSet[ref] = set
	}
	return v
}

func (b *connectorsBackend) List(ctx context.Context) ([]api.ConnectorView, error) {
	list, err := b.store.List()
	if err != nil {
		return nil, err
	}
	out := make([]api.ConnectorView, 0, len(list))
	for _, c := range list {
		out = append(out, b.view(ctx, c))
	}
	return out, nil
}

func (b *connectorsBackend) Get(ctx context.Context, id string) (api.ConnectorView, error) {
	c, err := b.store.Get(id)
	if err != nil {
		return api.ConnectorView{}, err
	}
	return b.view(ctx, c), nil
}

func (b *connectorsBackend) Add(ctx context.Context, c connectors.Connector) (api.ConnectorView, error) {
	c, err := b.store.Add(c)
	if err != nil {
		return api.ConnectorView{}, err
	}
	return b.view(ctx, c), nil
}

// transportFields are what a person approves when enabling a connector.
func transportFields(c connectors.Connector) []any {
	return []any{c.Transport, c.Command, c.Args, c.Env, c.ReadPaths, c.Network, c.SecretEnv, c.URL, c.Local, c.BearerSecret}
}

func (b *connectorsBackend) Update(ctx context.Context, id string, in connectors.Connector) (api.ConnectorView, error) {
	var disabled bool
	c, err := b.store.Update(id, func(c *connectors.Connector) (bool, error) {
		before := transportFields(*c)
		c.Name, c.Description, c.Trust = in.Name, in.Description, in.Trust
		c.Transport, c.Command, c.Args, c.Env, c.ReadPaths, c.Network = in.Transport, in.Command, in.Args, in.Env, in.ReadPaths, in.Network
		c.SecretEnv, c.URL, c.Local, c.BearerSecret = in.SecretEnv, in.URL, in.Local, in.BearerSecret
		changed := !reflect.DeepEqual(before, transportFields(*c))
		if changed && c.Enabled {
			// What runs changed: it needs a new approval.
			c.Enabled, disabled = false, true
		}
		return changed, nil
	})
	if err != nil {
		return api.ConnectorView{}, err
	}
	if disabled {
		b.mgr.Disconnect(id)
	}
	return b.view(ctx, c), nil
}

func (b *connectorsBackend) Remove(ctx context.Context, id string) error {
	c, err := b.store.Get(id)
	if err != nil {
		return err
	}
	b.mgr.Disconnect(id)
	if err := b.store.Remove(id); err != nil {
		return err
	}
	if b.secrets != nil {
		for _, ref := range secretRefs(c) {
			_ = b.secrets.Delete(ctx, connectors.SecretName(id, ref))
		}
	}
	return nil
}

func (b *connectorsBackend) PrepareEnable(ctx context.Context, id, sessionID, requestedBy string) (string, error) {
	if b.approvals == nil {
		return "", fmt.Errorf("%w: enabling needs the approval service", connectors.ErrInput)
	}
	c, err := b.store.Get(id)
	if err != nil {
		return "", err
	}
	if c.Enabled {
		return "", fmt.Errorf("%w: %s is already enabled", connectors.ErrInput, id)
	}
	what, preview := "", ""
	switch c.Transport {
	case "stdio":
		what = "start " + c.Command + " " + strings.Join(c.Args, " ")
		preview = fmt.Sprintf("Program: %s\nArguments: %q\nNetwork: %s\nSandbox: %s\nEnvironment: %d variables, %d credentials from the secret store",
			c.Command, c.Args, c.Network, b.sandbox, len(c.Env), len(c.SecretEnv))
	case "http":
		what = "connect to " + c.URL
		preview = fmt.Sprintf("Endpoint: %s\nCredential: %v", c.URL, c.BearerSecret != "")
	}
	a, err := b.approvals.Create(ctx, approvals.CreateRequest{
		SessionID: sessionID,
		Risk:      "HIGH RISK",
		Action: approvals.Action{
			Tool:        "mcp_enable",
			Params:      map[string]any{"connector": c.ID, "version": c.Version, "transport": c.Transport, "command": c.Command, "args": c.Args, "url": c.URL, "network": c.Network},
			Command:     what,
			Description: fmt.Sprintf("Enable MCP connector %q: %s. Its tools become available to agents, each call still asking for approval unless you allow it.", c.ID, what),
			Preview:     preview,
		},
	})
	if err != nil {
		return "", err
	}
	go b.finishEnable(c, a, requestedBy)
	return a.ID, nil
}

// finishEnable enables and connects the connector once approved, if it is
// still the version that was approved.
func (b *connectorsBackend) finishEnable(c connectors.Connector, a approvals.Approval, requestedBy string) {
	ctx, cancel := context.WithDeadline(context.Background(), a.ExpiresAt.Add(time.Minute))
	defer cancel()
	done, err := b.approvals.Wait(ctx, a.ID)
	if err != nil || done.Status != approvals.Approved {
		b.record(done.DecidedBy, "mcp.enable_declined", c.ID, map[string]any{"approval": a.ID, "status": string(done.Status)})
		return
	}
	_, err = b.store.Update(c.ID, func(cur *connectors.Connector) (bool, error) {
		if cur.Version != c.Version {
			return false, fmt.Errorf("%w: %s changed after the approval was requested", connectors.ErrInput, c.ID)
		}
		cur.Enabled = true
		return false, nil
	})
	if err != nil {
		b.record(done.DecidedBy, "mcp.enable_stale", c.ID, map[string]any{"approval": a.ID, "error": err.Error()})
		return
	}
	b.record(done.DecidedBy, "mcp.enabled", c.ID, map[string]any{"approval": a.ID, "requested_by": requestedBy, "version": c.Version})
	if err := b.mgr.Connect(context.Background(), c.ID); err != nil {
		b.log.Warn("mcp connector enabled but didn't connect", "connector", c.ID, "err", err)
	}
}

func (b *connectorsBackend) Disable(_ context.Context, id string) error {
	if _, err := b.store.Update(id, func(c *connectors.Connector) (bool, error) { c.Enabled = false; return false, nil }); err != nil {
		return err
	}
	b.mgr.Disconnect(id)
	return nil
}

func (b *connectorsBackend) Reconnect(ctx context.Context, id string) error {
	return b.mgr.Connect(ctx, id)
}

func (b *connectorsBackend) SetToolPolicy(_ context.Context, id, tool, policy string) error {
	if policy != connectors.PolicyAsk && policy != connectors.PolicyAllow && policy != connectors.PolicyDeny {
		return fmt.Errorf("%w: policy must be ask, allow or deny", connectors.ErrInput)
	}
	if tool == "" || len(tool) > 64 {
		return fmt.Errorf("%w: bad tool name", connectors.ErrInput)
	}
	if _, err := b.store.Update(id, func(c *connectors.Connector) (bool, error) {
		if c.ToolPolicy == nil {
			c.ToolPolicy = map[string]string{}
		}
		c.ToolPolicy[tool] = policy
		return false, nil
	}); err != nil {
		return err
	}
	b.mgr.SetPolicy(id, tool, policy)
	return nil
}

func (b *connectorsBackend) SetSecret(ctx context.Context, id, name, value string) error {
	if b.secrets == nil {
		return fmt.Errorf("%w: no secret store is configured", connectors.ErrInput)
	}
	if !connectors.ValidSecretName(name) {
		return fmt.Errorf("%w: secret names are letters, digits and single underscores", connectors.ErrInput)
	}
	if _, err := b.store.Get(id); err != nil {
		return err
	}
	return b.secrets.Set(ctx, connectors.SecretName(id, name), value)
}

// wireConnectors sets up MCP connectors: the registry, the manager (local
// servers run in the same sandbox as agent commands), the API, and the
// agents' access to connector tools. Enabled connectors connect in the
// background; a failure is recorded on the connector, not fatal.
func wireConnectors(ctx context.Context, cfg *config.Config, apiSrv *api.Server, rt *agents.Runtime, svc *approvals.Service,
	auditLog *audit.Log, localFS agents.LocalFSConfig, lm *lifecycle.Manager, log *slog.Logger) {
	store := connectors.NewStore(filepath.Join(userdir.App(userdir.Config()), "mcp-connectors.json"))
	var sec secrets.Adapter
	var secForMgr connectors.Secrets
	if ac, err := buildAdapterConfig(cfg); err == nil {
		if a, err := secrets.NewAdapter(ac); err == nil {
			sec, secForMgr = a, a
		} else {
			log.Warn("mcp connectors: no secret store, credentials unavailable", "err", err)
		}
	}
	b := &connectorsBackend{store: store, secrets: sec, approvals: svc, audit: auditLog, sandbox: localFS.SandboxInfo.Label(), log: log}
	b.mgr = connectors.New(connectors.Config{
		Store: store, Secrets: secForMgr, Sandbox: localFS.Sandbox, Box: localFS.SandboxConfig,
		StateDir: userdir.App(userdir.Cache()), Version: version, Log: log,
		Audit: func(action, id string, d map[string]any) { b.record("system", action, id, d) },
	})
	apiSrv.SetConnectors(b)
	rt.Coordinator().SetExternalTools(b.mgr.AgentTools)
	go b.mgr.Start(ctx)
	lm.OnShutdown("mcp connectors", func(context.Context) error { b.mgr.Close(); return nil })
}
