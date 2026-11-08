package agents

import "errors"

// Durable /agent runs (Phase 3, L.2). A run recorded in the event log saves
// a RunCheckpoint after every step; after a restart the interrupted run can
// be resumed from it (see LoopConfig.Resume for what is and isn't re-run).

// RunCheckpoint is what an /agent run saves after every step.
type RunCheckpoint struct {
	Session  string `json:"session"`
	Task     string `json:"task"`
	Check    string `json:"check,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	// Workspace is the folder the run worked in; a resume must use it too.
	Workspace string `json:"workspace,omitempty"`
	// Network is what the run was granted ("on" or "off"); a resume keeps
	// it without asking again.
	Network string    `json:"network,omitempty"`
	State   LoopState `json:"state"`
}

// Message is the /agent command that restarts this run's task.
func (cp RunCheckpoint) Message() string {
	m := "/agent "
	if cp.ReadOnly {
		m += "--read-only "
	}
	if cp.Check != "" {
		m += `--check "` + cp.Check + `" `
	}
	return m + cp.Task
}

// RunCheckpointStore keeps checkpoints by event-log run id.
type RunCheckpointStore interface {
	SaveCheckpoint(runID string, cp RunCheckpoint) error
	LoadCheckpoint(runID string) (RunCheckpoint, error)
}

// ErrResumeMismatch: the checkpoint is for a different session or task.
var ErrResumeMismatch = errors.New("agents: the checkpoint belongs to a different session or task")

// SetRunCheckpoints enables checkpoints for runs recorded in the event log.
func (c *Coordinator) SetRunCheckpoints(s RunCheckpointStore) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checkpoints = s
}

func (c *Coordinator) runCheckpoints() RunCheckpointStore {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkpoints
}
