package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sandeepbollavaram/vortex/internal/approvals"
)

// ApprovalDecider applies one decision through the shared approval service
// (agents.Runtime.DecideApproval). Every channel uses it; none has its own
// privileged path (milestone B). approvalID may be empty with a session to
// decide that session's current approval.
type ApprovalDecider func(ctx context.Context, sessionID, approvalID string, approve bool, d approvals.Decider) (transcript string, err error)

// ChannelApprovals connects the approval service to the messaging channels:
// it announces every new approval with Approve/Reject buttons and turns a
// button press or an /approve command from any channel into a decision
// carrying the sender's identity.
type ChannelApprovals struct {
	decide   ApprovalDecider
	telegram *TelegramBot
	tgChat   int64
	whatsapp *WhatsAppBot
	waTo     string
}

// NewChannelApprovals builds the bridge around decide.
func NewChannelApprovals(decide ApprovalDecider) *ChannelApprovals {
	return &ChannelApprovals{decide: decide}
}

// WithTelegram announces approvals in chat and accepts its button presses.
func (c *ChannelApprovals) WithTelegram(bot *TelegramBot, chat int64) *ChannelApprovals {
	c.telegram, c.tgChat = bot, chat
	if bot != nil {
		bot.SetCallbackResolver(c)
	}
	return c
}

// WithWhatsApp announces approvals to the number to.
func (c *ChannelApprovals) WithWhatsApp(bot *WhatsAppBot, to string) *ChannelApprovals {
	c.whatsapp, c.waTo = bot, to
	if bot != nil {
		bot.SetApprovals(c)
	}
	return c
}

// Notify is the approval service's change hook: a new approval is announced
// on every configured channel; a final one is reported where it was not
// decided, so a phone never shows buttons for an action already settled.
// Sends run in the background so the agent never waits on a channel.
func (c *ChannelApprovals) Notify(a approvals.Approval) {
	var text string
	if a.Status == approvals.Pending {
		text = approvalPrompt(a)
	} else {
		text = approvalOutcome(a)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if c.telegram != nil && c.tgChat != 0 && (a.Status == approvals.Pending || a.DecidedVia != "telegram") {
			var err error
			if a.Status == approvals.Pending {
				err = c.telegram.SendApprovalRequest(ctx, c.tgChat, text, "approve:"+a.ID, "reject:"+a.ID)
			} else {
				err = c.telegram.SendMessage(ctx, c.tgChat, text)
			}
			if err != nil {
				slog.Default().Warn("telegram approval notice failed", "approval", a.ID, "err", err)
			}
		}
		if c.whatsapp != nil && c.waTo != "" && (a.Status == approvals.Pending || a.DecidedVia != "whatsapp") {
			var err error
			if a.Status == approvals.Pending {
				err = c.whatsapp.SendApprovalRequest(ctx, c.waTo, text, a.ID)
			} else {
				err = c.whatsapp.SendText(ctx, c.waTo, text)
			}
			if err != nil {
				slog.Default().Warn("whatsapp approval notice failed", "approval", a.ID, "err", err)
			}
		}
	}()
}

// Resolve implements CallbackResolver for callers without the sender; a
// decision always needs one, so it only reports whether the data is an
// approval decision (and is then consumed, not re-submitted as a message).
func (c *ChannelApprovals) Resolve(data string) bool {
	_, _, ok := parseDecision(data)
	return ok
}

// ResolveFrom decides an approval from a Telegram button press by fromID and
// replies in chatID.
func (c *ChannelApprovals) ResolveFrom(ctx context.Context, data string, fromID, chatID int64) bool {
	id, approve, ok := parseDecision(data)
	if !ok {
		return false
	}
	reply := c.Decide(ctx, "telegram", strconv.FormatInt(fromID, 10), "", id, approve)
	if c.telegram != nil {
		_ = c.telegram.SendMessage(ctx, chatID, reply)
	}
	return true
}

// Decide applies a decision from channel by the channel user external and
// returns the reply to show them.
func (c *ChannelApprovals) Decide(ctx context.Context, channel, external, sessionID, approvalID string, approve bool) string {
	if c == nil || c.decide == nil {
		return "Approvals are not available."
	}
	tr, err := c.decide(ctx, sessionID, approvalID, approve, approvals.Decider{Channel: channel, External: external})
	return DecisionReply(approvalID, approve, tr, err)
}

// DecisionReply is the user-facing text for a decision outcome.
func DecisionReply(id string, approve bool, transcript string, err error) string {
	label := "the pending action"
	if id != "" {
		label = id
	}
	switch {
	case err == nil && approve:
		return "✅ Approved " + label + "\n" + transcript
	case err == nil:
		return "❌ Rejected " + label
	case errors.Is(err, approvals.ErrForbidden):
		return "⛔ You may not decide " + label + "."
	case errors.Is(err, approvals.ErrExpired):
		return "⌛ " + label + " expired; nothing was run."
	case errors.Is(err, approvals.ErrNotPending):
		return "ℹ️ " + label + " was already decided."
	case errors.Is(err, approvals.ErrActionMismatch):
		return "⚠️ " + label + " no longer matches the action you saw; nothing was run."
	case errors.Is(err, approvals.ErrNotFound):
		return "No approval " + label + "."
	default:
		if strings.Contains(err.Error(), "no pending approval") {
			return "No pending action to " + map[bool]string{true: "approve", false: "reject"}[approve] + "."
		}
		return "⚠️ " + err.Error()
	}
}

// parseDecision reads "approve:<id>" or "reject:<id>".
func parseDecision(data string) (id string, approve, ok bool) {
	switch {
	case strings.HasPrefix(data, "approve:"):
		id, approve = strings.TrimPrefix(data, "approve:"), true
	case strings.HasPrefix(data, "reject:"):
		id = strings.TrimPrefix(data, "reject:")
	default:
		return "", false, false
	}
	if !validApprovalID(id) {
		return "", false, false
	}
	return id, approve, true
}

// validApprovalID accepts the service's 16-hex-digit ids only.
func validApprovalID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// maxApprovalPreview caps the preview so the prompt stays well under
// Telegram's 4096-character message limit.
const maxApprovalPreview = 3000

// approvalPrompt renders what the approver is actually signing off on: the
// tool, its risk level, the one-line summary, the command or content preview,
// the approval id and the action's fingerprint (the start of its SHA-256). An
// approver must never see a prompt that is identical for every action (audit
// F-01). The text is sent without a parse mode, so the model-influenced
// fields cannot inject formatting.
func approvalPrompt(a approvals.Approval) string {
	var b strings.Builder
	b.WriteString("🔐 Approval required (" + a.ID + ")\n")
	if a.Tool != "" {
		b.WriteString("Tool: " + a.Tool + "\n")
	}
	if a.Risk != "" {
		b.WriteString("Risk: " + a.Risk + "\n")
	}
	if a.Description != "" {
		b.WriteString("Action: " + a.Description + "\n")
	}
	if a.Command != "" {
		b.WriteString("Command: " + a.Command + "\n")
	}
	if a.SessionID != "" {
		b.WriteString("Session: " + a.SessionID + "\n")
	}
	if len(a.ActionHash) >= 16 {
		b.WriteString("Fingerprint: " + a.ActionHash[:16] + "\n")
	}
	if !a.ExpiresAt.IsZero() {
		b.WriteString(fmt.Sprintf("Expires in: %s\n", time.Until(a.ExpiresAt).Round(time.Minute)))
	}
	preview := a.Preview
	if preview == "" && a.Command == "" && a.Description == "" {
		preview = "(no details were supplied for this action — reject unless you expected it)"
	}
	if preview != "" {
		if len(preview) > maxApprovalPreview {
			cut := maxApprovalPreview
			for cut > 0 && !utf8.RuneStart(preview[cut]) {
				cut--
			}
			preview = preview[:cut] + "\n…(truncated)"
		}
		b.WriteString("\n" + preview)
	}
	return b.String()
}

// approvalOutcome tells a channel how an approval ended.
func approvalOutcome(a approvals.Approval) string {
	who := a.DecidedBy
	if who == "" {
		who = "the server"
	}
	switch a.Status {
	case approvals.Approved:
		return "✅ " + a.ID + " (" + a.Tool + ") was approved via " + a.DecidedVia + " by " + who + "."
	case approvals.Rejected:
		return "❌ " + a.ID + " (" + a.Tool + ") was rejected via " + a.DecidedVia + " by " + who + "."
	case approvals.Expired:
		return "⌛ " + a.ID + " (" + a.Tool + ") expired without a decision."
	default:
		reason := a.Reason
		if reason == "" {
			reason = "cancelled"
		}
		return "ℹ️ " + a.ID + " (" + a.Tool + ") was closed: " + reason + "."
	}
}
