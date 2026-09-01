// Package crypto encrypts secret values (API keys, etc.) at rest using
// AES-256-GCM under a master key, so the database never stores plaintext
// (docs/V0.1_SPEC.md F-9).
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

var ErrMasterKeyMissing = errors.New("crypto: master key environment variable is not set")

// SecretBox encrypts and decrypts secret values with a fixed AES-256 key.
type SecretBox struct {
	gcm cipher.AEAD
}

// NewSecretBox loads a base64-encoded 32-byte key from the given
// environment variable and builds an AES-GCM cipher from it.
func NewSecretBox(envVar string) (*SecretBox, error) {
	encoded := os.Getenv(envVar)
	if encoded == "" {
		return nil, fmt.Errorf("%w: %s", ErrMasterKeyMissing, envVar)
	}

	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("crypto: decoding %s: %w", envVar, err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: %s must decode to 32 bytes, got %d", envVar, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: building cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: building GCM: %w", err)
	}

	return &SecretBox{gcm: gcm}, nil
}

// Seal encrypts plaintext and returns the ciphertext and the nonce used.
func (b *SecretBox) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, b.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: generating nonce: %w", err)
	}
	ciphertext = b.gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

// Open decrypts ciphertext using the given nonce.
func (b *SecretBox) Open(ciphertext, nonce []byte) ([]byte, error) {
	plaintext, err := b.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypting: %w", err)
	}
	return plaintext, nil
}

// GenerateMasterKey returns a fresh base64-encoded 32-byte key, suitable
// for FORGEAI_MASTER_KEY. Used by `forgeai init`.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("crypto: generating master key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
