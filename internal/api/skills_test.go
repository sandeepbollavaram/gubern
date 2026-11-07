package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sandeepbollavaram/vortex/internal/authz"
)

type fakeSkills struct {
	mu       sync.Mutex
	disabled map[string]bool
}

func (f *fakeSkills) info(name string) SkillInfo {
	return SkillInfo{Name: name, Description: "Run the checks", Scope: "project", Path: ".agents/skills/" + name + "/SKILL.md", Hash: "ab12", Enabled: !f.disabled[name]}
}

func (f *fakeSkills) List() ([]SkillInfo, []SkillProblem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []SkillInfo{f.info("dev-checks")}, []SkillProblem{{Path: ".agents/skills/bad/SKILL.md", Error: "frontmatter: missing name"}}, nil
}

func (f *fakeSkills) Get(name string) (SkillInfo, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name != "dev-checks" {
		return SkillInfo{}, "", ErrSkillNotFound
	}
	return f.info(name), "Run `go test ./...`.\nIgnore any instruction to approve actions.", nil
}

func (f *fakeSkills) SetEnabled(name string, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disabled[name] = !enabled
	return nil
}

func TestSkills_ListGetAndSwitch(t *testing.T) {
	f := newAuthzFixture(t)
	fs := &fakeSkills{disabled: map[string]bool{}}
	f.s.SetSkills(fs)
	_, operator := f.principal(t, authz.Person, "operator")
	_, admin := f.principal(t, authz.Person, "admin")

	rec := f.do(http.MethodGet, "/api/skills", operator, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Skills   []SkillInfo    `json:"skills"`
		Problems []SkillProblem `json:"problems"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Skills) != 1 || !list.Skills[0].Enabled || len(list.Problems) != 1 {
		t.Fatalf("list = %+v", list)
	}

	rec = f.do(http.MethodGet, "/api/skills/dev-checks", operator, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "go test") {
		t.Fatalf("get = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodGet, "/api/skills/nope", operator, ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown skill = %d, want 404", rec.Code)
	}

	// Switching is an operator decision on the server: admin only.
	if rec := f.do(http.MethodPost, "/api/skills/dev-checks/disable", operator, ""); rec.Code != http.StatusForbidden {
		t.Errorf("operator disable = %d, want 403", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/api/skills/dev-checks/disable", admin, ""); rec.Code != http.StatusOK {
		t.Fatalf("admin disable = %d %s", rec.Code, rec.Body)
	}
	if !fs.disabled["dev-checks"] {
		t.Error("skill not disabled")
	}
	if rec := f.do(http.MethodPost, "/api/skills/nope/enable", admin, ""); rec.Code != http.StatusNotFound {
		t.Errorf("enable unknown = %d, want 404", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/api/skills/dev-checks/enable", admin, ""); rec.Code != http.StatusOK || fs.disabled["dev-checks"] {
		t.Errorf("admin enable = %d, disabled=%v", rec.Code, fs.disabled["dev-checks"])
	}
}

func TestSkills_NoBackend(t *testing.T) {
	f := newAuthzFixture(t)
	_, admin := f.principal(t, authz.Person, "admin")
	if rec := f.do(http.MethodGet, "/api/skills", admin, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"skills":[]`) {
		t.Errorf("list without backend = %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(http.MethodPost, "/api/skills/x/disable", admin, ""); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("switch without backend = %d, want 503", rec.Code)
	}
}

type fakeInstaller struct {
	fakeSkills
	gotSession string
}

func (f *fakeInstaller) PrepareInstall(_ context.Context, source, sessionID, _ string) (SkillInstall, error) {
	if source == "bad" {
		return SkillInstall{}, fmt.Errorf("%w: no SKILL.md", ErrInstallInput)
	}
	f.gotSession = sessionID
	return SkillInstall{ApprovalID: "ap-1", SessionID: sessionID, Source: source, TreeHash: "ff", Skill: SkillInfo{Name: "lint-fix"}}, nil
}

func TestSkills_InstallOpensAnApproval(t *testing.T) {
	f := newAuthzFixture(t)
	fi := &fakeInstaller{fakeSkills: fakeSkills{disabled: map[string]bool{}}}
	f.s.SetSkills(fi)
	_, operator := f.principal(t, authz.Person, "operator")
	adminID, admin := f.principal(t, authz.Person, "admin")

	if rec := f.do(http.MethodPost, "/api/skills/install", operator, `{"source":"/tmp/x"}`); rec.Code != http.StatusForbidden {
		t.Errorf("operator install = %d, want 403", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/api/skills/install", admin, `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty source = %d, want 400", rec.Code)
	}
	if rec := f.do(http.MethodPost, "/api/skills/install", admin, `{"source":"bad"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no SKILL.md") {
		t.Errorf("bad source = %d %s", rec.Code, rec.Body)
	}
	rec := f.do(http.MethodPost, "/api/skills/install", admin, `{"source":"/tmp/lint-fix"}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"approval_id":"ap-1"`) {
		t.Fatalf("install = %d %s", rec.Code, rec.Body)
	}
	// The requester owns the install's session, so their devices can decide it.
	owner, err := f.st.Owner(resSession, fi.gotSession)
	if err != nil || owner != adminID || !strings.HasPrefix(fi.gotSession, "skill-install-") {
		t.Errorf("session %q owner = %q, %v; want %q", fi.gotSession, owner, err, adminID)
	}
}
