package deployment

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

const (
	secretKeyVersion = 1
	TaskConfigScope  = "task-config"
	secretAADPrefix  = "aurora-aiops/deployment-secret/v1"
)

type SecretCipher interface {
	Seal(scope, resourceID string, plaintext []byte) (SealedSecret, error)
	Open(scope, resourceID string, sealed SealedSecret) ([]byte, error)
	SealResource(kind, serverID, projectID string, plaintext []byte) (SealedSecret, error)
	OpenResource(kind, serverID, projectID string, sealed SealedSecret) ([]byte, error)
}
type aesGCMSecretCipher struct{ aead cipher.AEAD }

func NewSecretCipher(key []byte) (SecretCipher, error) {
	if len(key) != 32 {
		return nil, ErrEncryptionUnavailable
	}
	block, err := aes.NewCipher(append([]byte(nil), key...))
	if err != nil {
		return nil, fmt.Errorf("initialize deployment cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize deployment cipher: %w", err)
	}
	return &aesGCMSecretCipher{aead: aead}, nil
}
func (c *aesGCMSecretCipher) Seal(scope, resourceID string, plaintext []byte) (SealedSecret, error) {
	if scope == "" || resourceID == "" {
		return SealedSecret{}, ErrInvalidInput
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return SealedSecret{}, fmt.Errorf("generate deployment nonce: %w", err)
	}
	return SealedSecret{Nonce: nonce, Ciphertext: c.aead.Seal(nil, nonce, plaintext, secretAAD(scope, resourceID)), KeyVersion: secretKeyVersion}, nil
}
func (c *aesGCMSecretCipher) Open(scope, resourceID string, sealed SealedSecret) ([]byte, error) {
	if sealed.KeyVersion != secretKeyVersion || len(sealed.Nonce) != c.aead.NonceSize() || len(sealed.Ciphertext) < c.aead.Overhead() {
		return nil, ErrInvalidInput
	}
	plain, err := c.aead.Open(nil, sealed.Nonce, sealed.Ciphertext, secretAAD(scope, resourceID))
	if err != nil {
		return nil, fmt.Errorf("open deployment secret: %w", err)
	}
	return plain, nil
}

func (c *aesGCMSecretCipher) SealResource(kind, serverID, projectID string, plaintext []byte) (SealedSecret, error) {
	if kind == "" || serverID == "" || projectID == "" {
		return SealedSecret{}, ErrInvalidInput
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return SealedSecret{}, fmt.Errorf("generate deployment nonce: %w", err)
	}
	return SealedSecret{Nonce: nonce, Ciphertext: c.aead.Seal(nil, nonce, plaintext, resourceAAD(kind, serverID, projectID)), KeyVersion: secretKeyVersion}, nil
}

func (c *aesGCMSecretCipher) OpenResource(kind, serverID, projectID string, sealed SealedSecret) ([]byte, error) {
	if kind == "" || serverID == "" || projectID == "" || sealed.KeyVersion != secretKeyVersion || len(sealed.Nonce) != c.aead.NonceSize() || len(sealed.Ciphertext) < c.aead.Overhead() {
		return nil, ErrInvalidInput
	}
	plain, err := c.aead.Open(nil, sealed.Nonce, sealed.Ciphertext, resourceAAD(kind, serverID, projectID))
	if err != nil {
		return nil, fmt.Errorf("open deployment resource secret: %w", err)
	}
	return plain, nil
}

func secretAAD(scope, resourceID string) []byte {
	return []byte(secretAADPrefix + "\x00" + scope + "\x00" + resourceID)
}

func resourceAAD(kind, serverID, projectID string) []byte {
	return []byte(secretAADPrefix + "\x00" + kind + "\x00" + serverID + "\x00" + projectID)
}
