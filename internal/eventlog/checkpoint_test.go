package eventlog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestCheckpoint_SaveLoadAndResumeOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	l, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	run, _, _, _ := l.StartRun(context.Background(), "s1", "p", "")
	if _, _, err := l.Checkpoint(run.ID); !errors.Is(err, ErrNoCheckpoint) {
		t.Fatalf("no checkpoint yet: %v", err)
	}
	_ = l.SaveCheckpoint(run.ID, []byte(`{"v":1}`))
	_ = l.SaveCheckpoint(run.ID, []byte(`{"v":2}`))
	if d, by, err := l.Checkpoint(run.ID); err != nil || string(d) != `{"v":2}` || by != "" {
		t.Fatalf("checkpoint = %s %q %v", d, by, err)
	}
	if err := l.ClaimResume(run.ID, "r-new"); !errors.Is(err, ErrNotInterrupted) {
		t.Errorf("claiming a live run = %v", err)
	}
	_ = l.Close()

	l, err = Open(path, Options{}) // the restart marks it interrupted
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if err := l.ClaimResume(run.ID, "r-a"); err != nil {
		t.Fatal(err)
	}
	if err := l.ClaimResume(run.ID, "r-b"); !errors.Is(err, ErrAlreadyResumed) {
		t.Errorf("second claim = %v, want already resumed", err)
	}
	if _, by, _ := l.Checkpoint(run.ID); by != "r-a" {
		t.Errorf("resumed_by = %q", by)
	}
}
