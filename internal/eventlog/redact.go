package eventlog

import (
	"regexp"
	"strings"
)

// Redacted replaces every secret the redactor finds.
const Redacted = "[REDACTED]"

// secretPatterns match credentials that must never be persisted or streamed.
// Each pattern's first capture group, when present, is kept (the key name in
// "password=..."), and the rest of the match is replaced.
var secretPatterns = []*regexp.Regexp{
	// PEM private keys (whole block).
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY-----`),
	// Gubern API keys: <16 hex id>.<64 hex secret>.
	regexp.MustCompile(`\b[0-9a-f]{16}\.[0-9a-f]{64}\b`),
	// Provider keys and tokens with well-known prefixes.
	regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{30,}`),
	regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`),
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`),
	regexp.MustCompile(`\b[0-9]{8,10}:AA[0-9A-Za-z_\-]{33}\b`), // Telegram bot token
	// JSON Web Tokens.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`),
	// Authorization headers.
	regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic|token)\s+)[^\s"']+`),
	regexp.MustCompile(`(?i)(x-api-key:\s*)[^\s"']+`),
	// Credentials in URLs: scheme://user:password@host.
	regexp.MustCompile(`([a-z][a-z0-9+.\-]*://[^\s:/@"']+:)[^\s@/"']+(@)`),
	// key=value / key: value assignments of secret-named keys.
	regexp.MustCompile(`(?i)\b((?:[a-z0-9_\-]*(?:password|passwd|secret|token|api[_\-]?key|access[_\-]?key|private[_\-]?key|client[_\-]?secret))["']?\s*[:=]\s*["']?)[^\s"',;&]{4,}`),
}

// Redact returns s with every recognised secret replaced by [REDACTED].
func Redact(s string) string {
	if s == "" {
		return s
	}
	for _, re := range secretPatterns {
		if !re.MatchString(s) {
			continue
		}
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			sub := re.FindStringSubmatch(m)
			switch len(sub) {
			case 3: // prefix + secret + suffix (URL credentials)
				return sub[1] + Redacted + sub[2]
			case 2: // keep the key name / header prefix
				return sub[1] + Redacted
			default:
				return Redacted
			}
		})
	}
	return s
}

// ContainsSecret reports whether Redact would change s.
func ContainsSecret(s string) bool { return Redact(s) != s }

// redactAll applies Redact to every string in fields.
func redactAll(fields ...*string) {
	for _, f := range fields {
		if f != nil && *f != "" && strings.TrimSpace(*f) != "" {
			*f = Redact(*f)
		}
	}
}
