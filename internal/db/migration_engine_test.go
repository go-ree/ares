package db

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"
)

func TestNormalizedOperationTimeoutUsesSafeDefault(t *testing.T) {
	for _, value := range []time.Duration{0, -time.Second} {
		if got := normalizedOperationTimeout(value); got != defaultMigrationOperationTimeout {
			t.Fatalf("normalizedOperationTimeout(%v) = %v, want %v", value, got, defaultMigrationOperationTimeout)
		}
	}
	if got := normalizedOperationTimeout(1750 * time.Millisecond); got != 1750*time.Millisecond {
		t.Fatalf("positive operation timeout changed to %v", got)
	}
}

func TestNormalizedAresMySQLDSNEnablesTimeParsing(t *testing.T) {
	got, err := normalizedAresMySQLDSN("user:password@tcp(mysql:3306)/ares")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mysql.ParseDSN(got)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.ParseTime {
		t.Fatal("normalized runtime DSN did not enable parseTime")
	}
}

func TestMigrationEngineFingerprintIsStable(t *testing.T) {
	const expected = "ce1f7d0c756005b70d12a520cf541effd64cc4e456bb0f7a74512a6291ce02c3"
	files := []string{
		"migrations.go",
		"guarded_migrations.go",
		"migration_ledger.go",
		"schema_manifest.go",
		"schema_manifest_catalog.go",
	}
	if got := sourceFingerprint(t, files); got != expected {
		t.Errorf("migration engine fingerprint = %s, want %s; engine changes require an explicit safety review", got, expected)
	}
}

func TestSanitizedMigrationErrorPreservesClassificationWithoutLeaking(t *testing.T) {
	cause := &SchemaStateError{Problems: []string{"password=TopSecretValue"}}
	err := sanitizedMigrationError{cause: cause}
	if !errors.Is(err, ErrSchemaState) {
		t.Fatalf("sanitized error lost classification: %v", err)
	}
	if strings.Contains(err.Error(), "TopSecretValue") {
		t.Fatalf("sanitized error leaked secret: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "password=<redacted>") {
		t.Fatalf("sanitized error omitted redaction marker: %s", err.Error())
	}
}

func TestMigrationStatusSanitizesDatabaseControlledProblems(t *testing.T) {
	status := MigrationStatus{
		Initialized:   true,
		ManifestDiffs: []string{"default password=ManifestSecret"},
		Problems:      []string{"connect user:ShortSecret@/ares"},
	}
	got := status.String()
	for _, secret := range []string{"ManifestSecret", "ShortSecret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("rendered migration status leaked %q: %s", secret, got)
		}
	}
	if strings.Count(got, "<redacted>") < 2 {
		t.Fatalf("rendered migration status omitted redaction markers: %s", got)
	}
}

func TestMigrationErrorSanitizesSupportedMySQLDSNForms(t *testing.T) {
	for _, value := range []string{
		"connect user:short-secret@/ares",
		"connect user:p@ssword@tcp(mysql:3306)/ares",
		"connect user:p@ssword@unix(/tmp/mysql.sock)/ares",
	} {
		got := sanitizeMigrationErrorText(value)
		if strings.Contains(got, "secret") || strings.Contains(got, "p@ssword") {
			t.Errorf("DSN was not sanitized: %q", got)
		}
	}
}

func TestMigrationErrorSanitizesAuthorizationSchemes(t *testing.T) {
	for _, value := range []string{
		"Authorization: Bearer TopSecretBearer",
		"Authorization: Basic dXNlcjpwYXNz",
	} {
		got := sanitizeMigrationErrorText(value)
		if strings.Contains(got, "TopSecretBearer") || strings.Contains(got, "dXNlcjpwYXNz") {
			t.Errorf("authorization value was not sanitized: %q", got)
		}
	}
}

func TestMigrationErrorSanitizesTerminalControlAndFormatCharacters(t *testing.T) {
	got := sanitizeMigrationErrorText("table evil\x1b[31m\u009b2J\u007f\u202e warning\nnext")
	for _, character := range got {
		if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
			t.Fatalf("sanitized output retains unsafe rune U+%04X: %q", character, got)
		}
	}
	if !strings.Contains(got, "evil�[31m�2J��") || !strings.Contains(got, "warning next") {
		t.Fatalf("sanitized output did not preserve readable diagnostics: %q", got)
	}
}

func TestSupportedDatabaseVersionProblemRequiresMySQL84(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
		comment string
		valid   bool
	}{
		{name: "mysql patch release", version: "8.4.6", comment: "MySQL Community Server - GPL", valid: true},
		{name: "mysql innovation release", version: "9.4.0", comment: "MySQL Community Server - GPL"},
		{name: "mysql 8.0", version: "8.0.43", comment: "MySQL Community Server - GPL"},
		{name: "mariadb", version: "8.4.0-MariaDB", comment: "MariaDB Server"},
	} {
		t.Run(test.name, func(t *testing.T) {
			problem := supportedDatabaseVersionProblem(test.version, test.comment)
			if (problem == "") != test.valid {
				t.Fatalf("supportedDatabaseVersionProblem(%q, %q) = %q, valid=%t", test.version, test.comment, problem, test.valid)
			}
		})
	}
}
