package deployment

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const maxRedactedOutput = 16 * 1024

var (
	redactAuthorization = regexp.MustCompile(`(?im)(\bAuthorization\s*:\s*(?:Bearer|Basic|Token)\s+)[^\s]+`)
	redactQuotedField   = regexp.MustCompile(`(?i)(\b(?:password|passphrase|token)\b\s*[:=]\s*)("[^"]*"|'[^']*')`)
	redactField         = regexp.MustCompile(`(?i)(\b(?:password|passphrase|token)\b\s*[:=]\s*)([^\s,;&}]+)`)
	redactPEM           = regexp.MustCompile(`(?s)(-----BEGIN [^-\r\n]+-----\s+).*?(\s+-----END [^-\r\n]+-----)`)
)

// RedactingLogger accumulates bounded informational output after applying the
// same masking rules used before persistence. It is safe for concurrent Info
// calls.
type RedactingLogger struct {
	mu      sync.Mutex
	secrets []string
	output  strings.Builder
}

// NewRedactingLogger creates a logger seeded with exact sensitive values.
func NewRedactingLogger(secrets ...string) *RedactingLogger {
	unique := make(map[string]struct{}, len(secrets))
	clean := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if _, ok := unique[secret]; ok {
			continue
		}
		unique[secret] = struct{}{}
		clean = append(clean, secret)
	}
	sort.Slice(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	return &RedactingLogger{secrets: clean}
}

// Redact masks exact and URL-encoded seeded values, credentials in common
// command/log formats, authorization headers, and PEM bodies.
func (l *RedactingLogger) Redact(message string) string {
	for _, secret := range l.secrets {
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
		for _, encoded := range []string{url.QueryEscape(secret), url.PathEscape(secret)} {
			if encoded != secret {
				message = strings.ReplaceAll(message, encoded, "[REDACTED]")
			}
		}
	}
	message = redactPEM.ReplaceAllString(message, `${1}[REDACTED]${2}`)
	message = redactAuthorization.ReplaceAllString(message, `${1}[REDACTED]`)
	message = redactQuotedField.ReplaceAllString(message, `${1}[REDACTED]`)
	message = redactField.ReplaceAllString(message, `${1}[REDACTED]`)
	return message
}

// Info appends one redacted informational line.
func (l *RedactingLogger) Info(message string) {
	redacted := l.Redact(message)
	l.mu.Lock()
	l.output.WriteString(redacted)
	l.output.WriteByte('\n')
	l.mu.Unlock()
}

// Output returns all logged text capped at 16 KiB. Redaction is performed in
// Info before this cap, so truncated output cannot reveal a seeded value.
func (l *RedactingLogger) Output() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	value := l.output.String()
	if len(value) <= maxRedactedOutput {
		return value
	}
	return value[:maxRedactedOutput]
}
