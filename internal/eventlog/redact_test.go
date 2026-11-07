package eventlog

import (
	"strings"
	"testing"
)

func TestRedact_Secrets(t *testing.T) {
	secrets := []string{
		"0123456789abcdef." + strings.Repeat("ab", 32),
		"sk-ant-api03-" + strings.Repeat("x", 40),
		"sk-proj-" + strings.Repeat("A1", 20),
		"ghp_" + strings.Repeat("a", 36),
		"github_pat_" + strings.Repeat("B", 40),
		"xoxb-123456789012-abcdefABCDEF",
		"AKIAABCDEFGHIJKLMNOP",
		"AIza" + strings.Repeat("Q", 35),
		"glpat-" + strings.Repeat("z", 20),
		"123456789:AA" + strings.Repeat("h", 33),
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36P",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEow\nabc\n-----END RSA PRIVATE KEY-----",
	}
	for _, s := range secrets {
		text := "output: " + s + " done"
		got := Redact(text)
		if strings.Contains(got, s) || !strings.Contains(got, Redacted) {
			t.Errorf("not redacted: %q → %q", s, got)
		}
	}
	assigned := map[string]string{
		"password=hunter2hunter":            "password=",
		`"api_key": "abcd1234efgh"`:         `"api_key": "`,
		"DB_PASSWORD: s3cr3tpass":           "DB_PASSWORD: ",
		"Authorization: Bearer abc.def.ghi": "Authorization: Bearer ",
		"X-API-Key: deadbeefcafe":           "X-API-Key: ",
		"client_secret=QWERTYUIOP":          "client_secret=",
	}
	for in, keep := range assigned {
		got := Redact(in)
		if !strings.HasPrefix(got, keep) || !strings.Contains(got, Redacted) {
			t.Errorf("%q → %q, want prefix %q kept and the value redacted", in, got, keep)
		}
	}
	if got := Redact("postgres://app:pa55word@db:5432/x"); got != "postgres://app:"+Redacted+"@db:5432/x" {
		t.Errorf("URL credentials → %q", got)
	}
}

func TestRedact_LeavesOrdinaryText(t *testing.T) {
	for _, s := range []string{
		"", "Listing files in /workspace", "go test ./... passed (12 tests)",
		"commit 4d4a240ee2760e3d5493232c80b85183437ca4e2", // a git hash is not a secret
		"token budget: 250000", "the password field is empty",
		"see https://example.com/docs?page=2",
	} {
		if got := Redact(s); got != s {
			t.Errorf("ordinary text changed: %q → %q", s, got)
		}
	}
}
