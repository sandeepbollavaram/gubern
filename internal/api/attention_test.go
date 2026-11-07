package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type stubCheckpoints struct{ recs []CheckpointRecord }

func (s stubCheckpoints) List() []CheckpointRecord              { return s.recs }
func (stubCheckpoints) Approve(string) error                    { return nil }
func (stubCheckpoints) Reject(string, string) error             { return nil }
func (stubCheckpoints) Edit(string, []CheckpointFileEdit) error { return nil }

func TestAttention_MergesSourcesMostUrgentFirst(t *testing.T) {
	addr := newAgentTestServerWith(t, eventRuntime{}, func(s *Server) {
		s.SetCheckpointProvider(stubCheckpoints{recs: []CheckpointRecord{
			{ID: "cp1", SessionID: "team-1", Title: "Implement the API", Status: "pending", CreatedAt: time.Now().Add(-time.Minute)},
			{ID: "cp2", SessionID: "team-1", Title: "Already approved", Status: "approved"},
		}})
		s.SetHealingProvider(func() HealingStatus {
			return HealingStatus{
				Checks: []HealingCheck{
					{Name: "api-backend-2", Healthy: false, ConsecutiveFailures: 4, LastCheck: time.Now()},
					{Name: "web", Healthy: true},
				},
				SLOAlerts: []HealingSLOAlert{{RouteName: "api", Target: 0.999, Current: 0.98, BurnRate: 14.4, AlertLevel: "warning"}},
			}
		})
	})
	resp, err := http.Get("http://" + addr + "/api/attention")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Items []AttentionItem `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, it := range body.Items {
		kinds = append(kinds, it.Kind+"/"+it.Severity)
	}
	// High first (approval is older than the failing check), then medium.
	if got, want := strings.Join(kinds, ","), "approval/high,check/high,checkpoint/medium,slo/medium"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	a := body.Items[0]
	if a.Href != "/chat/s1?approval=abc" || a.ExpiresAt == nil || a.SessionID != "s1" {
		t.Errorf("approval item = %+v", a)
	}
	for _, it := range body.Items {
		if strings.Contains(it.Title, "Already approved") || strings.Contains(it.Title, "web") {
			t.Errorf("resolved or healthy item listed: %+v", it)
		}
	}
}

func TestAttention_EmptyIsArray(t *testing.T) {
	addr := newAgentTestServer(t, stubRuntime{})
	resp, err := http.Get("http://" + addr + "/api/attention")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&raw)
	if string(raw["items"]) != "[]" {
		t.Errorf("items = %s, want []", raw["items"])
	}
}
