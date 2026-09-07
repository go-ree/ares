package auth

import (
	"strings"
	"testing"
)

func TestEnabledLoginCapableAdminPredicateUsesExactSecurityComparisons(t *testing.T) {
	for _, fragment := range []string{
		"BINARY u.role = BINARY ?",
		"BINARY u.auth_source = BINARY 'bootstrap'",
		"BINARY u.auth_source = BINARY 'oidc'",
		"BINARY i.issuer = BINARY ?",
	} {
		if !strings.Contains(enabledLoginCapableAdminPredicate, fragment) {
			t.Fatalf("administrator predicate omitted exact comparison %q: %s", fragment, enabledLoginCapableAdminPredicate)
		}
	}
}
