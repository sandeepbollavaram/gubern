package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/api"
)

func writeSkill(t *testing.T, ws, name, body string) {
	t.Helper()
	dir := filepath.Join(ws, ".agents", "skills", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := "---\nname: " + name + "\ndescription: Does " + name + "\nallowed-tools: run_terminal\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsBackend_RealFiles(t *testing.T) {
	ws := t.TempDir()
	cfg := t.TempDir()
	writeSkill(t, ws, "dev-checks", "Run the tests.")
	writeSkill(t, ws, "release-check", "Check the release notes.")
	b := newSkillsBackend(ws, cfg)

	list, problems, err := b.List()
	if err != nil || len(problems) != 0 || len(list) != 2 {
		t.Fatalf("List = %v %v %v", list, problems, err)
	}
	if list[0].Path != ".agents/skills/dev-checks/SKILL.md" || !list[0].Enabled || len(list[0].AllowedTools) != 1 {
		t.Errorf("info = %+v", list[0])
	}

	info, body, err := b.Get("release-check")
	if err != nil || body != "Check the release notes." || info.Hash == "" {
		t.Fatalf("Get = %+v %q %v", info, body, err)
	}
	if _, _, err := b.Get("missing"); !errors.Is(err, api.ErrSkillNotFound) {
		t.Errorf("Get missing = %v", err)
	}

	if err := b.SetEnabled("dev-checks", false); err != nil {
		t.Fatal(err)
	}
	list, _, _ = b.List()
	if list[0].Name != "dev-checks" || list[0].Enabled {
		t.Errorf("after disable: %+v", list[0])
	}
}
