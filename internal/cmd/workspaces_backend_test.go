package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/api"
)

func TestWorkspacesBackend(t *testing.T) {
	def, cfg := t.TempDir(), t.TempDir()
	other := filepath.Join(t.TempDir(), "My Project")
	if err := os.Mkdir(other, 0o750); err != nil {
		t.Fatal(err)
	}
	b := newWorkspacesBackend(def, cfg)

	list, err := b.List()
	if err != nil || len(list) != 1 || !list[0].Default || list[0].Name != "default" {
		t.Fatalf("initial list = %+v %v", list, err)
	}
	ws, err := b.Add("", other)
	if err != nil || ws.Name != "my-project" {
		t.Fatalf("Add = %+v %v", ws, err)
	}
	if p, err := b.Resolve("my-project"); err != nil || filepath.Base(p) != "My Project" {
		t.Errorf("Resolve = %q %v", p, err)
	}
	if p, _ := b.Resolve(""); p != def {
		t.Errorf("Resolve default = %q", p)
	}
	if _, err := b.Resolve("nope"); !errors.Is(err, api.ErrWorkspaceNotFound) {
		t.Errorf("Resolve unknown = %v", err)
	}
	for name, tc := range map[string][2]string{
		"relative":  {"", "some/folder"},
		"missing":   {"", filepath.Join(def, "missing")},
		"duplicate": {"", other},
		"bad name":  {"Bad Name", def},
		"reserved":  {"default", def},
	} {
		if _, err := b.Add(tc[0], tc[1]); !errors.Is(err, api.ErrWorkspaceInput) {
			t.Errorf("%s: Add = %v, want input error", name, err)
		}
	}
	if err := b.Remove("default"); !errors.Is(err, api.ErrWorkspaceInput) {
		t.Errorf("removing default = %v", err)
	}
	if err := b.Remove("my-project"); err != nil {
		t.Fatal(err)
	}
	if list, _ := b.List(); len(list) != 1 {
		t.Errorf("after remove: %+v", list)
	}
}
