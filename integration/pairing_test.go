//go:build integration

package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// Milestone F against a real server: device pairing (QR, replay, expiry,
// guessing, revocation) and channel pairing with identity conflicts.

func pairKey(t *testing.T) string {
	pk, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pk)
}

func TestPairing_DeviceLifecycle(t *testing.T) {
	key := seedAdminKey(t)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)

	code, raw := apiCall(t, p, http.MethodPost, "/api/pairing", key, map[string]any{"purpose": "device", "label": "phone"})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, raw)
	}
	var pr struct {
		Code string `json:"code"`
		QR   string `json:"qr"`
	}
	_ = json.Unmarshal(raw, &pr)
	// The QR carries the server address and the code; a scanner reads it.
	u, err := url.Parse(pr.QR)
	if err != nil || u.Scheme != "gubern" || u.Query().Get("c") != pr.Code || u.Query().Get("u") == "" {
		t.Fatalf("qr = %q", pr.QR)
	}
	redeem := func(c string) (int, map[string]any) {
		code, raw := apiCall(t, p, http.MethodPost, "/api/pairing/redeem", "", map[string]string{"code": c, "label": "Ada phone", "public_key": pairKey(t)})
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return code, m
	}
	st, dev := redeem(u.Query().Get("c"))
	if st != http.StatusCreated {
		t.Fatalf("redeem = %d %v", st, dev)
	}
	secret, _ := dev["secret"].(string)
	devID, _ := dev["device_id"].(string)
	if code, _ := apiCall(t, p, http.MethodGet, "/api/agents/pending", secret, nil); code != http.StatusOK {
		t.Errorf("device reads approvals = %d", code)
	}
	if code, _ := apiCall(t, p, http.MethodGet, "/api/keys", secret, nil); code != http.StatusForbidden {
		t.Errorf("device beyond its ceiling = %d, want 403", code)
	}
	// QR replay: a second scan of the same code gets nothing.
	if st, _ := redeem(pr.Code); st != http.StatusBadRequest {
		t.Errorf("replayed QR = %d, want 400", st)
	}
	// Revoking the device stops its credential on the next request.
	if code, raw := apiCall(t, p, http.MethodDelete, "/api/devices/"+devID, key, nil); code != http.StatusOK {
		t.Fatalf("revoke = %d %s", code, raw)
	}
	if code, _ := apiCall(t, p, http.MethodGet, "/api/agents/pending", secret, nil); code != http.StatusUnauthorized {
		t.Errorf("revoked device = %d, want 401", code)
	}
	if strings.Contains(p.Logs(), secret) {
		t.Error("the device secret appears in the server log")
	}
}

func TestPairing_GuessingIsRateLimited(t *testing.T) {
	_ = seedAdminKey(t)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)
	limited := false
	for i := 0; i < 25 && !limited; i++ {
		code, _ := apiCall(t, p, http.MethodPost, "/api/pairing/redeem", "", map[string]string{"code": fmt.Sprintf("ZZZZ-ZZZZ-%04d", i), "label": "x", "public_key": pairKey(t)})
		switch code {
		case http.StatusTooManyRequests:
			limited = true
		case http.StatusBadRequest:
		default:
			t.Fatalf("guess %d = %d", i, code)
		}
	}
	if !limited {
		t.Fatal("guessing was never rate limited")
	}
}

func TestPairing_ExpiredCodeRealServer(t *testing.T) {
	t.Setenv("GUBERN_PAIRING_TTL", "2s")
	key := seedAdminKey(t)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)
	code, raw := apiCall(t, p, http.MethodPost, "/api/pairing", key, map[string]any{"purpose": "device"})
	if code != http.StatusCreated {
		t.Fatalf("create = %d %s", code, raw)
	}
	var pr struct {
		Code      string    `json:"code"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	_ = json.Unmarshal(raw, &pr)
	if until := time.Until(pr.ExpiresAt); until > 3*time.Second {
		t.Errorf("code lifetime = %s, want 2s", until)
	}
	time.Sleep(2500 * time.Millisecond)
	if code, raw := apiCall(t, p, http.MethodPost, "/api/pairing/redeem", "", map[string]string{"code": pr.Code, "label": "late", "public_key": pairKey(t)}); code != http.StatusBadRequest {
		t.Errorf("expired code = %d %s, want 400", code, raw)
	}
}

// TestPairing_TelegramLinkConflictAndRevocation: a stranger links their
// Telegram account with /pair; the same account can't be linked to a second
// person; a code for another channel is refused; revoking the link shuts the
// account out even though it is on the allow-list.
func TestPairing_TelegramLinkConflictAndRevocation(t *testing.T) {
	tg := newFakeTelegramAPI(t)
	t.Setenv("GUBERN_TELEGRAM_TOKEN", "123:test")
	t.Setenv("GUBERN_TELEGRAM_API_URL", tg.srv.URL)
	t.Setenv("GUBERN_TELEGRAM_DEFAULT_CHAT", "7")
	t.Setenv("GUBERN_TELEGRAM_ALLOWED_IDS", "7,555")
	t.Setenv("GUBERN_TELEGRAM_SECRET", "tg-secret")
	k := seedKeys(t, map[string]seededKey{"owner": {"admin", "setup key", []auth.Role{auth.RoleAdmin}}})
	keyFile := filepath.Join(t.TempDir(), "bob")
	if out, c := cli(t, t.TempDir(), "identity", "add-person", "bob", "--role", "operator", "--key-file", keyFile); c != 0 {
		t.Fatalf("add-person: %s", out)
	}
	bk, _ := os.ReadFile(keyFile)
	k["bob"] = strings.TrimSpace(string(bk))
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)

	send := func(from int64, text string) int {
		body, _ := json.Marshal(map[string]any{"update_id": time.Now().UnixNano(), "message": map[string]any{
			"chat": map[string]any{"id": from}, "from": map[string]any{"id": from}, "text": text,
		}})
		req, _ := http.NewRequest(http.MethodPost, p.APIAddr+"/webhook/telegram", strings.NewReader(string(body)))
		req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "tg-secret")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	newCode := func(key, purpose string) string {
		code, raw := apiCall(t, p, http.MethodPost, "/api/pairing", key, map[string]any{"purpose": purpose})
		if code != http.StatusCreated {
			t.Fatalf("create %s code = %d %s", purpose, code, raw)
		}
		var pr struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &pr)
		return pr.Code
	}

	// A stranger (not on the allow-list) links their account to the owner.
	if c := send(900, "/pair "+newCode(k["owner"], "telegram")); c != http.StatusOK {
		t.Fatalf("pair = %d", c)
	}
	tg.waitText(t, "now linked")
	_, raw := apiCall(t, p, http.MethodGet, "/api/links", k["owner"], nil)
	if !strings.Contains(string(raw), `"subject":"900"`) {
		t.Fatalf("links = %s", raw)
	}
	// Linked now, so its messages are accepted.
	if c := send(900, "/status"); c != http.StatusOK {
		t.Errorf("linked account message = %d", c)
	}

	// Cross-person conflict: Bob's code can't take the owner's account.
	send(900, "/pair "+newCode(k["bob"], "telegram"))
	tg.waitText(t, "linked to someone else")

	// Cross-channel: a WhatsApp code is refused on Telegram.
	send(901, "/pair "+newCode(k["bob"], "whatsapp"))
	tg.waitText(t, "invalid, used or expired")

	// Allow-listed account 555 is linked to Bob, then the link is revoked:
	// revocation beats the allow-list.
	send(555, "/pair "+newCode(k["bob"], "telegram"))
	tg.waitText(t, "linked to bob")
	if c, raw := apiCall(t, p, http.MethodDelete, "/api/links/telegram/555", k["bob"], nil); c != http.StatusOK {
		t.Fatalf("unlink = %d %s", c, raw)
	}
	if c := send(555, "/status"); c != http.StatusForbidden {
		t.Errorf("revoked, allow-listed account = %d, want 403", c)
	}
	// Bob cannot unlink the owner's account.
	if c, _ := apiCall(t, p, http.MethodDelete, "/api/links/telegram/900", k["bob"], nil); c != http.StatusForbidden {
		t.Errorf("bob unlinks the owner = %d, want 403", c)
	}
}
