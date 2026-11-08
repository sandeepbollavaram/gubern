package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/llm"
)

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	write(t, dir, "keep.txt", "original\n")
	write(t, dir, "edit.txt", "v1\n")
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitCheckpoints_SnapshotDiffRestore(t *testing.T) {
	ctx := context.Background()
	dir := gitRepo(t)
	write(t, dir, "user-wip.txt", "the user's own uncommitted work\n")
	headBefore := gitOut(t, dir, "rev-parse", "HEAD")
	statusBefore := gitOut(t, dir, "status", "--porcelain")

	g := NewGitCheckpointer(ctx, dir, "r-test1")
	if g == nil {
		t.Fatal("no checkpointer for a git repo")
	}
	base, err := g.Snapshot(ctx, "before the run")
	if err != nil || base.Step != 0 {
		t.Fatalf("baseline = %+v %v", base, err)
	}
	write(t, dir, "edit.txt", "v2\n")
	write(t, dir, "src/new.go", "package src\n")
	one, err := g.Snapshot(ctx, "write_file edit.txt")
	if err != nil || one.Step != 1 || len(one.Files) != 2 {
		t.Fatalf("step 1 = %+v %v", one, err)
	}
	if skipped, _ := g.Snapshot(ctx, "nothing changed"); skipped.Commit != "" {
		t.Error("an unchanged workspace made a checkpoint")
	}
	write(t, dir, "edit.txt", "v3\n")
	if _, err := g.Snapshot(ctx, "edit again"); err != nil {
		t.Fatal(err)
	}

	// The user's branch, index and working changes are untouched.
	if gitOut(t, dir, "rev-parse", "HEAD") != headBefore {
		t.Error("HEAD moved")
	}
	if !strings.Contains(gitOut(t, dir, "status", "--porcelain"), "user-wip.txt") || statusBefore == "" {
		t.Error("the user's working changes were affected")
	}

	cps, err := ListGitCheckpoints(ctx, dir, "r-test1")
	if err != nil || len(cps) != 3 || cps[1].Label != "write_file edit.txt" {
		t.Fatalf("list = %+v %v", cps, err)
	}
	diff, err := GitCheckpointDiff(ctx, dir, "r-test1", 1, 0)
	if err != nil || !strings.Contains(diff, "+v2") || !strings.Contains(diff, "src/new.go") {
		t.Errorf("diff = %q %v", diff, err)
	}

	restored, err := RestoreGitCheckpoint(ctx, dir, "r-test1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "edit.txt") != "v1\n" || read(t, dir, "src/new.go") != "<missing>" {
		t.Errorf("after restore: edit.txt=%q new.go=%q (restored %v)", read(t, dir, "edit.txt"), read(t, dir, "src/new.go"), restored)
	}
	if read(t, dir, "user-wip.txt") != "the user's own uncommitted work\n" || read(t, dir, "keep.txt") != "original\n" {
		t.Error("restore touched files the run didn't change")
	}
	if gitOut(t, dir, "rev-parse", "HEAD") != headBefore {
		t.Error("restore moved HEAD")
	}
	if cps2, _ := ListGitCheckpoints(ctx, dir, "r-test1"); len(cps2) < 4 || !strings.HasPrefix(cps2[len(cps2)-1].Label, "restored to step 0") {
		t.Errorf("restore wasn't recorded: %+v", cps2)
	}
}

func TestGitCheckpoints_NotARepo(t *testing.T) {
	if NewGitCheckpointer(context.Background(), t.TempDir(), "r-x") != nil {
		t.Error("checkpointer for a folder that isn't a repository")
	}
	dir := gitRepo(t)
	sub := filepath.Join(dir, "sub")
	_ = os.Mkdir(sub, 0o750)
	if NewGitCheckpointer(context.Background(), sub, "r-x") != nil {
		t.Error("checkpointer for a sub-folder of a repository")
	}
	if NewGitCheckpointer(context.Background(), dir, "../bad") != nil {
		t.Error("accepted a bad run id")
	}
}

// TestAgentRun_TakesGitCheckpoints: an /agent run in a git workspace records a
// baseline and a checkpoint after the write, announced as events; a read
// doesn't make one.
func TestAgentRun_TakesGitCheckpoints(t *testing.T) {
	repo := gitRepo(t)
	model := &scriptedModel{replies: []llm.ToolChatResult{
		call("r", "read_file", map[string]any{"path": "keep.txt"}),
		call("w", "write_file", map[string]any{"path": "edit.txt", "content": "changed\n"}),
		{Text: "edited"},
	}}
	c, _ := agentCoord(t, toolGateway{model}, func(context.Context, ApprovalRequest) bool { return true })
	c.SetWorkspaceTools(func(root string) (*ToolRegistry, error) {
		reg := NewToolRegistry()
		return reg, RegisterLocalTools(reg, LocalFSConfig{Root: root})
	})
	var labels []string
	ctx := agentevents.WithSink(agentevents.WithWorkspace(agentevents.WithRunID(context.Background(), "r-gitrun"), repo),
		func(e agentevents.Event) {
			if e.Type == agentevents.CheckpointCreated {
				labels = append(labels, e.Status+":"+e.Label)
			}
		})
	if _, err := c.HandleMessage(ctx, "/agent edit it", "gc1"); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || labels[0] != "0:before the run" || labels[1] != "1:write_file edit.txt" {
		t.Errorf("checkpoint events = %v", labels)
	}
	cps, err := ListGitCheckpoints(context.Background(), repo, "r-gitrun")
	if err != nil || len(cps) != 2 || len(cps[1].Files) != 1 || cps[1].Files[0] != "M edit.txt" {
		t.Errorf("checkpoints = %+v %v", cps, err)
	}
}

// TestGitCheckpoints_RestoreLeavesLaterUserEdits: a restore resets only what
// the run's checkpoints changed, not files edited after the run.
func TestGitCheckpoints_RestoreLeavesLaterUserEdits(t *testing.T) {
	ctx := context.Background()
	dir := gitRepo(t)
	g := NewGitCheckpointer(ctx, dir, "r-later")
	if _, err := g.Snapshot(ctx, "before the run"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "edit.txt", "run's change\n")
	if _, err := g.Snapshot(ctx, "write_file edit.txt"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "keep.txt", "the user's later edit\n") // after the run
	restored, err := RestoreGitCheckpoint(ctx, dir, "r-later", 0)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, dir, "edit.txt") != "v1\n" || read(t, dir, "keep.txt") != "the user's later edit\n" || len(restored) != 1 {
		t.Errorf("restored %v; edit.txt=%q keep.txt=%q", restored, read(t, dir, "edit.txt"), read(t, dir, "keep.txt"))
	}
}
