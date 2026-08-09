package assets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
)

const credentialKeyVersion = 1

var credentialAssociatedData = []byte("aurora-aiops/asset-credential/v1")

type CredentialCipher interface {
	Encrypt(CredentialSecret) (CredentialEnvelope, error)
	Decrypt(CredentialEnvelope) (CredentialSecret, error)
}

type aesGCMCredentialCipher struct {
	aead cipher.AEAD
}

func NewAESGCMCredentialCipher(key []byte) (CredentialCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("asset credential encryption key must be 32 bytes")
	}

	keyCopy := append([]byte(nil), key...)
	block, err := aes.NewCipher(keyCopy)
	if err != nil {
		return nil, errors.New("cannot initialize asset credential encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("cannot initialize asset credential encryption")
	}

	return &aesGCMCredentialCipher{aead: aead}, nil
}

func (c *aesGCMCredentialCipher) Encrypt(secret CredentialSecret) (CredentialEnvelope, error) {
	plaintext, err := json.Marshal(secret)
	if err != nil {
		return CredentialEnvelope{}, errors.New("cannot serialize asset credential")
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return CredentialEnvelope{}, errors.New("cannot generate asset credential nonce")
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, credentialAssociatedData)

	return CredentialEnvelope{
		Nonce:      nonce,
		Ciphertext: ciphertext,
		KeyVersion: credentialKeyVersion,
	}, nil
}

func (c *aesGCMCredentialCipher) Decrypt(envelope CredentialEnvelope) (CredentialSecret, error) {
	if envelope.KeyVersion != credentialKeyVersion {
		return CredentialSecret{}, errors.New("unsupported asset credential key version")
	}
	if len(envelope.Nonce) != c.aead.NonceSize() {
		return CredentialSecret{}, errors.New("invalid asset credential nonce")
	}
	if len(envelope.Ciphertext) < c.aead.Overhead() {
		return CredentialSecret{}, errors.New("invalid asset credential ciphertext")
	}

	plaintext, err := c.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, credentialAssociatedData)
	if err != nil {
		return CredentialSecret{}, errors.New("cannot decrypt asset credential")
	}

	var secret CredentialSecret
	if err := json.Unmarshal(plaintext, &secret); err != nil {
		return CredentialSecret{}, errors.New("cannot deserialize asset credential")
	}
	return secret, nil
}
