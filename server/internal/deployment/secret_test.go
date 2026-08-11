package deployment

import (
	"bytes"
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
