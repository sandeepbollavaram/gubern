package messaging

import (
	"context"
	"strings"
	"sync"
	"time"
)

// ChannelIdentity connects a channel to the identity store (milestone F):
// accounts linked by pairing are let in, revoked accounts are refused even
// when an environment allow-list still names them, and "/pair <code>" links
// the sending account to a person.
type ChannelIdentity struct {
	Linked  func(provider, subject string) bool
	Revoked func(provider, subject string) bool
	// Pair redeems code for the channel account and returns the reply.
	Pair func(ctx context.Context, provider, subject, code string) string
}

func (c ChannelIdentity) linked(provider, subject string) bool {
	return c.Linked != nil && c.Linked(provider, subject)
}

func (c ChannelIdentity) revoked(provider, subject string) bool {
	return c.Revoked != nil && c.Revoked(provider, subject)
}

// pairLimiter allows a few /pair attempts per account per window, on top of
// the code's own single use, expiry and wrong-channel burn.
type pairLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

const (
	pairAttempts = 5
	pairWindow   = 10 * time.Minute
)

func (l *pairLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string][]time.Time{}
	}
	now := time.Now()
	var recent []time.Time
	for _, t := range l.seen[key] {
		if now.Sub(t) < pairWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= pairAttempts {
		l.seen[key] = recent
		return false
	}
	l.seen[key] = append(recent, now)
	return true
}

// pairCommand reports whether text is "/pair <code>" and returns the code.
func pairCommand(text string) (string, bool) {
	cmd, arg, _ := strings.Cut(strings.TrimSpace(text), " ")
	if !strings.EqualFold(cmd, "/pair") {
		return "", false
	}
	return strings.TrimSpace(arg), true
}

// handlePair runs a /pair attempt for provider:subject and returns the reply.
func handlePair(ctx context.Context, ci ChannelIdentity, l *pairLimiter, provider, subject, code string) string {
	if ci.Pair == nil {
		return "Pairing is not available on this server."
	}
	if code == "" {
		return "Usage: /pair XXXX-XXXX-XXXX (create a code in the Gubern dashboard under Devices)."
	}
	if !l.allow(provider + ":" + subject) {
		return "Too many pairing attempts. Wait ten minutes and try again."
	}
	return ci.Pair(ctx, provider, subject, code)
}
