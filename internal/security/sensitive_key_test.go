package security

import "testing"

func TestIsSensitiveKeyRecognizesCommonSpellings(t *testing.T) {
	for _, key := range []string{
		"access_token", "accessToken", "AccessToken", "accesstoken",
		"client-secret", "clientSecret", "CLIENT_SECRET",
		"api_key", "apiKey", "APIKey", "privateKey", "dbPassword",
		"credential_id", "passwd", "credentials", "clientSecrets", "accessTokens",
		"dbPasswords", "apiKeys", "privateKeys", "Authorization", "cookie", "session_id",
		"clientKey", "access_key_id", "AWSAccessKeyID", "sshKey", "signingKey",
		"pwd", "dbPwd", "pass", "passphrase", "keyPassphrase", "kubeconfig", "clusterKubeconfig",
		"auth", "basicAuth", "registryAuth", "dockerConfigJson", ".dockerconfigjson", "tlsKey",
		"githubPat", "gitlabPAT", "sonar.login", "oauthBearer", "personalAccessToken",
	} {
		if !IsSensitiveKey(key) {
			t.Errorf("IsSensitiveKey(%q) = false, want true", key)
		}
	}
}

func TestIsSensitiveKeyAllowsOrdinaryMetadata(t *testing.T) {
	for _, key := range []string{"branch", "image", "message", "secretary", "tokenizer", "monkey", "apiVersion"} {
		if IsSensitiveKey(key) {
			t.Errorf("IsSensitiveKey(%q) = true, want false", key)
		}
	}
}

func TestValidateNoSensitiveKeysReportsNestedPathWithoutValue(t *testing.T) {
	value := map[string]any{
		"targets": []any{map[string]any{"clientSecret": "must-not-appear"}},
	}
	err := ValidateNoSensitiveKeys(value, "output")
	if err == nil || err.Error() != "output.targets[0].clientSecret 疑似敏感字段" {
		t.Fatalf("error = %v", err)
	}
	if err := ValidateNoSensitiveKeys(map[string]any{"region": "cn", "count": 2}, "output"); err != nil {
		t.Fatalf("ordinary metadata rejected: %v", err)
	}
}

func TestValidateJSONNoSensitiveKeys(t *testing.T) {
	if err := ValidateJSONNoSensitiveKeys([]byte(`{"metadata":{"accessTokens":["redacted"]}}`), "result"); err == nil {
		t.Fatal("sensitive JSON key should be rejected")
	}
	if err := ValidateJSONNoSensitiveKeys([]byte(`{"metadata":{"region":"cn"}}`), "result"); err != nil {
		t.Fatalf("ordinary JSON rejected: %v", err)
	}
	if err := ValidateJSONNoSensitiveKeys([]byte(`{} {}`), "result"); err == nil {
		t.Fatal("multiple JSON values should be rejected")
	}
}
