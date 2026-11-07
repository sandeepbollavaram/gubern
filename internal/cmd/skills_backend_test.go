package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/approvals"
	agentskills "github.com/sandeepbollavaram/vortex/internal/skills"
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
	b := newSkillsBackend(ws, cfg, nil, nil, nil)

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

func openTestApprovals(t *testing.T) *approvals.Service {
	t.Helper()
	svc, _, err := approvals.Open(filepath.Join(t.TempDir(), "approvals.db"), approvals.Options{
		TTL: time.Minute,
		Authorizer: func(_ context.Context, _ approvals.Approval, d approvals.Decider) (approvals.Decider, error) {
			return d, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestSkillsBackend_InstallOnlyAfterApproval: an install opens a HIGH-risk
// approval showing the files and tree hash; nothing lands in the workspace
// until it is approved, and then the skill is pinned in the lock.
func TestSkillsBackend_InstallOnlyAfterApproval(t *testing.T) {
	ws, cfg := t.TempDir(), t.TempDir()
	src := filepath.Join(t.TempDir(), "lint-fix")
	writeSkill(t, filepath.Dir(src), "lint-fix", "Fix lint.")
	// writeSkill nests under .agents/skills; point at that folder.
	src = filepath.Join(filepath.Dir(src), ".agents", "skills", "lint-fix")
	svc := openTestApprovals(t)
	b := newSkillsBackend(ws, cfg, svc, nil, nil)

	res, err := b.PrepareInstall(context.Background(), src, "skill-install-t1", "p-owner")
	if err != nil {
		t.Fatal(err)
	}
	a, err := svc.Get(res.ApprovalID)
	if err != nil || a.Risk != "HIGH RISK" || a.Tool != "skill_install" || !strings.Contains(a.Preview, res.TreeHash) {
		t.Fatalf("approval = %+v, %v", a, err)
	}
	target := filepath.Join(ws, ".agents", "skills", "lint-fix")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("skill installed before approval")
	}
	if _, err := svc.Decide(context.Background(), a.ID, approvals.Decision{Approve: true, ActionHash: a.ActionHash, Decider: approvals.Decider{PrincipalID: "p-owner", Channel: "mobile"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the install", func() bool { _, err := os.Stat(filepath.Join(target, "SKILL.md")); return err == nil })
	var got api.SkillInfo
	waitFor(t, "the lock", func() bool {
		list, _, _ := b.List()
		for _, s := range list {
			if s.Name == "lint-fix" {
				got = s
			}
		}
		return got.Pinned
	})
	if !got.Enabled || got.Tampered || got.Source != src {
		t.Errorf("installed skill = %+v", got)
	}
}

func TestSkillsBackend_RejectedInstallLeavesNothing(t *testing.T) {
	ws, cfg := t.TempDir(), t.TempDir()
	srcRoot := t.TempDir()
	writeSkill(t, srcRoot, "notes", "Take notes.")
	src := filepath.Join(srcRoot, ".agents", "skills", "notes")
	svc := openTestApprovals(t)
	b := newSkillsBackend(ws, cfg, svc, nil, nil)
	res, err := b.PrepareInstall(context.Background(), src, "skill-install-t2", "p-owner")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := svc.Get(res.ApprovalID)
	if _, err := svc.Decide(context.Background(), a.ID, approvals.Decision{Approve: false, ActionHash: a.ActionHash, Decider: approvals.Decider{PrincipalID: "p-owner", Channel: "api"}}); err != nil {
		t.Fatal(err)
	}
	q := agentskills.QuarantineDir(cfg)
	waitFor(t, "quarantine cleanup", func() bool { e, _ := os.ReadDir(q); return len(e) == 0 })
	if _, err := os.Stat(filepath.Join(ws, ".agents", "skills", "notes")); !os.IsNotExist(err) {
		t.Error("rejected skill was installed")
	}
	if _, err := b.PrepareInstall(context.Background(), "https://example.com/x.git@main", "skill-install-t3", "p"); !errors.Is(err, api.ErrInstallInput) {
		t.Errorf("unpinned git source = %v, want ErrInstallInput", err)
	}
}
