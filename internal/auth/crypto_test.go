package auth

import (
	"bytes"
	"testing"
)

func TestFlowEncryptionBindsStateAndBrowser(t *testing.T) {
	keys, err := newKeySet("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	state := tokenHash("state")
	binding := tokenHash("binding")
	ciphertext, err := keys.encryptFlowVerifier("pkce-verifier", state, binding)
	if err != nil {
		t.Fatal(err)
	}
	got, err := keys.decryptFlowVerifier(ciphertext, state, binding)
	if err != nil || got != "pkce-verifier" {
		t.Fatalf("decrypt = %q, %v", got, err)
	}
	if _, err := keys.decryptFlowVerifier(ciphertext, tokenHash("other-state"), binding); err == nil {
		t.Fatal("ciphertext was not bound to state")
	}
	if _, err := keys.decryptFlowVerifier(ciphertext, state, tokenHash("other-browser")); err == nil {
		t.Fatal("ciphertext was not bound to browser")
	}
}

func TestIdentityHashIsLengthPrefixed(t *testing.T) {
	if bytes.Equal(identityHash("ab", "c"), identityHash("a", "bc")) {
		t.Fatal("identity hash is ambiguous without length framing")
	}
	if bytes.Equal(identityHash("ISSUER", "sub"), identityHash("issuer", "sub")) {
		t.Fatal("identity hash is not case-sensitive")
	}
}

func TestCSRFTokenIsSessionBound(t *testing.T) {
	keys, err := newKeySet("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	token := keys.csrfToken("session-one")
	if !keys.validCSRF("session-one", token) {
		t.Fatal("valid CSRF token was rejected")
	}
	if keys.validCSRF("session-two", token) {
		t.Fatal("CSRF token was accepted for another session")
	}
}
