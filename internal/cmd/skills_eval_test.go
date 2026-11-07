package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeEvalServer: tasks pass only while the skill is on, except "easy",
// which always passes. Each run first asks two approvals: the check (must be
// approved) and a stray command (must be rejected).
type fakeEvalServer struct {
	mu        sync.Mutex
	enabled   bool
	decisions map[string]bool // approval id -> approved
	pending   map[string][]map[string]string
}

func (f *fakeEvalServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/skills/notes":
		_ = json.NewEncoder(w).Encode(map[string]any{"skill": map[string]any{"enabled": f.enabled}})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/skills/notes/"):
		f.enabled = strings.HasSuffix(r.URL.Path, "/enable")
		_ = json.NewEncoder(w).Encode(map[string]any{})
	case r.URL.Path == "/api/agents/submit":
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		sess, msg := b["session_id"], b["message"]
		f.pending[sess] = []map[string]string{
			{"id": sess + "-check", "tool": "run_terminal", "command": "test -f notes.md", "action_hash": strings.Repeat("a", 64)},
			{"id": sess + "-stray", "tool": "run_terminal", "command": "rm -rf /", "action_hash": strings.Repeat("b", 64)},
		}
		// Wait (unlocked) for the runner to decide both.
		for i := 0; i < 100; i++ {
			_, a := f.decisions[sess+"-check"]
			_, c := f.decisions[sess+"-stray"]
			if a && c {
				break
			}
			f.mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			f.mu.Lock()
		}
		pass := f.enabled || strings.Contains(msg, "easy")
		reply := "done\n✗ Not verified: test -f notes.md still fails after 3 attempt(s)"
		if pass {
			reply = "done\n✓ Verified: test -f notes.md passed"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"response": reply})
	case r.URL.Path == "/api/approvals":
		sess := r.URL.Query().Get("session_id")
		var out []map[string]string
		for _, a := range f.pending[sess] {
			if _, decided := f.decisions[a["id"]]; !decided {
				out = append(out, a)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"approvals": out})
	case strings.HasSuffix(r.URL.Path, "/decision"):
		var b struct {
			Approved bool `json:"approved"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/approvals/"), "/decision")
		f.decisions[id] = b.Approved
		_ = json.NewEncoder(w).Encode(map[string]any{})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestSkillsEval_ComparesAndApprovesOnlyTheCheck(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, ws, "notes", "Take notes.")
	skill := filepath.Join(ws, ".agents", "skills", "notes")
	evals := `{"tasks":[{"name":"hard","task":"write notes.md","check":"test -f notes.md"},{"name":"easy","task":"easy: write notes.md","check":"test -f notes.md"}]}`
	if err := os.WriteFile(filepath.Join(skill, "evals.json"), []byte(evals), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeEvalServer{enabled: false, decisions: map[string]bool{}, pending: map[string][]map[string]string{}}
	srv := httptest.NewServer(f)
	defer srv.Close()

	rep, err := runSkillsEval(context.Background(), srv.URL, "k", ws, "notes", 2, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if rep.With != 1 || rep.Without != 0.5 || rep.Tasks[0].WithPass != 2 || rep.Tasks[0].WithoutPass != 0 || rep.Tasks[1].WithoutPass != 2 {
		t.Errorf("report = %+v", rep)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enabled {
		t.Error("the skill's original state (off) was not restored")
	}
	if len(f.decisions) != 16 { // 2 tasks x 2 runs x 2 variants x 2 approvals
		t.Errorf("decisions = %d, want 16", len(f.decisions))
	}
	for id, approved := range f.decisions {
		if strings.HasSuffix(id, "-check") != approved {
			t.Errorf("%s approved=%v; only the declared check may be approved", id, approved)
		}
	}
}

func TestSkillsEval_RejectsBadEvalFiles(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, ws, "notes", "x")
	p := filepath.Join(ws, ".agents", "skills", "notes", "evals.json")
	for _, doc := range []string{`{"tasks":[]}`, `{"tasks":[{"task":"x","check":""}]}`, `{"tasks":[{"task":"x","check":"echo \"hi\""}]}`, `nope`} {
		if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadEvalTasks(ws, "notes"); err == nil {
			t.Errorf("accepted %s", doc)
		}
	}
}
