package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	agentskills "github.com/sandeepbollavaram/vortex/internal/skills"
	"github.com/sandeepbollavaram/vortex/internal/tui"
)

// `gubern skills eval <name>`: does a skill help? Each eval task runs as an
// /agent run with a completion check, N times with the skill on and N times
// with it off, against the running server and its configured model. A task
// passes only when its check passes (the run ends "✓ Verified").
//
// The runner approves exactly one kind of action by itself: in its own eval
// sessions, the run_terminal call whose command is the task's declared check.
// It rejects every other approval, so a task that needs anything else simply
// fails, and the eval never widens what an agent may do. Run it against a
// throwaway workspace: the tasks really execute there.

// EvalTask is one task in a skill's evals file.
type EvalTask struct {
	Name  string `json:"name"`
	Task  string `json:"task"`
	Check string `json:"check"`
}

type evalFile struct {
	Tasks []EvalTask `json:"tasks"`
}

func newSkillsEvalCommand() *cobra.Command {
	var (
		dir     string
		runs    int
		timeout time.Duration
		asJSON  bool
	)
	c := &cobra.Command{
		Use:   "eval <skill>",
		Short: "Measure whether a skill helps: its eval tasks with and without it",
		Long: "Runs the skill's eval tasks (agents/gubern.yaml `evals:`, default evals.json)\n" +
			"as /agent runs with completion checks on the running server, --runs times\n" +
			"with the skill on and off, and reports pass rates. The tasks execute in the\n" +
			"server's workspace: use a throwaway one. The skill's on/off state is\n" +
			"restored afterwards.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = wd
			}
			key := tui.NewClient(tui.ClientConfig{BaseURL: defaultAPIBase()}).LoadAPIKey()
			rep, err := runSkillsEval(cmd.Context(), defaultAPIBase(), key, dir, args[0], runs, timeout)
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rep)
			}
			rep.print(cmd.OutOrStdout())
			return nil
		},
	}
	c.Flags().StringVar(&dir, "dir", "", "workspace holding the skill (default: current directory)")
	c.Flags().IntVar(&runs, "runs", 3, "runs per task and variant")
	c.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "limit for one run")
	c.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	return c
}

// EvalReport is the outcome of one skills eval.
type EvalReport struct {
	Skill   string           `json:"skill"`
	Runs    int              `json:"runs"`
	Tasks   []EvalTaskResult `json:"tasks"`
	With    float64          `json:"pass_rate_with"`
	Without float64          `json:"pass_rate_without"`
}

// EvalTaskResult is one task's passes with and without the skill.
type EvalTaskResult struct {
	Name        string `json:"name"`
	WithPass    int    `json:"with_pass"`
	WithoutPass int    `json:"without_pass"`
}

func (r EvalReport) print(out io.Writer) {
	_, _ = fmt.Fprintf(out, "Skill %s, %d run(s) per task and variant\n\n", r.Skill, r.Runs)
	_, _ = fmt.Fprintf(out, "  %-32s %8s %8s\n", "task", "with", "without")
	for _, t := range r.Tasks {
		_, _ = fmt.Fprintf(out, "  %-32s %5d/%-2d %5d/%-2d\n", t.Name, t.WithPass, r.Runs, t.WithoutPass, r.Runs)
	}
	_, _ = fmt.Fprintf(out, "\n  pass rate with the skill:    %5.1f%%\n  pass rate without the skill: %5.1f%%\n", 100*r.With, 100*r.Without)
	switch d := r.With - r.Without; {
	case d > 0:
		_, _ = fmt.Fprintf(out, "  the skill helped (+%.1f points)\n", 100*d)
	case d < 0:
		_, _ = fmt.Fprintf(out, "  the skill made it worse (%.1f points)\n", 100*d)
	default:
		_, _ = fmt.Fprintln(out, "  no difference")
	}
}

// loadEvalTasks reads the skill's evals file from the workspace.
func loadEvalTasks(dir, name string) ([]EvalTask, error) {
	skillDir := filepath.Join(dir, ".agents", "skills", name)
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		return nil, fmt.Errorf("no skill %q under %s", name, filepath.Join(dir, ".agents", "skills"))
	}
	rel := "evals.json"
	if sc, ok, err := agentskills.ReadSidecar(skillDir); err != nil {
		return nil, err
	} else if ok && sc.Evals != "" {
		rel = sc.Evals
	}
	b, err := os.ReadFile(filepath.Join(skillDir, filepath.FromSlash(rel))) //nolint:gosec // inside the skill folder; sidecar paths are checked
	if err != nil {
		return nil, fmt.Errorf("reading the skill's evals: %w", err)
	}
	var f evalFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if len(f.Tasks) == 0 {
		return nil, fmt.Errorf("%s has no tasks", rel)
	}
	for i, t := range f.Tasks {
		if strings.TrimSpace(t.Task) == "" || strings.TrimSpace(t.Check) == "" || strings.ContainsAny(t.Check, "\"\n") {
			return nil, fmt.Errorf("%s: task %d needs a task and a one-line check without double quotes", rel, i+1)
		}
		if t.Name == "" {
			f.Tasks[i].Name = fmt.Sprintf("task %d", i+1)
		}
	}
	return f.Tasks, nil
}

func runSkillsEval(ctx context.Context, base, key, dir, name string, runs int, timeout time.Duration) (EvalReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if key == "" {
		return EvalReport{}, errors.New("no API key found; run `gubern setup` or set GUBERN_API_KEY")
	}
	if runs < 1 {
		return EvalReport{}, errors.New("--runs must be at least 1")
	}
	tasks, err := loadEvalTasks(dir, name)
	if err != nil {
		return EvalReport{}, err
	}
	var skill struct {
		Skill struct {
			Enabled bool `json:"enabled"`
		} `json:"skill"`
	}
	if st, raw, err := apiCall(ctx, http.MethodGet, base+"/api/skills/"+url.PathEscape(name), key, nil, &skill); err != nil {
		return EvalReport{}, err
	} else if st != http.StatusOK {
		return EvalReport{}, fmt.Errorf("the server doesn't have skill %q (%d): %s", name, st, strings.TrimSpace(raw))
	}
	setEnabled := func(on bool) error {
		action := "disable"
		if on {
			action = "enable"
		}
		st, raw, err := apiCall(ctx, http.MethodPost, base+"/api/skills/"+url.PathEscape(name)+"/"+action, key, nil, nil)
		if err != nil {
			return err
		}
		if st != http.StatusOK {
			return fmt.Errorf("switching the skill %s (%d): %s", action, st, strings.TrimSpace(raw))
		}
		return nil
	}
	defer func() { _ = setEnabled(skill.Skill.Enabled) }() // restore

	rep := EvalReport{Skill: name, Runs: runs}
	results := make([]EvalTaskResult, len(tasks))
	for i, t := range tasks {
		results[i].Name = t.Name
	}
	for _, with := range []bool{true, false} {
		if err := setEnabled(with); err != nil {
			return EvalReport{}, err
		}
		for r := 0; r < runs; r++ {
			for i, t := range tasks {
				ok, err := evalOnce(ctx, base, key, t, timeout)
				if err != nil {
					return EvalReport{}, err
				}
				if ok && with {
					results[i].WithPass++
				} else if ok {
					results[i].WithoutPass++
				}
			}
		}
	}
	total := float64(runs * len(tasks))
	for _, r := range results {
		rep.With += float64(r.WithPass) / total
		rep.Without += float64(r.WithoutPass) / total
	}
	rep.Tasks = results
	return rep, nil
}

// evalOnce runs one task in a fresh session and reports whether its check
// passed. While it runs, it approves the check command and rejects anything
// else asked in that session.
func evalOnce(ctx context.Context, base, key string, t EvalTask, timeout time.Duration) (bool, error) {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	session := "eval-" + hex.EncodeToString(b)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	type result struct {
		reply string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		body, _ := json.Marshal(map[string]string{"message": fmt.Sprintf("/agent --check \"%s\" %s", t.Check, t.Task), "session_id": session})
		var res struct {
			Response string `json:"response"`
		}
		st, raw, err := apiCall(ctx, http.MethodPost, base+"/api/agents/submit", key, body, &res)
		if err == nil && st != http.StatusOK {
			err = fmt.Errorf("submit refused (%d): %s", st, strings.TrimSpace(raw))
		}
		done <- result{res.Response, err}
	}()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case r := <-done:
			if r.err != nil {
				return false, r.err
			}
			return strings.Contains(r.reply, "✓ Verified:"), nil
		case <-ctx.Done():
			return false, nil // too slow counts as a failure
		case <-tick.C:
			decideEvalApprovals(ctx, base, key, session, t.Check)
		}
	}
}

func decideEvalApprovals(ctx context.Context, base, key, session, check string) {
	var list struct {
		Approvals []struct {
			ID         string `json:"id"`
			Tool       string `json:"tool"`
			Command    string `json:"command"`
			ActionHash string `json:"action_hash"`
		} `json:"approvals"`
	}
	if _, _, err := apiCall(ctx, http.MethodGet, base+"/api/approvals?status=pending&session_id="+url.QueryEscape(session), key, nil, &list); err != nil {
		return
	}
	for _, a := range list.Approvals {
		approve := a.Tool == "run_terminal" && strings.TrimSpace(a.Command) == strings.TrimSpace(check)
		body, _ := json.Marshal(map[string]any{"approved": approve, "action_hash": a.ActionHash, "via": "api", "reason": "skills eval"})
		_, _, _ = apiCall(ctx, http.MethodPost, base+"/api/approvals/"+url.PathEscape(a.ID)+"/decision", key, body, nil)
	}
}
