//go:build integration

package integration

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// Milestone I against a real server: Discord interactions, verified with the
// application key, decide approvals through the shared service and follow
// the same identity rules as Telegram and WhatsApp.
func TestDiscord_SameIdentityAndApprovalRules(t *testing.T) {
	testutil.StartFakeAI(t)
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	var mu sync.Mutex
	var notices []string
	fakeAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		notices = append(notices, string(b))
		mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	}))
	defer fakeAPI.Close()
	t.Setenv("GUBERN_DISCORD_PUBLIC_KEY", hex.EncodeToString(pk))
	t.Setenv("GUBERN_DISCORD_BOT_TOKEN", "bot-token")
	t.Setenv("GUBERN_DISCORD_APPROVAL_CHANNEL", "chan1")
	t.Setenv("GUBERN_DISCORD_ALLOWED_USERS", "111")
	t.Setenv("GUBERN_DISCORD_API_URL", fakeAPI.URL)
	k := seedKeys(t, map[string]seededKey{"owner": {"admin", "setup key", []auth.Role{auth.RoleAdmin}}})
	keyFile := filepath.Join(t.TempDir(), "bob")
	if out, c := cli(t, t.TempDir(), "identity", "add-person", "bob", "--role", "operator", "--key-file", keyFile); c != 0 {
		t.Fatalf("add-person: %s", out)
	}
	bk, _ := os.ReadFile(keyFile)
	k["bob"] = strings.TrimSpace(string(bk))
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)

	interact := func(body map[string]any, forge bool) (int, string) {
		raw, _ := json.Marshal(body)
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		sig := ed25519.Sign(sk, append([]byte(ts), raw...))
		if forge {
			sig[0] ^= 0xff
		}
		req, _ := http.NewRequest(http.MethodPost, p.APIAddr+"/webhook/discord", bytes.NewReader(raw))
		req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
		req.Header.Set("X-Signature-Timestamp", ts)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		out, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(out)
	}
	press := func(user, custom string, forge bool) (int, string) {
		return interact(map[string]any{"type": 3, "member": map[string]any{"user": map[string]any{"id": user}}, "data": map[string]any{"custom_id": custom}}, forge)
	}
	cmd := func(user, name, opt, val string) string {
		_, out := interact(map[string]any{"type": 2, "user": map[string]any{"id": user}, "data": map[string]any{"name": name, "options": []map[string]any{{"name": opt, "value": val}}}}, false)
		return out
	}
	newCode := func(key string) string {
		_, raw := apiCall(t, p, http.MethodPost, "/api/pairing", key, map[string]any{"purpose": "discord"})
		var pr struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &pr)
		return pr.Code
	}

	if c, out := interact(map[string]any{"type": 1}, false); c != http.StatusOK || !strings.Contains(out, `"type":1`) {
		t.Fatalf("ping = %d %s", c, out)
	}

	// A real run's approval is announced in Discord with buttons.
	done := submitAsync(t, p, k["owner"], "dc-1", toolRun)
	item := waitPending(t, p, k["owner"], "dc-1")
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		got := strings.Join(notices, "\n")
		mu.Unlock()
		if strings.Contains(got, "approve:"+item.ActionID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no Discord notice for %s: %s", item.ActionID, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Forged signature and a stranger are refused; nothing changes.
	if c, _ := press("111", "approve:"+item.ActionID, true); c != http.StatusUnauthorized {
		t.Errorf("forged interaction = %d", c)
	}
	if _, out := press("999", "approve:"+item.ActionID, false); !strings.Contains(out, "not allowed") {
		t.Errorf("stranger = %s", out)
	}
	if a := getApproval(t, p, k["owner"], item.ActionID); a.Status != "pending" {
		t.Fatalf("refused presses changed status to %s", a.Status)
	}
	// The allow-listed user approves; first decision wins.
	if _, out := press("111", "approve:"+item.ActionID, false); !strings.Contains(out, "Approved") {
		t.Fatalf("allowed press = %s", out)
	}
	select {
	case res := <-done:
		if !strings.Contains(res.response, "fake-ai: done") {
			t.Errorf("run reply = %q", res.response)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish after the Discord approval")
	}
	a := getApproval(t, p, k["owner"], item.ActionID)
	if a.Status != "approved" || a.DecidedVia != "discord" || a.DecidedBy != "discord:111" {
		t.Fatalf("record = %+v", a)
	}
	if c, _ := apiCall(t, p, http.MethodPost, "/api/approvals/"+a.ID+"/decision", k["owner"], map[string]any{"approved": false, "action_hash": a.ActionHash}); c != http.StatusConflict {
		t.Errorf("dashboard after Discord = %d, want 409", c)
	}

	// Pairing: a stranger links to the owner; the same account can't be taken
	// by Bob's code; a linked user decides as the person.
	if out := cmd("777", "pair", "code", newCode(k["owner"])); !strings.Contains(out, "now linked") {
		t.Fatalf("pair = %s", out)
	}
	if out := cmd("777", "pair", "code", newCode(k["bob"])); !strings.Contains(out, "someone else") {
		t.Fatalf("conflict = %s", out)
	}
	done2 := submitAsync(t, p, k["owner"], "dc-2", toolRun)
	item2 := waitPending(t, p, k["owner"], "dc-2")
	if _, out := press("777", "reject:"+item2.ActionID, false); !strings.Contains(out, "Rejected") {
		t.Fatalf("linked reject = %s", out)
	}
	<-done2
	if a := getApproval(t, p, k["owner"], item2.ActionID); a.DecidedBy != "owner" || a.DecidedVia != "discord" {
		t.Errorf("linked decision recorded as %+v", a)
	}

	// Revocation beats the allow-list: link 111 to Bob, revoke, refused.
	if out := cmd("111", "pair", "code", newCode(k["bob"])); !strings.Contains(out, "now linked") {
		t.Fatalf("pair 111 = %s", out)
	}
	if c, raw := apiCall(t, p, http.MethodDelete, "/api/links/discord/111", k["bob"], nil); c != http.StatusOK {
		t.Fatalf("unlink = %d %s", c, raw)
	}
	if out := cmd("111", "approve", "id", item2.ActionID); !strings.Contains(out, "not registered") {
		t.Errorf("revoked, allow-listed user = %s", out)
	}
}
