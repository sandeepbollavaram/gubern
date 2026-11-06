package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/audit"
)

func runEntry(at time.Time, detail map[string]any) audit.Entry {
	return audit.Entry{Timestamp: at, Actor: "agent", Detail: detail}
}

func TestSummarizeRuns(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	started := []audit.Entry{
		runEntry(now.Add(-3*time.Hour), map[string]any{"run": "a1", "session": "s1", "task": "fix the build"}),
		runEntry(now.Add(-2*time.Hour), map[string]any{"run": "b2", "session": "s2", "task": "cut off"}),
		runEntry(now.Add(-2*time.Minute), map[string]any{"run": "c3", "session": "s3", "task": "in flight"}),
		runEntry(now.Add(-time.Hour), map[string]any{"session": "old"}), // no id: skipped
	}
	finished := []audit.Entry{
		runEntry(now.Add(-170*time.Minute), map[string]any{"run": "a1", "stop": "done", "verified": true}),
		runEntry(now.Add(-time.Hour), map[string]any{"stop": "done"}), // pre-id entry: ignored
	}
	rows := summarizeRuns(started, finished, now)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	want := []struct{ id, status, verified string }{
		{"c3", "running", ""}, {"b2", "interrupted", ""}, {"a1", "done", "yes"},
	}
	for i, w := range want {
		if rows[i].ID != w.id || rows[i].Status != w.status || rows[i].Verified != w.verified {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}

func TestWriteRuns(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRuns(&buf, nil); err != nil || !strings.Contains(buf.String(), "No /agent runs") {
		t.Errorf("empty: %q %v", buf.String(), err)
	}
	buf.Reset()
	rows := []runRow{{ID: "b2", Started: time.Now(), Session: "s2", Task: strings.Repeat("x", 80) + "\nmore", Status: "interrupted"}}
	if err := writeRuns(&buf, rows); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "interrupted") || !strings.Contains(out, "…") || strings.Count(out, "\n") != 2 {
		t.Errorf("table = %q", out)
	}
}
