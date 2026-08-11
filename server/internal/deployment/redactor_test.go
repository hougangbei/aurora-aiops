package deployment

import (
	"strings"
	"testing"
)

func TestRedactingLoggerMasksSecretsAndSensitiveFields(t *testing.T) {
	privateKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nprivate-body\n-----END OPENSSH PRIVATE KEY-----"
	kubeconfig := "apiVersion: v1\nusers:\n- name: admin\n  token: kube-token\n"
	l := NewRedactingLogger("p@ss", privateKey, "token=abc123", kubeconfig)
	l.Info("password=p@ss passphrase=p@ss token=abc123 Authorization: Bearer abc123")
	l.Info("url=https://example.test/?secret=p%40ss")
	l.Info(privateKey)
	l.Info(kubeconfig)
	out := l.Output()
	for _, secret := range []string{"p@ss", "p%40ss", "abc123", "private-body", "kube-token"} {
		if strings.Contains(out, secret) {
			t.Fatalf("output contains secret %q: %q", secret, out)
		}
	}
	if strings.Count(out, "[REDACTED]") < 5 {
		t.Fatalf("output=%q", out)
	}
	if !strings.Contains(out, "Authorization: Bearer [REDACTED]") {
		t.Fatalf("authorization was not redacted: %q", out)
	}
	if !strings.Contains(out, "normal") {
		l.Info("normal progress")
		if !strings.Contains(l.Output(), "normal progress") {
			t.Fatal("normal progress text was not preserved")
		}
	}
}

func TestRedactingLoggerCapsOutputAfterRedaction(t *testing.T) {
	l := NewRedactingLogger("secret-value")
	l.Info(strings.Repeat("secret-value ", 2000))
	out := l.Output()
	if len(out) > 16*1024 {
		t.Fatalf("output length=%d, want <= %d", len(out), 16*1024)
	}
	if strings.Contains(out, "secret-value") {
		t.Fatal("secret remained in capped output")
	}
}
