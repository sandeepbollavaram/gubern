package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/api"
)

func TestSkillsInstall_CLIOpensApprovalAndWaits(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, ws, "lint-fix", "Fix lint.")
	src := filepath.Join(ws, ".agents", "skills", "lint-fix")
	var polls atomic.Int32
	var gotSource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/skills/install":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			gotSource = b["source"]
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.SkillInstall{ApprovalID: "ap1", TreeHash: "ff", Skill: api.SkillInfo{Name: "lint-fix"},
				Files: []api.SkillFile{{Path: "SKILL.md", Size: 10, SHA256: strings.Repeat("a", 64)}}})
		case r.URL.Path == "/api/approvals/ap1":
			st := "pending"
			if polls.Add(1) > 1 {
				st = "approved"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": st, "decided_by": "owner"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := runSkillsInstall(context.Background(), &out, srv.URL, "k", src, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if gotSource != src || !strings.Contains(out.String(), "approval   ap1") || !strings.Contains(out.String(), "installed and pinned") {
		t.Errorf("source=%q output:\n%s", gotSource, out.String())
	}
	if err := runSkillsInstall(context.Background(), &out, srv.URL, "k", t.TempDir(), 0); err == nil {
		t.Error("a folder without SKILL.md was sent")
	}
	if err := runSkillsInstall(context.Background(), &out, srv.URL, "", src, 0); err == nil {
		t.Error("ran without an API key")
	}
}
