package messaging

import (
	"context"
	"strings"
	"testing"
)

type fakeIdent struct {
	linked, revoked map[string]bool
	paired          []string
}

func (f *fakeIdent) ci() ChannelIdentity {
	return ChannelIdentity{
		Linked:  func(p, s string) bool { return f.linked[p+":"+s] },
		Revoked: func(p, s string) bool { return f.revoked[p+":"+s] },
		Pair: func(_ context.Context, p, s, code string) string {
			f.paired = append(f.paired, p+":"+s+"="+code)
			return "linked"
		},
	}
}

func TestTelegram_IdentityDecidesAccess(t *testing.T) {
	f := newFakeTelegram(t)
	bot := newTestBot(t, f, TelegramConfig{AllowedIDs: []int64{7, 8}})
	fi := &fakeIdent{linked: map[string]bool{"telegram:9": true}, revoked: map[string]bool{"telegram:8": true}}
	bot.SetIdentity(fi.ci())
	if !bot.allowed(7) {
		t.Error("allow-listed user refused")
	}
	if bot.allowed(8) {
		t.Error("revoked user let in by the allow-list")
	}
	if !bot.allowed(9) {
		t.Error("paired user refused")
	}
	if bot.allowed(10) {
		t.Error("stranger let in")
	}
}

func TestTelegram_PairFromStrangerPrivateChatOnly(t *testing.T) {
	f := newFakeTelegram(t)
	bot := newTestBot(t, f, TelegramConfig{AllowedIDs: []int64{7}})
	fi := &fakeIdent{}
	bot.SetIdentity(fi.ci())
	from := &struct {
		ID int64 `json:"id"`
	}{ID: 55}
	private := &telegramMessage{From: from, Text: "/pair abcd-efgh-jkmn"}
	private.Chat.ID = 55
	if reply, ok := bot.tryPair(context.Background(), private); !ok || reply != "linked" {
		t.Fatalf("private pair = %q %v", reply, ok)
	}
	if len(fi.paired) != 1 || fi.paired[0] != "telegram:55=abcd-efgh-jkmn" {
		t.Fatalf("paired = %v", fi.paired)
	}
	group := &telegramMessage{From: from, Text: "/pair abcd-efgh-jkmn"}
	group.Chat.ID = -100
	if _, ok := bot.tryPair(context.Background(), group); ok {
		t.Error("pairing accepted in a group chat")
	}
	other := &telegramMessage{From: from, Text: "hello"}
	other.Chat.ID = 55
	if _, ok := bot.tryPair(context.Background(), other); ok {
		t.Error("ordinary text treated as pairing")
	}
	// Repeated attempts from one account are limited.
	var last string
	for i := 0; i < pairAttempts+1; i++ {
		last, _ = bot.tryPair(context.Background(), private)
	}
	if !strings.Contains(last, "Too many") {
		t.Errorf("attempt %d reply = %q", pairAttempts+1, last)
	}
}

func TestWhatsApp_PairAndRevocation(t *testing.T) {
	f := newFakeWhatsApp(t)
	wb := newTestWA(t, f, WhatsAppConfig{AllowedNumbers: []string{"15550001111"}})
	fi := &fakeIdent{revoked: map[string]bool{"whatsapp:15550001111": true}, linked: map[string]bool{"whatsapp:15550002222": true}}
	wb.SetIdentity(fi.ci())
	if wb.senderAllowed("15550001111") {
		t.Error("revoked number let in by the allow-list")
	}
	if !wb.senderAllowed("15550002222") {
		t.Error("paired number refused")
	}
	wb.dispatch(context.Background(), nil, "15559990000", "/pair QQQQ-RRRR-SSSS")
	if len(fi.paired) != 1 || fi.paired[0] != "whatsapp:15559990000=QQQQ-RRRR-SSSS" {
		t.Fatalf("paired = %v", fi.paired)
	}
}
