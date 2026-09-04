package db

import (
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestValidateGuardedMigrationPrincipalsRejectsPrivilegedOrSharedIdentity(t *testing.T) {
	tests := []struct {
		name      string
		migration string
		admin     string
		want      string
	}{
		{name: "root", migration: "RoOt", admin: "ares_admin", want: "root cannot"},
		{name: "same configured user", migration: "ares_migrator", admin: "ARES_MIGRATOR", want: "different users"},
		{name: "unsafe migration user", migration: "ares-migrator", admin: "ares_admin", want: "ASCII"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateGuardedMigrationPrincipals(
				&mysql.Config{User: test.migration, DBName: "ares"},
				&mysql.Config{User: test.admin, DBName: "ares"},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestValidateGuardedMigrationPrincipalsDefersDatabaseIdentityToServer(t *testing.T) {
	err := validateGuardedMigrationPrincipals(
		&mysql.Config{User: "ares_migrator", DBName: "ARES"},
		&mysql.Config{User: "ares_admin", DBName: "ares"},
	)
	if err != nil {
		t.Fatalf("database identity must be compared using DATABASE() on the physical connections: %v", err)
	}
}

func TestGuardedCurrentUsernameSeparatesAccountHost(t *testing.T) {
	username, ok := guardedCurrentUsername("ares_admin@localhost")
	if !ok || username != "ares_admin" {
		t.Fatalf("CURRENT_USER parsing = %q, %t", username, ok)
	}
	for _, invalid := range []string{"", "missing-host@", "@localhost", "missing-separator"} {
		if username, ok := guardedCurrentUsername(invalid); ok {
			t.Errorf("CURRENT_USER %q parsed as %q", invalid, username)
		}
	}
}

func TestGuardedMigrationAccountLockNameHasStableCrossProcessVector(t *testing.T) {
	const want = "ares_migration_account_5dd87d90f1f441dacb22fe37bc966b4a"
	if got := guardedMigrationAccountLockName("ares_migrator"); got != want {
		t.Fatalf("guarded account lock = %q, want %q", got, want)
	}
}

func TestGuardedMigrationDatabaseGrantPatternIsLiteral(t *testing.T) {
	tests := []struct {
		name           string
		database       string
		partialRevokes bool
		want           string
	}{
		{name: "legacy grant mode escapes underscore", database: "ares_prod", want: `ares\_prod`},
		{name: "legacy grant mode escapes all metacharacters", database: `a%res\\prod_1`, want: `a\%res\\\\prod\_1`},
		{name: "partial revokes makes names literal", database: `a%res\\prod_1`, partialRevokes: true, want: `a%res\\prod_1`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := guardedMigrationDatabaseGrantPattern(test.database, test.partialRevokes); got != test.want {
				t.Fatalf("grant pattern = %q, want %q", got, test.want)
			}
		})
	}
}

func TestGuardedOneTimePasswordDoesNotExposeSQLQuotingCharacters(t *testing.T) {
	first, err := guardedOneTimePassword()
	if err != nil {
		t.Fatal(err)
	}
	second, err := guardedOneTimePassword()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) < 64 {
		t.Fatal("guarded passwords are not independently random at the expected strength")
	}
	if strings.ContainsAny(first+second, "'\"\\") {
		t.Fatal("guarded password contains an SQL quoting character")
	}
}
