package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// IsSensitiveKey reports whether a persisted configuration/input key is likely
// to contain a credential. It understands separators and common camel/Pascal
// case spellings so access_token, accessToken and AccessToken are equivalent.
func IsSensitiveKey(key string) bool {
	words := keyWords(strings.TrimSpace(key))
	if len(words) == 0 {
		return false
	}
	for _, word := range words {
		switch word {
		case "password", "passwords", "passwd", "secret", "secrets", "credential", "credentials",
			"token", "tokens", "authorization", "cookie", "cookies", "jwt":
			return true
		}
	}
	for index := 0; index+1 < len(words); index++ {
		if (words[index] == "api" || words[index] == "private" || words[index] == "access" ||
			words[index] == "client" || words[index] == "ssh" || words[index] == "signing" ||
			words[index] == "encryption") &&
			(words[index+1] == "key" || words[index+1] == "keys") {
			return true
		}
		if words[index] == "session" && (words[index+1] == "id" || words[index+1] == "key") {
			return true
		}
	}

	// Some clients send all-lowercase concatenated keys. Keep this fallback
	// deliberately limited to well-known credential suffixes/prefixes.
	compact := strings.Join(words, "")
	for _, marker := range []string{
		"password", "passwords", "passwd", "secret", "secrets",
		"credential", "credentials", "token", "tokens", "authorization",
		"cookie", "cookies", "jwt", "sessionid",
	} {
		if strings.HasSuffix(compact, marker) {
			return true
		}
	}
	for _, marker := range []string{
		"apikey", "apikeys", "privatekey", "privatekeys", "accesskey", "accesskeys", "accesskeyid",
		"clientkey", "clientkeys", "sshkey", "sshkeys", "signingkey", "signingkeys",
		"encryptionkey", "encryptionkeys",
	} {
		if strings.HasSuffix(compact, marker) {
			return true
		}
	}
	return false
}

// ValidateNoSensitiveKeys recursively validates JSON-shaped data before it is
// persisted. It reports only the key path, never the associated value.
func ValidateNoSensitiveKeys(value any, root string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			path := joinKeyPath(root, key)
			if IsSensitiveKey(key) {
				return fmt.Errorf("%s 疑似敏感字段", path)
			}
			if err := ValidateNoSensitiveKeys(nested, path); err != nil {
				return err
			}
		}
	case map[string]string:
		for key := range typed {
			path := joinKeyPath(root, key)
			if IsSensitiveKey(key) {
				return fmt.Errorf("%s 疑似敏感字段", path)
			}
		}
	case []any:
		for index, nested := range typed {
			if err := ValidateNoSensitiveKeys(nested, fmt.Sprintf("%s[%d]", root, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateJSONNoSensitiveKeys decodes exactly one JSON value and applies the
// recursive key policy. Callers can use it as a final persistence boundary for
// executor-owned output.
func ValidateJSONNoSensitiveKeys(raw json.RawMessage, root string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("%s 不是有效 JSON：%w", root, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s 只能包含一个 JSON 值", root)
	}
	return ValidateNoSensitiveKeys(value, root)
}

func joinKeyPath(root, key string) string {
	if root == "" {
		return key
	}
	return root + "." + key
}

func keyWords(value string) []string {
	runes := []rune(value)
	words := make([]string, 0, 4)
	current := make([]rune, 0, len(runes))
	flush := func() {
		if len(current) == 0 {
			return
		}
		words = append(words, strings.ToLower(string(current)))
		current = current[:0]
	}
	for index, char := range runes {
		if !unicode.IsLetter(char) && !unicode.IsDigit(char) {
			flush()
			continue
		}
		if unicode.IsUpper(char) && len(current) > 0 {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || (unicode.IsUpper(previous) && nextIsLower) {
				flush()
			}
		}
		current = append(current, char)
	}
	flush()
	return words
}
