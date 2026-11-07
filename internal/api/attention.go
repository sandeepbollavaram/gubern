package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AttentionItem is one thing waiting on a person (GET /api/attention): a
// pending approval, a team checkpoint, a failing health check or an SLO
// breach. The dashboard's Inbox, its badge, Home and Ctrl+K all read this one
// list, so they never disagree (docs/design/dashboard-ux-spec.md §2.1).
type AttentionItem struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`     // approval | checkpoint | check | slo
	Severity string `json:"severity"` // critical | high | medium | info
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	// Href is the dashboard path that resolves the item.
	Href      string     `json:"href"`
	SessionID string     `json:"session_id,omitempty"`
	At        time.Time  `json:"at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

var severityRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "info": 3}

func riskSeverity(risk string) string {
	switch strings.ToUpper(risk) {
	case "CRITICAL", "CRITICAL — REVIEW CAREFULLY":
		return "critical"
	case "HIGH RISK":
		return "high"
	default:
		return "medium"
	}
}

// attention gathers every item from the wired providers, most urgent first.
func (s *Server) attention() []AttentionItem {
	items := []AttentionItem{}
	if lister, ok := s.agentRuntime.(pendingLister); ok && s.agentRuntime != nil {
		for _, p := range lister.PendingApprovals() {
			exp := p.ExpiresAt
			detail := p.Command
			if detail == "" {
				detail = p.Preview
			}
			if detail == "" {
				detail = p.Description
			}
			title := p.Tool
			if p.Workspace != "" {
				title += " in " + p.Workspace
			}
			items = append(items, AttentionItem{
				ID: "approval:" + p.ActionID, Kind: "approval", Severity: riskSeverity(p.Risk),
				Title: title, Detail: detail, SessionID: p.SessionID,
				Href: "/chat/" + url.PathEscape(p.SessionID) + "?approval=" + url.QueryEscape(p.ActionID),
				At:   p.CreatedAt, ExpiresAt: &exp,
			})
		}
	}
	if s.checkpoints != nil {
		for _, c := range s.checkpoints.List() {
			if c.Status != "" && c.Status != "pending" {
				continue
			}
			items = append(items, AttentionItem{
				ID: "checkpoint:" + c.ID, Kind: "checkpoint", Severity: "medium",
				Title: "Review: " + c.Title, Detail: c.Description, SessionID: c.SessionID,
				Href: "/chat/" + url.PathEscape(c.SessionID) + "?checkpoint=" + url.QueryEscape(c.ID),
				At:   c.CreatedAt,
			})
		}
	}
	if s.healingProvider != nil {
		h := s.healingProvider()
		for _, c := range h.Checks {
			if c.Healthy {
				continue
			}
			sev := "medium"
			if c.ConsecutiveFailures >= 3 {
				sev = "high"
			}
			items = append(items, AttentionItem{
				ID: "check:" + c.Name, Kind: "check", Severity: sev,
				Title:  "Health check failing: " + c.Name,
				Detail: plural(c.ConsecutiveFailures, "consecutive failure"),
				Href:   "/operate/healing", At: c.LastCheck,
			})
		}
		for _, a := range h.SLOAlerts {
			sev := "medium"
			if strings.EqualFold(a.AlertLevel, "critical") || strings.EqualFold(a.AlertLevel, "page") {
				sev = "high"
			}
			items = append(items, AttentionItem{
				ID: "slo:" + a.RouteName, Kind: "slo", Severity: sev,
				Title:  "SLO breach on route " + a.RouteName,
				Detail: "burn rate " + trimFloat(a.BurnRate) + "×, current " + trimFloat(a.Current*100) + "% vs target " + trimFloat(a.Target*100) + "%",
				Href:   "/operate/routes?route=" + url.QueryEscape(a.RouteName), At: time.Now(),
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := severityRank[items[i].Severity], severityRank[items[j].Severity]
		if ri != rj {
			return ri < rj
		}
		return items[i].At.Before(items[j].At)
	})
	return items
}

// handleAttention serves GET /api/attention.
func (s *Server) handleAttention(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": s.attention()})
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
