// Package connectors connects Gubern to MCP servers (Phase 5, Milestone N).
//
// An MCP server is an untrusted tool provider: it can extend what Gubern
// does, never what Gubern lets it do. This package owns connector
// configuration, transports (a sandboxed local process, or streamable HTTP
// with destination checks), tool discovery and the tool adapter. Identity,
// approval and audit stay with the existing Gubern services; the protocol is
// the official Go SDK. See docs/design/mcp-connector-architecture.md.
package connectors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Connector is one configured MCP server.
type Connector struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Transport is "stdio" (a local process) or "http" (streamable HTTP).
	Transport string `json:"transport"`

	// stdio
	Command   string            `json:"command,omitempty"` // absolute path
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`        // non-secret variables
	ReadPaths []string          `json:"read_paths,omitempty"` // extra read-only folders (Linux sandbox)
	Network   string            `json:"network,omitempty"`    // "off" (default) or "on"
	// SecretEnv maps an environment variable to a secret-store name; the
	// value is read at start-up and given to the process only.
	SecretEnv map[string]string `json:"secret_env,omitempty"`

	// http
	URL string `json:"url,omitempty"`
	// Local allows a loopback endpoint (a server on this machine).
	Local bool `json:"local,omitempty"`
	// BearerSecret names the secret sent as "Authorization: Bearer …".
	BearerSecret string `json:"bearer_secret,omitempty"`

	Enabled bool   `json:"enabled"`
	Trust   string `json:"trust"` // "untrusted" (default) or "trusted"
	// ToolPolicy is per tool: "ask" (default), "allow" or "deny".
	ToolPolicy map[string]string `json:"tool_policy,omitempty"`

	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LastConnected time.Time `json:"last_connected,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	// Version increases on every change; tool approvals are bound to it.
	Version int `json:"version"`
}

// Policy values.
const (
	PolicyAsk   = "ask"
	PolicyAllow = "allow"
	PolicyDeny  = "deny"
)

// ErrInput marks a request the caller can fix; its message is safe to show.
var ErrInput = errors.New("connector")

// ErrNotFound: no connector with that id.
var ErrNotFound = errors.New("connector not found")

var (
	idRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	// Secret names fit the local secret store ([A-Za-z0-9_]) and have no
	// "__", which separates connector and name in SecretName.
	secretRE = regexp.MustCompile(`^[A-Za-z0-9]+(_[A-Za-z0-9]+)*$`)
)

// envDenied are variables a connector may not set: they change how
// programs load code, or are Gubern's own.
func envDenied(name string) bool {
	up := strings.ToUpper(name)
	switch {
	case strings.HasPrefix(up, "GUBERN_"), strings.HasPrefix(up, "VORTEX_"),
		strings.HasPrefix(up, "LD_"), strings.HasPrefix(up, "DYLD_"),
		up == "PATH", up == "NODE_OPTIONS", up == "PYTHONPATH", up == "PYTHONSTARTUP",
		up == "COMSPEC", up == "SYSTEMROOT", up == "PATHEXT":
		return true
	}
	return false
}

// SecretName is the secret-store key for a connector's credential:
// mcp_<id with - as _>__<name>. Unique because names never contain "__".
func SecretName(connectorID, name string) string {
	return "mcp_" + strings.ReplaceAll(connectorID, "-", "_") + "__" + name
}

// ValidSecretName reports whether name can name a connector credential.
func ValidSecretName(name string) bool { return len(name) <= 64 && secretRE.MatchString(name) }

// Validate checks c and fills defaults. Errors wrap ErrInput.
func (c *Connector) Validate() error {
	bad := func(f string, a ...any) error { return fmt.Errorf("%w: "+f, append([]any{ErrInput}, a...)...) }
	if !idRE.MatchString(c.ID) {
		return bad("id must be 1-32 lowercase letters, digits or '-'")
	}
	if c.Name == "" {
		c.Name = c.ID
	}
	if len(c.Name) > 80 || len(c.Description) > 500 {
		return bad("name or description too long")
	}
	if c.Trust == "" {
		c.Trust = "untrusted"
	}
	if c.Trust != "untrusted" && c.Trust != "trusted" {
		return bad("trust must be untrusted or trusted")
	}
	for tool, p := range c.ToolPolicy {
		if p != PolicyAsk && p != PolicyAllow && p != PolicyDeny {
			return bad("tool %q policy must be ask, allow or deny", tool)
		}
	}
	switch c.Transport {
	case "stdio":
		if c.URL != "" || c.BearerSecret != "" || c.Local {
			return bad("url, bearer_secret and local are for http connectors")
		}
		if !filepath.IsAbs(c.Command) {
			return bad("command must be an absolute path")
		}
		if fi, err := os.Stat(c.Command); err != nil || fi.IsDir() {
			return bad("command %s is not a file", c.Command)
		}
		if len(c.Args) > 64 {
			return bad("too many arguments")
		}
		for _, a := range c.Args {
			if len(a) > 4096 || strings.ContainsRune(a, 0) {
				return bad("argument too long or contains NUL")
			}
		}
		for k, v := range c.Env {
			if !envNameRE.MatchString(k) || envDenied(k) || len(v) > 4096 {
				return bad("environment variable %q is not allowed", k)
			}
		}
		for k, v := range c.SecretEnv {
			if !envNameRE.MatchString(k) || envDenied(k) || !ValidSecretName(v) {
				return bad("secret variable %q is not allowed", k)
			}
		}
		for _, p := range c.ReadPaths {
			if !filepath.IsAbs(p) {
				return bad("read_paths must be absolute")
			}
		}
		if c.Network == "" {
			c.Network = "off"
		}
		if c.Network != "off" && c.Network != "on" {
			return bad("network must be on or off")
		}
	case "http":
		if c.Command != "" || len(c.Args) > 0 || len(c.Env) > 0 || len(c.SecretEnv) > 0 {
			return bad("command, args and env are for stdio connectors")
		}
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" || u.User != nil {
			return bad("url must be an absolute URL without credentials")
		}
		loop := isLoopbackHost(u.Hostname())
		switch {
		case u.Scheme == "https":
		case u.Scheme == "http" && loop && c.Local:
		default:
			return bad("url must be https (http only for a local, loopback server)")
		}
		if loop && !c.Local {
			return bad("a loopback url needs local: true")
		}
		if c.BearerSecret != "" && !ValidSecretName(c.BearerSecret) {
			return bad("bearer_secret is not a valid name")
		}
	default:
		return bad("transport must be stdio or http")
	}
	return nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Store keeps connectors in a JSON file.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore uses the file at path (created on first save).
func NewStore(path string) *Store { return &Store{path: path} }

type storeFile struct {
	Connectors []Connector `json:"connectors"`
}

func (s *Store) load() ([]Connector, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f storeFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("connectors: %s: %w", s.path, err)
	}
	return f.Connectors, nil
}

func (s *Store) save(list []Connector) error {
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(storeFile{Connectors: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns every connector.
func (s *Store) List() ([]Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Get returns one connector.
func (s *Store) Get(id string) (Connector, error) {
	list, err := s.List()
	if err != nil {
		return Connector{}, err
	}
	for _, c := range list {
		if c.ID == id {
			return c, nil
		}
	}
	return Connector{}, ErrNotFound
}

// Add stores a new, disabled connector.
func (s *Store) Add(c Connector) (Connector, error) {
	c.Enabled = false
	c.Version = 1
	c.LastError, c.LastConnected = "", time.Time{}
	if err := c.Validate(); err != nil {
		return Connector{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return Connector{}, err
	}
	for _, o := range list {
		if o.ID == c.ID {
			return Connector{}, fmt.Errorf("%w: a connector named %q exists", ErrInput, c.ID)
		}
	}
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	return c, s.save(append(list, c))
}

// Update applies fn to the connector and saves it, bumping its version when
// fn reports a configuration change.
func (s *Store) Update(id string, fn func(c *Connector) (changed bool, err error)) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return Connector{}, err
	}
	for i := range list {
		if list[i].ID != id {
			continue
		}
		c := list[i]
		changed, err := fn(&c)
		if err != nil {
			return Connector{}, err
		}
		if err := c.Validate(); err != nil {
			return Connector{}, err
		}
		if changed {
			c.Version++
			c.UpdatedAt = time.Now().UTC()
		}
		list[i] = c
		return c, s.save(list)
	}
	return Connector{}, ErrNotFound
}

// Remove deletes the connector.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.load()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID == id {
			return s.save(append(list[:i], list[i+1:]...))
		}
	}
	return ErrNotFound
}
