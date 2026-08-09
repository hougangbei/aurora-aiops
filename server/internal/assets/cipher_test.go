package assets

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

func TestAESGCMCredentialCipherRoundTrip(t *testing.T) {
	cipher, err := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher() error = %v", err)
	}
	secret := CredentialSecret{
		Password:   "p@ss",
		PrivateKey: "key",
		Passphrase: "phrase",
	}

	envelope, err := cipher.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if envelope.KeyVersion != 1 {
		t.Fatalf("Encrypt() KeyVersion = %d, want 1", envelope.KeyVersion)
	}
	decrypted, err := cipher.Decrypt(envelope)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if decrypted != secret {
		t.Fatalf("Decrypt() = %#v, want %#v", decrypted, secret)
	}
}

func TestAESGCMCredentialCipherDoesNotExposePlaintext(t *testing.T) {
	cipher, err := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x23}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher() error = %v", err)
	}
	secret := CredentialSecret{
		Password:   "p@ss",
		PrivateKey: "private-key-material",
		Passphrase: "phrase",
	}

	envelope, err := cipher.Encrypt(secret)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	serializedEnvelope := append(append([]byte(nil), envelope.Nonce...), envelope.Ciphertext...)
	for _, plaintext := range []string{secret.Password, secret.PrivateKey, secret.Passphrase} {
		if bytes.Contains(serializedEnvelope, []byte(plaintext)) {
			t.Fatalf("encrypted envelope contains a plaintext credential field")
		}
	}
}

func TestAESGCMCredentialCipherDetectsTampering(t *testing.T) {
	cipher, err := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x17}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher() error = %v", err)
	}
	envelope, err := cipher.Encrypt(CredentialSecret{Password: "p@ss"})
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	tampered := CredentialEnvelope{
		Nonce:      append([]byte(nil), envelope.Nonce...),
		Ciphertext: append([]byte(nil), envelope.Ciphertext...),
		KeyVersion: envelope.KeyVersion,
	}
	tampered.Ciphertext[len(tampered.Ciphertext)/2] ^= 0xff

	decrypted, err := cipher.Decrypt(tampered)
	if err == nil {
		t.Fatal("Decrypt() error = nil after ciphertext tampering")
	}
	if decrypted != (CredentialSecret{}) {
		t.Fatalf("Decrypt() returned plaintext after ciphertext tampering")
	}
	assertErrorDoesNotExposeSecrets(t, err, "p@ss")
}

func TestAESGCMCredentialCipherRejectsInvalidKeyLengths(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33, 64} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			if _, err := NewAESGCMCredentialCipher(make([]byte, size)); err == nil {
				t.Fatalf("NewAESGCMCredentialCipher() error = nil for %d-byte key", size)
			}
		})
	}
}

func TestAESGCMCredentialCipherRejectsUnsupportedKeyVersion(t *testing.T) {
	cipher, err := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x08}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher() error = %v", err)
	}
	envelope, err := cipher.Encrypt(CredentialSecret{Password: "p@ss"})
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	envelope.KeyVersion = 2

	decrypted, err := cipher.Decrypt(envelope)
	if err == nil {
		t.Fatal("Decrypt() error = nil for unsupported key version")
	}
	if decrypted != (CredentialSecret{}) {
		t.Fatal("Decrypt() returned plaintext for unsupported key version")
	}
	assertErrorDoesNotExposeSecrets(t, err, "p@ss")
}

func TestAESGCMCredentialCipherUsesFreshNonce(t *testing.T) {
	cipher, err := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x65}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher() error = %v", err)
	}
	secret := CredentialSecret{Password: "p@ss", PrivateKey: "key", Passphrase: "phrase"}

	first, err := cipher.Encrypt(secret)
	if err != nil {
		t.Fatalf("first Encrypt() error = %v", err)
	}
	second, err := cipher.Encrypt(secret)
	if err != nil {
		t.Fatalf("second Encrypt() error = %v", err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) {
		t.Fatal("Encrypt() reused a nonce")
	}
	if bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("Encrypt() produced identical ciphertext")
	}
	for i, envelope := range []CredentialEnvelope{first, second} {
		decrypted, err := cipher.Decrypt(envelope)
		if err != nil {
			t.Fatalf("Decrypt(envelope %d) error = %v", i, err)
		}
		if decrypted != secret {
			t.Fatalf("Decrypt(envelope %d) = %#v, want %#v", i, decrypted, secret)
		}
	}
}

func TestAESGCMCredentialCipherRejectsMalformedEnvelopes(t *testing.T) {
	cipher, err := NewAESGCMCredentialCipher(bytes.Repeat([]byte{0x91}, 32))
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher() error = %v", err)
	}
	valid, err := cipher.Encrypt(CredentialSecret{Password: "p@ss"})
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	tests := map[string]CredentialEnvelope{
		"nil nonce": {
			Nonce: nil, Ciphertext: append([]byte(nil), valid.Ciphertext...), KeyVersion: 1,
		},
		"empty nonce": {
			Nonce: []byte{}, Ciphertext: append([]byte(nil), valid.Ciphertext...), KeyVersion: 1,
		},
		"short nonce": {
			Nonce: []byte{1}, Ciphertext: append([]byte(nil), valid.Ciphertext...), KeyVersion: 1,
		},
		"long nonce": {
			Nonce: make([]byte, len(valid.Nonce)+1), Ciphertext: append([]byte(nil), valid.Ciphertext...), KeyVersion: 1,
		},
		"nil ciphertext": {
			Nonce: append([]byte(nil), valid.Nonce...), Ciphertext: nil, KeyVersion: 1,
		},
		"truncated ciphertext": {
			Nonce: append([]byte(nil), valid.Nonce...), Ciphertext: append([]byte(nil), valid.Ciphertext[:1]...), KeyVersion: 1,
		},
	}

	for name, envelope := range tests {
		t.Run(name, func(t *testing.T) {
			decrypted, err := cipher.Decrypt(envelope)
			if err == nil {
				t.Fatal("Decrypt() error = nil for malformed envelope")
			}
			if decrypted != (CredentialSecret{}) {
				t.Fatal("Decrypt() returned plaintext for malformed envelope")
			}
			assertErrorDoesNotExposeSecrets(t, err, "p@ss")
		})
	}
}

func TestAESGCMCredentialCipherCopiesCallerKey(t *testing.T) {
	key := bytes.Repeat([]byte{0x31}, 32)
	cipher, err := NewAESGCMCredentialCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCredentialCipher() error = %v", err)
	}
	for i := range key {
		key[i] = 0
	}

	envelope, err := cipher.Encrypt(CredentialSecret{Password: "p@ss"})
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	decrypted, err := cipher.Decrypt(envelope)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if decrypted.Password != "p@ss" {
		t.Fatal("cipher behavior changed after caller mutated key")
	}
}

func assertErrorDoesNotExposeSecrets(t *testing.T, err error, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error exposes a credential field")
		}
	}
}
