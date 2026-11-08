package agents

import (
	"context"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/llm"
)

// TestRuntime_CarriesRunIdentity: a run submitted through the runtime (the
// API's path) is checkpointed under its event-log run id. The runtime runs
// requests under its own context, which used to drop everything but the
// event sink, so no checkpoint was ever saved.
func TestRuntime_CarriesRunIdentity(t *testing.T) {
	dir := t.TempDir()
	reg := NewToolRegistry()
	if err := RegisterLocalTools(reg, LocalFSConfig{Root: dir}); err != nil {
		t.Fatal(err)
	}
	bus := NewBus()
	c, err := NewCoordinator(CoordinatorConfig{Bus: bus, AIGateway: toolGateway{&scriptedModel{replies: []llm.ToolChatResult{{Text: "done"}}}},
		LocalTools: reg, WorkingDir: dir, MaxAgents: 2})
	if err != nil {
		t.Fatal(err)
	}
	store := &memCheckpoints{all: map[string][]RunCheckpoint{}, last: map[string]RunCheckpoint{}}
	c.SetRunCheckpoints(store)
	rt, err := NewRuntime(RuntimeConfig{Bus: bus, Coordinator: c, MaxAgents: 2, MaxConcurrent: 4, SandboxBase: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := agentevents.WithRunID(context.Background(), "r-api")
	ch, err := rt.Submit(ctx, "/agent say hi", "rt1")
	if err != nil {
		t.Fatal(err)
	}
	timeout := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case _, ok := <-ch:
			done = !ok
		case <-timeout:
			t.Fatal("run did not finish")
		}
	}
	if len(store.all["r-api"]) == 0 {
		t.Fatalf("no checkpoint under the API's run id (saved under: %v)", keys(store.all))
	}
}

func keys(m map[string][]RunCheckpoint) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
