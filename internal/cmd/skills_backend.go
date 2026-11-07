package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/approvals"
	"github.com/sandeepbollavaram/vortex/internal/audit"
	agentskills "github.com/sandeepbollavaram/vortex/internal/skills"
)

// skillsBackend serves the workspace's skills to the API: the same set /agent
// offers the model (workspace skills, re-read on every call, so edits show
// without a restart), with the operator's on/off switches, and installs new
// skills only after an approval.
type skillsBackend struct {
	workspace  string
	statePath  string
	lockPath   string
	quarantine string
	approvals  *approvals.Service // nil: install not available
	audit      *audit.Log         // nil: no audit log configured
	log        *slog.Logger
}

func newSkillsBackend(workspace, userConfigDir string, svc *approvals.Service, auditLog *audit.Log, log *slog.Logger) skillsBackend {
	if log == nil {
		log = slog.Default()
	}
	return skillsBackend{
		workspace:  workspace,
		statePath:  agentskills.StatePath(userConfigDir),
		lockPath:   agentskills.LockPath(userConfigDir),
		quarantine: agentskills.QuarantineDir(userConfigDir),
		approvals:  svc,
		audit:      auditLog,
		log:        log,
	}
}

type discovered struct {
	found    []agentskills.Skill
	problems []agentskills.Problem
	disabled map[string]bool
	lock     map[string]agentskills.LockEntry
	tampered map[string]bool
}

func (b skillsBackend) discover() (discovered, error) {
	d := discovered{disabled: map[string]bool{}, lock: map[string]agentskills.LockEntry{}, tampered: map[string]bool{}}
	if b.workspace == "" {
		return d, nil
	}
	d.found, d.problems = agentskills.Discover(agentskills.DefaultRoots(b.workspace, ""))
	var err error
	if d.disabled, err = agentskills.Disabled(b.statePath); err != nil {
		return d, err
	}
	if d.lock, err = agentskills.ReadLock(b.lockPath); err != nil {
		return d, err
	}
	d.tampered = agentskills.Tampered(d.found, d.lock)
	return d, nil
}

func (b skillsBackend) info(s agentskills.Skill, d discovered) api.SkillInfo {
	e, pinned := d.lock[filepath.Clean(s.Dir)]
	return api.SkillInfo{
		Name:          s.Name,
		Description:   s.Description,
		Scope:         s.Scope,
		Path:          b.rel(filepath.Join(s.Dir, "SKILL.md")),
		Hash:          s.Hash,
		License:       s.License,
		Compatibility: s.Compatibility,
		AllowedTools:  s.AllowedTools,
		Enabled:       !d.disabled[s.Name] && !d.tampered[s.Name],
		Pinned:        pinned,
		Tampered:      d.tampered[s.Name],
		Source:        e.Source,
	}
}

// rel shows a path relative to the workspace, so the API doesn't leak the
// server's directory layout for files inside it.
func (b skillsBackend) rel(p string) string {
	if r, err := filepath.Rel(b.workspace, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func (b skillsBackend) List() ([]api.SkillInfo, []api.SkillProblem, error) {
	d, err := b.discover()
	if err != nil {
		return nil, nil, err
	}
	out := make([]api.SkillInfo, 0, len(d.found))
	for _, s := range d.found {
		out = append(out, b.info(s, d))
	}
	probs := make([]api.SkillProblem, 0, len(d.problems))
	for _, p := range d.problems {
		probs = append(probs, api.SkillProblem{Path: b.rel(p.Path), Error: p.Err.Error()})
	}
	return out, probs, nil
}

func (b skillsBackend) Get(name string) (api.SkillInfo, string, error) {
	d, err := b.discover()
	if err != nil {
		return api.SkillInfo{}, "", err
	}
	for _, s := range d.found {
		if s.Name != name {
			continue
		}
		body, err := s.Body()
		if err != nil {
			return api.SkillInfo{}, "", err
		}
		return b.info(s, d), body, nil
	}
	return api.SkillInfo{}, "", api.ErrSkillNotFound
}

func (b skillsBackend) SetEnabled(name string, enabled bool) error {
	return agentskills.SetEnabled(b.statePath, name, enabled)
}

// PrepareInstall fetches source into quarantine, opens a HIGH-risk approval
// that shows exactly what would be installed, and installs only if that
// approval is approved. It returns as soon as the approval is open.
func (b skillsBackend) PrepareInstall(ctx context.Context, source, sessionID, requestedBy string) (api.SkillInstall, error) {
	if b.approvals == nil || b.workspace == "" {
		return api.SkillInstall{}, errors.New("skill install needs the approval service and a workspace")
	}
	f, err := agentskills.Fetch(ctx, source, b.quarantine)
	if err != nil {
		return api.SkillInstall{}, fmt.Errorf("%w: %v", api.ErrInstallInput, err)
	}
	root := filepath.Join(b.workspace, ".agents", "skills")
	target := filepath.Join(root, f.Skill.Name)
	files := make([]api.SkillFile, 0, len(f.Files))
	var list strings.Builder
	for _, fi := range f.Files {
		files = append(files, api.SkillFile{Path: fi.Path, Size: fi.Size, SHA256: fi.SHA256})
		_, _ = fmt.Fprintf(&list, "%s  (%d bytes, sha256 %s)\n", fi.Path, fi.Size, fi.SHA256[:16])
	}
	body, _ := f.Skill.Body()
	if len(body) > 1500 {
		body = body[:1500] + "\n…"
	}
	a, err := b.approvals.Create(ctx, approvals.CreateRequest{
		SessionID: sessionID,
		Risk:      "HIGH RISK",
		Workspace: b.workspace,
		Action: approvals.Action{
			Tool:        "skill_install",
			Params:      map[string]any{"name": f.Skill.Name, "source": source, "tree_hash": f.TreeHash, "target": b.rel(target)},
			Command:     fmt.Sprintf("install skill %s into %s", f.Skill.Name, b.rel(target)),
			Description: fmt.Sprintf("Install the skill %q from %s (%d files)", f.Skill.Name, source, len(f.Files)),
			Preview:     "Files:\n" + list.String() + "\nTree hash: " + f.TreeHash + "\n\nSKILL.md:\n" + body,
		},
	})
	if err != nil {
		_ = f.Remove()
		return api.SkillInstall{}, err
	}
	go b.finishInstall(f, root, a)
	return api.SkillInstall{
		ApprovalID: a.ID,
		SessionID:  sessionID,
		Source:     source,
		TreeHash:   f.TreeHash,
		Skill: api.SkillInfo{
			Name: f.Skill.Name, Description: f.Skill.Description, Scope: "project",
			Path: b.rel(filepath.Join(target, "SKILL.md")), Hash: f.Skill.Hash, License: f.Skill.License,
			AllowedTools: f.Skill.AllowedTools,
		},
		Files: files,
	}, nil
}

// finishInstall waits for the decision and installs on approval. Anything
// else (rejected, expired, cancelled, server stopping) discards the
// quarantine copy.
func (b skillsBackend) finishInstall(f agentskills.Fetched, root string, a approvals.Approval) {
	ctx, cancel := context.WithDeadline(context.Background(), a.ExpiresAt.Add(time.Minute))
	defer cancel()
	done, err := b.approvals.Wait(ctx, a.ID)
	if err != nil || done.Status != approvals.Approved {
		_ = f.Remove()
		b.record(done.DecidedBy, "skill.install_declined", f.Skill.Name, map[string]any{"approval": a.ID, "status": string(done.Status)})
		return
	}
	e, err := agentskills.Install(f, root, b.lockPath, agentskills.LockEntry{InstalledBy: done.DecidedBy, ApprovalID: a.ID})
	if err != nil {
		_ = f.Remove()
		b.log.Warn("skill install failed after approval", "skill", f.Skill.Name, "error", err)
		b.record(done.DecidedBy, "skill.install_failed", f.Skill.Name, map[string]any{"approval": a.ID, "error": err.Error()})
		return
	}
	b.record(done.DecidedBy, "skill.installed", f.Skill.Name, map[string]any{"approval": a.ID, "source": e.Source, "tree_hash": e.TreeHash, "dir": b.rel(e.Dir)})
}

func (b skillsBackend) record(actor, action, resource string, detail map[string]any) {
	if b.audit == nil {
		return
	}
	if err := b.audit.Append(context.Background(), actor, action, resource, detail); err != nil {
		b.log.Warn("audit append failed", "action", action, "error", err)
	}
}
