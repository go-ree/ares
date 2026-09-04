package entity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuthEntitiesDoNotSerializeCredentialMaterial(t *testing.T) {
	values := []any{
		AuthUser{PasswordHash: "password-hash"},
		AuthIdentity{IdentityHash: []byte("identity-hash")},
		AuthSession{SessionHash: "session-hash"},
		AuthOIDCFlow{
			StateHash:          "state-hash",
			NonceHash:          "nonce-hash",
			BindingHash:        "binding-hash",
			VerifierCiphertext: "verifier-ciphertext",
		},
	}
	for _, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{
			"password-hash", "identity-hash", "session-hash", "state-hash",
			"nonce-hash", "binding-hash", "verifier-ciphertext",
		} {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("%T JSON leaks %q: %s", value, secret, encoded)
			}
		}
	}
}

func TestAuthEntityTableNamesAreStable(t *testing.T) {
	wants := map[string]string{
		(&AuthUser{}).TableName():           "auth_users",
		(&AuthIdentity{}).TableName():       "auth_identities",
		(&AuthSession{}).TableName():        "auth_sessions",
		(&AuthOIDCFlow{}).TableName():       "auth_oidc_flows",
		(&AuthBootstrapState{}).TableName(): "auth_bootstrap_state",
		(&AuditEvent{}).TableName():         "audit_events",
	}
	if len(wants) != 6 {
		t.Fatalf("auth table names are not unique: %v", wants)
	}
	for got, want := range wants {
		if got != want {
			t.Errorf("table name = %q, want %q", got, want)
		}
	}
}
