package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/sandeepbollavaram/vortex/internal/agents"
	"github.com/sandeepbollavaram/vortex/internal/audit"
)

// runRow is one /agent run, rebuilt from its audit entries.
type runRow struct {
	ID       string
	Started  time.Time
	Session  string
	Task     string
	Status   string // stop reason ("done", "budget_exceeded", ...), "running" or "interrupted"
	Verified string // "yes"/"no" when the run had a --check, else ""
}

// summarizeRuns pairs agent.run.started with agent.run.finished by run id,
// newest first. A run with no finished entry is "running" while it could
// still be inside AgentRunTimeout, and "interrupted" after that: the server
// stopped or crashed mid-run. Finished entries written before runs had ids
// (no started entry) are skipped.
func summarizeRuns(started, finished []audit.Entry, now time.Time) []runRow {
	done := map[string]audit.Entry{}
	for _, e := range finished {
		if id := detailString(e, "run"); id != "" {
			done[id] = e
		}
	}
	rows := make([]runRow, 0, len(started))
	for _, s := range started {
		id := detailString(s, "run")
		if id == "" {
			continue
		}
		r := runRow{ID: id, Started: s.Timestamp, Session: detailString(s, "session"), Task: detailString(s, "task")}
		if f, ok := done[id]; ok {
			r.Status = detailString(f, "stop")
			if v, ok := f.Detail["verified"].(bool); ok {
				r.Verified = map[bool]string{true: "yes", false: "no"}[v]
			}
		} else if now.Sub(s.Timestamp) <= agents.AgentRunTimeout+time.Minute {
			r.Status = "running"
		} else {
			r.Status = "interrupted"
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Started.After(rows[j].Started) })
	return rows
}

func detailString(e audit.Entry, key string) string {
	s, _ := e.Detail[key].(string)
	return s
}

// newRunsCommand builds `gubern runs`: recent /agent runs from the audit log,
// including ones a restart or crash cut off.
func newRunsCommand() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "runs",
		Short: "List recent /agent runs, including interrupted ones",
		Long: "Lists /agent runs recorded in the audit log, newest first: when each\n" +
			"started, its session, how it ended and whether its --check passed. A run\n" +
			"marked \"interrupted\" started but never finished (the server stopped or\n" +
			"crashed during it); check what it changed before re-running it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log, err := openAuditLog()
			if err != nil {
				return err
			}
			started, err := log.Query(audit.QueryFilter{Actor: "agent", Action: "agent.run.started"})
			if err != nil {
				return err
			}
			finished, err := log.Query(audit.QueryFilter{Actor: "agent", Action: "agent.run.finished"})
			if err != nil {
				return err
			}
			rows := summarizeRuns(started, finished, time.Now())
			if limit > 0 && len(rows) > limit {
				rows = rows[:limit]
			}
			return writeRuns(cmd.OutOrStdout(), rows)
		},
	}
	c.Flags().IntVar(&limit, "limit", 20, "show at most this many runs (0 = all)")
	return c
}

func writeRuns(w io.Writer, rows []runRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(w, "No /agent runs recorded yet.")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "STARTED\tRUN\tSESSION\tSTATUS\tCHECK\tTASK")
	for _, r := range rows {
		check := r.Verified
		if check == "" {
			check = "-"
		}
		task := strings.ReplaceAll(r.Task, "\n", " ")
		if len(task) > 60 {
			task = task[:60] + "…"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Started.Local().Format("2006-01-02 15:04"), r.ID, r.Session, r.Status, check, task)
	}
	return tw.Flush()
}
