package messaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type discordHarness struct {
	bot  *DiscordBot
	sk   ed25519.PrivateKey
	dec  *recordingDecider
	fi   *fakeIdent
	api  *httptest.Server
	mu   sync.Mutex
	sent []map[string]any
}

func newDiscordHarness(t *testing.T) *discordHarness {
	t.Helper()
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	h := &discordHarness{sk: sk, dec: &recordingDecider{}, fi: &fakeIdent{linked: map[string]bool{}, revoked: map[string]bool{}}}
	h.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		m["_path"] = r.Method + " " + r.URL.Path
		m["_auth"] = r.Header.Get("Authorization")
		h.mu.Lock()
		h.sent = append(h.sent, m)
		h.mu.Unlock()
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(h.api.Close)
	bot, err := NewDiscordBot(DiscordConfig{
		PublicKey: hex.EncodeToString(pk), BotToken: "bot-token", ApprovalChannel: "chan1",
		AllowedUsers: []string{"111", "222"}, BaseURL: h.api.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	bot.SetIdentity(h.fi.ci())
	NewChannelApprovals(h.dec.fn()).WithDiscord(bot)
	h.bot = bot
	return h
}

func (h *discordHarness) send(t *testing.T, body any, tamper func(*http.Request)) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := ed25519.Sign(h.sk, append([]byte(ts), raw...))
	req := httptest.NewRequest(http.MethodPost, "/webhook/discord", bytes.NewReader(raw))
	req.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	req.Header.Set("X-Signature-Timestamp", ts)
	if tamper != nil {
		tamper(req)
	}
	rec := httptest.NewRecorder()
	h.bot.HandleInteractions(nil).ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func content(out map[string]any) string {
	d, _ := out["data"].(map[string]any)
	s, _ := d["content"].(string)
	return s
}

func button(user, custom string) map[string]any {
	return map[string]any{"type": 3, "member": map[string]any{"user": map[string]any{"id": user}}, "data": map[string]any{"custom_id": custom}}
}

func command(user, name string, opts map[string]string) map[string]any {
	var o []map[string]any
	for k, v := range opts {
		o = append(o, map[string]any{"name": k, "value": v})
	}
	return map[string]any{"type": 2, "user": map[string]any{"id": user}, "data": map[string]any{"name": name, "options": o}}
}

func TestDiscord_SignatureChecks(t *testing.T) {
	h := newDiscordHarness(t)
	if c, out := h.send(t, map[string]any{"type": 1}, nil); c != http.StatusOK || out["type"] != float64(1) {
		t.Fatalf("ping = %d %v", c, out)
	}
	for name, tamper := range map[string]func(*http.Request){
		"no signature": func(r *http.Request) { r.Header.Del("X-Signature-Ed25519") },
		"forged":       func(r *http.Request) { r.Header.Set("X-Signature-Ed25519", strings.Repeat("00", 64)) },
		"stale": func(r *http.Request) {
			r.Header.Set("X-Signature-Timestamp", strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10))
		},
		"tampered body": func(r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(`{"type":3,"data":{"custom_id":"approve:0123456789abcdef"}}`))
		},
	} {
		if c, _ := h.send(t, map[string]any{"type": 1}, tamper); c != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", name, c)
		}
	}
	if len(h.dec.calls) != 0 {
		t.Error("an unverified request reached the decider")
	}
}

func TestDiscord_DecisionsFollowIdentity(t *testing.T) {
	h := newDiscordHarness(t)
	h.fi.revoked["discord:222"] = true
	h.fi.linked["discord:333"] = true

	// Allow-listed user: decides as discord:111.
	if _, out := h.send(t, button("111", "approve:"+id16), nil); !strings.Contains(content(out), "Approved") {
		t.Fatalf("allowed button = %v", out)
	}
	if c := h.dec.last(t); c.d.Channel != "discord" || c.d.External != "111" || !c.approve {
		t.Fatalf("decision = %+v", c)
	}
	n := len(h.dec.calls)
	// Revoked (though allow-listed) and strangers are refused.
	for _, u := range []string{"222", "999"} {
		if _, out := h.send(t, button(u, "approve:"+id16), nil); !strings.Contains(content(out), "not allowed") {
			t.Errorf("user %s = %v", u, out)
		}
	}
	if len(h.dec.calls) != n {
		t.Fatal("a refused user reached the decider")
	}
	// A paired user decides; /reject command form.
	if _, out := h.send(t, command("333", "reject", map[string]string{"id": id16}), nil); !strings.Contains(content(out), "Rejected") {
		t.Fatalf("paired /reject = %v", out)
	}
	if c := h.dec.last(t); c.d.External != "333" || c.approve || c.session != "discord:333" {
		t.Fatalf("decision = %+v", c)
	}
	// Malformed ids are refused before the decider.
	if _, out := h.send(t, command("111", "approve", map[string]string{"id": "nope"}), nil); !strings.Contains(content(out), "Usage") {
		t.Errorf("bad id = %v", out)
	}
}

func TestDiscord_PairFromAnyone(t *testing.T) {
	h := newDiscordHarness(t)
	_, out := h.send(t, command("777", "pair", map[string]string{"code": "AAAA-BBBB-CCCC"}), nil)
	if !strings.Contains(content(out), "linked") || len(h.fi.paired) != 1 || h.fi.paired[0] != "discord:777=AAAA-BBBB-CCCC" {
		t.Fatalf("pair = %v paired=%v", out, h.fi.paired)
	}
	if d, _ := out["data"].(map[string]any); d["flags"] != float64(discordEphemeral) {
		t.Error("pairing reply is not ephemeral")
	}
	// Other commands still need access.
	if _, out := h.send(t, command("777", "ask", map[string]string{"message": "hi"}), nil); !strings.Contains(content(out), "not registered") {
		t.Errorf("stranger /ask = %v", out)
	}
}

func TestDiscord_NoticesCarryButtons(t *testing.T) {
	h := newDiscordHarness(t)
	h.bot.appr.Notify(sampleApproval())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		n := len(h.sent)
		h.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.sent) == 0 {
		t.Fatal("no notice sent")
	}
	m := h.sent[0]
	raw, _ := json.Marshal(m["components"])
	if m["_path"] != "POST /channels/chan1/messages" || m["_auth"] != "Bot bot-token" || !strings.Contains(string(raw), "approve:"+id16) {
		t.Fatalf("notice = %v", m)
	}
}
