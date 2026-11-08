package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/llm"
)

type memCheckpoints struct {
	mu   sync.Mutex
	all  map[string][]RunCheckpoint // every save, in order
	last map[string]RunCheckpoint
}

func (m *memCheckpoints) SaveCheckpoint(id string, cp RunCheckpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.all[id] = append(m.all[id], cp)
	m.last[id] = cp
	return nil
}

func (m *memCheckpoints) LoadCheckpoint(id string) (RunCheckpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.last[id]
	if !ok {
		return cp, errors.New("no checkpoint")
	}
	return cp, nil
}

// TestCoordinator_ResumesAnInterruptedRun: run 1 is cut off right after the
// model asked for a write (its write-ahead checkpoint is the last one saved);
// run 2 resumes it. The write is never performed, the model is told its
// outcome is unknown, and the run finishes.
func TestCoordinator_ResumesAnInterruptedRun(t *testing.T) {
	store := &memCheckpoints{all: map[string][]RunCheckpoint{}, last: map[string]RunCheckpoint{}}
	model := &scriptedModel{replies: []llm.ToolChatResult{
		call("w1", "write_file", map[string]any{"path": "out.txt", "content": "1"}),
		{Text: "first run done"},
	}}
	c, dir := agentCoord(t, toolGateway{model}, func(context.Context, ApprovalRequest) bool { return true })
	c.SetRunCheckpoints(store)
	ctx := agentevents.WithRunID(context.Background(), "r-1")
	if _, err := c.HandleMessage(ctx, "/agent write out.txt", "res1"); err != nil {
		t.Fatal(err)
	}
	saves := store.all["r-1"]
	if len(saves) < 3 || saves[0].Task != "write out.txt" || saves[0].Session != "res1" {
		t.Fatalf("saves = %d, first = %+v", len(saves), saves[0])
	}
	// Simulate the crash: the last durable checkpoint is the one saved as the
	// approved write began executing (before its result was recorded).
	var ahead RunCheckpoint
	for _, cp := range saves {
		n := len(cp.State.Messages)
		if n > 0 && len(cp.State.Messages[n-1].ToolCalls) > 0 && len(cp.State.Started) == 1 && cp.State.Started[0] == "w1" {
			ahead = cp
		}
	}
	if ahead.Task == "" {
		t.Fatal("no checkpoint marks the write as started")
	}
	_ = os.Remove(filepath.Join(dir, "out.txt")) // the interrupted write didn't land
	store.last["r-1"] = ahead

	model.replies = []llm.ToolChatResult{{Text: "I checked; out.txt is missing, so I'll leave it to you."}}
	model.seen = nil
	ctx2 := agentevents.WithResume(agentevents.WithRunID(context.Background(), "r-2"), "r-1")
	reply, err := c.HandleMessage(ctx2, "/agent write out.txt", "res1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !os.IsNotExist(err) {
		t.Error("the interrupted write was repeated")
	}
	if !strings.Contains(reply, "Resuming") || !strings.Contains(reply, "outcome is unknown") {
		t.Errorf("reply = %q", reply)
	}
	if len(store.all["r-2"]) == 0 {
		t.Error("the resumed run saved no checkpoints of its own")
	}

	// A checkpoint can't be used by another session or task.
	ctx3 := agentevents.WithResume(agentevents.WithRunID(context.Background(), "r-3"), "r-1")
	if reply, _ := c.HandleMessage(ctx3, "/agent write out.txt", "other-session"); !strings.Contains(reply, "Can't resume") {
		t.Errorf("other session resumed: %q", reply)
	}
	if reply, _ := c.HandleMessage(ctx3, "/agent delete everything", "res1"); !strings.Contains(reply, "Can't resume") {
		t.Errorf("other task resumed: %q", reply)
	}
}

func TestRunCheckpoint_Message(t *testing.T) {
	cp := RunCheckpoint{Task: "fix the build", Check: "go test ./...", ReadOnly: true}
	f, rest, err := parseAgentFlags(strings.TrimPrefix(cp.Message(), "/agent "))
	if err != nil || rest != "fix the build" || f.check != "go test ./..." || !f.readOnly {
		t.Errorf("round trip: %+v %q %v", f, rest, err)
	}
}
