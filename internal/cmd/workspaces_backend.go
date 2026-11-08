package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/userdir"
)

// workspacesBackend keeps the folders agent runs may work in: the server's
// default working folder (always present, named "default") plus the ones
// an operator added, stored in <user config>/gubern/workspaces.json.
type workspacesBackend struct {
	defaultDir string
	path       string
	mu         sync.Mutex
}

type storedWorkspace struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

var (
	workspaceNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	nonNameChars    = regexp.MustCompile(`[^a-z0-9]+`)
)

const defaultWorkspaceName = "default"

func newWorkspacesBackend(defaultDir, userConfigDir string) *workspacesBackend {
	return &workspacesBackend{defaultDir: defaultDir, path: filepath.Join(userdir.App(userConfigDir), "workspaces.json")}
}

func (b *workspacesBackend) load() ([]storedWorkspace, error) {
	raw, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []storedWorkspace
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", b.path, err)
	}
	return out, nil
}

func (b *workspacesBackend) save(list []storedWorkspace) error {
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func (b *workspacesBackend) List() ([]api.WorkspaceInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	list, err := b.load()
	if err != nil {
		return nil, err
	}
	out := []api.WorkspaceInfo{{Name: defaultWorkspaceName, Path: b.defaultDir, Default: true, Exists: isDir(b.defaultDir)}}
	for _, w := range list {
		out = append(out, api.WorkspaceInfo{Name: w.Name, Path: w.Path, Exists: isDir(w.Path)})
	}
	return out, nil
}

func (b *workspacesBackend) Resolve(name string) (string, error) {
	if name == "" || name == defaultWorkspaceName {
		return b.defaultDir, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	list, err := b.load()
	if err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Name == name {
			if !isDir(w.Path) {
				return "", fmt.Errorf("%w: %s no longer exists", api.ErrWorkspaceInput, w.Path)
			}
			return w.Path, nil
		}
	}
	return "", api.ErrWorkspaceNotFound
}

func (b *workspacesBackend) Add(name, path string) (api.WorkspaceInfo, error) {
	if !filepath.IsAbs(path) {
		return api.WorkspaceInfo{}, fmt.Errorf("%w: the path must be absolute", api.ErrWorkspaceInput)
	}
	abs, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil || !isDir(abs) {
		return api.WorkspaceInfo{}, fmt.Errorf("%w: %s is not an existing folder", api.ErrWorkspaceInput, path)
	}
	if name == "" {
		// Derived from the folder's name: "My Project" → "my-project".
		name = strings.Trim(nonNameChars.ReplaceAllString(strings.ToLower(filepath.Base(abs)), "-"), "-")
	}
	if !workspaceNameRE.MatchString(name) || len(name) > 40 || name == defaultWorkspaceName {
		return api.WorkspaceInfo{}, fmt.Errorf("%w: name must be 1-40 lowercase letters, digits and dashes, not %q", api.ErrWorkspaceInput, defaultWorkspaceName)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	list, err := b.load()
	if err != nil {
		return api.WorkspaceInfo{}, err
	}
	for _, w := range list {
		if w.Name == name {
			return api.WorkspaceInfo{}, fmt.Errorf("%w: a workspace named %q already exists", api.ErrWorkspaceInput, name)
		}
		if filepath.Clean(w.Path) == abs {
			return api.WorkspaceInfo{}, fmt.Errorf("%w: that folder is already the workspace %q", api.ErrWorkspaceInput, w.Name)
		}
	}
	list = append(list, storedWorkspace{Name: name, Path: abs})
	if err := b.save(list); err != nil {
		return api.WorkspaceInfo{}, err
	}
	return api.WorkspaceInfo{Name: name, Path: abs, Exists: true}, nil
}

func (b *workspacesBackend) Remove(name string) error {
	if name == defaultWorkspaceName {
		return fmt.Errorf("%w: the default workspace can't be removed", api.ErrWorkspaceInput)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	list, err := b.load()
	if err != nil {
		return err
	}
	for i, w := range list {
		if w.Name == name {
			return b.save(append(list[:i], list[i+1:]...))
		}
	}
	return api.ErrWorkspaceNotFound
}
