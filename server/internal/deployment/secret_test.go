package deployment

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func TestSecretCipherAuthenticatesTaskConfig(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	cipher, err := NewSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 9
	sealed, err := cipher.Seal(TaskConfigScope, "task-1", []byte(`{"token":"top-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cipher.Open(TaskConfigScope, "task-1", sealed); err != nil || string(got) != `{"token":"top-secret"}` {
		t.Fatalf("Open=(%q,%v)", got, err)
	}
	if _, err := cipher.Open(TaskConfigScope, "task-2", sealed); err == nil {
		t.Fatal("wrong task ID opened secret")
	}
	if _, err := cipher.Open("wrong-scope", "task-1", sealed); err == nil {
		t.Fatal("wrong scope opened secret")
	}
	sealed.Ciphertext[0] ^= 1
	if _, err := cipher.Open(TaskConfigScope, "task-1", sealed); err == nil {
		t.Fatal("tampered secret opened")
	}
}

func TestSecretCipherRejectsInvalidKeyAndEnvelope(t *testing.T) {
	if _, err := NewSecretCipher(nil); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("missing key=%v", err)
	}
	if _, err := NewSecretCipher(make([]byte, 31)); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("short key=%v", err)
	}
	cipher, err := NewSecretCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.Open(TaskConfigScope, "task", SealedSecret{KeyVersion: 2}); err == nil {
		t.Fatal("unknown version opened")
	}
	if _, err := cipher.Open(TaskConfigScope, "task", SealedSecret{KeyVersion: 1, Nonce: []byte{1}, Ciphertext: []byte{2}}); err == nil {
		t.Fatal("invalid envelope opened")
	}
}

func TestSecretCipherAuthenticatesResourceIdentity(t *testing.T) {
	cipher, err := NewSecretCipher(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("apiVersion: v1\nclusters: []\n")
	sealed, err := cipher.SealResource("kubeconfig", "server-1", "kubernetes", plain)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := cipher.OpenResource("kubeconfig", "server-1", "kubernetes", sealed); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("OpenResource=(%q,%v)", got, err)
	}
	for _, tc := range []struct {
		name, kind, server, project string
	}{
		{"kind", "token", "server-1", "kubernetes"},
		{"server", "kubeconfig", "server-2", "kubernetes"},
		{"project", "kubeconfig", "server-1", "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := cipher.OpenResource(tc.kind, tc.server, tc.project, sealed); err == nil {
				t.Fatal("resource with wrong associated identity opened")
			}
		})
	}
	if bytes.Contains(sealed.Ciphertext, plain) || bytes.Contains(sealed.Ciphertext, []byte(base64.StdEncoding.EncodeToString(plain))) {
		t.Fatal("resource envelope contains plaintext")
	}
}

func TestSecretCipherRejectsInvalidResourceIdentity(t *testing.T) {
	cipher, err := NewSecretCipher(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][3]string{{"", "server", "project"}, {"kubeconfig", "", "project"}, {"kubeconfig", "server", ""}} {
		if _, err := cipher.SealResource(tc[0], tc[1], tc[2], []byte("secret")); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("SealResource(%q,%q,%q)=%v", tc[0], tc[1], tc[2], err)
		}
	}
}
