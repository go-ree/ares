package integration

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-ree/ares/internal/jenkins"
)

func TestStoredJenkinsCredentialCannotBeRedirectedAtBoot(t *testing.T) {
	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}
	ciphertext, err := cipher.encrypt(
		"jenkins-token",
		jenkinsCredentialContext("https://trusted.example.test", "build-user"),
	)
	if err != nil {
		t.Fatalf("encrypt() error = %v", err)
	}

	var requests atomic.Int64
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"NORMAL"}`))
	}))
	defer attacker.Close()

	previousRuntime := jenkins.Current()
	runtimeSettings.Lock()
	previousConfig := runtimeSettings.jenkins
	previousError := runtimeSettings.jenkinsError
	previousRevision := runtimeSettings.jenkinsRevision
	runtimeSettings.jenkinsRevision++
	revision := runtimeSettings.jenkinsRevision
	runtimeSettings.Unlock()
	t.Cleanup(func() {
		if previousRuntime == nil {
			jenkins.Disable()
		} else {
			jenkins.Activate(previousRuntime)
		}
		runtimeSettings.Lock()
		runtimeSettings.jenkins = previousConfig
		runtimeSettings.jenkinsError = previousError
		runtimeSettings.jenkinsRevision = previousRevision
		runtimeSettings.Unlock()
	})

	// Simulate a database attacker retaining the ciphertext while replacing the
	// authenticated target address. Decryption must fail before any HTTP request.
	applyStoredJenkinsAtBoot(storedJenkinsConfig{
		Enabled:         true,
		Address:         attacker.URL,
		Username:        "build-user",
		TimeoutSeconds:  1,
		TokenCiphertext: ciphertext,
	}, cipher, revision)

	if got := requests.Load(); got != 0 {
		t.Fatalf("tampered stored address received %d request(s), want zero", got)
	}
	if jenkins.Current() != nil {
		t.Fatal("tampered stored configuration unexpectedly activated Jenkins")
	}
	runtimeSettings.RLock()
	activationError := runtimeSettings.jenkinsError
	runtimeSettings.RUnlock()
	if activationError == "" {
		t.Fatal("tampered stored configuration did not record an activation failure")
	}
}

func TestStoredKubernetesCredentialCannotChangeIdentity(t *testing.T) {
	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}
	ciphertext, err := cipher.encrypt(
		"apiVersion: v1",
		kubernetesCredentialContext("production", "primary"),
	)
	if err != nil {
		t.Fatalf("encrypt() error = %v", err)
	}

	for name, cluster := range map[string]storedKubernetesCluster{
		"environment": {
			Environment: "staging", Name: "primary", KubeconfigCiphertext: ciphertext,
		},
		"name": {
			Environment: "production", Name: "secondary", KubeconfigCiphertext: ciphertext,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runtimeKubernetesConfigs(storedKubernetesConfig{
				Enabled: true, Clusters: []storedKubernetesCluster{cluster},
			}, cipher)
			if err == nil {
				t.Fatalf("runtimeKubernetesConfigs() accepted changed %s", name)
			}
		})
	}
}
