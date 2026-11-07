package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

// eventRuntime emits structured events through the request's sink, the way
// the real coordinator does, and lists one pending approval.
type eventRuntime struct{ stubRuntime }

func (eventRuntime) Submit(ctx context.Context, _, sessionID string) (<-chan string, error) {
	ch := make(chan string, 4)
	go func() {
		defer close(ch)
		agentevents.Emit(ctx, agentevents.Event{Type: agentevents.Text, SessionID: sessionID, Text: "Looking. "})
		ch <- "Looking. "
		agentevents.Emit(ctx, agentevents.Event{Type: agentevents.ToolStart, SessionID: sessionID, StepID: "st-1", Tool: "read_file"})
		agentevents.Emit(ctx, agentevents.Event{Type: agentevents.ToolEnd, SessionID: sessionID, StepID: "st-1", Tool: "read_file", Status: "ok"})
		agentevents.Emit(ctx, agentevents.Event{Type: agentevents.ApprovalRequired, SessionID: sessionID, Approval: &agentevents.Pending{ActionID: "abc", Tool: "run_terminal", Risk: "HIGH RISK"}})
	}()
	return ch, nil
}

func (eventRuntime) PendingApprovals() []agentevents.Pending {
	return []agentevents.Pending{{SessionID: "s1", ActionID: "abc", Tool: "run_terminal", Risk: "HIGH RISK", ExpiresAt: time.Now().Add(time.Minute)}}
}

func readSSE(t *testing.T, resp *http.Response) []string {
	t.Helper()
	var events []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			events = append(events, name)
			if name == "done" {
				break
			}
		}
	}
	return events
}

func TestAgentSubmit_EventStreamIsOrderedAndTerminated(t *testing.T) {
	addr := newAgentTestServer(t, eventRuntime{})
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/api/agents/submit?events=1", strings.NewReader(`{"message":"hi","session_id":"s1"}`))
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	got := strings.Join(readSSE(t, resp), ",")
	if want := "text,tool_start,tool_end,approval_required,done"; got != want {
		t.Errorf("events = %s, want %s", got, want)
	}
}

// Without events=1 the classic chunk stream is unchanged (TUI, scripts).
func TestAgentSubmit_ClassicStreamUnchanged(t *testing.T) {
	addr := newAgentTestServer(t, eventRuntime{})
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/api/agents/submit", strings.NewReader(`{"message":"hi","session_id":"s1"}`))
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	sc := bufio.NewScanner(resp.Body)
	var sawChunk bool
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: tool_start") || strings.HasPrefix(line, "event: approval_required") {
			t.Fatalf("structured event leaked into the classic stream: %q", line)
		}
		if strings.Contains(line, `"chunk":"Looking. "`) {
			sawChunk = true
		}
		if line == "event: done" {
			break
		}
	}
	if !sawChunk {
		t.Error("classic chunk missing")
	}
}

func TestAgentPending_ListsPendingActions(t *testing.T) {
	addr := newAgentTestServer(t, eventRuntime{})
	resp, err := http.Get("http://" + addr + "/api/agents/pending")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Pending []agentevents.Pending `json:"pending"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Pending) != 1 || body.Pending[0].ActionID != "abc" || body.Pending[0].Risk != "HIGH RISK" {
		t.Errorf("pending = %+v", body.Pending)
	}
	// A runtime that can't list pending actions yields an empty list, not null.
	addr2 := newAgentTestServer(t, stubRuntime{})
	resp2, err := http.Get("http://" + addr2 + "/api/agents/pending")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(resp2.Body).Decode(&raw)
	if string(raw["pending"]) != "[]" {
		t.Errorf("pending = %s, want []", raw["pending"])
	}
}
