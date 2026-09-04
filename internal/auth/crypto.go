package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedFlowPrefix = "v1:"

type keySet struct {
	flowAEAD   cipher.AEAD
	csrfKey    []byte
	sessionKey []byte
}

func newKeySet(rootKey string) (*keySet, error) {
	if len(rootKey) < 32 {
		return nil, errors.New("auth root key must contain at least 32 bytes")
	}
	flowKey := deriveSubkey([]byte(rootKey), "ares/auth/oidc-flow/v1")
	block, err := aes.NewCipher(flowKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &keySet{
		flowAEAD:   aead,
		csrfKey:    deriveSubkey([]byte(rootKey), "ares/auth/csrf/v1"),
		sessionKey: deriveSubkey([]byte(rootKey), "ares/auth/session-digest/v1"),
	}, nil
}

func deriveSubkey(root []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, root)
	_, _ = mac.Write([]byte(purpose))
	return mac.Sum(nil)
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func tokenHash(token string) []byte {
	digest := sha256.Sum256([]byte(token))
	return digest[:]
}

func (k *keySet) sessionHash(token string) []byte {
	mac := hmac.New(sha256.New, k.sessionKey)
	_, _ = mac.Write([]byte(token))
	return mac.Sum(nil)
}

func identityHash(issuer, subject string) []byte {
	hash := sha256.New()
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(issuer)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(issuer))
	binary.BigEndian.PutUint64(length[:], uint64(len(subject)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write([]byte(subject))
	return hash.Sum(nil)
}

func (k *keySet) encryptFlowVerifier(verifier string, stateHash, bindingHash []byte) (string, error) {
	nonce := make([]byte, k.flowAEAD.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate OIDC flow nonce: %w", err)
	}
	aad := flowAAD(stateHash, bindingHash)
	sealed := k.flowAEAD.Seal(nonce, nonce, []byte(verifier), aad)
	return encryptedFlowPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (k *keySet) decryptFlowVerifier(ciphertext string, stateHash, bindingHash []byte) (string, error) {
	if !strings.HasPrefix(ciphertext, encryptedFlowPrefix) {
		return "", errors.New("unsupported OIDC flow ciphertext")
	}
	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedFlowPrefix))
	if err != nil || len(payload) < k.flowAEAD.NonceSize() {
		return "", errors.New("invalid OIDC flow ciphertext")
	}
	nonceSize := k.flowAEAD.NonceSize()
	plaintext, err := k.flowAEAD.Open(nil, payload[:nonceSize], payload[nonceSize:], flowAAD(stateHash, bindingHash))
	if err != nil {
		return "", errors.New("invalid OIDC flow ciphertext")
	}
	return string(plaintext), nil
}

func flowAAD(stateHash, bindingHash []byte) []byte {
	result := make([]byte, 0, len(stateHash)+len(bindingHash)+1)
	result = append(result, stateHash...)
	result = append(result, 0)
	result = append(result, bindingHash...)
	return result
}

func (k *keySet) csrfToken(sessionToken string) string {
	mac := hmac.New(sha256.New, k.csrfKey)
	_, _ = mac.Write([]byte("session\x00"))
	_, _ = mac.Write([]byte(sessionToken))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (k *keySet) validCSRF(sessionToken, provided string) bool {
	expected := k.csrfToken(sessionToken)
	return len(provided) == len(expected) && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}
