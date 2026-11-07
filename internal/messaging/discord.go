package messaging

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sandeepbollavaram/vortex/internal/agents"
)

// Discord (milestone I, docs/design/discord.md): Gubern answers Discord's
// HTTP interactions endpoint. Every request is verified with the
// application's Ed25519 public key. Slash commands /ask, /approve, /reject
// and /pair, and the Approve/Reject buttons on approval notices, go through
// the same approval service and identity checks as every other channel.

// DiscordConfig configures the bot.
type DiscordConfig struct {
	// PublicKey is the application's Ed25519 public key (hex), from the
	// Discord developer portal. Without it the interactions endpoint is not
	// mounted.
	PublicKey string
	// BotToken authorizes outbound messages (approval notices).
	BotToken string
	// ApprovalChannel is the channel id approval notices are posted to.
	ApprovalChannel string
	// AllowedUsers are Discord user ids allowed before pairing.
	AllowedUsers []string
	// BaseURL overrides https://discord.com/api/v10 (tests, proxies).
	BaseURL string
	Client  *http.Client
}

// DiscordBot handles interactions and posts notices.
type DiscordBot struct {
	cfg      DiscordConfig
	pub      ed25519.PublicKey
	base     string
	client   *http.Client
	ident    ChannelIdentity
	pairings pairLimiter
	appr     *ChannelApprovals
	inflight sync.WaitGroup
}

// NewDiscordBot validates cfg.
func NewDiscordBot(cfg DiscordConfig) (*DiscordBot, error) {
	pk, err := hex.DecodeString(strings.TrimSpace(cfg.PublicKey))
	if err != nil || len(pk) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("messaging: discord public key must be 64 hex digits")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://discord.com/api/v10"
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &DiscordBot{cfg: cfg, pub: pk, base: base, client: client}, nil
}

// SetIdentity connects the bot to the identity store (pairing, revocation).
func (d *DiscordBot) SetIdentity(ci ChannelIdentity) { d.ident = ci }

// SetApprovals lets the bot decide approvals and post approval notices.
func (d *DiscordBot) SetApprovals(c *ChannelApprovals) { d.appr = c }

// allowed reports whether Discord user id may use the bot: revocation wins,
// then the allow-list, then a pairing link.
func (d *DiscordBot) allowed(id string) bool {
	if id == "" || d.ident.revoked("discord", id) {
		return false
	}
	for _, u := range d.cfg.AllowedUsers {
		if strings.TrimSpace(u) == id {
			return true
		}
	}
	return d.ident.linked("discord", id)
}

// verify checks Discord's signature over timestamp+body. Requests older than
// five minutes are refused, so a captured request can't be replayed later.
func (d *DiscordBot) verify(r *http.Request, body []byte) bool {
	sig, err := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	ts := r.Header.Get("X-Signature-Timestamp")
	if err != nil || len(sig) != ed25519.SignatureSize || ts == "" {
		return false
	}
	if sec, err := strconv.ParseInt(ts, 10, 64); err != nil || time.Since(time.Unix(sec, 0)).Abs() > 5*time.Minute {
		return false
	}
	return ed25519.Verify(d.pub, append([]byte(ts), body...), sig)
}

type discordUser struct {
	ID string `json:"id"`
}

type discordInteraction struct {
	Type          int    `json:"type"`
	ID            string `json:"id"`
	Token         string `json:"token"`
	ApplicationID string `json:"application_id"`
	GuildID       string `json:"guild_id"`
	Member        *struct {
		User discordUser `json:"user"`
	} `json:"member"`
	User *discordUser `json:"user"`
	Data struct {
		Name     string `json:"name"`
		CustomID string `json:"custom_id"`
		Options  []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"options"`
	} `json:"data"`
}

func (i discordInteraction) userID() string {
	if i.Member != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func (i discordInteraction) option(name string) string {
	for _, o := range i.Data.Options {
		if o.Name == name {
			if s, ok := o.Value.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// Interaction and response types (Discord API).
const (
	discordPing               = 1
	discordCommand            = 2
	discordComponent          = 3
	discordPong               = 1
	discordMessage            = 4
	discordDeferredMessage    = 5
	discordEphemeral          = 64
	discordButtonStyleSuccess = 3
	discordButtonStyleDanger  = 4
)

func ephemeral(text string) map[string]any {
	if len(text) > 1900 {
		text = text[:1900] + "…"
	}
	return map[string]any{"type": discordMessage, "data": map[string]any{"content": text, "flags": discordEphemeral, "allowed_mentions": map[string]any{"parse": []string{}}}}
}

// HandleInteractions is the /webhook/discord handler.
func (d *DiscordBot) HandleInteractions(runtime *agents.Runtime) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}
		if !d.verify(r, body) {
			http.Error(w, "invalid request signature", http.StatusUnauthorized)
			return
		}
		var in discordInteraction
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "bad interaction", http.StatusBadRequest)
			return
		}
		reply := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		user := in.userID()
		ctx := context.WithoutCancel(r.Context())
		switch in.Type {
		case discordPing:
			reply(map[string]any{"type": discordPong})
		case discordComponent:
			id, approve, ok := parseDecision(in.Data.CustomID)
			switch {
			case !ok:
				reply(ephemeral("Unknown button."))
			case !d.allowed(user):
				reply(ephemeral("You are not allowed to decide Gubern approvals. Pair your account with /pair first."))
			default:
				reply(ephemeral(d.appr.Decide(ctx, "discord", user, "", id, approve)))
			}
		case discordCommand:
			reply(d.command(ctx, runtime, in, user))
		default:
			reply(ephemeral("Unsupported interaction."))
		}
	})
}

// command handles /pair, /approve, /reject and /ask.
func (d *DiscordBot) command(ctx context.Context, runtime *agents.Runtime, in discordInteraction, user string) map[string]any {
	name := strings.ToLower(in.Data.Name)
	if name == "pair" { // works for anyone: the code is the credential
		return ephemeral(handlePair(ctx, d.ident, &d.pairings, "discord", user, in.option("code")))
	}
	if !d.allowed(user) {
		return ephemeral("You are not registered with this Gubern server. Ask its owner for a pairing code and run /pair.")
	}
	switch name {
	case "approve", "reject":
		id := in.option("id")
		if id != "" && !validApprovalID(id) {
			return ephemeral("Usage: /" + name + " id:<approval id>")
		}
		return ephemeral(d.appr.Decide(ctx, "discord", user, "discord:"+user, id, name == "approve"))
	case "ask":
		msg := in.option("message")
		if msg == "" || runtime == nil {
			return ephemeral("Usage: /ask message:<what you need>")
		}
		// Answer after the interaction is acknowledged (Discord allows 3 s).
		d.inflight.Add(1)
		go func() {
			defer d.inflight.Done()
			text := "⚠️ The agent is not available."
			if ch, err := runtime.Submit(ctx, msg, "discord:"+user); err == nil {
				var b strings.Builder
				for chunk := range ch {
					b.WriteString(chunk)
				}
				text = b.String()
			}
			d.followUp(ctx, in.ApplicationID, in.Token, text)
		}()
		return map[string]any{"type": discordDeferredMessage, "data": map[string]any{"flags": discordEphemeral}}
	default:
		return ephemeral("Commands: /ask, /approve, /reject, /pair")
	}
}

// Wait blocks until deferred replies finish (tests, shutdown).
func (d *DiscordBot) Wait() { d.inflight.Wait() }

func (d *DiscordBot) do(ctx context.Context, method, path string, payload any, botAuth bool) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, method, d.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if botAuth {
		req.Header.Set("Authorization", "Bot "+d.cfg.BotToken)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("discord %s %s: %d", method, path, resp.StatusCode)
	}
	return nil
}

// followUp edits the deferred reply with the agent's answer.
func (d *DiscordBot) followUp(ctx context.Context, appID, token, text string) {
	if len(text) > 1900 {
		text = text[:1900] + "…"
	}
	if err := d.do(ctx, http.MethodPatch, "/webhooks/"+appID+"/"+token+"/messages/@original",
		map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}}, false); err != nil {
		slog.Default().Warn("discord follow-up failed", "err", err)
	}
}

// SendApprovalRequest posts an approval notice with Approve/Reject buttons
// to the approval channel.
func (d *DiscordBot) SendApprovalRequest(ctx context.Context, text, id string) error {
	if d.cfg.ApprovalChannel == "" || d.cfg.BotToken == "" {
		return nil
	}
	if len(text) > 1900 {
		text = text[:1900] + "…"
	}
	return d.do(ctx, http.MethodPost, "/channels/"+d.cfg.ApprovalChannel+"/messages", map[string]any{
		"content":          text,
		"allowed_mentions": map[string]any{"parse": []string{}},
		"components": []map[string]any{{"type": 1, "components": []map[string]any{
			{"type": 2, "style": discordButtonStyleSuccess, "label": "Approve", "custom_id": "approve:" + id},
			{"type": 2, "style": discordButtonStyleDanger, "label": "Reject", "custom_id": "reject:" + id},
		}}},
	}, true)
}

// SendText posts a plain notice to the approval channel.
func (d *DiscordBot) SendText(ctx context.Context, text string) error {
	if d.cfg.ApprovalChannel == "" || d.cfg.BotToken == "" {
		return nil
	}
	return d.do(ctx, http.MethodPost, "/channels/"+d.cfg.ApprovalChannel+"/messages",
		map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}}, true)
}
