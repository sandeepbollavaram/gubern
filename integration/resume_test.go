//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// The Phase 3 gate (docs/design/agent-platform-phases-2-5.md §3): kill the
// server at different points of 20 agent runs, restart it, resume what was
// interrupted, and check that no side effect ever happened twice and no run
// was left interrupted.

// startRun submits an /agent run on the event stream and returns its run id;
// the body is drained in the background until the server goes away.
func startRun(t *testing.T, p *testutil.VortexProcess, key, path string, body any) string {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPost, p.APIAddr+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("%s = %d: %s", path, resp.StatusCode, b)
	}
	id := resp.Header.Get("X-Gubern-Run-Id")
	go func() {
		defer func() { _ = resp.Body.Close() }()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() { //nolint:revive // drain until the stream or the server ends
		}
	}()
	return id
}

type runState struct {
	Status string `json:"status"`
}

func runStatus(t *testing.T, p *testutil.VortexProcess, key, id string) string {
	t.Helper()
	code, body := apiCall(t, p, http.MethodGet, "/api/runs/"+id, key, nil)
	if code != http.StatusOK {
		t.Fatalf("GET run %s = %d %s", id, code, body)
	}
	var r runState
	_ = json.Unmarshal(body, &r)
	return r.Status
}

var liveRunStatus = map[string]bool{"running": true, "waiting_approval": true, "verifying": true}

// approvePending approves whatever the session has pending; it reports how
// many it approved.
func approvePending(t *testing.T, p *testutil.VortexProcess, key, session string) int {
	t.Helper()
	code, body := apiCall(t, p, http.MethodGet, "/api/approvals?status=pending&session_id="+session, key, nil)
	if code != http.StatusOK {
		return 0
	}
	var out struct {
		Approvals []struct {
			ID         string `json:"id"`
			ActionHash string `json:"action_hash"`
		} `json:"approvals"`
	}
	_ = json.Unmarshal(body, &out)
	for _, a := range out.Approvals {
		apiCall(t, p, http.MethodPost, "/api/approvals/"+a.ID+"/decision", key,
			map[string]any{"approved": true, "action_hash": a.ActionHash, "via": "api"})
	}
	return len(out.Approvals)
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("timed out waiting for %s", what)
	return false
}

// finishRun drives a run to a final status, approving what it asks for.
func finishRun(t *testing.T, p *testutil.VortexProcess, key, session, id string) string {
	t.Helper()
	var st string
	waitFor(t, "run "+id+" to finish", 60*time.Second, func() bool {
		approvePending(t, p, key, session)
		st = runStatus(t, p, key, id)
		return !liveRunStatus[st]
	})
	return st
}

func TestResume_KillNineNeverRepeatsASideEffect(t *testing.T) {
	testutil.StartFakeAI(t)
	key := seedAdminKey(t)
	work := t.TempDir()
	t.Setenv("GUBERN_WORK_DIR", work)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	ledger := filepath.Join(work, "ledger.txt")

	command := func(i int) string {
		// Slow enough that a kill can land while it runs.
		if runtime.GOOS == "windows" {
			return fmt.Sprintf("ping -n 2 127.0.0.1 >NUL && echo run-%02d>>ledger.txt", i)
		}
		return fmt.Sprintf("sleep 1 && echo run-%02d >> ledger.txt", i)
	}

	const runs = 20
	killed, resumed := 0, 0
	for i := 0; i < runs; i++ {
		session := fmt.Sprintf("k9-%02d", i)
		msg := fmt.Sprintf(`/agent append to the ledger [[tool:run_terminal {"command":%q}]]`, command(i))
		id := startRun(t, p, key, "/api/agents/submit?events=1", map[string]any{"message": msg, "session_id": session})
		if !waitFor(t, "approval for "+session, 30*time.Second, func() bool { return runStatus(t, p, key, id) == "waiting_approval" }) {
			t.Fatalf("run %d never asked for approval", i)
		}

		mode := i % 4
		switch mode {
		case 0: // killed while waiting for the approval: nothing has run
		case 1: // killed right after the approval: the command is in flight
			approvePending(t, p, key, session)
			time.Sleep(150 * time.Millisecond)
		case 2: // killed after the command probably finished
			approvePending(t, p, key, session)
			time.Sleep(2500 * time.Millisecond)
		case 3: // not killed
			approvePending(t, p, key, session)
			if st := finishRun(t, p, key, session, id); liveRunStatus[st] || st == "interrupted" {
				t.Errorf("run %d ended %q", i, st)
			}
			continue
		}

		p.Kill(t)
		killed++
		p = p.Restart(t)
		st := runStatus(t, p, key, id)
		if st != "interrupted" {
			// It finished before the kill landed.
			if liveRunStatus[st] {
				t.Errorf("run %d is %q after a restart", i, st)
			}
			continue
		}
		rid := startRun(t, p, key, "/api/runs/"+id+"/resume", nil)
		resumed++
		if st := finishRun(t, p, key, session, rid); liveRunStatus[st] || st == "interrupted" {
			t.Errorf("resumed run %d ended %q", i, st)
		}
	}

	// Give commands orphaned by a kill time to land before counting.
	time.Sleep(3 * time.Second)
	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("reading the ledger: %v", err)
	}
	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l := strings.TrimSpace(line); l != "" {
			counts[l]++
		}
	}
	repeated := 0
	for line, n := range counts {
		if n > 1 {
			repeated++
			t.Errorf("%s happened %d times; a side effect must happen at most once", line, n)
		}
	}
	t.Logf("%d runs, %d killed, %d resumed, %d side effects recorded, %d repeated", runs, killed, resumed, len(counts), repeated)
	if killed == 0 || resumed == 0 || len(counts) == 0 {
		t.Errorf("the test exercised nothing: killed=%d resumed=%d effects=%d", killed, resumed, len(counts))
	}
}
