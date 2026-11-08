package agents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

// Git checkpoints (Phase 4, M.1). In a workspace that is a git repository,
// an /agent run records the workspace's state before it starts and after
// every step that changed files, as commits on hidden refs
// refs/gubern/checkpoints/<run>/<step>. They are built with plumbing and a
// temporary index, so the user's branch, index and working changes are
// never touched, and nothing is pushed (the refs are outside refs/heads and
// refs/tags). A checkpoint can be shown as a diff and restored.

// checkpointRefPrefix is where a run's checkpoints live.
const checkpointRefPrefix = "refs/gubern/checkpoints/"

var runIDRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)

// GitCheckpoint is one recorded state of the workspace.
type GitCheckpoint struct {
	Step   int       `json:"step"`
	Commit string    `json:"commit"`
	Label  string    `json:"label"`
	At     time.Time `json:"at"`
	// Files changed since the previous checkpoint ("M path", "A path", "D path").
	Files []string `json:"files"`
}

// GitCheckpointer snapshots one run's workspace.
type GitCheckpointer struct {
	dir, runID string
	step       int
	last       string // last checkpoint commit
}

// gitCmd runs git in dir with hooks off and no prompts.
func gitCmd(ctx context.Context, dir string, env []string, stdin []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	full := append([]string{"-C", dir, "-c", "core.hooksPath=" + os.DevNull, "-c", "core.autocrlf=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) //nolint:gosec // fixed git subcommands; dir and refs are validated
	cmd.Env = append(os.Environ(), append([]string{"GIT_TERMINAL_PROMPT=0"}, env...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// NewGitCheckpointer returns a checkpointer for dir, or nil when dir is not
// the top of a git work tree (or git isn't installed): checkpoints are then
// simply not taken.
func NewGitCheckpointer(ctx context.Context, dir, runID string) *GitCheckpointer {
	if !runIDRE.MatchString(runID) {
		return nil
	}
	top, err := gitCmd(ctx, dir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	a, _ := filepath.EvalSymlinks(filepath.Clean(top))
	b, _ := filepath.EvalSymlinks(filepath.Clean(dir))
	if !strings.EqualFold(a, b) {
		return nil // a sub-folder of a repo: restoring would touch files outside the workspace
	}
	return &GitCheckpointer{dir: dir, runID: runID}
}

var gubernIdent = []string{
	"GIT_AUTHOR_NAME=Gubern", "GIT_AUTHOR_EMAIL=checkpoints@gubern.invalid",
	"GIT_COMMITTER_NAME=Gubern", "GIT_COMMITTER_EMAIL=checkpoints@gubern.invalid",
}

// Snapshot records the workspace as the next checkpoint, unless nothing
// changed since the last one. It returns the checkpoint (zero if skipped).
func (g *GitCheckpointer) Snapshot(ctx context.Context, label string) (GitCheckpoint, error) {
	idx, err := os.CreateTemp("", "gubern-index-*")
	if err != nil {
		return GitCheckpoint{}, err
	}
	idxPath := idx.Name()
	_ = idx.Close()
	_ = os.Remove(idxPath) // git creates it
	defer func() { _ = os.Remove(idxPath) }()
	env := append([]string{"GIT_INDEX_FILE=" + idxPath}, gubernIdent...)

	parent := g.last
	if parent == "" {
		parent, _ = gitCmd(ctx, g.dir, nil, nil, "rev-parse", "--verify", "-q", "HEAD")
	}
	if parent != "" {
		if _, err := gitCmd(ctx, g.dir, env, nil, "read-tree", parent); err != nil {
			return GitCheckpoint{}, err
		}
	}
	if _, err := gitCmd(ctx, g.dir, env, nil, "add", "-A", "--", "."); err != nil {
		return GitCheckpoint{}, err
	}
	tree, err := gitCmd(ctx, g.dir, env, nil, "write-tree")
	if err != nil {
		return GitCheckpoint{}, err
	}
	if g.last != "" {
		if prevTree, _ := gitCmd(ctx, g.dir, nil, nil, "rev-parse", g.last+"^{tree}"); prevTree == tree {
			return GitCheckpoint{}, nil // nothing changed
		}
	}
	args := []string{"commit-tree", tree, "-m", label}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	commit, err := gitCmd(ctx, g.dir, env, nil, args...)
	if err != nil {
		return GitCheckpoint{}, err
	}
	ref := fmt.Sprintf("%s%s/%03d", checkpointRefPrefix, g.runID, g.step)
	if _, err := gitCmd(ctx, g.dir, nil, nil, "update-ref", ref, commit); err != nil {
		return GitCheckpoint{}, err
	}
	cp := GitCheckpoint{Step: g.step, Commit: commit, Label: label, At: time.Now().UTC()}
	if g.last != "" {
		cp.Files = changedFiles(ctx, g.dir, g.last, commit)
	}
	g.step++
	g.last = commit
	return cp, nil
}

func changedFiles(ctx context.Context, dir, from, to string) []string {
	out, err := gitCmd(ctx, dir, nil, nil, "diff", "--no-renames", "--name-status", from, to)
	if err != nil || out == "" {
		return nil
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) >= 2 {
			files = append(files, f[0]+" "+strings.Join(f[1:], " "))
		}
	}
	return files
}

// ListGitCheckpoints returns a run's checkpoints in step order, each with
// the files changed since the one before.
func ListGitCheckpoints(ctx context.Context, dir, runID string) ([]GitCheckpoint, error) {
	if !runIDRE.MatchString(runID) {
		return nil, errors.New("invalid run id")
	}
	out, err := gitCmd(ctx, dir, nil, nil, "for-each-ref", "--format=%(refname) %(objectname) %(committerdate:iso-strict) %(contents:subject)",
		checkpointRefPrefix+runID+"/")
	if err != nil {
		return nil, err
	}
	var cps []GitCheckpoint
	for _, l := range strings.Split(out, "\n") {
		f := strings.SplitN(l, " ", 4)
		if len(f) < 3 {
			continue
		}
		step, err := strconv.Atoi(filepath.Base(f[0]))
		if err != nil {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[2])
		cp := GitCheckpoint{Step: step, Commit: f[1], At: at}
		if len(f) == 4 {
			cp.Label = f[3]
		}
		cps = append(cps, cp)
	}
	sort.Slice(cps, func(i, j int) bool { return cps[i].Step < cps[j].Step })
	for i := 1; i < len(cps); i++ {
		cps[i].Files = changedFiles(ctx, dir, cps[i-1].Commit, cps[i].Commit)
	}
	return cps, nil
}

func findCheckpoint(cps []GitCheckpoint, step int) (GitCheckpoint, int, bool) {
	for i, c := range cps {
		if c.Step == step {
			return c, i, true
		}
	}
	return GitCheckpoint{}, 0, false
}

// GitCheckpointDiff is the patch a step made (from the checkpoint before it),
// bounded to maxBytes.
func GitCheckpointDiff(ctx context.Context, dir, runID string, step, maxBytes int) (string, error) {
	cps, err := ListGitCheckpoints(ctx, dir, runID)
	if err != nil {
		return "", err
	}
	cp, i, ok := findCheckpoint(cps, step)
	if !ok {
		return "", ErrNoSuchCheckpoint
	}
	if i == 0 {
		return "", nil // the baseline has nothing before it
	}
	out, err := gitCmd(ctx, dir, nil, nil, "diff", "--no-renames", "--no-color", cps[i-1].Commit, cp.Commit)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[:maxBytes] + "\n… (diff truncated)"
	}
	return out, nil
}

// ErrNoSuchCheckpoint is returned for an unknown step.
var ErrNoSuchCheckpoint = errors.New("no such checkpoint")

// RestoreGitCheckpoint puts every file the run's checkpoints changed after
// step back as it was at that checkpoint (files that didn't exist then are
// removed), then records the result as a new checkpoint. Files the run
// didn't touch are left alone. It returns the files it restored.
func RestoreGitCheckpoint(ctx context.Context, dir, runID string, step int) ([]string, error) {
	cps, err := ListGitCheckpoints(ctx, dir, runID)
	if err != nil {
		return nil, err
	}
	target, _, ok := findCheckpoint(cps, step)
	if !ok {
		return nil, ErrNoSuchCheckpoint
	}
	latest := cps[len(cps)-1]
	g := &GitCheckpointer{dir: dir, runID: runID, step: latest.Step + 1, last: latest.Commit}
	// Only files the run's recorded checkpoints changed: edits made after the
	// run (perhaps the user's own) aren't part of what was approved.
	out, err := gitCmd(ctx, dir, nil, nil, "diff", "--no-renames", "--name-only", "-z", target.Commit, latest.Commit)
	if err != nil {
		return nil, err
	}
	var restored []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		abs := filepath.Join(dir, filepath.FromSlash(p))
		if rel, err := filepath.Rel(dir, abs); err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		raw, err := rawBlob(ctx, dir, target.Commit+":"+p)
		if err != nil {
			// Not in the target: the run created it.
			if rmErr := os.Remove(abs); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				return restored, rmErr
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
				return restored, err
			}
			if err := os.WriteFile(abs, raw, 0o644); err != nil { //nolint:gosec // restoring a workspace file
				return restored, err
			}
		}
		restored = append(restored, p)
	}
	_, _ = g.Snapshot(ctx, "restored to step "+strconv.Itoa(step))
	return restored, nil
}

// rawBlob reads a blob byte for byte (gitCmd trims trailing newlines).
func rawBlob(ctx context.Context, dir, spec string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "cat-file", "blob", spec) //nolint:gosec // validated spec
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd.Output()
}

// workspaceCheckpoints starts git checkpoints for a run in workDir: a
// baseline now, and a snapshot after each successful step of a tool that
// changes state. It returns the AfterStep hook, or nil when workDir isn't a
// git repository.
func (c *Coordinator) workspaceCheckpoints(ctx context.Context, sessionID, runID, workDir string, tools *ToolRegistry, emit func(string)) func(LoopStep) {
	g := NewGitCheckpointer(ctx, workDir, runID)
	if g == nil {
		return nil
	}
	snap := func(label string) {
		cp, err := g.Snapshot(ctx, label)
		if err != nil {
			emit("⚠ Couldn't record a checkpoint: " + err.Error())
			return
		}
		if cp.Commit == "" {
			return
		}
		agentevents.Emit(ctx, agentevents.Event{Type: agentevents.CheckpointCreated, SessionID: sessionID,
			Label: label, Status: strconv.Itoa(cp.Step), Text: cp.Commit})
	}
	snap("before the run")
	effects := map[string]bool{}
	for _, s := range tools.Specs() {
		effects[s.Name] = s.SideEffect
	}
	return func(step LoopStep) {
		if step.Outcome == "ok" && effects[step.Tool] {
			snap(step.Tool + " " + checkpointLabel(step.Args))
		}
	}
}

// checkpointLabel summarises a step's arguments for its checkpoint.
func checkpointLabel(args map[string]any) string {
	for _, k := range []string{"path", "command", "message"} {
		if v, ok := args[k].(string); ok && v != "" {
			if len(v) > 80 {
				v = v[:80] + "…"
			}
			return v
		}
	}
	return ""
}
