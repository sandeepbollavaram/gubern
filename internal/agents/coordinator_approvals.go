package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agentevents"
	"github.com/sandeepbollavaram/vortex/internal/approvals"
)

// ApprovalBroker is the persistent approval service (internal/approvals) as
// the coordinator uses it. With a broker every approval-gated action is a
// stored approval decided once through DecideApproval, whichever channel the
// decision comes from (milestone B, docs/design/approval-service.md).
type ApprovalBroker interface {
	Create(ctx context.Context, req approvals.CreateRequest) (approvals.Approval, error)
	Wait(ctx context.Context, id string) (approvals.Approval, error)
	Decide(ctx context.Context, id string, d approvals.Decision) (approvals.Approval, error)
	Get(id string) (approvals.Approval, error)
	List(f approvals.Filter) ([]approvals.Approval, error)
	Cancel(id, reason string)
}

// SetApprovals wires the persistent approval service. Call before serving.
func (c *Coordinator) SetApprovals(b ApprovalBroker) {
	c.mu.Lock()
	c.cfg.Approvals = b
	c.mu.Unlock()
}

func (c *Coordinator) broker() ApprovalBroker {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg.Approvals
}

// Errors from DecideApproval besides the approvals package's own.
var (
	ErrNoPendingApproval = errors.New("agents: no pending approval for this session")
	ErrNoApprovalService = errors.New("agents: approval service not configured")
	ErrWrongSession      = errors.New("agents: approval belongs to another session")
)

// DecideRequest is one decision from any channel.
type DecideRequest struct {
	// ApprovalID names the approval. When empty, the session's current
	// pending approval is used (the legacy "/approve" form).
	ApprovalID string
	SessionID  string
	Approve    bool
	// ActionHash, when set, must equal the approval's hash.
	ActionHash string
	Decider    approvals.Decider
	Reason     string
}

// copyParams deep-copies params through JSON so later changes to the live
// map can't alter the approved action.
func copyParams(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(params)
	if err != nil {
		out := make(map[string]any, len(params))
		for k, v := range params {
			out[k] = v
		}
		return out
	}
	var out map[string]any
	if json.Unmarshal(b, &out) != nil {
		return params
	}
	return out
}

// approvalAction is the canonical action an approval request authorizes.
func approvalAction(tool string, params map[string]any, req ApprovalRequest) approvals.Action {
	cmd := req.Command
	if cmd != "" && len(req.Args) > 0 {
		for _, a := range req.Args {
			cmd += " " + a
		}
	}
	// Local terminal tools carry the command as a parameter: show it as the
	// approval's command too, so every client (and an automated eval
	// approver) sees exactly what will run.
	if c, ok := params["command"].(string); ok && cmd == "" {
		cmd = c
	}
	if tool == "" {
		tool = req.Tool
	}
	if tool == "" && req.Command != "" {
		tool = "run_command"
	}
	return approvals.Action{
		Tool: tool, Params: copyParams(params), Command: cmd,
		Description: req.Description, Preview: req.Preview,
	}
}

// pendingFromApproval is what clients see for a stored approval.
func pendingFromApproval(a approvals.Approval) agentevents.Pending {
	return agentevents.Pending{
		SessionID: a.SessionID, ActionID: a.ID, ActionHash: a.ActionHash, Tool: a.Tool, Risk: a.Risk,
		Description: a.Description, Command: a.Command,
		Preview:   agentevents.Truncate(a.Preview, agentevents.MaxOutput),
		Workspace: a.Workspace, CreatedAt: a.CreatedAt, ExpiresAt: a.ExpiresAt,
	}
}

// openApproval stores a pending approval for req and registers it for the
// session. tool is set for actions that the decision itself executes (the
// async path); nil when a run waits for the decision.
func (c *Coordinator) openApproval(ctx context.Context, b ApprovalBroker, session, name string, tool Tool, params map[string]any, req ApprovalRequest) (*pendingApproval, approvals.Approval, error) {
	action := approvalAction(name, params, req)
	a, err := b.Create(ctx, approvals.CreateRequest{
		SessionID: session, Action: action, Risk: req.Risk(), Workspace: c.cfg.WorkingDir,
	})
	if err != nil {
		return nil, approvals.Approval{}, err
	}
	pa := &pendingApproval{
		toolName: action.Tool, tool: tool, params: params, result: make(chan bool, 1),
		description: req.Description, actionID: a.ID, actionHash: a.ActionHash, approvalReq: req,
		created: a.CreatedAt, info: pendingFromApproval(a),
	}
	c.mu.Lock()
	if old := c.pending[session]; old != nil && old.actionHash != "" {
		// A session has one open action at a time; the older one can no
		// longer be decided.
		defer b.Cancel(old.actionID, "superseded by a newer action in the session")
	}
	c.pending[session] = pa
	c.mu.Unlock()
	return pa, a, nil
}

// requestApprovalBrokered is ExecuteLocalTool's approval step with the
// approval service: the action is stored and announced, and the call returns
// at once; the decision (from any channel) executes it via DecideApproval.
func (c *Coordinator) requestApprovalBrokered(ctx context.Context, b ApprovalBroker, session, name string, tool Tool, params map[string]any, req ApprovalRequest, emit func(string), stepEnd func(status, output string)) (any, error) {
	pa, a, err := c.openApproval(ctx, b, session, name, tool, params, req)
	if err != nil {
		emit("⚠ approval service unavailable; action not run")
		stepEnd("failed", "approval service unavailable")
		return nil, fmt.Errorf("agents: approval service unavailable: %w", err)
	}
	stepEnd("waiting", "")
	info := pa.info
	agentevents.Emit(ctx, agentevents.Event{Type: agentevents.ApprovalRequired, SessionID: session, Tool: name, Approval: &info})
	emit(approvalLine(req, a.ID))
	if req.Preview != "" {
		emit(req.Preview)
	}
	return nil, nil
}

// loopApproverBrokered is the loop's approval function with the approval
// service: the run waits for the stored approval's final status.
func (c *Coordinator) loopApproverBrokered(b ApprovalBroker, sessionID string, emit func(string)) ApprovalFunc {
	return func(ctx context.Context, req ApprovalRequest) bool {
		pa, a, err := c.openApproval(ctx, b, sessionID, req.Tool, nil, req.Params, req)
		if err != nil {
			emit("⚠ approval service unavailable; action not run")
			return false
		}
		info := pa.info
		agentevents.Emit(ctx, agentevents.Event{Type: agentevents.ApprovalRequired, SessionID: sessionID, Tool: req.Tool, Approval: &info})
		emit(approvalLine(req, a.ID))
		if req.Preview != "" {
			emit(req.Preview)
		}
		final, err := b.Wait(ctx, a.ID)
		c.mu.Lock()
		if c.pending[sessionID] == pa {
			delete(c.pending, sessionID)
		}
		c.mu.Unlock()
		approved := err == nil && final.Status == approvals.Approved
		// The loop runs req as given: confirm it is still the approved action.
		if approved && approvals.HashAction(approvalAction(req.Tool, req.Params, req)) != final.ActionHash {
			slog.Default().Error("approved action changed before it ran; refusing", "approval", a.ID)
			approved = false
		}
		status := string(final.Status)
		if status == "" {
			status = "cancelled"
		}
		agentevents.Emit(ctx, agentevents.Event{
			Type: agentevents.ApprovalDecided, SessionID: sessionID, Tool: req.Tool, Status: status,
			DecidedVia: final.DecidedVia, DecidedBy: final.DecidedBy, Approval: &info,
		})
		if !approved && final.Status == approvals.Expired {
			emit("✗ Approval expired; the action was not run")
		}
		return approved
	}
}

// DecideApproval applies one decision from any channel (API and dashboard,
// TUI, Telegram, WhatsApp, Slack, ...). The approval service decides
// atomically and authorizes the decider; when this call wins and the action
// was parked for its decision, the action runs here, after its hash is
// checked again. It returns a transcript for the decider.
func (c *Coordinator) DecideApproval(ctx context.Context, r DecideRequest) (string, approvals.Approval, error) {
	b := c.broker()
	if b == nil {
		return "", approvals.Approval{}, ErrNoApprovalService
	}
	id := r.ApprovalID
	if id == "" {
		c.mu.Lock()
		pa := c.pending[r.SessionID]
		c.mu.Unlock()
		if pa == nil || pa.actionHash == "" {
			return "", approvals.Approval{}, ErrNoPendingApproval
		}
		id = pa.actionID
	} else if r.SessionID != "" {
		cur, err := b.Get(id)
		if err != nil {
			return "", cur, err
		}
		if cur.SessionID != r.SessionID {
			return "", approvals.Approval{}, ErrWrongSession
		}
	}
	a, err := b.Decide(ctx, id, approvals.Decision{
		Approve: r.Approve, ActionHash: r.ActionHash, Decider: r.Decider, Reason: r.Reason,
	})
	if err != nil {
		return "", a, err
	}
	approved := a.Status == approvals.Approved

	c.mu.Lock()
	pa := c.pending[a.SessionID]
	if pa != nil && pa.actionID == a.ID && pa.tool != nil {
		delete(c.pending, a.SessionID)
	} else {
		pa = nil // a run is waiting on the approval, or nothing is parked
	}
	c.mu.Unlock()

	if pa == nil {
		if approved {
			return "✓ Action approved", a, nil
		}
		return "✗ Action rejected", a, nil
	}
	if !approved {
		return "✗ Action rejected by user", a, nil
	}
	if approvals.HashAction(approvalAction(pa.toolName, pa.params, pa.approvalReq)) != a.ActionHash {
		slog.Default().Error("parked action changed after approval; refusing", "approval", a.ID)
		return "⚠ The action changed after it was approved; it was not run", a, nil
	}
	ectx, cancel := context.WithTimeout(context.WithoutCancel(ctx), handleMessageTimeout)
	defer cancel()
	result, xerr := executeApprovedLocal(ectx, pa.tool, pa.params)
	c.auditExecuted(ectx, a.SessionID, pa.toolName, xerr)
	if xerr != nil {
		return "⚠ " + xerr.Error(), a, nil
	}
	return toolDoneLine(pa.toolName, result), a, nil
}

// pendingApprovalsBrokered lists open approvals from the store, oldest first.
func pendingApprovalsBrokered(b ApprovalBroker) []agentevents.Pending {
	list, err := b.List(approvals.Filter{Status: approvals.Pending, Limit: 500})
	if err != nil {
		slog.Default().Warn("listing approvals failed", "err", err)
		return []agentevents.Pending{}
	}
	out := make([]agentevents.Pending, 0, len(list))
	now := time.Now()
	for _, a := range list {
		if now.Before(a.ExpiresAt) {
			out = append(out, pendingFromApproval(a))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// runToolBrokered asks the approval service for a sandboxed tool action and
// waits for the decision.
func (c *Coordinator) runToolBrokered(ctx context.Context, b ApprovalBroker, req ApprovalRequest) bool {
	a, err := b.Create(ctx, approvals.CreateRequest{
		SessionID: "sandbox", Action: approvalAction(req.Tool, req.Params, req), Risk: req.Risk(), Workspace: c.cfg.WorkingDir,
	})
	if err != nil {
		return false
	}
	final, err := b.Wait(ctx, a.ID)
	return err == nil && final.Status == approvals.Approved
}
