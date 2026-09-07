package integration

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedValuePrefix = "v2:"

var errEncryptionUnavailable = errors.New("ARES_SETTINGS_ENCRYPTION_KEY is required to store integration credentials")

var errEncryptionContextRequired = errors.New("credential encryption context is required")

// ErrCredentialReentryRequired marks credentials written by an older format
// that did not authenticate the integration identity. They must never be
// decrypted and silently re-encrypted because a modified database row could
// otherwise bind the old secret to an attacker-controlled endpoint.
var ErrCredentialReentryRequired = errors.New("saved integration credential must be re-entered")

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

func (c *secretCipher) encrypt(value string, additionalData []byte) (string, error) {
	if value == "" {
		return "", nil
	}
	if c == nil {
		return "", errEncryptionUnavailable
	}
	if len(additionalData) == 0 {
		return "", errEncryptionContextRequired
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(value), additionalData)
	return encryptedValuePrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *secretCipher) decrypt(value string, additionalData []byte) (string, error) {
	if value == "" {
		return "", nil
	}
	if c == nil {
		return "", errEncryptionUnavailable
	}
	if len(additionalData) == 0 {
		return "", errEncryptionContextRequired
	}
	if strings.HasPrefix(value, "v1:") {
		return "", ErrCredentialReentryRequired
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
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plaintext), nil
}

func credentialReentryRequired(value string) bool {
	return value != "" && !strings.HasPrefix(value, encryptedValuePrefix)
}

// The context is authenticated, not encrypted. It binds a stored secret to the
// exact integration identity that is allowed to use it, so editing config_data
// cannot redirect an existing credential to another endpoint or cluster.
func jenkinsCredentialContext(address, username string) []byte {
	return marshalCredentialContext(struct {
		Domain   string `json:"domain"`
		Provider string `json:"provider"`
		Address  string `json:"address"`
		Username string `json:"username"`
	}{
		Domain:   "ares.integration.credential.v2",
		Provider: providerJenkins,
		Address:  normalizedJenkinsAddress(address),
		Username: strings.TrimSpace(username),
	})
}

func kubernetesCredentialContext(environment, name string) []byte {
	return marshalCredentialContext(struct {
		Domain      string `json:"domain"`
		Provider    string `json:"provider"`
		Environment string `json:"environment"`
		Name        string `json:"name"`
	}{
		Domain:      "ares.integration.credential.v2",
		Provider:    providerKubernetes,
		Environment: strings.ToLower(strings.TrimSpace(environment)),
		Name:        strings.TrimSpace(name),
	})
}

func marshalCredentialContext(value any) []byte {
	// The concrete values above contain only strings, so encoding cannot fail.
	// Keeping the representation in a struct also gives deterministic field
	// ordering and avoids delimiter ambiguity in attacker-controlled values.
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encode credential context: %v", err))
	}
	return encoded
}
