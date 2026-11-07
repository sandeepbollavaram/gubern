package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSidecar(t *testing.T, doc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "gubern.yaml"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSidecar_Reads(t *testing.T) {
	dir := writeSidecar(t, "display_name: Changelog writer\nevals: tests/evals.json\ntags: release, docs\n")
	sc, ok, err := ReadSidecar(dir)
	if err != nil || !ok {
		t.Fatalf("ReadSidecar = %v %v", ok, err)
	}
	if sc.DisplayName != "Changelog writer" || sc.Evals != "tests/evals.json" || len(sc.Tags) != 2 || sc.Tags[1] != "docs" {
		t.Errorf("sidecar = %+v", sc)
	}
	if _, ok, err := ReadSidecar(t.TempDir()); ok || err != nil {
		t.Errorf("missing sidecar = %v %v", ok, err)
	}
}

func TestSidecar_Refusals(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown key":    "risk: low\n",
		"evals escapes":  "evals: ../../etc/passwd\n",
		"evals absolute": "evals: /etc/passwd\n",
		"backslash":      "evals: ..\\x.json\n",
		"not yaml":       "just text\n",
	} {
		if _, _, err := ReadSidecar(writeSidecar(t, doc)); err == nil {
			t.Errorf("%s: accepted %q", name, doc)
		}
	}
}
