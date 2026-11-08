package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agents"
	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/approvals"
	"github.com/sandeepbollavaram/vortex/internal/audit"
)

// runChangesBackend serves runs' git checkpoints and restores them only
// after an approval.
type runChangesBackend struct {
	approvals *approvals.Service
	audit     *audit.Log
	log       *slog.Logger
}

func (b runChangesBackend) List(ctx context.Context, dir, runID string) ([]api.RunCheckpointInfo, error) {
	if dir == "" {
		return nil, nil
	}
	cps, err := agents.ListGitCheckpoints(ctx, dir, runID)
	if err != nil {
		return nil, err
	}
	out := make([]api.RunCheckpointInfo, 0, len(cps))
	for _, c := range cps {
		files := c.Files
		if files == nil {
			files = []string{}
		}
		out = append(out, api.RunCheckpointInfo{Step: c.Step, Commit: c.Commit, Label: c.Label, At: c.At.Format(time.RFC3339), Files: files})
	}
	return out, nil
}

func (b runChangesBackend) Diff(ctx context.Context, dir, runID string, step int) (string, error) {
	return agents.GitCheckpointDiff(ctx, dir, runID, step, 256<<10)
}

func (b runChangesBackend) PrepareRestore(ctx context.Context, dir, runID, sessionID string, step int, requestedBy string) (string, []string, error) {
	if b.approvals == nil {
		return "", nil, errors.New("restores need the approval service")
	}
	cps, err := agents.ListGitCheckpoints(ctx, dir, runID)
	if err != nil || len(cps) == 0 {
		return "", nil, api.ErrNoCheckpoints
	}
	var target *agents.GitCheckpoint
	files := map[string]bool{}
	for i := range cps {
		if cps[i].Step == step {
			target = &cps[i]
		}
		if target != nil && cps[i].Step > step {
			for _, f := range cps[i].Files {
				if _, path, ok := strings.Cut(f, " "); ok {
					files[path] = true
				}
			}
		}
	}
	if target == nil {
		return "", nil, fmt.Errorf("run %s has no checkpoint %d", runID, step)
	}
	var list []string
	for f := range files {
		list = append(list, f)
	}
	a, err := b.approvals.Create(ctx, approvals.CreateRequest{
		SessionID: sessionID,
		Risk:      "HIGH RISK",
		Workspace: dir,
		Action: approvals.Action{
			Tool:        "restore_checkpoint",
			Params:      map[string]any{"run": runID, "step": step, "commit": target.Commit},
			Command:     fmt.Sprintf("restore %d file(s) to checkpoint %d of run %s", len(list), step, runID),
			Description: fmt.Sprintf("Undo the run's changes after step %d (%s)", step, target.Label),
			Preview:     "Files put back as they were at that step (files the run created are removed):\n" + strings.Join(list, "\n"),
		},
	})
	if err != nil {
		return "", nil, err
	}
	go func() {
		wctx, cancel := context.WithDeadline(context.Background(), a.ExpiresAt.Add(time.Minute))
		defer cancel()
		done, err := b.approvals.Wait(wctx, a.ID)
		if err != nil || done.Status != approvals.Approved {
			return
		}
		restored, err := agents.RestoreGitCheckpoint(context.Background(), dir, runID, step)
		detail := map[string]any{"step": step, "approval": a.ID, "files": len(restored)}
		if err != nil {
			detail["error"] = err.Error()
			b.log.Warn("restoring a checkpoint failed", "run", runID, "step", step, "error", err)
		}
		if b.audit != nil {
			_ = b.audit.Append(context.Background(), done.DecidedBy, "run.restored", runID, detail)
		}
	}()
	return a.ID, list, nil
}
