//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// apiCall sends method path with an optional API key and JSON body to the
// started server and returns the status and body. key == "" sends no
// credential.
func apiCall(t *testing.T, p *testutil.VortexProcess, method, path, key string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, p.APIAddr+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

type pendingItem struct {
	SessionID string `json:"session_id"`
	ActionID  string `json:"action_id"`
	Tool      string `json:"tool"`
	Command   string `json:"command"`
}

// waitPending polls GET /api/agents/pending with key until session has a
// pending action, and returns it.
func waitPending(t *testing.T, p *testutil.VortexProcess, key, session string) pendingItem {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		code, body := apiCall(t, p, http.MethodGet, "/api/agents/pending", key, nil)
		if code == http.StatusOK {
			var out struct {
				Pending []pendingItem `json:"pending"`
			}
			_ = json.Unmarshal(body, &out)
			for _, it := range out.Pending {
				if it.SessionID == session {
					return it
				}
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("no pending approval for session %s within 30s\n%s", session, p.Logs())
	return pendingItem{}
}

// submitAsync starts POST /api/agents/submit in the background; the returned
// channel yields the final status and response text.
func submitAsync(t *testing.T, p *testutil.VortexProcess, key, session, message string) <-chan submitResult {
	t.Helper()
	ch := make(chan submitResult, 1)
	go func() {
		b, _ := json.Marshal(map[string]string{"message": message, "session_id": session})
		req, _ := http.NewRequest(http.MethodPost, p.APIAddr+"/api/agents/submit", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
		if err != nil {
			ch <- submitResult{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			Response string `json:"response"`
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(raw, &out)
		ch <- submitResult{status: resp.StatusCode, response: out.Response, raw: string(raw)}
	}()
	return ch
}

type submitResult struct {
	status   int
	response string
	raw      string
	err      error
}

// TestFakeAI_AgentRunApprovalRoundTrip drives a real server with the fake AI
// provider: an /agent run proposes a command, the run pauses on a real pending
// approval, an API decision resolves it, and the run finishes with the fake's
// deterministic answer.
func TestFakeAI_AgentRunApprovalRoundTrip(t *testing.T) {
	fake := testutil.StartFakeAI(t)
	key := seedAdminKey(t)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)

	const session = "fake-ai-1"
	done := submitAsync(t, p, key, session,
		`/agent say hi [[tool:run_terminal {"command":"echo gubern-fake"}]]`)

	item := waitPending(t, p, key, session)
	if item.Tool != "run_terminal" || item.ActionID == "" {
		t.Fatalf("pending = %+v, want run_terminal with an action id", item)
	}
	code, body := apiCall(t, p, http.MethodPost, "/api/agents/approve", key,
		map[string]any{"session_id": session, "action_id": item.ActionID, "approved": true})
	if code != http.StatusOK {
		t.Fatalf("approve = %d %s", code, body)
	}

	select {
	case res := <-done:
		if res.err != nil || res.status != http.StatusOK {
			t.Fatalf("submit = %d %v %s", res.status, res.err, res.raw)
		}
		if !strings.Contains(res.response, "fake-ai: done") {
			t.Errorf("run reply lacks the fake's final answer:\n%s", res.response)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish after approval")
	}
	if got := fake.ProposedTools(); len(got) != 1 || got[0] != "run_terminal" {
		t.Errorf("proposed tools = %v, want [run_terminal]", got)
	}
}

// TestFakeAI_ChatIsDeterministic checks a plain chat reply comes from the fake
// provider, not a real model.
func TestFakeAI_ChatIsDeterministic(t *testing.T) {
	fake := testutil.StartFakeAI(t)
	key := seedAdminKey(t)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)

	code, body := apiCall(t, p, http.MethodPost, "/api/agents/submit", key,
		map[string]string{"message": "hello there [[reply:pong-42]]", "session_id": "fake-ai-2"})
	if code != http.StatusOK {
		t.Fatalf("submit = %d %s", code, body)
	}
	if !strings.Contains(string(body), "pong-42") {
		t.Errorf("reply = %s, want the fake's scripted pong-42", body)
	}
	if gen, _ := fake.Counts(); gen == 0 {
		t.Error("the fake provider was never called")
	}
}
