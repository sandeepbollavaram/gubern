package skills

import (
	"os"
	"path/filepath"
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
