package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestPublishedMigrationChecksumsAreStable(t *testing.T) {
	want := map[uint64]string{
		1: "adf55db01694dcc4fa6e5dda17b9196ae17b80496c58c914f86b994f2b1c117b",
		2: "0d7e2a98c2981a9e3383b0003bdddb10b4e10a15ad8dc0aebf9ac14223ebfac5",
		3: "f889da04714679e3cc304b82d9a26b6100f63e23569bd5a04f26d361ca279912",
		4: "0301b14dea0c3dacf2260dcdfd28fa2da486a596308ae43ccfeec47cb5638e01",
		5: "5fdb78c86cb338613d32e6e05c9ad38e652ba30fe83bf02564d0e110574aef0a",
	}
	for _, migration := range schemaMigrations {
		if got := migration.checksum(); got != want[migration.epoch] {
			t.Errorf("epoch %d checksum = %s, want %s; published migrations are append-only",
				migration.epoch, got, want[migration.epoch])
		}
	}
}

func TestPublishedMigrationImplementationFingerprintsAreStable(t *testing.T) {
	filesByEpoch := map[uint64][]string{
		1: {"null_string_migration.go", "../tool/nullable_text.go"},
		2: {"null_string_migration.go", "../tool/nullable_text.go", "pluggable_cicd_migration.go"},
		3: {"null_string_migration.go", "../tool/nullable_text.go", "pluggable_cicd_migration.go", "cicd_runtime_hardening_migration.go"},
		4: {"null_string_migration.go", "../tool/nullable_text.go", "pluggable_cicd_migration.go", "versioned_schema_migration.go"},
		5: {"pluggable_cicd_migration.go", "auth_rbac_migration.go", "../canonicaljson/canonical.go"},
	}
	for _, migration := range schemaMigrations {
		got := sourceFingerprint(t, filesByEpoch[migration.epoch])
		if got != migration.implementationID {
			t.Errorf("epoch %d implementation fingerprint = %s, want %s; add a new migration instead of editing published behavior",
				migration.epoch, got, migration.implementationID)
		}
	}
	const bootstrapFingerprint = "b53d25ea09633eb13ead83d59e25176ec82e7ffe19326d065888952701301376"
	if got := sourceFingerprint(t, []string{
		"schema_bootstrap.go", "../entity/tables.go",
	}); got != bootstrapFingerprint {
		t.Errorf("bootstrap fingerprint = %s, want %s; bootstrap changes require an explicit schema decision", got, bootstrapFingerprint)
	}
}

func TestPersistentEntitySourcesAreStable(t *testing.T) {
	files := []string{
		"../entity/app.go",
		"../entity/app_config_domains.go",
		"../entity/auth.go",
		"../entity/dev_language_rules.go",
		"../entity/integration_setting.go",
		"../entity/publish.go",
		"../entity/tables.go",
		"../entity/task_record_images.go",
		"../entity/workflow.go",
	}
	const expected = "780f3345381c4c3386ca9ce1e774e1abd8d5e5073e976af62cc1a7e0ddb67510"
	if got := sourceFingerprint(t, files); got != expected {
		t.Errorf("persistent entity fingerprint = %s, want %s; entity changes require a migration and manifest review", got, expected)
	}

	entries, err := os.ReadDir("../entity")
	if err != nil {
		t.Fatal(err)
	}
	actualFiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "status.go" {
			continue
		}
		actualFiles = append(actualFiles, "../entity/"+name)
	}
	if strings.Join(actualFiles, "\n") != strings.Join(files, "\n") {
		t.Fatalf("persistent entity source catalog changed:\n got %v\nwant %v", actualFiles, files)
	}
}

func sourceFingerprint(t *testing.T, files []string) string {
	t.Helper()
	digest := sha256.New()
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = digest.Write([]byte(file + "\n"))
		_, _ = digest.Write(content)
		_, _ = digest.Write([]byte("\n"))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func TestSchemaMigrationCatalogIsContiguousAndUnique(t *testing.T) {
	if len(schemaMigrations) != int(ApplicationSchemaEpoch) {
		t.Fatalf("catalog length = %d, application epoch = %d", len(schemaMigrations), ApplicationSchemaEpoch)
	}
	versions := make(map[string]struct{}, len(schemaMigrations))
	checksums := make(map[string]struct{}, len(schemaMigrations))
	for index, migration := range schemaMigrations {
		wantEpoch := uint64(index + 1)
		if migration.epoch != wantEpoch {
			t.Errorf("catalog position %d has epoch %d, want %d", index, migration.epoch, wantEpoch)
		}
		if _, duplicate := versions[migration.version]; duplicate {
			t.Errorf("duplicate migration version %q", migration.version)
		}
		versions[migration.version] = struct{}{}
		if _, duplicate := checksums[migration.checksum()]; duplicate {
			t.Errorf("duplicate migration checksum %q", migration.checksum())
		}
		checksums[migration.checksum()] = struct{}{}
		if migration.compatibleMin > migration.compatibleMax {
			t.Errorf("migration %s has invalid compatibility range [%d,%d]",
				migration.version, migration.compatibleMin, migration.compatibleMax)
		}
	}
}

func TestSchemaMigrationFactoriesBindExpectedImplementations(t *testing.T) {
	for _, migration := range schemaMigrations {
		if migration.up == nil || migration.verify == nil {
			t.Errorf("epoch %d has incomplete Up/verify wiring", migration.epoch)
		}
	}
}

func TestEpochVerificationDoesNotAccumulateHistoricalSchemaSnapshots(t *testing.T) {
	original := schemaMigrations
	t.Cleanup(func() { schemaMigrations = original })
	calls := make([]uint64, 0, 1)
	schemaMigrations = []schemaMigration{
		{epoch: 1, verify: func(*migrationSession) error {
			calls = append(calls, 1)
			return errors.New("historical manifest must not run")
		}},
		{epoch: 2, verify: func(*migrationSession) error {
			calls = append(calls, 2)
			return nil
		}},
	}

	if err := (&migrationSession{}).verifyEpochPostconditions(2); err != nil {
		t.Fatalf("verify future epoch: %v", err)
	}
	if got := fmt.Sprint(calls); got != "[2]" {
		t.Fatalf("epoch verifier calls = %s, want only latest snapshot [2]", got)
	}
}

func TestValidateLedgerRowsRejectsCatalogMetadataTampering(t *testing.T) {
	now := time.Now().UTC()
	row := validLedgerRow(schemaMigrations[0], now)
	row.Dirty.Int64 = 2
	status := validateLedgerRows([]ledgerRow{row})
	if !containsProblem(status.Problems, "dirty 必须为 0 或 1") {
		t.Fatalf("invalid dirty problems = %v", status.Problems)
	}
	if err := schemaStateBlocker(status); !errors.Is(err, ErrSchemaState) {
		t.Fatalf("invalid dirty blocker = %v, want ErrSchemaState", err)
	}

	row = validLedgerRow(schemaMigrations[0], now)
	row.CompatibleMax.Int64 = 99
	status = validateLedgerRows([]ledgerRow{row})
	if !containsProblem(status.Problems, "兼容区间不匹配") {
		t.Fatalf("compatibility tampering problems = %v", status.Problems)
	}

	row = validLedgerRow(schemaMigrations[0], now)
	row.LastError = sql.NullString{String: "stale", Valid: true}
	status = validateLedgerRows([]ledgerRow{row})
	if !containsProblem(status.Problems, "不应保留 last_error") {
		t.Fatalf("stale last_error problems = %v", status.Problems)
	}

	rows := make([]ledgerRow, len(schemaMigrations))
	for index, migration := range schemaMigrations {
		rows[index] = validLedgerRow(migration, now)
	}
	rows[3].LegacyAdopted.Int64 = 1
	status = validateLedgerRows(rows)
	if !containsProblem(status.Problems, "legacy_adopted 顺序非法") {
		t.Fatalf("epoch-four legacy adoption problems = %v", status.Problems)
	}

	rows[3].LegacyAdopted.Int64 = 0
	rows[0].LegacyAdopted.Int64 = 1
	rows[2].LegacyAdopted.Int64 = 1
	status = validateLedgerRows(rows)
	if !containsProblem(status.Problems, "legacy_adopted 顺序非法") {
		t.Fatalf("interleaved legacy adoption problems = %v", status.Problems)
	}
}

func TestSanitizeMigrationErrorRedactsSecretsAndKeepsValidUTF8(t *testing.T) {
	input := errors.New("connect user:db-password@tcp(mysql:3306) " +
		"https://admin:url-password@example.invalid password=plain token:abc Bearer xyz " +
		"ALTER USER 'ares'@'%' IDENTIFIED BY 'sql-secret' ACCOUNT LOCK " +
		strings.Repeat("迁", 2100))
	got := sanitizeMigrationError(input)
	for _, secret := range []string{"db-password", "url-password", "plain", "abc", "xyz", "sql-secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("sanitized error still contains %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("sanitized error contains no redaction marker: %s", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("sanitized error was truncated into invalid UTF-8")
	}
	if len([]rune(got)) > 2000 {
		t.Fatalf("sanitized error has %d runes, want at most 2000", len([]rune(got)))
	}
}

func TestMigrationStatusPrintsDirtyChecksumAndSanitizedError(t *testing.T) {
	status := MigrationStatus{Dirty: &DirtyMigration{
		Version: "v1", Checksum: strings.Repeat("a", 64), Error: "password=secret failure",
	}}
	got := status.String()
	for _, want := range []string{"dirty=v1", "checksum=" + strings.Repeat("a", 64), "password=<redacted>"} {
		if !strings.Contains(got, want) {
			t.Errorf("status %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "password=secret") {
		t.Fatalf("status leaked dirty error secret: %s", got)
	}
}

func TestMigrationStatusPrintsPendingChecksums(t *testing.T) {
	status := MigrationStatus{Pending: migrationInfos(schemaMigrations)}
	got := status.String()
	for _, migration := range schemaMigrations {
		if !strings.Contains(got, migration.version) || !strings.Contains(got, migration.checksum()) {
			t.Errorf("status omitted pending migration identity for epoch %d: %s", migration.epoch, got)
		}
	}
}

func TestComposeRuntimeGrantsCoverManagedTablesWithLeastPrivilege(t *testing.T) {
	script, err := os.ReadFile("../../deploy/compose/mysql/01-create-users.sh")
	if err != nil {
		t.Fatal(err)
	}
	content := string(script)
	for _, tableName := range sortedStringKeys(epoch5SemanticSchemaManifest.tables) {
		privilege := expectedRuntimeDMLPrivileges(tableName)
		if privilege == "" {
			needle := ".\\`" + tableName + "\\` TO '${MYSQL_RUNTIME_USER}'@'%'"
			if strings.Contains(content, needle) {
				t.Errorf("runtime grant script unexpectedly grants table-level privileges on %s", tableName)
			}
			continue
		}
		needle := "GRANT " + privilege + " ON \\`${MYSQL_DATABASE}\\`.\\`" + tableName +
			"\\` TO '${MYSQL_RUNTIME_USER}'@'%'"
		if !strings.Contains(content, needle) {
			t.Errorf("runtime grant script does not explicitly grant %s on %s", privilege, tableName)
		}
	}
	if strings.Contains(content, ".\\`schema_migrations\\` TO '${MYSQL_RUNTIME_USER}'@'%'") {
		t.Fatal("runtime grant script grants table-level migration ledger privileges")
	}
	for _, required := range []string{
		"SET DEFAULT ROLE NONE", "FROM mysql.role_edges", "remaining_role_count",
		"FROM mysql.proxies_priv", "REVOKE PROXY ON", "remaining_proxy_count",
		"@@GLOBAL.mandatory_roles", "WHERE User = ''", "Host <> '%'",
		"SELECT VERSION(), @@version_comment", "^8\\.4\\.[0-9]+", "*mariadb*",
		"FROM_USER = '${account_user}' AND FROM_HOST = '%'",
		"Proxied_user = '${account_user}' AND Proxied_host = '%'",
		"information_schema.TRIGGERS", "information_schema.EVENTS",
		"information_schema.ROUTINES", "information_schema.VIEWS",
		"WHERE DEFINER = '${account_user}@%'", "account_definer_object_count=",
		"DISCARD OLD PASSWORD", "--connect-timeout=\"$mysql_connect_timeout_seconds\"",
		"timeout \"${remaining}s\"", "SET ROLE ALL; SELECT CURRENT_USER(), CURRENT_ROLE()",
		"SELECT ID FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY '${account_user}'",
		"KILL CONNECTION ${session_id}", "remaining_session_count",
		"ALTER USER '${account_user}'@'%' ACCOUNT LOCK",
		"ALTER USER '${account_user}'@'%' ACCOUNT UNLOCK",
		"fail_closed_containment()", "containment_armed=true",
		"mkfifo \"$account_lock_directory/input\" \"$account_lock_directory/output\"",
		"--unbuffered --force", "--skip-reconnect --binary-mode --skip-commands",
		"root_mysql_batch()", "root_request=true",
		"account_lock_names=()", "LC_ALL=C sort -u",
		"GET_LOCK('${lock_name}', ${effective_lock_timeout})",
		"GET_LOCK('${lock_name}', 0)",
		"ares_migration_account_", "LEFT(SHA2('${MYSQL_MIGRATION_USER}', 256), 32)",
		"已锁定迁移账号仍有活跃会话",
		"unexpected_schema_grantee_count=",
		"SELECT @@GLOBAL.partial_revokes", "grant_database_pattern=",
		"ESCAPE CHAR(92)", "u.Show_view_priv",
		"JSON_EXTRACT(u.User_attributes, '$.Restrictions')",
		"SET SESSION sql_mode = 'NO_BACKSLASH_ESCAPES'",
		"IDENTIFIED BY '${account_password_sql}'",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("account script is missing safety primitive %q", required)
		}
	}
	for _, forbidden := range []string{
		"SET @account_password", "GRANT ALL PRIVILEGES",
		"GRANT INSERT, UPDATE, DELETE ON \\`${MYSQL_DATABASE}\\`.* TO '${MYSQL_RUNTIME_USER}'@'%'",
		"GRANT DROP ON \\`${MYSQL_DATABASE}\\`.* TO '${MYSQL_MIGRATION_USER}'@'%'",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("account script contains forbidden privilege or credential pattern %q", forbidden)
		}
	}
	firstWrite := strings.Index(content, "CREATE DATABASE IF NOT EXISTS")
	containmentArmed := strings.Index(content, "containment_armed=true")
	if firstWrite < 0 {
		t.Fatal("account script has no database creation statement")
	}
	if containmentArmed < 0 || containmentArmed > firstWrite {
		t.Error("account script does not arm fail-closed containment before its first database write")
	}
	finalGrant := strings.Index(content, "GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES")
	normalSessionSweep := strings.Index(content, "existing_session_ids=\"$(")
	oldSessionKill := -1
	if normalSessionSweep >= 0 {
		if relative := strings.Index(content[normalSessionSweep:], "KILL CONNECTION ${session_id}"); relative >= 0 {
			oldSessionKill = normalSessionSweep + relative
		}
	}
	if finalGrant < 0 || oldSessionKill < 0 || oldSessionKill > finalGrant {
		t.Error("account script does not terminate old sessions before granting final business privileges")
	}
	accountLock := strings.Index(content[firstWrite:], "ALTER USER '${account_user}'@'%' ACCOUNT LOCK")
	if accountLock >= 0 {
		accountLock += firstWrite
	}
	accountUnlock := strings.LastIndex(content, "ALTER USER '${account_user}'@'%' ACCOUNT UNLOCK")
	if accountLock < 0 || accountLock > oldSessionKill || accountUnlock < finalGrant {
		t.Error("account script does not keep the target account locked across session termination and final grants")
	}
	verification := strings.Index(content, "SET ROLE ALL; SELECT CURRENT_USER(), CURRENT_ROLE()")
	lastSuccess := strings.LastIndex(content, "task_succeeded=true")
	if verification < 0 || lastSuccess < verification {
		t.Error("account script can disarm fail-closed containment before final identity verification")
	}
	for _, readOnlyPreflight := range []string{
		"SELECT VERSION(), @@version_comment",
		"@@GLOBAL.mandatory_roles",
		"WHERE User = ''",
		"Host <> '%'",
		"FROM_USER = '${account_user}' AND FROM_HOST = '%'",
		"Proxied_user = '${account_user}' AND Proxied_host = '%'",
		"schema_executable_object_count=",
		"account_definer_object_count=",
		"unexpected_schema_grantee_count=",
		"migration_target_state=",
	} {
		position := strings.Index(content, readOnlyPreflight)
		if position < 0 || position > firstWrite {
			t.Errorf("fail-closed preflight %q does not run before account/database writes", readOnlyPreflight)
		}
	}
	compose, err := os.ReadFile("../../compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{
		"ARES_DATABASE_ACCOUNT_CONNECT_TIMEOUT_SECONDS:",
		"ARES_DATABASE_ACCOUNT_INIT_TIMEOUT_SECONDS:",
		"ARES_DATABASE_ACCOUNT_LOCK_TIMEOUT_SECONDS:",
	} {
		if got := strings.Count(string(compose), "\n      "+setting); got != 2 {
			t.Errorf("compose account jobs contain %d occurrences of %q, want 2", got, setting)
		}
	}
}

func expectedRuntimeDMLPrivileges(tableName string) string {
	switch tableName {
	case "pipelines", "pipelines_job_combination":
		return ""
	case "apps", "app_configs", "task_record", "env_configs", "integration_settings",
		"release_workflows", "app_config_workflows", "task_step_records":
		return "INSERT, UPDATE"
	case "app_config_domains", "auth_sessions", "auth_oidc_flows":
		return "INSERT, UPDATE, DELETE"
	case "task_record_images":
		return "INSERT, DELETE"
	case "auth_users":
		return "INSERT, UPDATE"
	case "auth_identities", "audit_events", "dev_language_rules", "release_workflow_versions":
		return "INSERT"
	case "auth_bootstrap_state":
		return "UPDATE"
	default:
		return "INSERT, UPDATE, DELETE"
	}
}

func validLedgerRow(migration schemaMigration, now time.Time) ledgerRow {
	return ledgerRow{
		Version:       migration.version,
		Epoch:         sql.NullInt64{Int64: int64(migration.epoch), Valid: true},
		Description:   sql.NullString{String: migration.description, Valid: true},
		Checksum:      sql.NullString{String: migration.checksum(), Valid: true},
		Dirty:         sql.NullInt64{Valid: true},
		StartedAt:     sql.NullTime{Time: now, Valid: true},
		FinishedAt:    sql.NullTime{Time: now, Valid: true},
		CompatibleMin: sql.NullInt64{Int64: int64(migration.compatibleMin), Valid: true},
		CompatibleMax: sql.NullInt64{Int64: int64(migration.compatibleMax), Valid: true},
		LegacyAdopted: sql.NullInt64{Valid: true},
		AppliedAt:     now,
	}
}
