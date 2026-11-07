//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/auth"
	"github.com/sandeepbollavaram/vortex/internal/testutil"
)

// Milestone A against a real server: legacy key migration, N6, resource
// isolation between people, permission changes and owner recovery. Every
// request goes over real HTTP to a real gubern process; the AI is the
// deterministic fake.

type seededKey struct {
	user, desc string
	roles      []auth.Role
}

// seedKeys writes a key store with keys (as an older Gubern would have left
// it), points GUBERN_APIKEY_STORE at it, and returns name -> secret.
func seedKeys(t *testing.T, keys map[string]seededKey) map[string]string {
	t.Helper()
	store := auth.NewAPIKeyStore()
	out := map[string]string{}
	for name, k := range keys {
		_, secret, err := store.Issue(k.user, "default", k.roles, k.desc, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = secret
	}
	path := filepath.Join(t.TempDir(), "apikeys.json")
	if err := store.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GUBERN_APIKEY_STORE", path)
	return out
}

// cli runs the gubern binary with an isolated config home (never the
// developer's real one) and the test's GUBERN_APIKEY_STORE.
func cli(t *testing.T, home string, args ...string) (string, int) {
	t.Helper()
	return (&testutil.VortexProcess{BinaryPath: getNetBinary(t), ConfigHome: home}).Run(t, args...)
}

const toolRun = `/agent check [[tool:run_terminal {"command":"echo gubern-fake"}]]`

// TestAuthz_LegacyKeyMigration: on first start an older key store is bound to
// principals without promoting anyone: the setup key is the owner, other keys
// keep what their role meant as service principals, unknown roles get nothing.
func TestAuthz_LegacyKeyMigration(t *testing.T) {
	testutil.StartFakeAI(t)
	k := seedKeys(t, map[string]seededKey{
		"owner":    {"admin", "setup key", []auth.Role{auth.RoleAdmin}},
		"ci-admin": {"ci", "ci deploy", []auth.Role{auth.RoleAdmin}},
		"operator": {"bot", "ops bot", []auth.Role{auth.RoleOperator}},
		"viewer":   {"wall", "wallboard", []auth.Role{auth.RoleViewer}},
		"odd":      {"x", "weird", []auth.Role{"superuser"}},
	})
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)
	if !strings.Contains(p.Logs(), "API keys bound to principals") {
		t.Errorf("startup did not report the key migration")
	}

	expect := func(name, method, path string, body any, want int) {
		t.Helper()
		if code, raw := apiCall(t, p, method, path, k[name], body); code != want {
			t.Errorf("%s %s %s = %d, want %d (%s)", name, method, path, code, want, raw)
		}
	}
	// N6: the viewer and the unknown-role key lost the data plane.
	expect("viewer", http.MethodPost, "/api/agents/submit", map[string]string{"message": "hi", "session_id": "v1"}, http.StatusForbidden)
	expect("viewer", http.MethodGet, "/v1/models", nil, http.StatusForbidden)
	expect("viewer", http.MethodPost, "/api/devops/command", map[string]string{"server": "x", "command": "uptime"}, http.StatusForbidden)
	expect("viewer", http.MethodGet, "/api/agents/status", nil, http.StatusOK)
	expect("odd", http.MethodGet, "/api/status", nil, http.StatusForbidden)
	expect("operator", http.MethodGet, "/api/keys", nil, http.StatusForbidden)
	expect("ci-admin", http.MethodGet, "/api/keys", nil, http.StatusOK)

	// A real approval: the owner's run pauses; the legacy admin service and
	// the operator (not its session) cannot decide it; the owner can.
	done := submitAsync(t, p, k["owner"], "own-1", toolRun)
	item := waitPending(t, p, k["owner"], "own-1")
	decide := map[string]any{"session_id": "own-1", "action_id": item.ActionID, "approved": true}
	expect("ci-admin", http.MethodPost, "/api/agents/approve", decide, http.StatusForbidden)
	expect("operator", http.MethodPost, "/api/agents/approve", decide, http.StatusForbidden)
	expect("viewer", http.MethodPost, "/api/agents/approve", decide, http.StatusForbidden)
	expect("owner", http.MethodPost, "/api/agents/approve", decide, http.StatusOK)
	select {
	case res := <-done:
		if !strings.Contains(res.response, "fake-ai: done") {
			t.Errorf("owner run reply = %q", res.response)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish")
	}

	// Migration is idempotent across restarts.
	p.Stop(t)
	p2 := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p2.Stop(t)
	if strings.Contains(p2.Logs(), "API keys bound to principals") {
		t.Error("restart migrated keys again")
	}
	if code, _ := apiCall(t, p2, http.MethodGet, "/api/keys", k["ci-admin"], nil); code != http.StatusOK {
		t.Errorf("ci-admin after restart = %d", code)
	}
}

// TestAuthz_PeopleAreIsolated: two people with the operator role, each with
// their own key (added with the local CLI), see and decide only their own
// sessions over the real API.
func TestAuthz_PeopleAreIsolated(t *testing.T) {
	testutil.StartFakeAI(t)
	k := seedKeys(t, map[string]seededKey{"owner": {"admin", "setup key", []auth.Role{auth.RoleAdmin}}})
	home := t.TempDir()
	dir := t.TempDir()
	for _, who := range []string{"ada", "bob"} {
		out, code := cli(t, home, "identity", "add-person", who, "--role", "operator", "--key-file", filepath.Join(dir, who))
		if code != 0 {
			t.Fatalf("add-person %s: %d %s", who, code, out)
		}
		raw, err := os.ReadFile(filepath.Join(dir, who))
		if err != nil {
			t.Fatal(err)
		}
		k[who] = strings.TrimSpace(string(raw))
		if strings.Contains(out, k[who]) {
			t.Errorf("add-person printed the secret")
		}
	}
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)

	done := submitAsync(t, p, k["ada"], "ada-1", toolRun)
	item := waitPending(t, p, k["ada"], "ada-1")

	// Bob sees nothing of Ada's and cannot act on it.
	code, raw := apiCall(t, p, http.MethodGet, "/api/agents/pending", k["bob"], nil)
	if code != http.StatusOK || strings.Contains(string(raw), "ada-1") {
		t.Errorf("bob pending = %d %s", code, raw)
	}
	code, raw = apiCall(t, p, http.MethodGet, "/api/attention", k["bob"], nil)
	if code != http.StatusOK || strings.Contains(string(raw), "ada-1") {
		t.Errorf("bob attention = %d %s", code, raw)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/agents/approve", map[string]any{"session_id": "ada-1", "action_id": item.ActionID, "approved": true}},
		{http.MethodGet, "/api/agents/history/ada-1", nil},
		{http.MethodPost, "/api/agents/submit", map[string]string{"message": "hijack", "session_id": "ada-1"}},
	} {
		if code, raw := apiCall(t, p, c.method, c.path, k["bob"], c.body); code != http.StatusForbidden {
			t.Errorf("bob %s %s = %d, want 403 (%s)", c.method, c.path, code, raw)
		}
	}
	// The owner sees Ada's approval; Ada decides it herself.
	if code, raw := apiCall(t, p, http.MethodGet, "/api/agents/pending", k["owner"], nil); code != http.StatusOK || !strings.Contains(string(raw), "ada-1") {
		t.Errorf("owner pending = %d %s", code, raw)
	}
	if code, raw := apiCall(t, p, http.MethodPost, "/api/agents/approve", k["ada"],
		map[string]any{"session_id": "ada-1", "action_id": item.ActionID, "approved": true}); code != http.StatusOK {
		t.Fatalf("ada approve = %d %s", code, raw)
	}
	select {
	case res := <-done:
		if !strings.Contains(res.response, "fake-ai: done") {
			t.Errorf("ada run reply = %q", res.response)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run did not finish")
	}
	// History lists only one's own sessions.
	_, raw = apiCall(t, p, http.MethodGet, "/api/agents/history", k["bob"], nil)
	if strings.Contains(string(raw), "ada-1") {
		t.Errorf("bob history lists ada's session: %s", raw)
	}
	// No secret ever reaches the server log.
	for who, secret := range k {
		if strings.Contains(p.Logs(), secret) {
			t.Errorf("%s's secret appears in the server log", who)
		}
	}
}

// TestAuthz_OwnerRecovery: the local recovery command refuses while the
// server runs, then issues a working owner key without printing it.
func TestAuthz_OwnerRecovery(t *testing.T) {
	k := seedKeys(t, map[string]seededKey{"owner": {"admin", "setup key", []auth.Role{auth.RoleAdmin}}})
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))

	// Lose the owner's key: revoke it through the API.
	code, raw := apiCall(t, p, http.MethodGet, "/api/keys", k["owner"], nil)
	if code != http.StatusOK {
		t.Fatalf("list keys = %d %s", code, raw)
	}
	var list struct {
		Keys []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"keys"`
	}
	_ = json.Unmarshal(raw, &list)
	for _, key := range list.Keys {
		if key.Description == "setup key" {
			if code, _ := apiCall(t, p, http.MethodDelete, "/api/keys/"+key.ID, k["owner"], nil); code != http.StatusOK {
				t.Fatalf("revoke = %d", code)
			}
		}
	}
	if code, _ := apiCall(t, p, http.MethodGet, "/api/keys", k["owner"], nil); code != http.StatusUnauthorized {
		t.Fatalf("revoked owner key = %d, want 401", code)
	}

	out, exit := p.Run(t, "identity", "recover-owner")
	if exit == 0 || !strings.Contains(out, "running") {
		t.Fatalf("recover-owner while running: exit %d %s", exit, out)
	}
	p.Stop(t)

	out, exit = p.Run(t, "identity", "recover-owner")
	if exit != 0 {
		t.Fatalf("recover-owner: exit %d %s", exit, out)
	}
	var keyFile string
	_ = filepath.Walk(p.ConfigHome, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.Name() == "tui-key" {
			keyFile = path
		}
		return nil
	})
	secret, err := os.ReadFile(keyFile)
	if err != nil || len(strings.TrimSpace(string(secret))) == 0 {
		t.Fatalf("tui-key not written (%q): %v", keyFile, err)
	}
	newKey := strings.TrimSpace(string(secret))
	if strings.Contains(out, newKey) {
		t.Error("recover-owner printed the secret")
	}
	if fi, _ := os.Stat(keyFile); fi != nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("tui-key mode = %v, want 0600", fi.Mode().Perm())
	}

	p2 := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p2.Stop(t)
	if code, raw := apiCall(t, p2, http.MethodGet, "/api/keys", newKey, nil); code != http.StatusOK {
		t.Errorf("recovered owner key = %d %s", code, raw)
	}
	if code, _ := apiCall(t, p2, http.MethodGet, "/api/keys", k["owner"], nil); code != http.StatusUnauthorized {
		t.Errorf("old revoked key works again = %d", code)
	}
}

// TestAuthz_LoopbackIsNotAnIdentity: loopback without a key reaches only the
// control-plane reads; agent data needs a credential.
func TestAuthz_LoopbackIsNotAnIdentity(t *testing.T) {
	_ = seedAdminKey(t)
	p := testutil.StartVortex(t, getNetBinary(t), testutil.WriteTestConfig(t, netConfig("")))
	defer p.Stop(t)
	if code, raw := apiCall(t, p, http.MethodGet, "/api/status", "", nil); code != http.StatusOK {
		t.Errorf("loopback /api/status = %d %s", code, raw)
	}
	for _, path := range []string{"/api/agents/pending", "/api/attention", "/api/logs", "/api/keys", "/v1/models"} {
		if code, _ := apiCall(t, p, http.MethodGet, path, "", nil); code != http.StatusUnauthorized {
			t.Errorf("loopback %s without key = %d, want 401", path, code)
		}
	}
}
