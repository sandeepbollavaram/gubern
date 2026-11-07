package eventlog

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
)

func openLog(t *testing.T, opts Options) (*Log, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.db")
	l, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, path
}

func text(s string) agentevents.Event { return agentevents.Event{Type: agentevents.Text, Text: s} }

func TestAppend_SequencesAndRedactsBeforeStoring(t *testing.T) {
	l, _ := openLog(t, Options{})
	run, _, _, err := l.StartRun(context.Background(), "s1", "p1", "")
	if err != nil {
		t.Fatal(err)
	}
	secret := "sk-ant-api03-" + strings.Repeat("k", 40)
	_, _ = l.Append(run.ID, text("key is "+secret))
	_, _ = l.Append(run.ID, agentevents.Event{Type: agentevents.ApprovalRequired, Approval: &agentevents.Pending{Command: "curl -H 'Authorization: Bearer tok123456' x"}})
	l.Finish(run.ID, StatusDone)

	sub, err := l.Subscribe(run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Live != nil {
		t.Error("finished run still live")
	}
	var types []string
	for i, ev := range sub.Replay {
		if ev.Seq != int64(i+1) || ev.RunID != run.ID || ev.SessionID != "s1" {
			t.Errorf("event %d = %+v", i, ev)
		}
		types = append(types, string(ev.Type))
	}
	if got := strings.Join(types, ","); got != "run_started,text,approval_required,run_finished" {
		t.Fatalf("types = %s", got)
	}
	if strings.Contains(sub.Replay[1].Text, secret) || !strings.Contains(sub.Replay[1].Text, Redacted) {
		t.Errorf("text stored unredacted: %q", sub.Replay[1].Text)
	}
	if strings.Contains(sub.Replay[2].Approval.Command, "tok123456") {
		t.Errorf("approval command stored unredacted: %q", sub.Replay[2].Approval.Command)
	}
	// The raw database holds no secret either.
	var n int
	_ = l.db.QueryRow(`SELECT COUNT(*) FROM events WHERE data LIKE ?`, "%"+secret+"%").Scan(&n)
	if n != 0 {
		t.Error("secret found in the stored rows")
	}
	if r, _ := l.Get(run.ID); r.Status != StatusDone || r.LastSeq != 4 || r.EndedAt == nil {
		t.Errorf("run = %+v", r)
	}
}

func TestSubscribe_ResumesWithoutGapOrDuplicate(t *testing.T) {
	l, _ := openLog(t, Options{})
	run, _, _, _ := l.StartRun(context.Background(), "s1", "p1", "")
	for i := 0; i < 5; i++ {
		_, _ = l.Append(run.ID, text("a"))
	}
	// A client saw up to seq 3, disconnected, and reconnects.
	sub, _ := l.Subscribe(run.ID, 3)
	got := []int64{}
	for _, ev := range sub.Replay {
		got = append(got, ev.Seq)
	}
	done := make(chan []int64)
	go func() {
		var live []int64
		for ev := range sub.Live {
			live = append(live, ev.Seq)
		}
		done <- live
	}()
	for i := 0; i < 3; i++ {
		_, _ = l.Append(run.ID, text("b"))
	}
	l.Finish(run.ID, StatusDone)
	got = append(got, <-done...)
	want := []int64{4, 5, 6, 7, 8, 9, 10}
	if len(got) != len(want) {
		t.Fatalf("seqs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seqs = %v, want %v", got, want)
		}
	}
}

func TestSubscribe_SlowSubscriberIsCutOffAndResumes(t *testing.T) {
	l, _ := openLog(t, Options{SubscriberBuffer: 2})
	run, _, _, _ := l.StartRun(context.Background(), "s1", "p1", "")
	sub, _ := l.Subscribe(run.ID, 0)
	for i := 0; i < 10; i++ {
		_, _ = l.Append(run.ID, text("x"))
	}
	var last int64
	for ev := range sub.Live { // closed after the buffer overflowed
		last = ev.Seq
	}
	if last == 0 || last >= 11 {
		t.Fatalf("slow subscriber got up to %d", last)
	}
	// It resumes from the store exactly where it stopped.
	again, _ := l.Subscribe(run.ID, last)
	if len(again.Replay) == 0 || again.Replay[0].Seq != last+1 {
		t.Fatalf("resume starts at %+v", again.Replay)
	}
	again.Close()
}

func TestDetachGrace_CancelsAnUnwatchedRun(t *testing.T) {
	l, _ := openLog(t, Options{DetachGrace: 100 * time.Millisecond})
	run, ctx, _, _ := l.StartRun(context.Background(), "s1", "p1", "")
	sub, _ := l.Subscribe(run.ID, 0)
	time.Sleep(200 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("a watched run was cancelled")
	}
	sub.Close() // the viewer goes away
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("unwatched run kept going past the grace")
	}
	// Reconnecting within the grace keeps a run alive.
	run2, ctx2, _, _ := l.StartRun(context.Background(), "s1", "p1", "")
	s1, _ := l.Subscribe(run2.ID, 0)
	s1.Close()
	time.Sleep(30 * time.Millisecond)
	s2, _ := l.Subscribe(run2.ID, 0)
	time.Sleep(200 * time.Millisecond)
	if ctx2.Err() != nil {
		t.Fatal("a reconnected run was cancelled")
	}
	s2.Close()
}

func TestStartRun_IdempotencyKey(t *testing.T) {
	l, _ := openLog(t, Options{})
	a, ctx, existing, _ := l.StartRun(context.Background(), "s1", "p1", "key-1")
	if existing || ctx == nil {
		t.Fatal("first start reported existing")
	}
	b, ctx2, existing, _ := l.StartRun(context.Background(), "s1", "p1", "key-1")
	if !existing || b.ID != a.ID || ctx2 != nil {
		t.Fatalf("duplicate start = %+v existing=%v", b, existing)
	}
	c, _, existing, _ := l.StartRun(context.Background(), "s1", "p2", "key-1")
	if existing || c.ID == a.ID {
		t.Fatal("another principal's key matched")
	}
}

func TestOpen_MarksRunsInterruptedAndPrunes(t *testing.T) {
	l, path := openLog(t, Options{})
	run, _, _, _ := l.StartRun(context.Background(), "s1", "p1", "")
	old, _, _, _ := l.StartRun(context.Background(), "s1", "p1", "")
	l.Finish(old.ID, StatusDone)
	_, _ = l.db.Exec(`UPDATE runs SET ended_at = ? WHERE id = ?`, time.Now().Add(-30*24*time.Hour).UnixMilli(), old.ID)
	_ = l.Close()

	l2, err := Open(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l2.Close() }()
	if r, _ := l2.Get(run.ID); r.Status != StatusInterrupted {
		t.Errorf("running run after restart = %s", r.Status)
	}
	if _, err := l2.Get(old.ID); err != ErrNotFound {
		t.Errorf("expired run not pruned: %v", err)
	}
	var n int
	_ = l2.db.QueryRow(`SELECT COUNT(*) FROM events WHERE run_id = ?`, old.ID).Scan(&n)
	if n != 0 {
		t.Errorf("%d events of a pruned run remain", n)
	}
}

func TestActivity_SkipsTextNewestFirst(t *testing.T) {
	l, _ := openLog(t, Options{})
	run, _, _, _ := l.StartRun(context.Background(), "s1", "p1", "")
	_, _ = l.Append(run.ID, text("hello"))
	_, _ = l.Append(run.ID, agentevents.Event{Type: agentevents.ToolStart, Tool: "read_file"})
	l.Finish(run.ID, StatusDone)
	other, _, _, _ := l.StartRun(context.Background(), "s2", "p1", "")
	l.Finish(other.ID, StatusCancelled)

	all, _ := l.Activity("", 10)
	if len(all) != 5 || all[0].Type != agentevents.RunFinished || all[0].Status != StatusCancelled {
		t.Fatalf("activity = %+v", all)
	}
	for _, ev := range all {
		if ev.Type == agentevents.Text {
			t.Error("text in the timeline")
		}
	}
	if s1, _ := l.Activity("s1", 10); len(s1) != 3 {
		t.Errorf("s1 activity = %d events", len(s1))
	}
}
