package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/tui"
)

// newSkillsInstallCommand builds `gubern skills install <source>`. It asks the
// running server to fetch and review the skill; the install itself waits for
// an approval, decided in the dashboard, the phone, chat or `gubern approve`.
// There is no local shortcut: an install always goes through the approval.
func newSkillsInstallCommand() *cobra.Command {
	var wait time.Duration
	c := &cobra.Command{
		Use:   "install <folder | https://host/repo.git@<commit>>",
		Short: "Install a skill into the server's workspace, after an approval",
		Long: "Fetches the skill into quarantine on the running server, hashes every file,\n" +
			"and opens a HIGH RISK approval that shows them. Nothing is installed until\n" +
			"someone approves it (dashboard, paired phone with step-up, or chat).\n" +
			"Git sources must name a full 40-character commit.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsInstall(cmd.Context(), cmd.OutOrStdout(), defaultAPIBase(), tui.NewClient(tui.ClientConfig{BaseURL: defaultAPIBase()}).LoadAPIKey(), args[0], wait)
		},
	}
	c.Flags().DurationVar(&wait, "wait", 0, "wait up to this long for the decision (e.g. 10m); 0 returns at once")
	return c
}

func runSkillsInstall(ctx context.Context, out io.Writer, base, key, source string, wait time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if key == "" {
		return errors.New("no API key found; run `gubern setup` or set GUBERN_API_KEY")
	}
	// A local folder is read by the server, whose working directory differs.
	if !strings.Contains(source, "://") {
		abs, err := filepath.Abs(source)
		if err != nil {
			return err
		}
		if _, err := os.Stat(filepath.Join(abs, "SKILL.md")); err != nil {
			return fmt.Errorf("%s has no SKILL.md", abs)
		}
		source = abs
	}
	body, _ := json.Marshal(map[string]string{"source": source})
	var res api.SkillInstall
	status, raw, err := apiCall(ctx, http.MethodPost, base+"/api/skills/install", key, body, &res)
	if err != nil {
		return err
	}
	if status != http.StatusAccepted {
		return fmt.Errorf("server refused the install (%d): %s", status, strings.TrimSpace(raw))
	}
	_, _ = fmt.Fprintf(out, "Fetched %s into quarantine.\n\n", res.Skill.Name)
	for _, f := range res.Files {
		_, _ = fmt.Fprintf(out, "  %-40s %8d B  sha256:%s\n", f.Path, f.Size, f.SHA256[:16])
	}
	_, _ = fmt.Fprintf(out, "\n  tree hash  %s\n  approval   %s (HIGH RISK)\n\n", res.TreeHash, res.ApprovalID)
	if wait <= 0 {
		_, _ = fmt.Fprintln(out, "Nothing is installed until this approval is approved (dashboard Inbox, your paired phone, or chat).")
		return nil
	}
	_, _ = fmt.Fprintf(out, "Waiting up to %s for a decision…\n", wait)
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		var a struct {
			Status    string `json:"status"`
			DecidedBy string `json:"decided_by"`
		}
		if _, _, err := apiCall(ctx, http.MethodGet, base+"/api/approvals/"+res.ApprovalID, key, nil, &a); err == nil && a.Status != "" && a.Status != "pending" {
			if a.Status == "approved" {
				_, _ = fmt.Fprintf(out, "Approved by %s. %s is installed and pinned.\n", a.DecidedBy, res.Skill.Name)
				return nil
			}
			return fmt.Errorf("install %s: nothing was installed", a.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return errors.New("still waiting for approval; the request stays open until it expires")
}

// apiCall sends one JSON request to the local server and decodes a JSON reply.
func apiCall(ctx context.Context, method, url, key string, body []byte, into any) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("can't reach the Gubern server at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 && into != nil {
		_ = json.Unmarshal(b, into)
	}
	return resp.StatusCode, string(b), nil
}
