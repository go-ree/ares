package integration

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const testEncryptionKey = "test-only-encryption-key-with-at-least-32-characters"

func TestSecretCipherRoundTrip(t *testing.T) {
	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}

	const plaintext = "jenkins-token-that-must-not-be-stored-in-plaintext"
	context := jenkinsCredentialContext("https://jenkins.example.test/", " build-user ")
	first, err := cipher.encrypt(plaintext, context)
	if err != nil {
		t.Fatalf("encrypt() error = %v", err)
	}
	second, err := cipher.encrypt(plaintext, context)
	if err != nil {
		t.Fatalf("second encrypt() error = %v", err)
	}

	if !strings.HasPrefix(first, encryptedValuePrefix) {
		t.Fatalf("ciphertext %q does not have version prefix %q", first, encryptedValuePrefix)
	}
	if strings.Contains(first, plaintext) {
		t.Fatal("ciphertext contains plaintext credential")
	}
	if first == second {
		t.Fatal("encrypting the same value twice produced identical ciphertext; nonce may be reused")
	}

	got, err := cipher.decrypt(first, jenkinsCredentialContext("https://jenkins.example.test", "build-user"))
	if err != nil {
		t.Fatalf("decrypt() error = %v", err)
	}
	if got != plaintext {
		t.Fatalf("decrypt() = %q, want %q", got, plaintext)
	}
}

func TestSecretCipherRejectsWrongKeyAndTampering(t *testing.T) {
	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}
	context := kubernetesCredentialContext("production", "primary")
	ciphertext, err := cipher.encrypt("sensitive-value", context)
	if err != nil {
		t.Fatalf("encrypt() error = %v", err)
	}

	wrongCipher, err := newSecretCipher("a-different-test-encryption-key-with-at-least-32-characters")
	if err != nil {
		t.Fatalf("newSecretCipher() with alternate key error = %v", err)
	}
	if _, err := wrongCipher.decrypt(ciphertext, context); err == nil {
		t.Fatal("decrypt() with a different key unexpectedly succeeded")
	}

	payload, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedValuePrefix))
	if err != nil {
		t.Fatalf("decode test ciphertext: %v", err)
	}
	payload[len(payload)-1] ^= 0xff
	tampered := encryptedValuePrefix + base64.RawStdEncoding.EncodeToString(payload)
	if _, err := cipher.decrypt(tampered, context); err == nil {
		t.Fatal("decrypt() accepted tampered ciphertext")
	}
}

func TestSecretCipherWithoutKey(t *testing.T) {
	cipher, err := newSecretCipher("  ")
	if err != nil {
		t.Fatalf("newSecretCipher() with an empty key error = %v", err)
	}
	if cipher != nil {
		t.Fatalf("newSecretCipher() with an empty key = %#v, want nil", cipher)
	}

	if got, err := cipher.encrypt("", nil); err != nil || got != "" {
		t.Fatalf("encrypt(empty) = %q, %v; want empty value and no error", got, err)
	}
	if got, err := cipher.decrypt("", nil); err != nil || got != "" {
		t.Fatalf("decrypt(empty) = %q, %v; want empty value and no error", got, err)
	}
	if _, err := cipher.encrypt("secret", []byte("context")); !errors.Is(err, errEncryptionUnavailable) {
		t.Fatalf("encrypt(non-empty) error = %v, want %v", err, errEncryptionUnavailable)
	}
	if _, err := cipher.decrypt(encryptedValuePrefix+"ciphertext", []byte("context")); !errors.Is(err, errEncryptionUnavailable) {
		t.Fatalf("decrypt(non-empty) error = %v, want %v", err, errEncryptionUnavailable)
	}
}

func TestSecretCipherRejectsShortKeyAndUnsupportedFormat(t *testing.T) {
	if _, err := newSecretCipher("too-short"); err == nil {
		t.Fatal("newSecretCipher() accepted a key shorter than 32 characters")
	}

	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}
	if _, err := cipher.decrypt("plaintext-value", []byte("context")); err == nil {
		t.Fatal("decrypt() accepted a credential without an encryption format prefix")
	}
	if _, err := cipher.decrypt(encryptedValuePrefix+"invalid-base64!", []byte("context")); err == nil {
		t.Fatal("decrypt() accepted invalid base64")
	}
}

func TestSecretCipherBindsCredentialToIntegrationContext(t *testing.T) {
	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}

	jenkinsContext := jenkinsCredentialContext("https://jenkins.example.test", "build-user")
	jenkinsCiphertext, err := cipher.encrypt("jenkins-token", jenkinsContext)
	if err != nil {
		t.Fatalf("encrypt Jenkins token: %v", err)
	}
	for name, changedContext := range map[string][]byte{
		"address":  jenkinsCredentialContext("https://attacker.example.test", "build-user"),
		"username": jenkinsCredentialContext("https://jenkins.example.test", "attacker"),
		"provider": kubernetesCredentialContext("production", "build-user"),
	} {
		t.Run("jenkins_"+name, func(t *testing.T) {
			if _, err := cipher.decrypt(jenkinsCiphertext, changedContext); err == nil {
				t.Fatalf("decrypt() accepted changed %s context", name)
			}
		})
	}

	kubernetesContext := kubernetesCredentialContext("production", "primary")
	kubeconfigCiphertext, err := cipher.encrypt("kubeconfig", kubernetesContext)
	if err != nil {
		t.Fatalf("encrypt kubeconfig: %v", err)
	}
	for name, changedContext := range map[string][]byte{
		"environment": kubernetesCredentialContext("staging", "primary"),
		"name":        kubernetesCredentialContext("production", "secondary"),
	} {
		t.Run("kubernetes_"+name, func(t *testing.T) {
			if _, err := cipher.decrypt(kubeconfigCiphertext, changedContext); err == nil {
				t.Fatalf("decrypt() accepted changed %s context", name)
			}
		})
	}
}

func TestSecretCipherRequiresContextAndRejectsLegacyFormat(t *testing.T) {
	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}
	if _, err := cipher.encrypt("secret", nil); !errors.Is(err, errEncryptionContextRequired) {
		t.Fatalf("encrypt() error = %v, want %v", err, errEncryptionContextRequired)
	}
	if _, err := cipher.decrypt(encryptedValuePrefix+"ciphertext", nil); !errors.Is(err, errEncryptionContextRequired) {
		t.Fatalf("decrypt() error = %v, want %v", err, errEncryptionContextRequired)
	}
	if _, err := cipher.decrypt("v1:legacy-ciphertext", []byte("context")); !errors.Is(err, ErrCredentialReentryRequired) {
		t.Fatalf("decrypt() legacy error = %v, want %v", err, ErrCredentialReentryRequired)
	}
	if !credentialReentryRequired("v1:legacy-ciphertext") || !credentialReentryRequired("unknown-format") {
		t.Fatal("legacy or unsupported credential format was not marked for re-entry")
	}
	if credentialReentryRequired(encryptedValuePrefix+"payload") || credentialReentryRequired("") {
		t.Fatal("current or empty credential was incorrectly marked for re-entry")
	}
}
