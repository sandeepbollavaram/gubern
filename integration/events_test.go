//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// Milestone D against a real server: recorded, redacted, resumable event
// streams.

type sseEvent struct {
	id, event string
	data      map[string]any
	ping      bool
}

// sseReader reads SSE frames from a response body.
type sseReader struct {
	sc   *bufio.Scanner
	body io.Closer
}

func openSSE(t *testing.T, ctx context.Context, method, url, key string, body any, headers map[string]string) (*http.Response, *sseReader) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, url, rd)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("%s %s = %d %s", method, url, resp.StatusCode, raw)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	return resp, &sseReader{sc: sc, body: resp.Body}
}

// next returns the next frame (an event, or a heartbeat comment).
func (s *sseReader) next() (sseEvent, bool) {
	var ev sseEvent
	got := false
	for s.sc.Scan() {
		line := s.sc.Text()
		switch {
		case line == "":
			if got {
				return ev, true
			}
		case strings.HasPrefix(line, ": "):
			return sseEvent{ping: true}, true
		case strings.HasPrefix(line, "id: "):
			ev.id, got = strings.TrimPrefix(line, "id: "), true
		case strings.HasPrefix(line, "event: "):
			ev.event, got = strings.TrimPrefix(line, "event: "), true
		case strings.HasPrefix(line, "data: "):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev.data)
			got = true
		}
	}
	return ev, false
}

func eventsServer(t *testing.T) (string, *testutil.VortexProcess) {
	t.Helper()
	testutil.StartFakeAI(t)
	key := seedAdminKey(t)
	return key, testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
}

// TestEvents_DisconnectAndResume: a client drops mid-run (while the run waits
// on an approval), reconnects with Last-Event-ID, and receives exactly the
// events it missed and the rest of the run: no gap, no duplicate, ids
// increasing by one.
func TestEvents_DisconnectAndResume(t *testing.T) {
	key, p := eventsServer(t)
	defer p.Stop(t)

	ctx, cancel := context.WithCancel(context.Background())
	resp, rd := openSSE(t, ctx, http.MethodPost, p.APIAddr+"/api/agents/submit?events=1", key,
		map[string]string{"message": toolRun, "session_id": "ev-1"}, nil)
	runID := resp.Header.Get("X-Gubern-Run-Id")
	if !strings.HasPrefix(runID, "r-") {
		t.Fatalf("run id header = %q", runID)
	}
	var seen []int64
	var approvalID string
	for approvalID == "" {
		ev, ok := rd.next()
		if !ok {
			t.Fatal("stream ended before the approval")
		}
		if ev.ping {
			continue
		}
		n, _ := strconv.ParseInt(ev.id, 10, 64)
		seen = append(seen, n)
		if ev.event == "approval_required" {
			approvalID, _ = ev.data["approval"].(map[string]any)["action_id"].(string)
		}
	}
	cancel() // the client goes away mid-run
	_ = rd.body.Close()

	// The run is still alive; decide its approval while nobody watches.
	if code, raw := apiCall(t, p, http.MethodPost, "/api/agents/approve", key,
		map[string]any{"session_id": "ev-1", "action_id": approvalID, "approved": true}); code != http.StatusOK {
		t.Fatalf("approve = %d %s", code, raw)
	}

	last := seen[len(seen)-1]
	_, rd2 := openSSE(t, context.Background(), http.MethodGet, p.APIAddr+"/api/runs/"+runID+"/events", key, nil,
		map[string]string{"Last-Event-ID": strconv.FormatInt(last, 10)})
	var done map[string]any
	for done == nil {
		ev, ok := rd2.next()
		if !ok {
			t.Fatal("resumed stream ended without done")
		}
		switch {
		case ev.ping:
		case ev.event == "done":
			done = ev.data
		default:
			n, _ := strconv.ParseInt(ev.id, 10, 64)
			seen = append(seen, n)
		}
	}
	_ = rd2.body.Close()
	for i, n := range seen {
		if n != int64(i+1) {
			t.Fatalf("event ids %v have a gap or duplicate at %d", seen, i)
		}
	}
	if done["status"] != "done" {
		t.Errorf("done = %v", done)
	}
	// The run is recorded and its timeline reads back.
	code, raw := apiCall(t, p, http.MethodGet, "/api/activity?session_id=ev-1", key, nil)
	if code != http.StatusOK || !strings.Contains(string(raw), "approval_decided") || !strings.Contains(string(raw), "run_finished") {
		t.Errorf("activity = %d %s", code, raw)
	}
}

// TestEvents_NoSecretsInPayloads: a secret in the model's reply is redacted
// in the live stream, in the stored events and on disk.
func TestEvents_NoSecretsInPayloads(t *testing.T) {
	key, p := eventsServer(t)
	secret := "sk-ant-api03-" + strings.Repeat("Z", 40)
	resp, rd := openSSE(t, context.Background(), http.MethodPost, p.APIAddr+"/api/agents/submit?events=1", key,
		map[string]string{"message": "show config [[reply:the key is " + secret + "]]", "session_id": "ev-2"}, nil)
	runID := resp.Header.Get("X-Gubern-Run-Id")
	var streamed strings.Builder
	for {
		ev, ok := rd.next()
		if !ok || ev.event == "done" {
			break
		}
		b, _ := json.Marshal(ev.data)
		streamed.Write(b)
	}
	_ = rd.body.Close()
	if strings.Contains(streamed.String(), secret) || !strings.Contains(streamed.String(), "[REDACTED]") {
		t.Fatalf("live stream = %s", streamed.String())
	}
	_, raw := apiCall(t, p, http.MethodGet, "/api/runs/"+runID+"/events", key, nil)
	if strings.Contains(string(raw), secret) {
		t.Fatal("stored events contain the secret")
	}
	p.Stop(t)
	// Nothing on disk holds it either (the database and its WAL).
	dir := filepath.Dir(os.Getenv("GUBERN_APIKEY_STORE"))
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasPrefix(info.Name(), "events.db") {
			b, _ := os.ReadFile(path)
			if bytes.Contains(b, []byte(secret)) {
				t.Errorf("%s contains the secret", info.Name())
			}
		}
		return nil
	})
}

// TestEvents_HeartbeatCancelAndDuplicates: idle streams get heartbeats; a
// duplicate submit with the same Idempotency-Key attaches to the same run;
// cancelling a run ends it, and its open approval, as cancelled.
func TestEvents_HeartbeatCancelAndDuplicates(t *testing.T) {
	t.Setenv("GUBERN_SSE_HEARTBEAT", "1s")
	key, p := eventsServer(t)
	defer p.Stop(t)
	hdr := map[string]string{"Idempotency-Key": "submit-42"}
	body := map[string]string{"message": toolRun, "session_id": "ev-3"}

	resp, rd := openSSE(t, context.Background(), http.MethodPost, p.APIAddr+"/api/agents/submit?events=1", key, body, hdr)
	runID := resp.Header.Get("X-Gubern-Run-Id")
	pings, approval := 0, false
	deadline := time.Now().Add(20 * time.Second)
	for (pings == 0 || !approval) && time.Now().Before(deadline) {
		ev, ok := rd.next()
		if !ok {
			t.Fatal("stream ended")
		}
		if ev.ping {
			pings++
		}
		if ev.event == "approval_required" {
			approval = true
		}
	}
	if pings == 0 || !approval {
		t.Fatalf("pings=%d approval=%v", pings, approval)
	}

	// The same submit again (a retry after a dropped connection) joins the
	// same run and replays it; no second run starts.
	resp2, rd2 := openSSE(t, context.Background(), http.MethodPost, p.APIAddr+"/api/agents/submit?events=1", key, body, hdr)
	if got := resp2.Header.Get("X-Gubern-Run-Id"); got != runID {
		t.Fatalf("duplicate submit run = %s, want %s", got, runID)
	}
	if ev, _ := rd2.next(); ev.id != "1" || ev.event != "run_started" {
		t.Errorf("duplicate stream starts with %+v", ev)
	}
	_ = rd2.body.Close()
	_, raw := apiCall(t, p, http.MethodGet, "/api/runs?session_id=ev-3", key, nil)
	var runs struct {
		Runs []map[string]any `json:"runs"`
	}
	_ = json.Unmarshal(raw, &runs)
	if len(runs.Runs) != 1 {
		t.Fatalf("runs = %s", raw)
	}

	if code, raw := apiCall(t, p, http.MethodPost, "/api/runs/"+runID+"/cancel", key, nil); code != http.StatusAccepted {
		t.Fatalf("cancel = %d %s", code, raw)
	}
	var done map[string]any
	for done == nil {
		ev, ok := rd.next()
		if !ok {
			t.Fatal("stream ended without done")
		}
		if ev.event == "done" {
			done = ev.data
		}
	}
	_ = rd.body.Close()
	if done["status"] != "cancelled" {
		t.Errorf("done = %v", done)
	}
	_, raw = apiCall(t, p, http.MethodGet, "/api/approvals?session_id=ev-3", key, nil)
	if !strings.Contains(string(raw), `"status":"cancelled"`) {
		t.Errorf("approval after cancel = %s", raw)
	}
	if code, _ := apiCall(t, p, http.MethodPost, "/api/runs/"+runID+"/cancel", key, nil); code != http.StatusConflict {
		t.Errorf("second cancel = %d, want 409", code)
	}
}

// TestEvents_ClassicStreamUnchanged: without ?events=1 the chunk stream keeps
// its old shape (data: chunks, then event: done) and records nothing.
func TestEvents_ClassicStreamUnchanged(t *testing.T) {
	key, p := eventsServer(t)
	defer p.Stop(t)
	resp, rd := openSSE(t, context.Background(), http.MethodPost, p.APIAddr+"/api/agents/submit", key,
		map[string]string{"message": "hi [[reply:classic-ok]]", "session_id": "ev-4"}, nil)
	if resp.Header.Get("X-Gubern-Run-Id") != "" {
		t.Error("classic stream started a logged run")
	}
	var all strings.Builder
	sawDone := false
	for {
		ev, ok := rd.next()
		if !ok {
			break
		}
		if ev.event == "done" {
			sawDone = true
			break
		}
		if ev.id != "" {
			t.Errorf("classic frame has an id: %+v", ev)
		}
		b, _ := json.Marshal(ev.data)
		all.Write(b)
	}
	_ = rd.body.Close()
	if !sawDone {
		t.Error("no done event")
	}
	_, raw := apiCall(t, p, http.MethodGet, "/api/runs?session_id=ev-4", key, nil)
	if strings.Contains(string(raw), "ev-4") {
		t.Errorf("classic stream was recorded: %s", raw)
	}
}
