package auth

import (
	"strings"
	"testing"
)

func TestEightCharacterPasswordPolicy(t *testing.T) {
	for _, password := range []string{"1234567", "12345678", "１２３４５６７８", "中文密码", strings.Repeat("x", maximumPasswordBytes+1), string([]byte{255, 255, 255, 255, 255, 255, 255, 255})} {
		if _, err := HashPassword(password); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
	for _, password := range []string{"abc123456", "中文密码支持八字", strings.Repeat("x", maximumPasswordBytes)} {
		hash, err := HashPassword(password)
		if err != nil || !VerifyPassword(hash, password) {
			t.Fatalf("valid password rejected: %v", err)
		}
	}
}
