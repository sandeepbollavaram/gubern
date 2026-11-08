package eventlog

import (
	"database/sql"
	"errors"
	"fmt"
)

// Checkpoints (Phase 3): an agent run's progress, saved after every step so
// a run interrupted by a restart can be resumed. The data is opaque here
// (the agent package defines it); the log only keeps it with its run and
// makes sure each interrupted run is resumed at most once.

// Errors.
var (
	ErrNoCheckpoint     = errors.New("eventlog: run has no checkpoint")
	ErrAlreadyResumed   = errors.New("eventlog: run was already resumed")
	ErrNotInterrupted   = errors.New("eventlog: only an interrupted run can be resumed")
	checkpointMigration = `
CREATE TABLE checkpoints (
	run_id     TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
	data       TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	resumed_by TEXT NOT NULL DEFAULT ''
);`
)

// SaveCheckpoint stores (replacing) runID's checkpoint.
func (l *Log) SaveCheckpoint(runID string, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.db.Exec(`INSERT INTO checkpoints (run_id, data, updated_at) VALUES (?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		runID, string(data), l.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("eventlog: save checkpoint: %w", err)
	}
	return nil
}

// Checkpoint returns runID's checkpoint and the run that resumed it ("" if
// none has).
func (l *Log) Checkpoint(runID string) (data []byte, resumedBy string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var s string
	err = l.db.QueryRow(`SELECT data, resumed_by FROM checkpoints WHERE run_id = ?`, runID).Scan(&s, &resumedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNoCheckpoint
	}
	if err != nil {
		return nil, "", err
	}
	return []byte(s), resumedBy, nil
}

// ClaimResume records that newRunID resumes the interrupted run fromRunID.
// Only one claim ever succeeds, so two clicks never start two resumes.
func (l *Log) ClaimResume(fromRunID, newRunID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, err := l.getLocked(fromRunID)
	if err != nil {
		return err
	}
	if r.Status != StatusInterrupted {
		return ErrNotInterrupted
	}
	res, err := l.db.Exec(`UPDATE checkpoints SET resumed_by = ? WHERE run_id = ? AND resumed_by = ''`, newRunID, fromRunID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	if _, _, err := l.checkpointLocked(fromRunID); err != nil {
		return err
	}
	return ErrAlreadyResumed
}

func (l *Log) checkpointLocked(runID string) ([]byte, string, error) {
	var s, by string
	err := l.db.QueryRow(`SELECT data, resumed_by FROM checkpoints WHERE run_id = ?`, runID).Scan(&s, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNoCheckpoint
	}
	return []byte(s), by, err
}
