package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agents"
	"github.com/sandeepbollavaram/vortex/internal/api"
	"github.com/sandeepbollavaram/vortex/internal/approvals"
	"github.com/sandeepbollavaram/vortex/internal/audit"
	"github.com/sandeepbollavaram/vortex/internal/authz"
	"github.com/sandeepbollavaram/vortex/internal/identity"
	"github.com/sandeepbollavaram/vortex/internal/messaging"
)

// approvalsStorePath is the approval database beside the key and identity
// stores.
func approvalsStorePath(keyStorePath string) string {
	return filepath.Join(filepath.Dir(keyStorePath), "approvals.db")
}

// approvalTTL is the approval lifetime: GUBERN_APPROVAL_TTL (a Go duration
// between 10s and 24h), else 15 minutes.
func approvalTTL(log *slog.Logger) time.Duration {
	v := strings.TrimSpace(os.Getenv("GUBERN_APPROVAL_TTL"))
	if v == "" {
		return approvals.DefaultTTL
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Second || d > 24*time.Hour {
		log.Warn("ignoring GUBERN_APPROVAL_TTL (want a duration from 1s to 24h)", "value", v)
		return approvals.DefaultTTL
	}
	return d
}

// openApprovalService opens the persistent approval service with the
// identity-aware authorizer and mirrors every lifecycle event into the
// HMAC-chained audit log.
func openApprovalService(keyStorePath string, ids *identity.Store, auditLog *audit.Log, log *slog.Logger) (*approvals.Service, error) {
	opts := approvals.Options{
		TTL:        approvalTTL(log),
		Authorizer: approvalAuthorizer(ids, envTrue("GUBERN_REQUIRE_LINKED_APPROVERS")),
	}
	if auditLog != nil {
		opts.Audit = func(action string, a approvals.Approval) {
			actor := a.DecidedBy
			if actor == "" {
				actor = "agent"
			}
			_ = auditLog.Append(context.Background(), actor, action, "approval:"+a.ID, map[string]any{
				"session": a.SessionID, "tool": a.Tool, "risk": a.Risk, "action_hash": a.ActionHash,
				"status": string(a.Status), "via": a.DecidedVia, "reason": a.Reason,
			})
		}
	}
	svc, cancelled, err := approvals.Open(approvalsStorePath(keyStorePath), opts)
	if err != nil {
		return nil, err
	}
	if cancelled > 0 {
		log.Warn("approvals left pending by the previous run were cancelled; their actions did not run", "count", cancelled)
	}
	return svc, nil
}

// approvalAuthorizer decides who may decide an approval (design:
// docs/design/approval-service.md §4):
//
//   - API clients (dashboard, mobile, TUI) decide as their authenticated
//     principal, which must be a person holding approvals:decide on the
//     approval's session;
//   - a channel user linked to a principal decides as that principal, under
//     the same rule; a revoked link is refused;
//   - a channel user not linked yet is accepted on the channel's own
//     allow-list, unless GUBERN_REQUIRE_LINKED_APPROVERS is set. Pairing
//     (milestone F) creates the links.
func approvalAuthorizer(ids *identity.Store, requireLinked bool) approvals.Authorizer {
	check := func(a approvals.Approval, pid string) error {
		sub, err := ids.Subject(pid)
		if err != nil {
			return fmt.Errorf("%w: unknown principal", approvals.ErrForbidden)
		}
		owner, err := ids.Owner("session", a.SessionID)
		if err != nil {
			return fmt.Errorf("%w: ownership unavailable", approvals.ErrForbidden)
		}
		d := authz.Decide(sub, authz.ApprovalsDecide, &authz.Resource{Kind: "session", ID: a.SessionID, Owner: owner}, time.Now())
		if !d.Allow {
			return fmt.Errorf("%w: %s", approvals.ErrForbidden, d.Reason)
		}
		return nil
	}
	return func(_ context.Context, a approvals.Approval, d approvals.Decider) (approvals.Decider, error) {
		switch d.Channel {
		case "api", "dashboard", "mobile", "tui":
			if d.PrincipalID == "" {
				return d, fmt.Errorf("%w: no principal", approvals.ErrForbidden)
			}
			return d, check(a, d.PrincipalID)
		case "telegram", "whatsapp", "slack", "discord":
			if d.External == "" {
				return d, fmt.Errorf("%w: no channel identity", approvals.ErrForbidden)
			}
			if p, err := ids.ExternalPrincipal(d.Channel, d.External); err == nil {
				d.PrincipalID = p.ID
				return d, check(a, p.ID)
			}
			if ids.ExternalRevoked(d.Channel, d.External) {
				return d, fmt.Errorf("%w: channel identity revoked", approvals.ErrForbidden)
			}
			if requireLinked {
				return d, fmt.Errorf("%w: channel identity not linked to a person", approvals.ErrForbidden)
			}
			return d, nil
		default:
			return d, fmt.Errorf("%w: channel %q cannot decide", approvals.ErrForbidden, d.Channel)
		}
	}
}

// brokerApprovalFunc is an agents.ApprovalFunc backed by the approval
// service, for callers outside the coordinator (DevOps) that need a decision
// before acting.
func brokerApprovalFunc(svc *approvals.Service, session string) agents.ApprovalFunc {
	return func(ctx context.Context, req agents.ApprovalRequest) bool {
		tool := req.Tool
		if tool == "" {
			tool = "run_command"
		}
		a, err := svc.Create(ctx, approvals.CreateRequest{
			SessionID: session, Risk: req.Risk(),
			Action: approvals.Action{Tool: tool, Params: req.Params, Command: req.Command, Description: req.Description, Preview: req.Preview},
		})
		if err != nil {
			return false
		}
		final, err := svc.Wait(ctx, a.ID)
		return err == nil && final.Status == approvals.Approved
	}
}

// wireChannelApprovals connects the approval service to Telegram, WhatsApp
// and Slack: approvals are announced there, and their decisions go through
// the runtime's single decision path.
func wireChannelApprovals(svc *approvals.Service, rt *agents.Runtime, m *messagingComponents, log *slog.Logger) *messaging.ChannelApprovals {
	decide := func(ctx context.Context, session, id string, approve bool, d approvals.Decider) (string, error) {
		tr, _, err := rt.DecideApproval(ctx, agents.DecideRequest{SessionID: session, ApprovalID: id, Approve: approve, Decider: d})
		return tr, err
	}
	ca := messaging.NewChannelApprovals(decide)
	if m.telegram != nil {
		ca.WithTelegram(m.telegram, m.tgChat)
	}
	if m.whatsapp != nil {
		ca.WithWhatsApp(m.whatsapp, os.Getenv("GUBERN_WA_DEFAULT_PHONE"))
	}
	if m.slack != nil {
		m.slack.SetApprovals(ca)
	}
	svc.OnChange(ca.Notify)
	m.approvals = ca
	if m.telegram != nil || m.whatsapp != nil || m.slack != nil {
		log.Info("approvals can be decided from messaging channels", "telegram", m.telegram != nil, "whatsapp", m.whatsapp != nil, "slack", m.slack != nil)
	}
	return ca
}

// telegramApproveHook is the /approve and /reject command: a decision by the
// sending Telegram user.
func telegramApproveHook(ca *messaging.ChannelApprovals) func(ctx context.Context, session, id string, from int64, ok bool) (string, bool) {
	return func(ctx context.Context, session, id string, from int64, ok bool) (string, bool) {
		if ca == nil {
			return "Approvals are not available.", true
		}
		return ca.Decide(ctx, "telegram", strconv.FormatInt(from, 10), session, id, ok), true
	}
}

// DecideApproval implements the API's approval decider.
func (a *agentRuntimeAdapter) DecideApproval(ctx context.Context, sessionID, approvalID string, approve bool, actionHash string, d approvals.Decider) (string, approvals.Approval, error) {
	tr, ap, err := a.rt.DecideApproval(ctx, agents.DecideRequest{
		SessionID: sessionID, ApprovalID: approvalID, Approve: approve, ActionHash: actionHash, Decider: d,
	})
	switch {
	case errors.Is(err, agents.ErrNoPendingApproval):
		err = api.ErrNoPendingApproval
	case errors.Is(err, agents.ErrWrongSession):
		err = api.ErrWrongSession
	}
	return tr, ap, err
}
