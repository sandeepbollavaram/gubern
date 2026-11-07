package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestState_DisableEnableRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gubern", "skills-state.json")

	got, err := Disabled(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing file: got %v, %v; want empty, nil", got, err)
	}
	if err := SetEnabled(path, "release-check", false); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(path, "dev-checks", false); err != nil {
		t.Fatal(err)
	}
	got, _ = Disabled(path)
	if !got["release-check"] || !got["dev-checks"] || len(got) != 2 {
		t.Fatalf("disabled = %v", got)
	}
	if err := SetEnabled(path, "release-check", true); err != nil {
		t.Fatal(err)
	}
	got, _ = Disabled(path)
	if got["release-check"] || !got["dev-checks"] {
		t.Fatalf("after enabling: %v", got)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}

func TestState_RejectsNonSkillNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	for _, n := range []string{"", "../x", "Upper", "a b", "-lead"} {
		if err := SetEnabled(path, n, false); err == nil {
			t.Errorf("SetEnabled(%q) accepted", n)
		}
	}
}

func TestEnabled_FiltersDisabled(t *testing.T) {
	all := []Skill{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got := Enabled(all, map[string]bool{"b": true})
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("Enabled = %v", got)
	}
	if len(Enabled(all, nil)) != 3 {
		t.Error("nil set should keep all")
	}
}

func TestDiscover_UnreadableRootIsAProblem(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the test user can't read (POSIX permissions, not root)")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "skills")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	found, problems := Discover([]Root{{Dir: filepath.Join(dir, "missing"), Scope: "user"}, {Dir: locked, Scope: "project"}})
	if len(found) != 0 || len(problems) != 1 || problems[0].Path != locked {
		t.Fatalf("found=%v problems=%v; want one problem for the unreadable root", found, problems)
	}
}
