//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// Milestone B against a real server: one persistent approval service, first
// decision wins, expiry, action-hash binding, restart handling, and a
// channel (Telegram) deciding through the same path.

type approvalView struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	Status     string `json:"status"`
	ActionHash string `json:"action_hash"`
	DecidedBy  string `json:"decided_by"`
	DecidedVia string `json:"decided_via"`
	Reason     string `json:"reason"`
}

func getApproval(t *testing.T, p *testutil.VortexProcess, key, id string) approvalView {
	t.Helper()
	code, raw := apiCall(t, p, http.MethodGet, "/api/approvals/"+id, key, nil)
	if code != http.StatusOK {
		t.Fatalf("GET approval %s = %d %s", id, code, raw)
	}
	var a approvalView
	_ = json.Unmarshal(raw, &a)
	return a
}

// startWithPerson seeds the owner key and an operator person "ada" (added
// with the local CLI) and returns their keys.
func startWithPerson(t *testing.T) (map[string]string, *testutil.VortexProcess) {
	t.Helper()
	testutil.StartFakeAI(t)
	k := seedKeys(t, map[string]seededKey{"owner": {"admin", "setup key", []auth.Role{auth.RoleAdmin}}})
	keyFile := filepath.Join(t.TempDir(), "ada")
	if out, code := cli(t, t.TempDir(), "identity", "add-person", "ada", "--role", "operator", "--key-file", keyFile); code != 0 {
		t.Fatalf("add-person: %s", out)
	}
	raw, _ := os.ReadFile(keyFile)
	k["ada"] = strings.TrimSpace(string(raw))
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	return k, p
}

func auditCount(t *testing.T, p *testutil.VortexProcess, key, action string) int {
	t.Helper()
	_, raw := apiCall(t, p, http.MethodGet, "/api/audit?action="+action+"&limit=100", key, nil)
	var out struct {
		Entries []json.RawMessage `json:"entries"`
	}
	_ = json.Unmarshal(raw, &out)
	return len(out.Entries)
}

// TestApprovals_SimultaneousDecisions: the session owner and the server
// owner decide the same approval at once over both API forms; exactly one
// decision wins and the rest are refused.
func TestApprovals_SimultaneousDecisions(t *testing.T) {
	k, p := startWithPerson(t)
	defer p.Stop(t)
	done := submitAsync(t, p, k["ada"], "race-1", toolRun)
	item := waitPending(t, p, k["ada"], "race-1")
	a := getApproval(t, p, k["ada"], item.ActionID)
	if len(a.ActionHash) != 64 || a.Status != "pending" {
		t.Fatalf("approval = %+v", a)
	}

	type result struct {
		who  string
		code int
	}
	results := make(chan result, 24)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			who := []string{"ada", "owner"}[i%2]
			var code int
			if i%3 == 0 {
				code, _ = apiCall(t, p, http.MethodPost, "/api/agents/approve", k[who],
					map[string]any{"session_id": "race-1", "action_id": a.ID, "approved": who == "ada"})
			} else {
				code, _ = apiCall(t, p, http.MethodPost, "/api/approvals/"+a.ID+"/decision", k[who],
					map[string]any{"approved": who == "ada", "action_hash": a.ActionHash, "via": "dashboard"})
			}
			results <- result{who, code}
		}(i)
	}
	wg.Wait()
	close(results)
	wins, winner := 0, ""
	for r := range results {
		switch r.code {
		case http.StatusOK:
			wins++
			winner = r.who
		case http.StatusConflict:
		default:
			t.Errorf("%s got %d, want 200 or 409", r.who, r.code)
		}
	}
	if wins != 1 {
		t.Fatalf("%d decisions won, want exactly 1", wins)
	}
	final := getApproval(t, p, k["owner"], a.ID)
	wantStatus := map[string]string{"ada": "approved", "owner": "rejected"}[winner]
	if final.Status != wantStatus || final.DecidedBy == "" {
		t.Errorf("final = %+v, winner %s", final, winner)
	}
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish")
	}
	if n := auditCount(t, p, k["owner"], "approval."+final.Status); n != 1 {
		t.Errorf("audit has %d approval.%s entries, want 1", n, final.Status)
	}
}

// TestApprovals_Expiry: an approval nobody decides expires; a late decision
// is refused and the run is told no.
func TestApprovals_Expiry(t *testing.T) {
	t.Setenv("GUBERN_APPROVAL_TTL", "2s")
	k, p := startWithPerson(t)
	defer p.Stop(t)
	done := submitAsync(t, p, k["ada"], "exp-1", toolRun)
	item := waitPending(t, p, k["ada"], "exp-1")
	time.Sleep(2500 * time.Millisecond)
	a := getApproval(t, p, k["ada"], item.ActionID)
	code, raw := apiCall(t, p, http.MethodPost, "/api/approvals/"+item.ActionID+"/decision", k["ada"],
		map[string]any{"approved": true, "action_hash": a.ActionHash})
	if code != http.StatusConflict || !strings.Contains(string(raw), "expired") && !strings.Contains(string(raw), "not-pending") {
		t.Fatalf("late decision = %d %s", code, raw)
	}
	if a := getApproval(t, p, k["ada"], item.ActionID); a.Status != "expired" {
		t.Fatalf("status = %s, want expired", a.Status)
	}
	select {
	case res := <-done:
		if !strings.Contains(res.response, "xpired") && !strings.Contains(res.response, "fake-ai: done") {
			t.Errorf("run reply = %q", res.response)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run still waiting after expiry")
	}
	if code, raw := apiCall(t, p, http.MethodGet, "/api/agents/pending", k["ada"], nil); code != http.StatusOK || strings.Contains(string(raw), item.ActionID) {
		t.Errorf("expired approval still listed: %s", raw)
	}
}

// TestApprovals_ModifiedActionReplay: a decision must confirm the exact
// action; a mismatched hash, a missing hash and a replayed decision are all
// refused without changing anything.
func TestApprovals_ModifiedActionReplay(t *testing.T) {
	k, p := startWithPerson(t)
	defer p.Stop(t)
	done := submitAsync(t, p, k["ada"], "mod-1", toolRun)
	item := waitPending(t, p, k["ada"], "mod-1")
	a := getApproval(t, p, k["ada"], item.ActionID)
	path := "/api/approvals/" + a.ID + "/decision"

	forged := strings.Repeat("0", 64)
	if code, raw := apiCall(t, p, http.MethodPost, path, k["ada"], map[string]any{"approved": true, "action_hash": forged}); code != http.StatusConflict || !strings.Contains(string(raw), "action-mismatch") {
		t.Fatalf("forged hash = %d %s", code, raw)
	}
	if code, _ := apiCall(t, p, http.MethodPost, path, k["ada"], map[string]any{"approved": true}); code != http.StatusBadRequest {
		t.Fatalf("missing hash = %d, want 400", code)
	}
	if got := getApproval(t, p, k["ada"], a.ID); got.Status != "pending" {
		t.Fatalf("refused decisions changed status to %s", got.Status)
	}
	if code, raw := apiCall(t, p, http.MethodPost, path, k["ada"], map[string]any{"approved": true, "action_hash": a.ActionHash}); code != http.StatusOK {
		t.Fatalf("matching decision = %d %s", code, raw)
	}
	<-done
	// Replays of the same decision, through either API form, are refused.
	if code, _ := apiCall(t, p, http.MethodPost, path, k["ada"], map[string]any{"approved": true, "action_hash": a.ActionHash}); code != http.StatusConflict {
		t.Errorf("replay = %d, want 409", code)
	}
	if code, _ := apiCall(t, p, http.MethodPost, "/api/agents/approve", k["ada"], map[string]any{"session_id": "mod-1", "action_id": a.ID, "approved": true}); code != http.StatusConflict && code != http.StatusNotFound {
		t.Errorf("legacy replay = %d", code)
	}
	if got := getApproval(t, p, k["ada"], a.ID); got.Status != "approved" || got.DecidedBy == "" || got.DecidedVia != "api" {
		t.Errorf("final = %+v", got)
	}
}

// TestApprovals_RestartDuringApproval: the server dies (killed, no graceful
// shutdown) while an approval is open; the next start cancels it, deciding it
// afterwards is refused, and nothing runs. (A graceful stop cancels it too,
// as "run ended before a decision".)
func TestApprovals_RestartDuringApproval(t *testing.T) {
	k, p := startWithPerson(t)
	_ = submitAsync(t, p, k["ada"], "rs-1", toolRun)
	item := waitPending(t, p, k["ada"], "rs-1")
	hash := getApproval(t, p, k["ada"], item.ActionID).ActionHash
	if err := p.Cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = p.Cmd.Process.Wait()
	p.MarkStopped()

	p2 := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p2.Stop(t)
	if !strings.Contains(p2.Logs(), "approvals left pending by the previous run were cancelled") {
		t.Error("restart did not report the cancelled approvals")
	}
	a := getApproval(t, p2, k["ada"], item.ActionID)
	if a.Status != "cancelled" || a.Reason != "server restarted" {
		t.Fatalf("after restart = %+v", a)
	}
	code, raw := apiCall(t, p2, http.MethodPost, "/api/approvals/"+item.ActionID+"/decision", k["ada"],
		map[string]any{"approved": true, "action_hash": hash})
	if code != http.StatusConflict {
		t.Fatalf("decision after restart = %d %s", code, raw)
	}
	if code, raw := apiCall(t, p2, http.MethodGet, "/api/agents/pending", k["ada"], nil); code != http.StatusOK || strings.Contains(string(raw), item.ActionID) {
		t.Errorf("cancelled approval listed as pending: %s", raw)
	}
	if n := auditCount(t, p2, k["owner"], "approval.approved"); n != 0 {
		t.Errorf("an approval was approved across the restart (%d audit entries)", n)
	}
}

// fakeTelegramAPI records what the bot sends.
type fakeTelegramAPI struct {
	srv  *httptest.Server
	mu   sync.Mutex
	sent []map[string]any
}

func newFakeTelegramAPI(t *testing.T) *fakeTelegramAPI {
	f := &fakeTelegramAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			var m map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &m)
			f.mu.Lock()
			f.sent = append(f.sent, m)
			f.mu.Unlock()
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTelegramAPI) waitText(t *testing.T, substr string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, m := range f.sent {
			if s, _ := m["text"].(string); strings.Contains(s, substr) {
				f.mu.Unlock()
				return m
			}
		}
		f.mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("telegram never received %q", substr)
	return nil
}

// TestApprovals_TelegramDecidesThroughTheService: an approval started from
// the dashboard is announced on Telegram; a button press by an allowed user
// decides it through the same service (recorded as telegram:<id>), and a
// press from anyone else is refused.
func TestApprovals_TelegramDecidesThroughTheService(t *testing.T) {
	tg := newFakeTelegramAPI(t)
	t.Setenv("GUBERN_TELEGRAM_TOKEN", "123:test")
	t.Setenv("GUBERN_TELEGRAM_API_URL", tg.srv.URL)
	t.Setenv("GUBERN_TELEGRAM_DEFAULT_CHAT", "7")
	t.Setenv("GUBERN_TELEGRAM_ALLOWED_IDS", "7")
	t.Setenv("GUBERN_TELEGRAM_SECRET", "tg-secret")
	k, p := startWithPerson(t)
	defer p.Stop(t)

	press := func(from int64, data string) int {
		body := map[string]any{"update_id": time.Now().UnixNano(), "callback_query": map[string]any{
			"id": "cb", "data": data, "from": map[string]any{"id": from},
			"message": map[string]any{"chat": map[string]any{"id": 7}},
		}}
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, p.APIAddr+"/webhook/telegram", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "tg-secret")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	done := submitAsync(t, p, k["ada"], "tg-1", toolRun)
	item := waitPending(t, p, k["ada"], "tg-1")
	prompt := tg.waitText(t, item.ActionID)
	if raw, _ := json.Marshal(prompt["reply_markup"]); !strings.Contains(string(raw), "approve:"+item.ActionID) {
		t.Fatalf("prompt buttons = %s", raw)
	}
	// A user not on the allow-list is refused before reaching the service.
	if code := press(8, "approve:"+item.ActionID); code != http.StatusForbidden {
		t.Errorf("stranger press = %d, want 403", code)
	}
	if a := getApproval(t, p, k["ada"], item.ActionID); a.Status != "pending" {
		t.Fatalf("stranger changed status to %s", a.Status)
	}
	if code := press(7, "approve:"+item.ActionID); code != http.StatusOK {
		t.Fatalf("allowed press = %d", code)
	}
	select {
	case res := <-done:
		if !strings.Contains(res.response, "fake-ai: done") {
			t.Errorf("run reply = %q", res.response)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish after the Telegram approval")
	}
	a := getApproval(t, p, k["ada"], item.ActionID)
	if a.Status != "approved" || a.DecidedVia != "telegram" || a.DecidedBy != "telegram:7" {
		t.Fatalf("record = %+v", a)
	}
	tg.waitText(t, "Approved "+item.ActionID)
	// The dashboard can no longer decide it.
	if code, _ := apiCall(t, p, http.MethodPost, "/api/approvals/"+a.ID+"/decision", k["owner"],
		map[string]any{"approved": false, "action_hash": a.ActionHash}); code != http.StatusConflict {
		t.Errorf("dashboard after telegram = %d, want 409", code)
	}
}
