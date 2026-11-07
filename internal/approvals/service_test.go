package approvals

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func allowAll(_ context.Context, _ Approval, d Decider) (Decider, error) { return d, nil }

func open(t *testing.T, opts Options) (*Service, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "approvals.db")
	if opts.Authorizer == nil {
		opts.Authorizer = allowAll
	}
	s, _, err := Open(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func req(cmd string) CreateRequest {
	return CreateRequest{SessionID: "s1", Risk: "HIGH RISK", Action: Action{
		Tool: "run_terminal", Params: map[string]any{"command": cmd, "cwd": "/w"}, Description: "Run " + cmd,
	}}
}

var api = Decider{PrincipalID: "owner", Channel: "api"}

func TestHashAction_Canonical(t *testing.T) {
	a := Action{Tool: "write_file", Params: map[string]any{"path": "a", "content": "x", "opts": map[string]any{"b": 1, "a": 2}}}
	b := Action{Tool: "write_file", Params: map[string]any{"opts": map[string]any{"a": 2, "b": 1}, "content": "x", "path": "a"}}
	if HashAction(a) != HashAction(b) {
		t.Fatal("key order changed the hash")
	}
	if len(HashAction(a)) != 64 {
		t.Fatalf("hash %q is not a full SHA-256", HashAction(a))
	}
	c := b
	c.Params = map[string]any{"opts": map[string]any{"a": 2, "b": 1}, "content": "y", "path": "a"}
	if HashAction(a) == HashAction(c) {
		t.Fatal("a changed parameter kept the hash")
	}
	d := a
	d.Preview = "other"
	if HashAction(a) == HashAction(d) {
		t.Fatal("a changed preview kept the hash")
	}
}

func TestDecide_FirstDecisionWinsUnderRace(t *testing.T) {
	s, _ := open(t, Options{})
	a, err := s.Create(context.Background(), req("ls"))
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	channels := []string{"api", "telegram", "whatsapp", "slack", "tui", "mobile"}
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Decide(context.Background(), a.ID, Decision{
				Approve: i%2 == 0, Decider: Decider{Channel: channels[i%len(channels)], External: "u"},
			})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrNotPending) {
				t.Errorf("loser got %v, want ErrNotPending", err)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d decisions won, want exactly 1", wins.Load())
	}
	got, _ := s.Get(a.ID)
	if got.Status != Approved && got.Status != Rejected || got.DecidedBy == "" || got.DecidedVia == "" || got.DecidedAt == nil {
		t.Fatalf("final record = %+v", got)
	}
	// Replaying a decision is refused and changes nothing.
	if _, err := s.Decide(context.Background(), a.ID, Decision{Approve: true, Decider: api}); !errors.Is(err, ErrNotPending) {
		t.Errorf("replay err = %v", err)
	}
	if again, _ := s.Get(a.ID); again.Status != got.Status || again.DecidedBy != got.DecidedBy {
		t.Errorf("replay changed the record: %+v", again)
	}
}

func TestDecide_ExpiryAndMismatch(t *testing.T) {
	s, _ := open(t, Options{TTL: time.Minute})
	clock := time.Now()
	s.now = func() time.Time { return clock }
	a, _ := s.Create(context.Background(), req("rm -rf build"))

	// A decision bound to a different action is refused and leaves it open.
	other := HashAction(req("rm -rf /").Action)
	if _, err := s.Decide(context.Background(), a.ID, Decision{Approve: true, ActionHash: other, Decider: api}); !errors.Is(err, ErrActionMismatch) {
		t.Fatalf("mismatch err = %v", err)
	}
	if got, _ := s.Get(a.ID); got.Status != Pending {
		t.Fatalf("mismatch changed status to %s", got.Status)
	}
	// The matching hash would work, but time passes first.
	clock = clock.Add(time.Minute)
	if _, err := s.Decide(context.Background(), a.ID, Decision{Approve: true, ActionHash: a.ActionHash, Decider: api}); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired err = %v", err)
	}
	if got, _ := s.Get(a.ID); got.Status != Expired {
		t.Fatalf("status = %s, want expired", got.Status)
	}
	// List reports due approvals as expired too.
	b, _ := s.Create(context.Background(), req("ls"))
	clock = clock.Add(2 * time.Minute)
	pend, _ := s.List(Filter{Status: Pending})
	if len(pend) != 0 {
		t.Errorf("pending after expiry = %+v", pend)
	}
	if got, _ := s.Get(b.ID); got.Status != Expired {
		t.Errorf("listed approval status = %s", got.Status)
	}
}

func TestOpen_CancelsPendingFromAPreviousProcess(t *testing.T) {
	s, path := open(t, Options{})
	a, _ := s.Create(context.Background(), req("ls"))
	decided, _ := s.Create(context.Background(), req("pwd"))
	if _, err := s.Decide(context.Background(), decided.ID, Decision{Approve: true, Decider: api}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	var audited []string
	s2, n, err := Open(path, Options{Authorizer: allowAll, Audit: func(action string, _ Approval) { audited = append(audited, action) }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if n != 1 {
		t.Fatalf("cancelled %d, want 1", n)
	}
	got, _ := s2.Get(a.ID)
	if got.Status != Cancelled || got.Reason != "server restarted" {
		t.Fatalf("after restart = %+v", got)
	}
	if _, err := s2.Decide(context.Background(), a.ID, Decision{Approve: true, Decider: api}); !errors.Is(err, ErrNotPending) {
		t.Fatalf("decide after restart err = %v", err)
	}
	if kept, _ := s2.Get(decided.ID); kept.Status != Approved {
		t.Errorf("a decided approval changed on restart: %s", kept.Status)
	}
	if len(audited) != 1 || audited[0] != "approval.cancelled" {
		t.Errorf("audit = %v", audited)
	}
}

func TestWait_ReturnsDecisionExpiryOrCancel(t *testing.T) {
	s, _ := open(t, Options{TTL: 300 * time.Millisecond})
	a, _ := s.Create(context.Background(), req("ls"))
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = s.Decide(context.Background(), a.ID, Decision{Approve: true, Decider: api})
	}()
	got, err := s.Wait(context.Background(), a.ID)
	if err != nil || got.Status != Approved || got.DecidedBy != "owner" {
		t.Fatalf("wait = %+v %v", got, err)
	}

	b, _ := s.Create(context.Background(), req("ls"))
	got, _ = s.Wait(context.Background(), b.ID)
	if got.Status != Expired {
		t.Fatalf("wait past expiry = %s", got.Status)
	}

	c, _ := s.Create(context.Background(), req("ls"))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	got, _ = s.Wait(ctx, c.ID)
	if got.Status != Cancelled {
		t.Fatalf("wait after cancel = %s", got.Status)
	}
	if _, err := s.Decide(context.Background(), c.ID, Decision{Approve: true, Decider: api}); !errors.Is(err, ErrNotPending) {
		t.Errorf("decided a cancelled approval: %v", err)
	}
}

func TestDecide_Authorization(t *testing.T) {
	refuse := func(_ context.Context, _ Approval, d Decider) (Decider, error) {
		if d.PrincipalID == "owner" {
			return d, nil
		}
		return d, ErrForbidden
	}
	s, _ := open(t, Options{Authorizer: refuse})
	a, _ := s.Create(context.Background(), req("ls"))
	if _, err := s.Decide(context.Background(), a.ID, Decision{Approve: true, Decider: Decider{PrincipalID: "mallory", Channel: "api"}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("refused decider err = %v", err)
	}
	if _, err := s.Decide(context.Background(), a.ID, Decision{Approve: true, Decider: Decider{PrincipalID: "owner"}}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("decision without a channel err = %v", err)
	}
	if got, _ := s.Get(a.ID); got.Status != Pending {
		t.Fatalf("refused decisions changed status: %s", got.Status)
	}
	if _, err := s.Decide(context.Background(), a.ID, Decision{Approve: false, Decider: api, Reason: "no"}); err != nil {
		t.Fatal(err)
	}

	// No authorizer configured: nothing is accepted.
	s2, _, err := Open(filepath.Join(t.TempDir(), "x.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	b, _ := s2.Create(context.Background(), req("ls"))
	if _, err := s2.Decide(context.Background(), b.ID, Decision{Approve: true, Decider: api}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("no authorizer err = %v", err)
	}
	if _, err := s2.Decide(context.Background(), "nope", Decision{Approve: true, Decider: api}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id err = %v", err)
	}
}

func TestOnChange_SeesLifecycle(t *testing.T) {
	s, _ := open(t, Options{})
	var mu sync.Mutex
	var seen []Status
	s.OnChange(func(a Approval) { mu.Lock(); seen = append(seen, a.Status); mu.Unlock() })
	a, _ := s.Create(context.Background(), req("ls"))
	_, _ = s.Decide(context.Background(), a.ID, Decision{Approve: true, Decider: api})
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != Pending || seen[1] != Approved {
		t.Fatalf("hook saw %v", seen)
	}
}
