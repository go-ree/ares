package integration

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedValuePrefix = "v1:"

var errEncryptionUnavailable = errors.New("ARES_SETTINGS_ENCRYPTION_KEY is required to store integration credentials")

type secretCipher struct {
	aead cipher.AEAD
}

func newSecretCipher(rawKey string) (*secretCipher, error) {
	rawKey = strings.TrimSpace(rawKey)
	if rawKey == "" {
		return nil, nil
	}
	if len(rawKey) < 32 {
		return nil, fmt.Errorf("ARES_SETTINGS_ENCRYPTION_KEY must contain at least 32 characters")
	}
	key := sha256.Sum256([]byte(rawKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &secretCipher{aead: aead}, nil
}

func (c *secretCipher) encrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if c == nil {
		return "", errEncryptionUnavailable
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(value), nil)
	return encryptedValuePrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *secretCipher) decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if c == nil {
		return "", errEncryptionUnavailable
	}
	if !strings.HasPrefix(value, encryptedValuePrefix) {
		return "", fmt.Errorf("unsupported encrypted credential format")
	}
	encoded := strings.TrimPrefix(value, encryptedValuePrefix)
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode credential: %w", err)
	}
	if len(payload) < c.aead.NonceSize() {
		return "", fmt.Errorf("encrypted credential is truncated")
	}
	nonce, ciphertext := payload[:c.aead.NonceSize()], payload[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plaintext), nil
}
