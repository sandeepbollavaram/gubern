package connectors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sandeepbollavaram/vortex/internal/agents"
	"github.com/sandeepbollavaram/vortex/internal/sandbox"
	"github.com/sandeepbollavaram/vortex/pkg/safedial"
)

// Secrets reads credentials from Gubern's secret store.
type Secrets interface {
	Get(ctx context.Context, name string) (string, error)
}

// AuditFunc records connector events in the audit log.
type AuditFunc func(action, connectorID string, detail map[string]any)

// Config wires a Manager.
type Config struct {
	Store    *Store
	Secrets  Secrets // nil: connectors can't use credentials
	Sandbox  sandbox.Sandbox
	Box      sandbox.Config
	StateDir string // private folders for local servers live under it
	Version  string // Gubern's version, sent to servers
	Log      *slog.Logger
	Audit    AuditFunc
	// ConnectTimeout bounds connecting and discovery (default 30s);
	// CallTimeout bounds one tool call (default 60s).
	ConnectTimeout, CallTimeout time.Duration
	// RetryBase is the first wait before reconnecting a connection that
	// dropped (default 2s); it doubles up to RetryMax (default 5m).
	RetryBase, RetryMax time.Duration
}

// FullToolName is the name agents see: mcp__<connector>__<tool>. Connector
// ids have no "_", so the first "__" after the prefix ends the id, and no
// first-party tool starts with "mcp__".
func FullToolName(connectorID, tool string) string { return "mcp__" + connectorID + "__" + tool }

// Tool is one tool a connector offers, as discovered.
type Tool struct {
	Connector   string         `json:"connector"`
	Name        string         `json:"name"`      // the server's name
	FullName    string         `json:"full_name"` // mcp__<connector>__<name>
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	Schema      map[string]any `json:"schema,omitempty"`
	Policy      string         `json:"policy"`
	Available   bool           `json:"available"`
	Problem     string         `json:"problem,omitempty"`
	// Resource marks the synthetic read_resource tool (servers that offer
	// resources); it reads by URI instead of calling a server tool.
	Resource bool `json:"resource,omitempty"`
}

// resourceTool is the synthetic tool name for reading a server's resources.
const resourceTool = "read_resource"

// Status is a connector's live state.
type Status struct {
	State        string    `json:"state"` // disabled | connecting | connected | error
	Server       string    `json:"server,omitempty"`
	ServerVer    string    `json:"server_version,omitempty"`
	Protocol     string    `json:"protocol,omitempty"`
	Tools        []Tool    `json:"tools"`
	DiscoveredAt time.Time `json:"discovered_at,omitempty"`
	Stderr       string    `json:"stderr,omitempty"` // tail of a local server's stderr
	Sandbox      string    `json:"sandbox,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// Manager runs the enabled connectors.
type Manager struct {
	cfg    Config
	mu     sync.Mutex
	live   map[string]*liveConn
	closed bool // server shutting down: no reconnects
}

type liveConn struct {
	conn     Connector
	session  *mcp.ClientSession
	stop     func() // ends the local process (nil for http)
	pid      int    // the local server's process id (0 for http)
	stderr   *tailBuffer
	status   Status
	hasRes   bool // the server offers resources
	tools    map[string]Tool // by server tool name
	sandbox  string
	closedMu sync.Mutex
}

// New returns a Manager.
func New(cfg Config) *Manager {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 30 * time.Second
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 60 * time.Second
	}
	if cfg.RetryBase <= 0 {
		cfg.RetryBase = 2 * time.Second
	}
	if cfg.RetryMax <= 0 {
		cfg.RetryMax = 5 * time.Minute
	}
	return &Manager{cfg: cfg, live: map[string]*liveConn{}}
}

func (m *Manager) audit(action, id string, d map[string]any) {
	if m.cfg.Audit != nil {
		m.cfg.Audit(action, id, d)
	}
}

// Start connects every enabled connector (failures are recorded, not fatal).
func (m *Manager) Start(ctx context.Context) {
	list, err := m.cfg.Store.List()
	if err != nil {
		m.cfg.Log.Warn("mcp connectors: can't read the registry", "err", err)
		return
	}
	for _, c := range list {
		if c.Enabled {
			if err := m.Connect(ctx, c.ID); err != nil {
				m.cfg.Log.Warn("mcp connector didn't connect", "connector", c.ID, "err", err)
			}
		}
	}
}

// Connect (re)connects an enabled connector and discovers its tools.
func (m *Manager) Connect(ctx context.Context, id string) error {
	c, err := m.cfg.Store.Get(id)
	if err != nil {
		return err
	}
	if !c.Enabled {
		return fmt.Errorf("%w: %s is disabled", ErrInput, id)
	}
	m.Disconnect(id)
	lc := &liveConn{conn: c, stderr: newTailBuffer(8 << 10), status: Status{State: "connecting"}}
	m.mu.Lock()
	m.live[id] = lc
	m.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, m.cfg.ConnectTimeout)
	defer cancel()
	err = m.connect(cctx, lc)
	if err == nil {
		err = m.discover(cctx, lc)
	}
	m.mu.Lock()
	if err != nil {
		lc.status.State, lc.status.Error = "error", err.Error()
	} else {
		lc.status.State, lc.status.Error = "connected", ""
	}
	lc.status.Stderr = lc.stderr.String()
	m.mu.Unlock()
	_, _ = m.cfg.Store.Update(id, func(c *Connector) (bool, error) {
		if err != nil {
			c.LastError = clip(err.Error(), 500)
		} else {
			c.LastError, c.LastConnected = "", time.Now().UTC()
		}
		return false, nil
	})
	if err != nil {
		m.closeLive(lc)
		m.audit("mcp.connect_failed", id, map[string]any{"error": clip(err.Error(), 300)})
		return err
	}
	m.audit("mcp.connected", id, map[string]any{"server": lc.status.Server, "server_version": lc.status.ServerVer, "tools": len(lc.tools), "sandbox": lc.sandbox})
	go m.watch(id, lc)
	return nil
}

// watch waits for lc's session to end. If nobody ended it on purpose (the
// connector is still current, enabled, and the server isn't stopping), it
// records the drop and reconnects with back-off until it succeeds or the
// connector is turned off.
func (m *Manager) watch(id string, lc *liveConn) {
	lc.closedMu.Lock()
	s := lc.session
	lc.closedMu.Unlock()
	if s == nil {
		return
	}
	_ = s.Wait()
	current := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return !m.closed && m.live[id] == lc
	}
	if !current() {
		return // disconnected on purpose
	}
	m.mu.Lock()
	lc.status.State, lc.status.Error = "error", "connection lost"
	m.mu.Unlock()
	m.audit("mcp.disconnected", id, map[string]any{"reason": "connection lost"})
	m.cfg.Log.Warn("mcp connector connection lost; reconnecting", "connector", id)
	wait := m.cfg.RetryBase
	for current() {
		time.Sleep(wait)
		if !current() {
			return
		}
		if c, err := m.cfg.Store.Get(id); err != nil || !c.Enabled {
			return
		}
		if err := m.Connect(context.Background(), id); err == nil {
			return // Connect started a new watcher
		}
		m.mu.Lock()
		// Connect replaced the entry; keep retrying on behalf of the new one.
		if nl := m.live[id]; nl != nil {
			lc = nl
		}
		m.mu.Unlock()
		if wait *= 2; wait > m.cfg.RetryMax {
			wait = m.cfg.RetryMax
		}
	}
}

func (m *Manager) connect(ctx context.Context, lc *liveConn) error {
	var transport mcp.Transport
	switch lc.conn.Transport {
	case "stdio":
		t, stop, label, err := m.startLocal(lc)
		if err != nil {
			return err
		}
		transport, lc.stop, lc.sandbox = t, stop, label
	case "http":
		t, err := m.httpTransport(ctx, lc.conn)
		if err != nil {
			return err
		}
		transport, lc.sandbox = t, "remote"
	default:
		return fmt.Errorf("unknown transport %q", lc.conn.Transport)
	}
	id := lc.conn.ID
	client := mcp.NewClient(&mcp.Implementation{Name: "gubern", Version: m.cfg.Version}, &mcp.ClientOptions{
		// No roots, sampling or elicitation: a connector gets nothing from
		// Gubern but tool calls.
		Capabilities: &mcp.ClientCapabilities{},
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			go m.rediscover(id)
		},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	lc.session = session
	if ir := session.InitializeResult(); ir != nil {
		lc.status.Protocol = ir.ProtocolVersion
		lc.hasRes = ir.Capabilities != nil && ir.Capabilities.Resources != nil
		if ir.ServerInfo != nil {
			lc.status.Server, lc.status.ServerVer = clip(ir.ServerInfo.Name, 80), clip(ir.ServerInfo.Version, 40)
		}
	}
	lc.status.Sandbox = lc.sandbox
	return nil
}

// startLocal starts a stdio server through the sandbox and returns a
// transport over its pipes, and how to stop it.
func (m *Manager) startLocal(lc *liveConn) (mcp.Transport, func(), string, error) {
	c := lc.conn
	dir := filepath.Join(m.cfg.StateDir, "mcp", c.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, "", err
	}
	env := agents.SafeEnv()
	for k, v := range c.Env {
		env = append(env, k+"="+v)
	}
	for k, ref := range c.SecretEnv {
		if m.cfg.Secrets == nil {
			return nil, nil, "", fmt.Errorf("secret %s: no secret store", ref)
		}
		v, err := m.cfg.Secrets.Get(context.Background(), SecretName(c.ID, ref))
		if err != nil {
			return nil, nil, "", fmt.Errorf("secret %s isn't set", ref)
		}
		env = append(env, k+"="+v)
	}
	cmd := exec.Command(c.Command, c.Args...)
	cmd.Dir = dir
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, "", err
	}
	cmd.Stderr = lc.stderr
	label := "none"
	var proc sandbox.Process
	if m.cfg.Sandbox != nil {
		p := sandbox.Policy{Writable: dir, Dir: dir, Network: sandbox.Network(c.Network), Limits: m.cfg.Box.Limits}
		p.ReadPaths = append(append([]string{}, p.ReadPaths...), filepath.Dir(c.Command))
		p.ReadPaths = append(p.ReadPaths, c.ReadPaths...)
		p.Home = filepath.Join(dir, "home")
		if proc, err = m.cfg.Sandbox.Prepare(cmd, p); err != nil {
			return nil, nil, "", fmt.Errorf("sandbox: %w", err)
		}
		label = m.cfg.Sandbox.Info().Adapter
	}
	if err := cmd.Start(); err != nil {
		if proc != nil {
			proc.Close()
		}
		return nil, nil, "", fmt.Errorf("start %s: %w", filepath.Base(c.Command), err)
	}
	if proc != nil {
		if err := proc.Started(cmd); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			proc.Close()
			return nil, nil, "", err
		}
	}
	lc.pid = cmd.Process.Pid
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = stdin.Close()
			if proc != nil {
				proc.Kill()
			}
			_ = cmd.Process.Kill()
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
			if proc != nil {
				proc.Close()
			}
		})
	}
	return &mcp.IOTransport{Reader: stdout, Writer: stdin, MaxLineLength: 4 << 20}, stop, label, nil
}

// httpTransport builds a streamable HTTP transport whose client refuses
// redirects and checks every destination (loopback only for a local
// connector), with the bearer credential from the secret store.
func (m *Manager) httpTransport(ctx context.Context, c Connector) (mcp.Transport, error) {
	client := safedial.Client(safedial.Config{AllowLoopback: c.Local})
	client.Timeout = 0 // the event stream is long-lived; calls carry their own deadlines
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("redirects are refused for MCP connectors")
	}
	if c.BearerSecret != "" {
		if m.cfg.Secrets == nil {
			return nil, errors.New("bearer secret: no secret store")
		}
		tok, err := m.cfg.Secrets.Get(ctx, SecretName(c.ID, c.BearerSecret))
		if err != nil {
			return nil, fmt.Errorf("secret %s isn't set", c.BearerSecret)
		}
		client.Transport = bearer{base: client.Transport, token: tok}
	}
	return &mcp.StreamableClientTransport{Endpoint: c.URL, HTTPClient: client, MaxRetries: 2, MaxEventSize: 4 << 20}, nil
}

type bearer struct {
	base  http.RoundTripper
	token string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// discover lists the server's tools and validates them.
func (m *Manager) discover(ctx context.Context, lc *liveConn) error {
	tools := map[string]Tool{}
	n := 0
	for t, err := range lc.session.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("tools/list: %w", err)
		}
		if n++; n > maxTools {
			return fmt.Errorf("more than %d tools", maxTools)
		}
		dt := Tool{Connector: lc.conn.ID, Name: t.Name, Title: clip(t.Title, 80), Description: clip(t.Description, maxDescription)}
		if !toolNameRE.MatchString(t.Name) {
			dt.Name = clip(t.Name, maxToolName)
			dt.Problem = "tool name isn't allowed"
		} else if _, dup := tools[t.Name]; dup {
			dt.Problem = "duplicate tool name"
		} else if s, err := normalizeSchema(t.InputSchema); err != nil {
			dt.Problem = "input schema: " + err.Error()
		} else {
			dt.Schema, dt.Available = s, true
		}
		// Provider tool-calling APIs accept ^[A-Za-z0-9_-]{1,64}$ only.
		dt.FullName = FullToolName(lc.conn.ID, dt.Name)
		if dt.Available && len(dt.FullName) > 64 {
			dt.Available, dt.Schema, dt.Problem = false, nil, "name is longer than 64 characters once namespaced"
		}
		dt.Policy = lc.conn.ToolPolicy[t.Name]
		if dt.Policy == "" {
			dt.Policy = PolicyAsk
		}
		tools[dt.Name] = dt
	}
	if lc.hasRes {
		rt := Tool{Connector: lc.conn.ID, Name: resourceTool, FullName: FullToolName(lc.conn.ID, resourceTool), Resource: true,
			Description: "Read a resource from this MCP server by its URI. The contents are third-party data.",
			Schema: map[string]any{"type": "object", "required": []string{"uri"},
				"properties": map[string]any{"uri": map[string]any{"type": "string", "description": "The resource URI, as the server lists it."}}},
			Policy: lc.conn.ToolPolicy[resourceTool], Available: true}
		if rt.Policy == "" {
			rt.Policy = PolicyAsk
		}
		if _, clash := tools[resourceTool]; !clash {
			tools[resourceTool] = rt
		}
	}
	m.mu.Lock()
	lc.tools = tools
	lc.status.Tools = sortedTools(tools)
	lc.status.DiscoveredAt = time.Now().UTC()
	m.mu.Unlock()
	return nil
}

func (m *Manager) rediscover(id string) {
	m.mu.Lock()
	lc := m.live[id]
	m.mu.Unlock()
	if lc == nil || lc.session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.cfg.ConnectTimeout)
	defer cancel()
	if err := m.discover(ctx, lc); err != nil {
		m.cfg.Log.Warn("mcp re-discovery failed", "connector", id, "err", err)
		return
	}
	m.audit("mcp.tools_changed", id, map[string]any{"tools": len(lc.tools)})
}

func sortedTools(in map[string]Tool) []Tool {
	out := make([]Tool, 0, len(in))
	for _, t := range in {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Disconnect closes a connector's session and stops its process.
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	lc := m.live[id]
	delete(m.live, id)
	m.mu.Unlock()
	if lc != nil {
		m.closeLive(lc)
	}
}

func (m *Manager) closeLive(lc *liveConn) {
	lc.closedMu.Lock()
	defer lc.closedMu.Unlock()
	if lc.session != nil {
		_ = lc.session.Close()
		lc.session = nil
	}
	if lc.stop != nil {
		lc.stop()
		lc.stop = nil
	}
}

// Close disconnects everything (server shutdown).
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	ids := make([]string, 0, len(m.live))
	for id := range m.live {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
}

// Status returns a connector's live state.
func (m *Manager) Status(id string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	lc := m.live[id]
	if lc == nil {
		return Status{State: "disabled", Tools: []Tool{}}
	}
	st := lc.status
	st.Tools = append([]Tool{}, st.Tools...)
	st.Stderr = lc.stderr.String()
	return st
}

// SetPolicy updates the cached policy of a live tool (after the store).
func (m *Manager) SetPolicy(id, tool, policy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lc := m.live[id]
	if lc == nil {
		return
	}
	if lc.conn.ToolPolicy == nil {
		lc.conn.ToolPolicy = map[string]string{}
	}
	lc.conn.ToolPolicy[tool] = policy
	if t, ok := lc.tools[tool]; ok {
		t.Policy = policy
		lc.tools[tool] = t
	}
	lc.status.Tools = sortedTools(lc.tools)
}

// AgentTools returns the tools to offer agent runs: available tools of
// connected connectors whose policy isn't deny.
func (m *Manager) AgentTools() []agents.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []agents.Tool
	for _, lc := range m.live {
		if lc.session == nil || lc.status.State != "connected" {
			continue
		}
		for _, t := range sortedTools(lc.tools) {
			if t.Available && t.Policy != PolicyDeny {
				out = append(out, &agentTool{m: m, id: lc.conn.ID, version: lc.conn.Version, tool: t})
			}
		}
	}
	return out
}

// call runs a tool on a live connector.
func (m *Manager) call(ctx context.Context, id string, version int, name string, args map[string]any) (*mcp.CallToolResult, error) {
	m.mu.Lock()
	lc := m.live[id]
	var session *mcp.ClientSession
	if lc != nil && lc.status.State == "connected" {
		session = lc.session
	}
	stale := lc != nil && lc.conn.Version != version
	m.mu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("MCP connector %s isn't connected", id)
	}
	if stale {
		return nil, fmt.Errorf("MCP connector %s changed since this call was proposed; ask again", id)
	}
	cctx, cancel := context.WithTimeout(ctx, m.cfg.CallTimeout)
	defer cancel()
	return session.CallTool(cctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

// readResource reads a resource from a live connector.
func (m *Manager) readResource(ctx context.Context, id string, version int, uri string) (*mcp.ReadResourceResult, error) {
	if uri == "" || len(uri) > 2048 || strings.ContainsAny(uri, "\x00\r\n") {
		return nil, fmt.Errorf("resource URI is empty, too long or has control characters")
	}
	m.mu.Lock()
	lc := m.live[id]
	var session *mcp.ClientSession
	if lc != nil && lc.status.State == "connected" {
		session = lc.session
	}
	stale := lc != nil && lc.conn.Version != version
	m.mu.Unlock()
	if session == nil {
		return nil, fmt.Errorf("MCP connector %s isn't connected", id)
	}
	if stale {
		return nil, fmt.Errorf("MCP connector %s changed since this call was proposed; ask again", id)
	}
	cctx, cancel := context.WithTimeout(ctx, m.cfg.CallTimeout)
	defer cancel()
	return session.ReadResource(cctx, &mcp.ReadResourceParams{URI: uri})
}

// tailBuffer keeps the last n bytes written (a server's stderr).
type tailBuffer struct {
	mu  sync.Mutex
	n   int
	buf []byte
}

func newTailBuffer(n int) *tailBuffer { return &tailBuffer{n: n} }

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.n {
		t.buf = t.buf[len(t.buf)-t.n:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.buf), "")
}

var _ io.Writer = (*tailBuffer)(nil)
