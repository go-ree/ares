package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"

	authdomain "github.com/go-ree/ares/internal/auth"
	workflowdomain "github.com/go-ree/ares/internal/workflow"
	"github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

const migrationProcessHelperEnv = "ARES_TEST_MIGRATION_PROCESS_HELPER"

func TestMigrationProcessHelper(t *testing.T) {
	if os.Getenv(migrationProcessHelperEnv) != "1" {
		return
	}
	dsn := os.Getenv("ARES_TEST_MIGRATION_PROCESS_DSN")
	readyFile := os.Getenv("ARES_TEST_MIGRATION_PROCESS_READY")
	startFile := os.Getenv("ARES_TEST_MIGRATION_PROCESS_START")
	if dsn == "" || readyFile == "" || startFile == "" {
		t.Fatal("migration process helper environment is incomplete")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(SafeMigrationErrorText(err))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatal(SafeMigrationErrorText(err))
	}
	_ = database.Close()
	if err := os.WriteFile(readyFile, []byte("ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(startFile); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for migration helper start signal")
		case <-time.After(10 * time.Millisecond):
		}
	}
	status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
	if err != nil {
		t.Fatal(SafeMigrationErrorText(err))
	}
	if !status.Compatible() {
		t.Fatalf("migration helper returned incompatible status: %s", status.String())
	}
}

const (
	mysqlIntegrationDSNEnv = "ARES_TEST_MYSQL_DSN"
	preW04FixturePath      = "testdata/pre_w04_main_e2cfd2a.sql"
	preW04FixtureSHA256    = "e0fab1c717f2a67793e8ede72950c7da82ef4590bbf0eab9a5364296d02db2d9"
)

func TestPreW04FixtureIsImmutable(t *testing.T) {
	content, err := os.ReadFile(filepath.Clean(preW04FixturePath))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if got := hex.EncodeToString(digest[:]); got != preW04FixtureSHA256 {
		t.Fatalf("pre-W04 fixture checksum = %s, want %s; replace the fixture only through an explicit baseline decision", got, preW04FixtureSHA256)
	}
}

func TestMySQL84Migrations(t *testing.T) {
	harness := newMySQLIntegrationHarness(t)

	t.Run("empty status is read-only and up is idempotent", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Initialized || status.Compatible() {
			t.Fatalf("empty database status = %+v, want uninitialized and incompatible", status)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("read-only status created %d tables in an empty database", got)
		}

		status, err = MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		if got := harness.tableCount(t, databaseName); got != len(epoch5SemanticSchemaManifest.tables)+1 {
			t.Fatalf("table count after migrate up = %d, want %d", got, len(epoch5SemanticSchemaManifest.tables)+1)
		}

		database := openIntegrationDatabase(t, dsn)
		var bootstrapRows, incompleteRows int
		if err := database.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(id = 1 AND completed_at IS NULL AND completed_by IS NULL), 0)
			FROM auth_bootstrap_state`).Scan(&bootstrapRows, &incompleteRows); err != nil {
			t.Fatal(err)
		}
		if bootstrapRows != 1 || incompleteRows != 1 {
			t.Fatalf("fresh bootstrap state = rows:%d incomplete_singletons:%d, want 1/1",
				bootstrapRows, incompleteRows)
		}
		before := readLedgerStamps(t, database)
		if len(before) != len(schemaMigrations) {
			t.Fatalf("ledger rows = %d, want %d", len(before), len(schemaMigrations))
		}
		status, err = MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		after := readLedgerStamps(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("idempotent migrate up changed the ledger\nbefore=%+v\nafter=%+v", before, after)
		}
	})

	t.Run("auth store preserves bootstrap and bounded identity lifecycle", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		store, err := authdomain.NewSQLStore(database)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
		oidcUser, err := store.UpsertOIDCUser(ctx, authdomain.OIDCUser{
			Issuer: "https://issuer.example.invalid", Subject: "first-viewer",
			IdentityHash: bytes.Repeat([]byte{0x41}, 32), DisplayName: "First Viewer",
		}, now, true)
		if err != nil {
			t.Fatal(err)
		}
		if oidcUser.Role != authdomain.RoleViewer {
			t.Fatalf("first OIDC role = %q, want viewer", oidcUser.Role)
		}

		// An OIDC viewer arriving first must not consume or deny the separate,
		// token-protected administrator bootstrap transition.
		if _, err := store.CreateBootstrapAdmin(ctx, authdomain.BootstrapUser{
			Username: "rolled-back-admin", DisplayName: "Rolled Back", PasswordHash: "test-password-hash",
		}, authdomain.AuditEvent{
			Action: strings.Repeat("x", 101), ResourceType: "authentication", Result: "succeeded",
			HTTPStatus: http.StatusOK, RequestID: "rollback-test",
		}, now); err == nil {
			t.Fatal("bootstrap unexpectedly committed when its audit event was rejected")
		}
		if available, err := store.BootstrapAvailable(ctx); err != nil || !available {
			t.Fatalf("failed bootstrap audit consumed initialization state: available=%v error=%v", available, err)
		}
		var rolledBackUsers int
		if err := database.QueryRow("SELECT COUNT(*) FROM auth_users WHERE username = 'rolled-back-admin'").Scan(&rolledBackUsers); err != nil {
			t.Fatal(err)
		}
		if rolledBackUsers != 0 {
			t.Fatalf("failed bootstrap audit retained %d administrator rows", rolledBackUsers)
		}
		administrator, err := store.CreateBootstrapAdmin(ctx, authdomain.BootstrapUser{
			Username: "bootstrap-admin", DisplayName: "Bootstrap Admin", PasswordHash: "test-password-hash",
		}, authdomain.AuditEvent{
			Action: "auth.bootstrap", ResourceType: "authentication", Result: "succeeded",
			HTTPStatus: http.StatusOK, RequestID: "bootstrap-test",
		}, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if administrator.Role != authdomain.RoleAdmin {
			t.Fatalf("bootstrap role = %q, want admin", administrator.Role)
		}
		if available, err := store.BootstrapAvailable(ctx); err != nil || available {
			t.Fatalf("bootstrap available after completion = %v, error = %v", available, err)
		}
		if hasAdmin, err := store.HasEnabledAdmin(ctx, true, ""); err != nil || !hasAdmin {
			t.Fatalf("enabled local admin = %v, error = %v", hasAdmin, err)
		}
		for _, malformed := range []struct {
			name       string
			role       string
			authSource string
		}{
			{name: "role case", role: "Admin", authSource: "bootstrap"},
			{name: "role trailing space", role: "admin ", authSource: "bootstrap"},
			{name: "auth source case", role: "admin", authSource: "Bootstrap"},
			{name: "auth source trailing space", role: "admin", authSource: "bootstrap "},
		} {
			if _, err := database.Exec(`UPDATE auth_users SET role = ?, auth_source = ? WHERE user_id = ?`,
				malformed.role, malformed.authSource, administrator.ID); err != nil {
				t.Fatal(err)
			}
			if hasAdmin, err := store.HasEnabledAdmin(ctx, true, ""); err != nil || hasAdmin {
				t.Fatalf("%s administrator = %v, error = %v; want unavailable", malformed.name, hasAdmin, err)
			}
		}
		if _, err := database.Exec(`UPDATE auth_users SET role = ?, auth_source = 'bootstrap' WHERE user_id = ?`,
			authdomain.RoleAdmin, administrator.ID); err != nil {
			t.Fatal(err)
		}
		if hasAdmin, err := store.HasEnabledAdmin(ctx, false, "https://issuer.example.invalid"); err != nil || hasAdmin {
			t.Fatalf("enabled OIDC admin before promotion = %v, error = %v", hasAdmin, err)
		}
		if _, err := database.Exec(`UPDATE auth_users SET role = ? WHERE user_id = ?`,
			authdomain.RoleAdmin, oidcUser.ID); err != nil {
			t.Fatal(err)
		}
		if hasAdmin, err := store.HasEnabledAdmin(ctx, false, "https://issuer.example.invalid"); err != nil || !hasAdmin {
			t.Fatalf("enabled OIDC admin = %v, error = %v", hasAdmin, err)
		}
		for _, malformedIssuer := range []string{
			"HTTPS://ISSUER.EXAMPLE.INVALID",
			"https://issuer.example.invalid ",
		} {
			if _, err := database.Exec(`UPDATE auth_identities SET issuer = ? WHERE user_id = ?`,
				malformedIssuer, oidcUser.ID); err != nil {
				t.Fatal(err)
			}
			if hasAdmin, err := store.HasEnabledAdmin(ctx, false, "https://issuer.example.invalid"); err != nil || hasAdmin {
				t.Fatalf("non-exact OIDC issuer %q administrator = %v, error = %v; want unavailable",
					malformedIssuer, hasAdmin, err)
			}
			disableLastLocal := false
			if _, err := store.UpdateUser(ctx, administrator.ID,
				authdomain.UserPatch{Enabled: &disableLastLocal}, now.Add(time.Minute),
				true, "https://issuer.example.invalid"); !errors.Is(err, authdomain.ErrLastAdmin) {
				t.Fatalf("last local admin removal with non-exact OIDC issuer %q fallback error = %v, want ErrLastAdmin",
					malformedIssuer, err)
			}
		}
		if _, err := database.Exec(`UPDATE auth_identities SET issuer = ? WHERE user_id = ?`,
			"https://issuer.example.invalid", oidcUser.ID); err != nil {
			t.Fatal(err)
		}
		if hasAdmin, err := store.HasEnabledAdmin(ctx, false, "https://other-issuer.example.invalid"); err != nil || hasAdmin {
			t.Fatalf("OIDC administrator from another issuer = %v, error = %v", hasAdmin, err)
		}
		if hasAdmin, err := store.HasEnabledAdmin(ctx, false, ""); err != nil || hasAdmin {
			t.Fatalf("admin with all login methods disabled = %v, error = %v", hasAdmin, err)
		}
		disabledValue := false
		for _, malformed := range []struct {
			name       string
			role       string
			authSource string
		}{
			{name: "role case", role: "Admin", authSource: "bootstrap"},
			{name: "role trailing space", role: "admin ", authSource: "bootstrap"},
			{name: "auth source case", role: "admin", authSource: "Bootstrap"},
			{name: "auth source trailing space", role: "admin", authSource: "bootstrap "},
		} {
			if _, err := database.Exec(`UPDATE auth_users SET role = ?, auth_source = ? WHERE user_id = ?`,
				malformed.role, malformed.authSource, administrator.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpdateUser(ctx, oidcUser.ID,
				authdomain.UserPatch{Enabled: &disabledValue}, now.Add(time.Minute),
				true, "https://issuer.example.invalid"); !errors.Is(err, authdomain.ErrLastAdmin) {
				t.Fatalf("OIDC last-admin removal with %s local fallback error = %v, want ErrLastAdmin",
					malformed.name, err)
			}
		}
		if _, err := database.Exec(`UPDATE auth_users SET role = ?, auth_source = 'bootstrap' WHERE user_id = ?`,
			authdomain.RoleAdmin, administrator.ID); err != nil {
			t.Fatal(err)
		}
		oidcUserB, err := store.UpsertOIDCUser(ctx, authdomain.OIDCUser{
			Issuer: "https://issuer-b.example.invalid", Subject: "second-admin",
			IdentityHash: bytes.Repeat([]byte{0x51}, 32), DisplayName: "Second OIDC Administrator",
		}, now, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE auth_users SET role = ? WHERE user_id = ?`,
			authdomain.RoleAdmin, oidcUserB.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateUser(ctx, oidcUserB.ID,
			authdomain.UserPatch{Enabled: &disabledValue}, now.Add(time.Minute),
			false, "https://issuer-b.example.invalid"); !errors.Is(err, authdomain.ErrLastAdmin) {
			t.Fatalf("issuer B admin removal with only issuer A fallback error = %v, want ErrLastAdmin", err)
		}
		if _, err := store.UpdateUser(ctx, oidcUserB.ID,
			authdomain.UserPatch{Enabled: &disabledValue}, now.Add(time.Minute),
			true, "https://issuer-b.example.invalid"); err != nil {
			t.Fatalf("issuer B admin removal with local fallback: %v", err)
		}
		if _, err := store.CreateBootstrapAdmin(ctx, authdomain.BootstrapUser{
			Username: "second-admin", DisplayName: "Second", PasswordHash: "test-password-hash",
		}, authdomain.AuditEvent{
			Action: "auth.bootstrap", ResourceType: "authentication", Result: "succeeded",
			HTTPStatus: http.StatusOK, RequestID: "second-bootstrap-test",
		}, now.Add(2*time.Minute)); !errors.Is(err, authdomain.ErrBootstrapUnavailable) {
			t.Fatalf("second bootstrap error = %v", err)
		}

		for index := 0; index < 20; index++ {
			digest := sha256.Sum256([]byte(fmt.Sprintf("active-session-%d", index)))
			createdAt := now.Add(-time.Duration(20-index) * time.Minute)
			if _, err := database.Exec(`INSERT INTO auth_sessions
				(session_hash, user_id, expires_at, revoked_at, last_seen_at, created_at)
				VALUES (?, ?, ?, NULL, ?, ?)`, digest[:], administrator.ID,
				now.Add(time.Hour), createdAt, createdAt); err != nil {
				t.Fatal(err)
			}
		}
		staleExpired := sha256.Sum256([]byte("stale-expired-session"))
		staleRevoked := sha256.Sum256([]byte("stale-revoked-session"))
		if _, err := database.Exec(`INSERT INTO auth_sessions
			(session_hash, user_id, expires_at, revoked_at, last_seen_at, created_at)
			VALUES (?, ?, ?, NULL, ?, ?), (?, ?, ?, ?, ?, ?)`,
			staleExpired[:], administrator.ID, now.Add(-8*24*time.Hour), now.Add(-9*24*time.Hour), now.Add(-9*24*time.Hour),
			staleRevoked[:], administrator.ID, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour),
			now.Add(-9*24*time.Hour), now.Add(-9*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		var sessionCleanupPlan string
		if err := database.QueryRow(`EXPLAIN FORMAT=JSON DELETE FROM auth_sessions
			WHERE expires_at <= ? ORDER BY expires_at ASC LIMIT ?`,
			now.Add(-24*time.Hour), 1000).Scan(&sessionCleanupPlan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sessionCleanupPlan, "idx_auth_sessions_expires") {
			t.Fatalf("session cleanup does not use expiration index: %s", sessionCleanupPlan)
		}
		newSession := sha256.Sum256([]byte("newest-active-session"))
		if err := store.CreateSession(ctx, newSession[:], administrator.ID, now.Add(time.Hour), now); err != nil {
			t.Fatal(err)
		}
		var activeSessions, retainedRevoked, staleSessions int
		if err := database.QueryRow(`SELECT
			SUM(revoked_at IS NULL AND expires_at > ?),
			SUM(revoked_at IS NOT NULL AND created_at >= ?),
			SUM((revoked_at IS NOT NULL OR expires_at <= ?) AND created_at < ?)
			FROM auth_sessions WHERE user_id = ?`, now, now.Add(-7*24*time.Hour),
			now, now.Add(-7*24*time.Hour), administrator.ID).
			Scan(&activeSessions, &retainedRevoked, &staleSessions); err != nil {
			t.Fatal(err)
		}
		if activeSessions != 20 || retainedRevoked != 1 || staleSessions != 0 {
			t.Fatalf("session retention = active:%d recent-revoked:%d stale:%d, want 20/1/0",
				activeSessions, retainedRevoked, staleSessions)
		}

		fixedLogin := now.Add(-time.Hour)
		if _, err := database.Exec(`UPDATE auth_users SET
			display_name = 'Disabled Original', last_login_at = ? WHERE user_id = ?`, fixedLogin, oidcUser.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateUser(ctx, oidcUser.ID,
			authdomain.UserPatch{Enabled: &disabledValue}, now.Add(2*time.Minute),
			false, "https://issuer.example.invalid"); !errors.Is(err, authdomain.ErrLastAdmin) {
			t.Fatalf("last issuer A admin removal without local fallback error = %v, want ErrLastAdmin", err)
		}
		if _, err := store.UpdateUser(ctx, oidcUser.ID,
			authdomain.UserPatch{Enabled: &disabledValue}, now.Add(2*time.Minute),
			true, "https://issuer.example.invalid"); err != nil {
			t.Fatalf("issuer A admin removal with local fallback: %v", err)
		}
		if hasAdmin, err := store.HasEnabledAdmin(ctx, false, "https://issuer.example.invalid"); err != nil || hasAdmin {
			t.Fatalf("disabled OIDC administrator = %v, error = %v", hasAdmin, err)
		}
		disabledSession := sha256.Sum256([]byte("disabled-user-session"))
		if err := store.CreateSession(ctx, disabledSession[:], oidcUser.ID, now.Add(time.Hour), now); !errors.Is(err, authdomain.ErrUnauthenticated) {
			t.Fatalf("disabled user session error = %v, want ErrUnauthenticated", err)
		}
		var lastLoginBefore string
		if err := database.QueryRow(`SELECT DATE_FORMAT(last_login_at, '%Y-%m-%d %H:%i:%s.%f')
			FROM auth_users WHERE user_id = ?`, oidcUser.ID).Scan(&lastLoginBefore); err != nil {
			t.Fatal(err)
		}
		disabled, err := store.UpsertOIDCUser(ctx, authdomain.OIDCUser{
			Issuer: "https://issuer.example.invalid", Subject: "first-viewer",
			IdentityHash: bytes.Repeat([]byte{0x41}, 32), DisplayName: "Must Not Update",
		}, now.Add(time.Hour), true)
		if err != nil {
			t.Fatal(err)
		}
		if disabled.Enabled || disabled.DisplayName != "Disabled Original" || disabled.LastLoginAt == nil {
			t.Fatalf("disabled OIDC identity was mutated during login: %+v", disabled)
		}
		var lastLoginAfter string
		if err := database.QueryRow(`SELECT DATE_FORMAT(last_login_at, '%Y-%m-%d %H:%i:%s.%f')
			FROM auth_users WHERE user_id = ?`, oidcUser.ID).Scan(&lastLoginAfter); err != nil {
			t.Fatal(err)
		}
		if lastLoginAfter != lastLoginBefore {
			t.Fatalf("disabled OIDC last_login_at changed from %s to %s", lastLoginBefore, lastLoginAfter)
		}

		if _, err := database.Exec(`INSERT INTO auth_oidc_flows
			(state_hash, nonce_hash, binding_hash, verifier_ciphertext, expires_at, consumed_at, created_at)
			VALUES (UNHEX(REPEAT('10', 32)), UNHEX(REPEAT('11', 32)), UNHEX(REPEAT('12', 32)),
			'old', ?, NULL, ?),
			(UNHEX(REPEAT('20', 32)), UNHEX(REPEAT('21', 32)), UNHEX(REPEAT('22', 32)),
			'consumed', ?, ?, ?)`, now.Add(-time.Minute), now.Add(-time.Hour),
			now.Add(time.Hour), now, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateOIDCFlow(ctx, authdomain.OIDCFlow{
			StateHash: bytes.Repeat([]byte{0x30}, 32), NonceHash: bytes.Repeat([]byte{0x31}, 32),
			BindingHash: bytes.Repeat([]byte{0x32}, 32), VerifierCiphertext: "new",
			ReturnPath: "/", ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		var flowCount int
		if err := database.QueryRow("SELECT COUNT(*) FROM auth_oidc_flows").Scan(&flowCount); err != nil {
			t.Fatal(err)
		}
		if flowCount != 1 {
			t.Fatalf("OIDC flow cleanup left %d rows, want 1 active row", flowCount)
		}

		if _, err := database.Exec("DELETE FROM auth_oidc_flows"); err != nil {
			t.Fatal(err)
		}
		digits := "(SELECT 0 AS n UNION ALL SELECT 1 UNION ALL SELECT 2 UNION ALL SELECT 3 " +
			"UNION ALL SELECT 4 UNION ALL SELECT 5 UNION ALL SELECT 6 UNION ALL SELECT 7 " +
			"UNION ALL SELECT 8 UNION ALL SELECT 9)"
		insertActive := `INSERT INTO auth_oidc_flows
			(state_hash, nonce_hash, binding_hash, verifier_ciphertext, return_path, expires_at, consumed_at, created_at)
			SELECT UNHEX(SHA2(CONCAT('state-', generated_numbers.n), 256)),
				UNHEX(SHA2(CONCAT('nonce-', generated_numbers.n), 256)),
				UNHEX(SHA2(CONCAT('binding-', generated_numbers.n), 256)),
				CONCAT('ciphertext-', generated_numbers.n), '/', ?, NULL, ?
			FROM (SELECT ones.n + tens.n * 10 + hundreds.n * 100 + thousands.n * 1000 AS n
				FROM ` + digits + ` AS ones
				CROSS JOIN ` + digits + ` AS tens
				CROSS JOIN ` + digits + ` AS hundreds
				CROSS JOIN ` + digits + ` AS thousands) AS generated_numbers
			WHERE generated_numbers.n < 4095`
		if _, err := database.Exec(insertActive, now.Add(time.Hour), now); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO auth_oidc_flows
			(state_hash, nonce_hash, binding_hash, verifier_ciphertext, expires_at, consumed_at, created_at)
			VALUES (UNHEX(REPEAT('80', 32)), UNHEX(REPEAT('81', 32)), UNHEX(REPEAT('82', 32)),
			'expired-at-capacity', ?, NULL, ?),
			(UNHEX(REPEAT('90', 32)), UNHEX(REPEAT('91', 32)), UNHEX(REPEAT('92', 32)),
			'consumed-at-capacity', ?, ?, ?)`, now, now.Add(-time.Hour),
			now.Add(time.Hour), now, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateOIDCFlow(ctx, authdomain.OIDCFlow{
			StateHash: bytes.Repeat([]byte{0xa0}, 32), NonceHash: bytes.Repeat([]byte{0xa1}, 32),
			BindingHash: bytes.Repeat([]byte{0xa2}, 32), VerifierCiphertext: "fills-capacity",
			ReturnPath: "/", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
		}); err != nil {
			t.Fatalf("expired and consumed flows occupied capacity: %v", err)
		}
		var activeFlows, staleFlows int
		if err := database.QueryRow(`SELECT
			SUM(consumed_at IS NULL AND expires_at > ?),
			SUM(consumed_at IS NOT NULL OR expires_at <= ?)
			FROM auth_oidc_flows`, now, now).Scan(&activeFlows, &staleFlows); err != nil {
			t.Fatal(err)
		}
		if activeFlows != 4096 || staleFlows != 0 {
			t.Fatalf("OIDC flow capacity state = active:%d stale:%d, want 4096/0", activeFlows, staleFlows)
		}
		if err := store.CreateOIDCFlow(ctx, authdomain.OIDCFlow{
			StateHash: bytes.Repeat([]byte{0xb0}, 32), NonceHash: bytes.Repeat([]byte{0xb1}, 32),
			BindingHash: bytes.Repeat([]byte{0xb2}, 32), VerifierCiphertext: "over-capacity",
			ReturnPath: "/", ExpiresAt: now.Add(time.Hour), CreatedAt: now,
		}); !errors.Is(err, authdomain.ErrOIDCFlowCapacity) {
			t.Fatalf("OIDC flow at capacity error = %v, want ErrOIDCFlowCapacity", err)
		}
	})

	t.Run("local password rotation is atomic and fails closed", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}

		database := openIntegrationDatabase(t, dsn)
		store, err := authdomain.NewSQLStore(database)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
		const oldPasswordHash = "test-old-password-hash"
		const newPasswordHash = "test-new-password-hash"
		administrator, err := store.CreateBootstrapAdmin(ctx, authdomain.BootstrapUser{
			Username: "password-admin", DisplayName: "Password Administrator", PasswordHash: oldPasswordHash,
		}, authdomain.AuditEvent{
			Action: "auth.bootstrap", ResourceType: "authentication", Result: "succeeded",
			HTTPStatus: http.StatusOK, RequestID: "password-bootstrap-test",
		}, now)
		if err != nil {
			t.Fatal(err)
		}

		firstSessionHash := sha256.Sum256([]byte("password-first-active-session"))
		secondSessionHash := sha256.Sum256([]byte("password-second-active-session"))
		firstExpiresAt := now.Add(2 * time.Hour)
		secondExpiresAt := now.Add(3 * time.Hour)
		if err := store.CreateSession(ctx, firstSessionHash[:], administrator.ID, firstExpiresAt, now); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateSession(ctx, secondSessionHash[:], administrator.ID, secondExpiresAt, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}

		failedAt := now.Add(10 * time.Minute)
		if err := store.ChangeLocalPassword(ctx, administrator.ID,
			"incorrect-old-password-hash", "must-not-be-stored", failedAt); !errors.Is(err, authdomain.ErrInvalidCredentials) {
			t.Fatalf("incorrect expected password hash error = %v, want ErrInvalidCredentials", err)
		}
		var storedPasswordHash string
		if err := database.QueryRow("SELECT password_hash FROM auth_users WHERE user_id = ?", administrator.ID).
			Scan(&storedPasswordHash); err != nil {
			t.Fatal(err)
		}
		if storedPasswordHash != oldPasswordHash {
			t.Fatalf("failed password change stored hash = %q, want unchanged old hash", storedPasswordHash)
		}
		for _, sessionTest := range []struct {
			name      string
			hash      []byte
			expiresAt time.Time
		}{
			{name: "first", hash: firstSessionHash[:], expiresAt: firstExpiresAt},
			{name: "second", hash: secondSessionHash[:], expiresAt: secondExpiresAt},
		} {
			session, err := store.FindSession(ctx, sessionTest.hash)
			if err != nil {
				t.Fatalf("find %s session after rejected password change: %v", sessionTest.name, err)
			}
			if session.RevokedAt != nil || !session.ExpiresAt.Equal(sessionTest.expiresAt) {
				t.Fatalf("%s session changed after rejected password change: revoked_at=%v expires_at=%s, want active through %s",
					sessionTest.name, session.RevokedAt, session.ExpiresAt, sessionTest.expiresAt)
			}
		}

		oidcUser, err := store.UpsertOIDCUser(ctx, authdomain.OIDCUser{
			Issuer: "https://password-issuer.example.invalid", Subject: "password-subject",
			IdentityHash: bytes.Repeat([]byte{0x71}, 32), DisplayName: "OIDC Password User",
		}, now, true)
		if err != nil {
			t.Fatal(err)
		}
		oidcSessionHash := sha256.Sum256([]byte("password-oidc-session"))
		oidcExpiresAt := now.Add(4 * time.Hour)
		if err := store.CreateSession(ctx, oidcSessionHash[:], oidcUser.ID, oidcExpiresAt, now); err != nil {
			t.Fatal(err)
		}

		changedAt := now.Add(20 * time.Minute)
		if err := store.ChangeLocalPassword(ctx, administrator.ID,
			oldPasswordHash, newPasswordHash, changedAt); err != nil {
			t.Fatalf("change local password: %v", err)
		}
		if err := database.QueryRow("SELECT password_hash FROM auth_users WHERE user_id = ?", administrator.ID).
			Scan(&storedPasswordHash); err != nil {
			t.Fatal(err)
		}
		if storedPasswordHash != newPasswordHash {
			t.Fatalf("successful password change stored hash = %q, want %q", storedPasswordHash, newPasswordHash)
		}
		for _, sessionTest := range []struct {
			name string
			hash []byte
		}{
			{name: "first", hash: firstSessionHash[:]},
			{name: "second", hash: secondSessionHash[:]},
		} {
			session, err := store.FindSession(ctx, sessionTest.hash)
			if err != nil {
				t.Fatalf("find %s session after password change: %v", sessionTest.name, err)
			}
			if session.RevokedAt == nil || !session.RevokedAt.Equal(changedAt) || !session.ExpiresAt.Equal(changedAt) {
				t.Fatalf("%s session after password change: revoked_at=%v expires_at=%s, want both %s",
					sessionTest.name, session.RevokedAt, session.ExpiresAt, changedAt)
			}
		}
		oidcSession, err := store.FindSession(ctx, oidcSessionHash[:])
		if err != nil {
			t.Fatalf("find unrelated OIDC session after local password change: %v", err)
		}
		if oidcSession.RevokedAt != nil || !oidcSession.ExpiresAt.Equal(oidcExpiresAt) {
			t.Fatalf("unrelated OIDC session changed with local password: revoked_at=%v expires_at=%s, want active through %s",
				oidcSession.RevokedAt, oidcSession.ExpiresAt, oidcExpiresAt)
		}

		// A login that already verified the old hash before the rotation must
		// fail its transactional hash recheck and cannot recreate a session.
		staleLoginHash := sha256.Sum256([]byte("password-stale-local-login"))
		if _, err := store.CreateLocalSession(ctx, administrator.ID, oldPasswordHash,
			staleLoginHash[:], nil, now.Add(6*time.Hour), now.Add(15*time.Minute)); !errors.Is(err, authdomain.ErrInvalidCredentials) {
			t.Fatalf("stale local login error = %v, want ErrInvalidCredentials", err)
		}
		if _, err := store.FindSession(ctx, staleLoginHash[:]); !errors.Is(err, authdomain.ErrSessionNotFound) {
			t.Fatalf("stale local login session error = %v, want ErrSessionNotFound", err)
		}
		var updatedAt time.Time
		if err := database.QueryRow("SELECT updated_at FROM auth_users WHERE user_id = ?", administrator.ID).
			Scan(&updatedAt); err != nil {
			t.Fatal(err)
		}
		if !updatedAt.Equal(changedAt) {
			t.Fatalf("stale local login changed updated_at to %s, want %s", updatedAt, changedAt)
		}

		freshLoginHash := sha256.Sum256([]byte("password-fresh-local-login"))
		freshLoginAt := now.Add(25 * time.Minute)
		freshUser, err := store.CreateLocalSession(ctx, administrator.ID, newPasswordHash,
			freshLoginHash[:], nil, now.Add(6*time.Hour), freshLoginAt)
		if err != nil {
			t.Fatalf("fresh local login after password rotation: %v", err)
		}
		if freshUser.LastLoginAt == nil || !freshUser.LastLoginAt.Equal(freshLoginAt) {
			t.Fatalf("fresh local login timestamp = %v, want %s", freshUser.LastLoginAt, freshLoginAt)
		}
		if session, err := store.FindSession(ctx, freshLoginHash[:]); err != nil || session.RevokedAt != nil {
			t.Fatalf("fresh local login session = %+v, error=%v", session, err)
		}

		if err := store.ChangeLocalPassword(ctx, oidcUser.ID, "", "must-not-be-stored", changedAt); !errors.Is(err, authdomain.ErrPasswordChangeUnsupported) {
			t.Fatalf("OIDC password change error = %v, want ErrPasswordChangeUnsupported", err)
		}
		var oidcPasswordHash sql.NullString
		if err := database.QueryRow("SELECT password_hash FROM auth_users WHERE user_id = ?", oidcUser.ID).
			Scan(&oidcPasswordHash); err != nil {
			t.Fatal(err)
		}
		if oidcPasswordHash.Valid {
			t.Fatalf("rejected OIDC password change stored hash %q", oidcPasswordHash.String)
		}
		oidcSession, err = store.FindSession(ctx, oidcSessionHash[:])
		if err != nil {
			t.Fatalf("find OIDC session after rejected password change: %v", err)
		}
		if oidcSession.RevokedAt != nil || !oidcSession.ExpiresAt.Equal(oidcExpiresAt) {
			t.Fatalf("OIDC session changed after rejected password change: revoked_at=%v expires_at=%s, want active through %s",
				oidcSession.RevokedAt, oidcSession.ExpiresAt, oidcExpiresAt)
		}

		disabledSessionHash := sha256.Sum256([]byte("password-disabled-local-session"))
		disabledExpiresAt := now.Add(5 * time.Hour)
		if err := store.CreateSession(ctx, disabledSessionHash[:], administrator.ID, disabledExpiresAt, now); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("UPDATE auth_users SET enabled = 0 WHERE user_id = ?", administrator.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.ChangeLocalPassword(ctx, administrator.ID,
			newPasswordHash, "disabled-user-new-hash", freshLoginAt.Add(time.Minute)); !errors.Is(err, authdomain.ErrPasswordChangeUnsupported) {
			t.Fatalf("disabled local password change error = %v, want ErrPasswordChangeUnsupported", err)
		}
		if err := database.QueryRow("SELECT password_hash FROM auth_users WHERE user_id = ?", administrator.ID).
			Scan(&storedPasswordHash); err != nil {
			t.Fatal(err)
		}
		if storedPasswordHash != newPasswordHash {
			t.Fatalf("rejected disabled-user password change stored hash = %q, want %q", storedPasswordHash, newPasswordHash)
		}
		disabledSession, err := store.FindSession(ctx, disabledSessionHash[:])
		if err != nil {
			t.Fatalf("find disabled local session after rejected password change: %v", err)
		}
		if disabledSession.RevokedAt != nil || !disabledSession.ExpiresAt.Equal(disabledExpiresAt) {
			t.Fatalf("disabled local session changed after rejected password change: revoked_at=%v expires_at=%s, want active through %s",
				disabledSession.RevokedAt, disabledSession.ExpiresAt, disabledExpiresAt)
		}
	})

	t.Run("workflow checksum survives MySQL JSON normalization", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}

		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO env_configs (env, description_cn)
			VALUES ('dev', 'Workflow checksum test')`); err != nil {
			t.Fatal(err)
		}
		appResult, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('workflow-checksum-test', 'Workflow checksum test', 'integration-test',
				'Integration Test', 'golang', 'https://example.invalid/workflow-checksum-test')`)
		if err != nil {
			t.Fatal(err)
		}
		appID, err := appResult.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		configResult, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type)
			VALUES (?, 'dev', 'golang')`, appID)
		if err != nil {
			t.Fatal(err)
		}
		configID64, err := configResult.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		configID := int(configID64)
		engine, err := xorm.NewEngine("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer engine.Close()
		service := workflowdomain.NewService(workflowdomain.NewXORMStore(engine), workflowdomain.DefaultRegistry())

		firstSpec := workflowdomain.WorkflowSpec{
			SchemaVersion: workflowdomain.SchemaVersionV1,
			Name:          "MySQL canonical checksum",
			Steps: []workflowdomain.StepSpec{{
				Key: "verify", Name: "Verify", Uses: workflowdomain.NoopUses,
				With: json.RawMessage(` {"output":{"z":100,"a":{"second":1.2300,"first":9007199254740993}},"message":"canonical"} `),
			}},
		}
		first, err := service.Save(ctx, configID, 0, "migration-test", 0, firstSpec)
		if err != nil {
			t.Fatalf("save workflow after migration: %v", err)
		}
		readBack, err := service.GetCurrent(ctx, configID)
		if err != nil {
			t.Fatalf("read workflow after MySQL JSON rewrite: %v", err)
		}
		if readBack.WorkflowVersionID != first.WorkflowVersionID || readBack.Revision != first.Revision {
			t.Fatalf("read workflow = %#v, saved = %#v", readBack, first)
		}

		secondSpec := firstSpec
		secondSpec.Name = "MySQL canonical checksum v2"
		second, err := service.Save(ctx, configID, readBack.Revision, "migration-test", 0, secondSpec)
		if err != nil {
			t.Fatalf("append workflow version after MySQL JSON rewrite: %v", err)
		}
		if second.Version != first.Version+1 || second.Revision != first.Revision+1 {
			t.Fatalf("second workflow version = %#v, first = %#v", second, first)
		}

		if _, err := database.Exec(`UPDATE release_workflow_versions
			SET spec = JSON_SET(spec, '$.name', 'tampered') WHERE version_id = ?`, second.WorkflowVersionID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.GetCurrent(ctx, configID); err == nil || !strings.Contains(err.Error(), "完整性校验失败") {
			t.Fatalf("tampered MySQL workflow error = %v", err)
		}
	})

	t.Run("demo seed workflows survive MySQL JSON normalization", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}

		engine, err := xorm.NewEngine("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
		previousEngine := Engine
		Engine = engine
		defer func() {
			Engine = previousEngine
			_ = engine.Close()
		}()
		if err := InitializeDemoData(); err != nil {
			t.Fatalf("initialize demo data: %v", err)
		}

		database := openIntegrationDatabase(t, dsn)
		rows, err := database.Query(`SELECT config_id FROM app_configs
			WHERE deleted_at IS NULL ORDER BY config_id`)
		if err != nil {
			t.Fatal(err)
		}
		configIDs := make([]int, 0, 12)
		for rows.Next() {
			var configID int
			if err := rows.Scan(&configID); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			configIDs = append(configIDs, configID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if len(configIDs) != 12 {
			t.Fatalf("demo config count = %d, want 12", len(configIDs))
		}

		service := workflowdomain.NewService(workflowdomain.NewXORMStore(engine), workflowdomain.DefaultRegistry())
		for _, configID := range configIDs {
			view, err := service.GetCurrent(ctx, configID)
			if err != nil {
				t.Fatalf("read demo workflow for config %d after MySQL JSON rewrite: %v", configID, err)
			}
			if len(view.Spec.Steps) != 2 {
				t.Fatalf("demo workflow for config %d has %d steps, want 2", configID, len(view.Spec.Steps))
			}
		}

		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
	})

	t.Run("structural drift needed by data contracts remains a schema-state result", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			statement string
			want      string
		}{
			{name: "missing table", statement: "DROP TABLE apps", want: "apps"},
			{name: "missing column", statement: "ALTER TABLE apps DROP COLUMN app_name_cn", want: "app_name_cn"},
		} {
			t.Run(test.name, func(t *testing.T) {
				dsn, _ := harness.newDatabase(t)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
					t.Fatal(err)
				}
				database := openIntegrationDatabase(t, dsn)
				if _, err := database.Exec(test.statement); err != nil {
					t.Fatal(err)
				}
				before := readLedgerStamps(t, database)

				status, err := InspectSchema(ctx, dsn, 45*time.Second)
				if err != nil {
					t.Fatalf("structural drift was misclassified as an operational error: %v", err)
				}
				if status.Compatible() || !containsProblem(status.ManifestDiffs, test.want) {
					t.Fatalf("structural drift status = %+v, want schema-state diagnostic containing %q", status, test.want)
				}
				after := readLedgerStamps(t, database)
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("read-only structural drift inspection changed the ledger\nbefore=%+v\nafter=%+v", before, after)
				}
			})
		}
	})

	t.Run("demo seed skips a catalog with a soft-deleted demo environment", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO env_configs
			(env, description_cn, enabled, sort_order, deleted_at)
			VALUES ('dev', '已停用的开发环境', 0, 10, NOW())`); err != nil {
			t.Fatal(err)
		}

		engine, err := xorm.NewEngine("mysql", dsn)
		if err != nil {
			t.Fatal(err)
		}
		previousEngine := Engine
		Engine = engine
		defer func() {
			Engine = previousEngine
			_ = engine.Close()
		}()
		if err := InitializeDemoData(); err != nil {
			t.Fatal(err)
		}
		var apps, configs int
		if err := database.QueryRow(`SELECT
			(SELECT COUNT(*) FROM apps),
			(SELECT COUNT(*) FROM app_configs)`).Scan(&apps, &configs); err != nil {
			t.Fatal(err)
		}
		if apps != 0 || configs != 0 {
			t.Fatalf("demo seed reused a soft-deleted environment: apps=%d configs=%d", apps, configs)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
	})

	t.Run("legacy null cleanup covers zero and negative signed primary keys", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		connection, err := database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()

		session := &migrationSession{ctx: ctx, executor: connection, operationTimeout: 45 * time.Second}
		if err := session.createMigrationLedger(); err != nil {
			t.Fatal(err)
		}
		if err := session.bootstrapEmptySchema(); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx,
			"SET SESSION sql_mode = CONCAT_WS(',', @@SESSION.sql_mode, 'NO_AUTO_VALUE_ON_ZERO')"); err != nil {
			t.Fatal(err)
		}

		const minimumInt32 = int64(-1 << 31)
		if _, err := connection.ExecContext(ctx, `INSERT INTO apps
			(app_id, app_name, rundeck_app_name, app_name_cn, owner, owner_cn, dev_language, description_cn, git_url)
			VALUES
			(?, ' Negative App ', 'NULL', 'NULL', 'owner', '负责人', 'golang', ' NULL ', 'https://example.invalid/negative'),
			(0, ' Zero App ', ' NULL ', ' NULL ', 'owner', '负责人', 'golang', 'NULL', 'https://example.invalid/zero')`, minimumInt32); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO app_configs
			(config_id, app_id, env, code_package_type, code_package_path, code_package_name, base_image, pre_stop_command)
			VALUES
			(?, ?, 'negative', 'NULL', ' NULL ', 'NULL', ' null ', 'NULL'),
			(0, 0, 'zero', ' NULL ', 'NULL', ' null ', ' NULL ', 'null')`, minimumInt32, minimumInt32); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO task_record
			(task_id, app_name, rundeck_app_name, branch, env, publisher, message, ci_job_name, cd_job_name, products)
			VALUES
			(?, 'negative-app', 'NULL', 'main', 'negative', 'tester', ' NULL ', 'null', ' NULL ', 'NULL'),
			(0, 'zero-app', ' null ', 'main', 'zero', 'tester', 'NULL', ' NULL ', 'null', ' NULL ')`, minimumInt32); err != nil {
			t.Fatal(err)
		}

		if err := session.executeDirtyMigration(schemaMigrations[0], false); err != nil {
			t.Fatalf("migrate signed legacy identifiers: %v", err)
		}
		rows, err := connection.QueryContext(ctx, `SELECT app_id, app_name_cn,
			rundeck_app_name IS NULL, description_cn IS NULL
			FROM apps WHERE app_id IN (?, 0) ORDER BY app_id`, minimumInt32)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		wantNames := []string{"Negative App", "Zero App"}
		index := 0
		for rows.Next() {
			var id int64
			var name string
			var rundeckNull, descriptionNull bool
			if err := rows.Scan(&id, &name, &rundeckNull, &descriptionNull); err != nil {
				t.Fatal(err)
			}
			if index >= len(wantNames) || name != wantNames[index] || !rundeckNull || !descriptionNull {
				t.Fatalf("normalized app row %d = id=%d name=%q rundeck_null=%t description_null=%t",
					index, id, name, rundeckNull, descriptionNull)
			}
			index++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if index != len(wantNames) {
			t.Fatalf("normalized app row count = %d, want %d", index, len(wantNames))
		}
		var invalidConfigs, invalidTasks int
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM app_configs
			WHERE config_id IN (?, 0) AND
			(code_package_type <> 'golang' OR code_package_path IS NOT NULL OR code_package_name IS NOT NULL OR
			 base_image IS NOT NULL OR pre_stop_command IS NOT NULL)`, minimumInt32).Scan(&invalidConfigs); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_record
			WHERE task_id IN (?, 0) AND
			(rundeck_app_name IS NOT NULL OR message IS NOT NULL OR ci_job_name IS NOT NULL OR
			 cd_job_name IS NOT NULL OR products IS NOT NULL)`, minimumInt32).Scan(&invalidTasks); err != nil {
			t.Fatal(err)
		}
		if invalidConfigs != 0 || invalidTasks != 0 {
			t.Fatalf("signed legacy rows were skipped: invalid_configs=%d invalid_tasks=%d", invalidConfigs, invalidTasks)
		}

		// Exercise the generic batch cursor against the full signed BIGINT
		// domain as well. Managed epoch-one keys are INT today, but this keeps
		// the helper safe if a future migration uses it for a BIGINT table.
		if _, err := connection.ExecContext(ctx, `CREATE TABLE signed_batch_probe (
			id BIGINT NOT NULL PRIMARY KEY, value VARCHAR(32) NULL
		) ENGINE=InnoDB`); err != nil {
			t.Fatal(err)
		}
		if _, err := connection.ExecContext(ctx, `INSERT INTO signed_batch_probe (id, value)
			VALUES (?, 'NULL'), (0, ' NULL ')`, int64(-1<<63)); err != nil {
			t.Fatal(err)
		}
		probeCondition := legacyNonNullTextSQL("`value`")
		if err := session.updateIDsInBatches("signed_batch_probe", "id", probeCondition,
			"`value` = CASE WHEN "+probeCondition+" THEN NULL ELSE `value` END", nil); err != nil {
			t.Fatalf("normalize full signed BIGINT domain: %v", err)
		}
		var normalizedProbeRows int
		if err := connection.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM signed_batch_probe WHERE value IS NULL").Scan(&normalizedProbeRows); err != nil {
			t.Fatal(err)
		}
		if normalizedProbeRows != 2 {
			t.Fatalf("normalized signed BIGINT rows = %d, want 2", normalizedProbeRows)
		}
	})

	t.Run("guarded migration locks the account before executing and drains it afterward", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		started := make(chan struct{})
		proceed := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(proceed)
			}
		}()

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(session *migrationSession) error {
			close(started)
			select {
			case <-proceed:
				return original.up(session)
			case <-session.ctx.Done():
				return session.ctx.Err()
			}
		}

		type guardedResult struct {
			status MigrationStatus
			err    error
		}
		result := make(chan guardedResult, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
			result <- guardedResult{status: status, err: err}
		}()

		select {
		case <-started:
		case outcome := <-result:
			t.Fatalf("guarded migration returned before execution observation: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for guarded migration execution")
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		assertIntegrationDSNCannotConnect(t, migrationDSN)

		close(proceed)
		released = true
		outcome := <-result
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		assertCompatibleStatus(t, outcome.status)
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		assertIntegrationDSNCannotConnect(t, migrationDSN)
	})

	t.Run("guarded migration uses the server canonical database name", func(t *testing.T) {
		var lowerCaseTableNames int
		if err := harness.admin.QueryRow("SELECT @@GLOBAL.lower_case_table_names").Scan(&lowerCaseTableNames); err != nil {
			t.Fatal(err)
		}
		if lowerCaseTableNames == 0 {
			t.Skip("server preserves case-sensitive database names")
		}

		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		migrationConfig, err := mysql.ParseDSN(migrationDSN)
		if err != nil {
			t.Fatal(err)
		}
		adminConfig, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		migrationConfig.DBName = strings.ToUpper(databaseName)
		adminConfig.DBName = strings.ToUpper(databaseName)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUpGuarded(ctx, migrationConfig.FormatDSN(), adminConfig.FormatDSN(), "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		harness.assertGuardedMigrationAccount(t, username, true, 0)
	})

	t.Run("guarded migration failure still locks rotates and drains the account", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(*migrationSession) error {
			return errors.New("password=guarded-failure-secret forced guarded migration failure")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) {
			t.Fatalf("forced guarded migration error = %v, want operational failure", err)
		}
		if strings.Contains(err.Error(), "guarded-failure-secret") {
			t.Fatalf("forced guarded migration error leaked secret: %v", err)
		}
		if status.Dirty == nil || status.Dirty.Epoch != schemaMigrations[0].epoch {
			t.Fatalf("forced guarded migration status = %+v, want dirty epoch %d", status, schemaMigrations[0].epoch)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		assertIntegrationDSNCannotConnect(t, migrationDSN)
	})

	t.Run("guarded migrations serialize account handoff before killing sessions", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		started := make(chan struct{})
		proceed := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(proceed)
			}
		}()

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(session *migrationSession) error {
			close(started)
			select {
			case <-proceed:
				return original.up(session)
			case <-session.ctx.Done():
				return session.ctx.Err()
			}
		}

		type guardedResult struct {
			status MigrationStatus
			err    error
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		firstResult := make(chan guardedResult, 1)
		secondResult := make(chan guardedResult, 1)
		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
			firstResult <- guardedResult{status: status, err: err}
		}()
		select {
		case <-started:
		case outcome := <-firstResult:
			t.Fatalf("first guarded migration returned before serialization test: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for first guarded migration")
		}

		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
			secondResult <- guardedResult{status: status, err: err}
		}()
		select {
		case outcome := <-secondResult:
			t.Fatalf("second guarded migration bypassed account guard: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-time.After(300 * time.Millisecond):
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)

		close(proceed)
		released = true
		first := <-firstResult
		if first.err != nil {
			t.Fatalf("first guarded migration: %v", first.err)
		}
		assertCompatibleStatus(t, first.status)
		second := <-secondResult
		if second.err != nil {
			t.Fatalf("second guarded migration: %v", second.err)
		}
		assertCompatibleStatus(t, second.status)
		harness.assertGuardedMigrationAccount(t, username, true, 0)
	})

	t.Run("guarded account lock timeout makes no account or schema writes", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		holder, err := harness.admin.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		lockName := guardedMigrationAccountLockName(username)
		var acquired int
		if err := holder.QueryRowContext(ctx, "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil || acquired != 1 {
			t.Fatalf("hold guarded migration account lock: acquired=%d error=%v", acquired, err)
		}
		defer func() {
			var released sql.NullInt64
			if err := holder.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
				t.Errorf("release held guarded migration account lock: %v", err)
			}
		}()

		type accountState struct {
			plugin, authentication, locked string
		}
		readState := func() accountState {
			var state accountState
			if err := harness.admin.QueryRow(`SELECT plugin, authentication_string, account_locked
				FROM mysql.user WHERE BINARY User = BINARY ? AND Host = '%'`, username).
				Scan(&state.plugin, &state.authentication, &state.locked); err != nil {
				t.Fatal(err)
			}
			return state
		}
		before := readState()
		startedAt := time.Now()
		_, err = MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 200*time.Millisecond)
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrSchemaState) {
			t.Fatalf("guard lock timeout error = %v, want operational context deadline", err)
		}
		if elapsed := time.Since(startedAt); elapsed > 3*time.Second {
			t.Fatalf("guard lock timeout returned after %s, want a prompt bounded failure", elapsed)
		}
		if after := readState(); after != before {
			t.Fatalf("guard lock timeout changed account state: before=%+v after=%+v", before, after)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("guard lock timeout created %d schema tables", got)
		}
	})

	t.Run("guarded migration refuses locked account with an active session without killing it", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT UNLOCK"); err != nil {
			t.Fatal(err)
		}
		migrationDatabase, err := sql.Open("mysql", migrationDSN)
		if err != nil {
			t.Fatal(err)
		}
		migrationDatabase.SetMaxOpenConns(1)
		migrationDatabase.SetMaxIdleConns(0)
		defer migrationDatabase.Close()
		activeConn, err := migrationDatabase.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer activeConn.Close()
		if _, err := activeConn.ExecContext(ctx, "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT LOCK"); err != nil {
			t.Fatal(err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		var authenticationBefore string
		if err := harness.admin.QueryRow(`SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err = MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "unsafe takeover") {
			t.Fatalf("locked active-session error = %v, want guarded operational refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		var authenticationAfter string
		if err := harness.admin.QueryRow(`SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("unsafe-takeover refusal rotated the guarded account credential")
		}
		var one int
		if err := activeConn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("unsafe-takeover refusal killed the active session: value=%d error=%v", one, err)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("unsafe-takeover refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration rejects an administrator without global session visibility", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		harness.next++
		adminUsername := fmt.Sprintf("aresadm_%x", time.Now().UnixNano()+int64(harness.next))
		adminPassword := fmt.Sprintf("Aa1!AresAdmin%x", time.Now().UnixNano())
		adminAccount := sqlStringLiteral(adminUsername) + "@'%'"
		if _, err := harness.admin.ExecContext(ctx,
			"CREATE USER "+adminAccount+" IDENTIFIED BY "+sqlStringLiteral(adminPassword)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if _, err := harness.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+adminAccount); err != nil {
				t.Errorf("drop limited integration administrator %s: %v", adminUsername, err)
			}
		})
		var partialRevokes int
		if err := harness.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
			t.Fatal(err)
		}
		grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
		if _, err := harness.admin.ExecContext(ctx, "GRANT CREATE USER ON *.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "GRANT SELECT ON mysql.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx,
			fmt.Sprintf("GRANT SELECT ON `%s`.* TO %s", grantPattern, adminAccount)); err != nil {
			t.Fatal(err)
		}
		limitedConfig := harness.adminConfig.Clone()
		limitedConfig.User = adminUsername
		limitedConfig.Passwd = adminPassword
		limitedConfig.DBName = databaseName
		limitedConfig.ParseTime = true

		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT UNLOCK"); err != nil {
			t.Fatal(err)
		}
		migrationDatabase, err := sql.Open("mysql", migrationDSN)
		if err != nil {
			t.Fatal(err)
		}
		migrationDatabase.SetMaxOpenConns(1)
		migrationDatabase.SetMaxIdleConns(0)
		defer migrationDatabase.Close()
		activeConn, err := migrationDatabase.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer activeConn.Close()
		if _, err := activeConn.ExecContext(ctx, "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT LOCK"); err != nil {
			t.Fatal(err)
		}
		var authenticationBefore string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err = MigrateUpGuarded(ctx, migrationDSN, limitedConfig.FormatDSN(), "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "PROCESS") {
			t.Fatalf("limited administrator error = %v, want operational PROCESS refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 1)
		var authenticationAfter string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("limited-administrator refusal changed the guarded account credential")
		}
		var one int
		if err := activeConn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("limited-administrator refusal killed the hidden session: value=%d error=%v", one, err)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("limited-administrator refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration rejects an administrator without authoritative metadata visibility", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		harness.next++
		adminUsername := fmt.Sprintf("aresmeta_%x", time.Now().UnixNano()+int64(harness.next))
		adminPassword := fmt.Sprintf("Aa1!AresMeta%x", time.Now().UnixNano())
		adminAccount := sqlStringLiteral(adminUsername) + "@'%'"
		if _, err := harness.admin.ExecContext(ctx,
			"CREATE USER "+adminAccount+" IDENTIFIED BY "+sqlStringLiteral(adminPassword)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cleanupCancel()
			if _, err := harness.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+adminAccount); err != nil {
				t.Errorf("drop metadata-limited integration administrator %s: %v", adminUsername, err)
			}
		})
		if _, err := harness.admin.ExecContext(ctx,
			"GRANT SELECT, PROCESS, CREATE USER, EVENT, SHOW VIEW ON *.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, "GRANT CONNECTION_ADMIN ON *.* TO "+adminAccount); err != nil {
			t.Fatal(err)
		}
		limitedConfig := harness.adminConfig.Clone()
		limitedConfig.User = adminUsername
		limitedConfig.Passwd = adminPassword
		limitedConfig.DBName = databaseName
		limitedConfig.ParseTime = true

		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(
			"CREATE TABLE `%s`.hidden_trigger_source (id BIGINT PRIMARY KEY)", databaseName)); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(
			"CREATE DEFINER=%s TRIGGER `%s`.hidden_guarded_trigger BEFORE INSERT ON `%s`.hidden_trigger_source "+
				"FOR EACH ROW SET NEW.id = NEW.id",
			guardedAccountSQL(username), databaseName, databaseName)); err != nil {
			t.Fatal(err)
		}
		limitedDatabase, err := sql.Open("mysql", limitedConfig.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		defer limitedDatabase.Close()
		var limitedVisibleTriggers int
		if err := limitedDatabase.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TRIGGERS
			WHERE BINARY TRIGGER_SCHEMA = BINARY ?`, databaseName).Scan(&limitedVisibleTriggers); err != nil {
			t.Fatal(err)
		}
		if limitedVisibleTriggers != 0 {
			t.Fatalf("metadata-limited administrator unexpectedly sees %d trigger(s)", limitedVisibleTriggers)
		}
		var authenticationBefore string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err = MigrateUpGuarded(ctx, migrationDSN, limitedConfig.FormatDSN(), "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "TRIGGER") {
			t.Fatalf("metadata-limited administrator error = %v, want operational metadata refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		var authenticationAfter string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("metadata-limited refusal changed the guarded account credential")
		}
		var rootVisibleTriggers int
		if err := harness.admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.TRIGGERS
			WHERE BINARY TRIGGER_SCHEMA = BINARY ? AND TRIGGER_NAME = 'hidden_guarded_trigger'`, databaseName).
			Scan(&rootVisibleTriggers); err != nil || rootVisibleTriggers != 1 {
			t.Fatalf("hidden trigger changed during refusal: count=%d error=%v", rootVisibleTriggers, err)
		}
	})

	t.Run("guarded migration authoritatively rejects an external inbound foreign key", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		// The external owner is cleaned first so its inbound reference cannot
		// prevent cleanup of the Ares schema.
		_, externalDatabase := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s.inbound_apps (
			id BIGINT NOT NULL PRIMARY KEY,
			app_id INT NOT NULL,
			CONSTRAINT operator_inbound_apps FOREIGN KEY (app_id)
			REFERENCES %s.apps (app_id) ON DELETE RESTRICT ON UPDATE CASCADE
		) ENGINE=InnoDB`, "`"+externalDatabase+"`", "`"+databaseName+"`")); err != nil {
			t.Fatal(err)
		}

		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		// Demonstrate the visibility gap this administrator pass closes: the
		// least-privilege migration account cannot see the external child row.
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT UNLOCK"); err != nil {
			t.Fatal(err)
		}
		migrationDatabase := openIntegrationDatabase(t, migrationDSN)
		var migrationVisible int
		if err := migrationDatabase.QueryRowContext(ctx, `SELECT COUNT(*)
			FROM information_schema.KEY_COLUMN_USAGE
			WHERE BINARY REFERENCED_TABLE_SCHEMA = BINARY ?
				AND BINARY TABLE_SCHEMA = BINARY ?
				AND REFERENCED_TABLE_NAME = 'apps'`, databaseName, externalDatabase).Scan(&migrationVisible); err != nil {
			t.Fatal(err)
		}
		if err := migrationDatabase.Close(); err != nil {
			t.Fatal(err)
		}
		if migrationVisible != 0 {
			t.Fatalf("least-privilege migration account unexpectedly sees %d external inbound foreign key row(s)", migrationVisible)
		}
		if _, err := harness.admin.ExecContext(ctx, "ALTER USER "+account+" ACCOUNT LOCK"); err != nil {
			t.Fatal(err)
		}
		var authenticationBefore string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationBefore); err != nil {
			t.Fatal(err)
		}

		_, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || !errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "反向引用") {
			t.Fatalf("guarded inbound foreign-key error = %v, want schema-state refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		var authenticationAfter string
		if err := harness.admin.QueryRowContext(ctx, `SELECT authentication_string FROM mysql.user
			WHERE BINARY User = BINARY ? AND Host = '%'`, username).Scan(&authenticationAfter); err != nil {
			t.Fatal(err)
		}
		if authenticationAfter != authenticationBefore {
			t.Fatal("authoritative inbound foreign-key refusal changed the guarded account credential")
		}
	})

	t.Run("guarded migration rejects an overprivileged direct grant", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var partialRevokes int
		if err := harness.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
			t.Fatal(err)
		}
		grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
		if _, err := harness.admin.ExecContext(ctx,
			fmt.Sprintf("GRANT DROP ON `%s`.* TO %s", grantPattern, account)); err != nil {
			t.Fatal(err)
		}

		_, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "unexpected direct schema privilege") {
			t.Fatalf("overprivileged guarded error = %v, want operational privilege refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("overprivileged guarded refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration rejects an unescaped wildcard schema grant", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		account := guardedAccountSQL(username)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var partialRevokes int
		if err := harness.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
			t.Fatal(err)
		}
		if partialRevokes != 0 {
			t.Skip("unescaped wildcard grants are literal when partial_revokes is enabled")
		}
		if !strings.Contains(databaseName, "_") {
			t.Fatalf("test database %q does not exercise an underscore wildcard", databaseName)
		}
		if _, err := harness.admin.ExecContext(ctx, "REVOKE ALL PRIVILEGES, GRANT OPTION FROM "+account); err != nil {
			t.Fatal(err)
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf(
			"GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES ON `%s`.* TO %s",
			databaseName, account)); err != nil {
			t.Fatal(err)
		}

		_, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 20*time.Second, 5*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), "unique literal pattern") {
			t.Fatalf("wildcard schema grant error = %v, want operational literal-pattern refusal", err)
		}
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("wildcard grant refusal created %d schema tables", got)
		}
	})

	t.Run("guarded migration cancels and contains after account lock holder disconnects", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		migrationDSN, username := harness.newGuardedMigrationUser(t, dsn, databaseName)
		started := make(chan struct{})
		proceed := make(chan struct{})
		released := false
		defer func() {
			if !released {
				close(proceed)
			}
		}()

		original := schemaMigrations[0]
		t.Cleanup(func() { schemaMigrations[0] = original })
		schemaMigrations[0].up = func(session *migrationSession) error {
			close(started)
			select {
			case <-proceed:
				return original.up(session)
			case <-session.ctx.Done():
				return session.ctx.Err()
			}
		}

		type guardedResult struct {
			status MigrationStatus
			err    error
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result := make(chan guardedResult, 1)
		go func() {
			status, err := MigrateUpGuarded(ctx, migrationDSN, dsn, "", 20*time.Second, 5*time.Second)
			result <- guardedResult{status: status, err: err}
		}()
		select {
		case <-started:
		case outcome := <-result:
			t.Fatalf("guarded migration returned before watchdog test: status=%s error=%v", outcome.status.String(), outcome.err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for guarded watchdog test migration")
		}

		lockName := guardedMigrationAccountLockName(username)
		var holderID sql.NullInt64
		if err := harness.admin.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)", lockName).Scan(&holderID); err != nil {
			t.Fatal(err)
		}
		if !holderID.Valid {
			t.Fatal("guarded migration account lock has no holder")
		}
		if _, err := harness.admin.ExecContext(ctx, fmt.Sprintf("KILL CONNECTION %d", holderID.Int64)); err != nil {
			t.Fatal(err)
		}
		select {
		case outcome := <-result:
			if outcome.err == nil || errors.Is(outcome.err, ErrSchemaState) || !strings.Contains(outcome.err.Error(), "watchdog") {
				t.Fatalf("guarded watchdog result = %v, want operational watchdog failure", outcome.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("guarded migration did not cancel after account lock holder disconnect")
		}
		close(proceed)
		released = true
		harness.assertGuardedMigrationAccount(t, username, true, 0)
		assertIntegrationDSNCannotConnect(t, migrationDSN)
	})

	t.Run("legacy ledger and data are adopted", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		before, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if !before.Initialized || !before.NeedsAdoption || len(before.Applied) != 3 || len(before.Pending) != 2 {
			t.Fatalf("legacy status = %+v, want three adopted candidates and two pending migrations", before)
		}

		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)

		database := openIntegrationDatabase(t, dsn)
		var adopted, native int
		if err := database.QueryRow(`SELECT
			SUM(epoch <= 3 AND legacy_adopted = 1),
			SUM(epoch IN (4, 5) AND legacy_adopted = 0)
			FROM schema_migrations`).Scan(&adopted, &native); err != nil {
			t.Fatal(err)
		}
		if adopted != 3 || native != 2 {
			t.Fatalf("ledger adoption counts = adopted:%d native:%d, want 3 and 2", adopted, native)
		}
		var appName, environment, packagePath string
		if err := database.QueryRow(`SELECT a.app_name, c.env, c.code_package_path
			FROM apps a JOIN app_configs c ON c.app_id = a.app_id
			WHERE a.app_id = 12345 AND c.config_id = 23456`).Scan(&appName, &environment, &packagePath); err != nil {
			t.Fatal(err)
		}
		if appName != "fixture-api" || environment != "staging" || packagePath != "/immutable/fixture" {
			t.Fatalf("fixture sentinel changed during adoption: %q %q %q", appName, environment, packagePath)
		}
		var attributedTasks, attributedWorkflowVersions int
		if err := database.QueryRow(`SELECT
			(SELECT COUNT(*) FROM task_record WHERE publisher_user_id IS NOT NULL),
			(SELECT COUNT(*) FROM release_workflow_versions WHERE created_by_user_id IS NOT NULL)`).Scan(
			&attributedTasks, &attributedWorkflowVersions); err != nil {
			t.Fatal(err)
		}
		if attributedTasks != 0 || attributedWorkflowVersions != 0 {
			t.Fatalf("epoch 5 migration guessed legacy actors: tasks=%d workflow_versions=%d",
				attributedTasks, attributedWorkflowVersions)
		}
	})

	t.Run("epoch two rejects trailing control characters before environment writes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		for index, environmentCode := range []string{"dev\n", "qa\r", "prod\r\n"} {
			if _, err := database.Exec(`INSERT INTO env_configs
				(env, cluster_name, description_cn, harbor_url, harbor_project_name, node_version, maven_version)
				VALUES (?, '', ?, '', '', '', '')`, environmentCode, fmt.Sprintf("invalid-%d", index)); err != nil {
				t.Fatal(err)
			}
			appResult, err := database.Exec(`INSERT INTO apps
				(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
				VALUES (?, ?, 'owner', '负责人', 'golang', 'https://example.invalid/environment-control')`,
				fmt.Sprintf("environment-control-%d", index), fmt.Sprintf("环境控制字符 %d", index))
			if err != nil {
				t.Fatal(err)
			}
			appID, err := appResult.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type)
				VALUES (?, ?, 'golang')`, appID, environmentCode); err != nil {
				t.Fatal(err)
			}
		}
		var catalogBefore, configsBefore string
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY id) FROM env_configs").Scan(&catalogBefore); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY config_id) FROM app_configs").Scan(&configsBefore); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || !strings.Contains(err.Error(), "active environment codes") {
			t.Fatalf("trailing control-character migration error = %v, want canonical-code refusal", err)
		}
		var catalogAfter, configsAfter string
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY id) FROM env_configs").Scan(&catalogAfter); err != nil {
			t.Fatal(err)
		}
		if err := database.QueryRow("SELECT GROUP_CONCAT(HEX(env) ORDER BY config_id) FROM app_configs").Scan(&configsAfter); err != nil {
			t.Fatal(err)
		}
		if catalogAfter != catalogBefore || configsAfter != configsBefore {
			t.Fatalf("refused environment migration changed codes: catalog %q -> %q, configs %q -> %q",
				catalogBefore, catalogAfter, configsBefore, configsAfter)
		}
		var workflowTables int
		if err := database.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'release_workflows'`).Scan(&workflowTables); err != nil {
			t.Fatal(err)
		}
		if workflowTables != 0 {
			t.Fatal("refused environment migration continued into workflow schema writes")
		}
	})

	t.Run("epoch two does not backfill malformed historical task environments", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		const historicalEnvironment = "dev\n"
		if _, err := database.Exec(`INSERT INTO task_record (app_name, branch, env, publisher)
			VALUES ('historical-invalid-env', 'main', ?, 'migration-test')`, historicalEnvironment); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var catalogRows int
		if err := database.QueryRow("SELECT COUNT(*) FROM env_configs").Scan(&catalogRows); err != nil {
			t.Fatal(err)
		}
		if catalogRows != 0 {
			t.Fatalf("malformed historical task environment created %d catalog row(s)", catalogRows)
		}
		var storedEnvironment string
		if err := database.QueryRow("SELECT env FROM task_record WHERE app_name = 'historical-invalid-env'").Scan(&storedEnvironment); err != nil {
			t.Fatal(err)
		}
		if storedEnvironment != historicalEnvironment {
			t.Fatalf("historical task environment changed to %q", storedEnvironment)
		}
	})

	t.Run("legacy unknown version fails closed without adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO schema_migrations (version, applied_at)
			VALUES ('20990101_001_unknown', '2026-09-03 01:04:04')`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "未知版本") {
			t.Fatalf("unknown legacy version status is not fail-closed: %+v", status)
		}
		assertLegacyLedgerUnchanged(t, database, 4)
	})

	t.Run("legacy version gap fails closed without adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec("DELETE FROM schema_migrations WHERE version = ?", pluggableCICDMigrationVersion); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "连续前缀") {
			t.Fatalf("gapped legacy ledger status is not fail-closed: %+v", status)
		}
		assertLegacyLedgerUnchanged(t, database, 2)
	})

	t.Run("unsupported legacy ledger shape fails closed before writes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`CREATE TABLE schema_migrations (
			version VARCHAR(128) NOT NULL PRIMARY KEY,
			unexpected_metadata VARCHAR(64) NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "applied_at") || !containsProblem(status.Problems, "不支持的列") {
			t.Fatalf("unsupported ledger shape problems = %v", status.Problems)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused adoption changed ledger columns: before=%v after=%v", before, after)
		}
	})

	t.Run("legacy epoch-one schema drift fails before adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE apps
			MODIFY COLUMN description_cn VARCHAR(255) NOT NULL`); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "legacy") && !strings.Contains(err.Error(), "IS_NULLABLE") {
			t.Fatalf("legacy epoch-one drift error = %v, status=%+v", err, status)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused epoch-one drift changed ledger columns: before=%v after=%v", before, after)
		}
	})

	t.Run("legacy epoch-two default drift fails before adoption", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec("ALTER TABLE env_configs ALTER COLUMN enabled SET DEFAULT 0"); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !strings.Contains(err.Error(), "默认值") {
			t.Fatalf("legacy epoch-two default drift error = %v", err)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused epoch-two drift changed ledger columns: before=%v after=%v", before, after)
		}
	})

	t.Run("invalid empty complete ledger fails closed before bootstrap", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{
			ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
		}
		if err := session.createMigrationLedger(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			CONVERT TO CHARACTER SET latin1 COLLATE latin1_swedish_ci`); err != nil {
			t.Fatal(err)
		}
		beforeColumns := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "schema_migrations") {
			t.Fatalf("invalid complete ledger problems = %v", status.Problems)
		}
		if got := harness.tableCount(t, databaseName); got != 1 {
			t.Fatalf("invalid complete ledger triggered bootstrap: table count=%d, want 1", got)
		}
		if afterColumns := ledgerColumnNames(t, database); !reflect.DeepEqual(afterColumns, beforeColumns) {
			t.Fatalf("invalid complete ledger changed columns: before=%v after=%v", beforeColumns, afterColumns)
		}
	})

	t.Run("empty legacy ledger with business data fails before adoption", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`CREATE TABLE schema_migrations (
			version VARCHAR(128) NOT NULL PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(schemaBootstrapTables[0].statement); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('legacy-data', '旧数据', 'owner', '负责人', 'golang', 'https://example.invalid/repo')`); err != nil {
			t.Fatal(err)
		}
		beforeColumns := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "包含业务数据") {
			t.Fatalf("empty legacy ledger with data status = %+v", status)
		}
		if got := harness.tableCount(t, databaseName); got != 2 {
			t.Fatalf("refused empty legacy adoption changed table count=%d, want 2", got)
		}
		if afterColumns := ledgerColumnNames(t, database); !reflect.DeepEqual(afterColumns, beforeColumns) {
			t.Fatalf("refused empty legacy adoption changed columns: before=%v after=%v", beforeColumns, afterColumns)
		}
	})

	t.Run("empty legacy ledger with malformed managed table fails before adoption", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`CREATE TABLE schema_migrations (
			version VARCHAR(128) NOT NULL PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`CREATE TABLE apps (
			bogus INT NOT NULL PRIMARY KEY
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`); err != nil {
			t.Fatal(err)
		}
		beforeColumns := ledgerColumnNames(t, database)

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !status.NeedsAdoption || !containsProblem(status.Problems, "缺少列") {
			t.Fatalf("empty legacy ledger with malformed table status = %+v", status)
		}
		if got := harness.tableCount(t, databaseName); got != 2 {
			t.Fatalf("malformed legacy database changed table count=%d, want 2", got)
		}
		if afterColumns := ledgerColumnNames(t, database); !reflect.DeepEqual(afterColumns, beforeColumns) {
			t.Fatalf("malformed legacy database changed ledger: before=%v after=%v", beforeColumns, afterColumns)
		}
	})

	t.Run("tampered partial adoption metadata is not overwritten", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD COLUMN epoch BIGINT UNSIGNED NULL,
			ADD COLUMN checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL`); err != nil {
			t.Fatal(err)
		}
		const tampered = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		if _, err := database.Exec(`UPDATE schema_migrations SET epoch = 1, checksum = ?
			WHERE version = ?`, tampered, schemaMigrations[0].version); err != nil {
			t.Fatal(err)
		}
		before := ledgerColumnNames(t, database)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "已有元数据") {
			t.Fatalf("partial adoption tampering problems = %v", status.Problems)
		}
		after := ledgerColumnNames(t, database)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("refused partial adoption changed columns: before=%v after=%v", before, after)
		}
		var checksum string
		if err := database.QueryRow("SELECT checksum FROM schema_migrations WHERE version = ?",
			schemaMigrations[0].version).Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		if checksum != tampered {
			t.Fatalf("refused partial adoption overwrote checksum: %s", checksum)
		}
	})

	t.Run("interrupted legacy adoption resumes safely", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD COLUMN epoch BIGINT UNSIGNED NULL,
			ADD COLUMN description VARCHAR(255) NULL,
			ADD COLUMN checksum CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
	})

	t.Run("legacy adoption accepts an equivalent epoch index name", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD COLUMN epoch BIGINT UNSIGNED NULL,
			ADD UNIQUE INDEX operator_epoch_unique (epoch)`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var equivalentIndexes int
		if err := database.QueryRow(`SELECT COUNT(DISTINCT INDEX_NAME)
			FROM information_schema.STATISTICS
			WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'
				AND COLUMN_NAME = 'epoch' AND NON_UNIQUE = 0`).Scan(&equivalentIndexes); err != nil {
			t.Fatal(err)
		}
		if equivalentIndexes != 1 {
			t.Fatalf("equivalent epoch indexes = %d, want 1", equivalentIndexes)
		}
	})

	t.Run("pending migration accepts equivalent operator index names", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		database := migrateDatabaseToEpoch(t, dsn, 3)
		for _, statement := range []string{
			"UPDATE schema_migrations SET dirty = 1, finished_at = NULL, last_error = 'simulated interruption' WHERE version = '" + cicdRuntimeHardeningMigrationVersion + "'",
			"ALTER TABLE app_configs RENAME INDEX uk_app_active_env TO operator_app_active_env",
			"ALTER TABLE task_record RENAME INDEX idx_task_workflow_poll TO operator_task_workflow_poll",
			"ALTER TABLE task_step_records RENAME INDEX idx_step_status_uses TO operator_step_status_uses",
		} {
			if _, err := database.Exec(statement); err != nil {
				t.Fatalf("prepare renamed-index fixture with %q: %v", statement, err)
			}
		}

		status, err := MigrateUp(ctx, dsn, cicdRuntimeHardeningMigrationVersion, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		for _, check := range []struct {
			table   string
			columns string
			unique  int
		}{
			{"app_configs", "app_id,active_env", 0},
			{"task_record", "engine_version,status,deleted_at,updated_at,task_id", 1},
			{"task_step_records", "status,uses,task_id", 1},
		} {
			var count int
			if err := database.QueryRow(`SELECT COUNT(*) FROM (
				SELECT INDEX_NAME
				FROM information_schema.STATISTICS
				WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND NON_UNIQUE = ?
				GROUP BY INDEX_NAME
				HAVING GROUP_CONCAT(COLUMN_NAME ORDER BY SEQ_IN_INDEX SEPARATOR ',') = ?
			) equivalent_indexes`, check.table, check.unique, check.columns).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("%s equivalent index count = %d, want 1", check.table, count)
			}
		}
	})

	t.Run("legacy epoch prefixes reject future and unknown schema before adoption", func(t *testing.T) {
		cases := []struct {
			name      string
			epoch     uint64
			statement string
			want      string
		}{
			{
				name: "epoch one future column", epoch: 1,
				statement: "ALTER TABLE app_configs ADD COLUMN active_env VARCHAR(100) GENERATED ALWAYS AS (IF(deleted_at IS NULL, env, NULL)) STORED",
				want:      "manifest 外列",
			},
			{
				name: "epoch two future index", epoch: 2,
				statement: "ALTER TABLE task_record ADD INDEX operator_future_poll (engine_version, status, deleted_at, updated_at, task_id)",
				want:      "manifest 外普通索引",
			},
			{
				name: "epoch three unknown table", epoch: 3,
				statement: "CREATE TABLE operator_data (id INT PRIMARY KEY) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci",
				want:      "manifest 外基础表",
			},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				dsn, _ := harness.newDatabase(t)
				database := migrateDatabaseToEpoch(t, dsn, test.epoch)
				convertLedgerToLegacy(t, database)
				if _, err := database.Exec(test.statement); err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
				assertSchemaStateError(t, err)
				if !strings.Contains(err.Error(), test.want) {
					t.Fatalf("legacy epoch %d drift error = %v, want %q", test.epoch, err, test.want)
				}
				assertLegacyLedgerUnchanged(t, database, int(test.epoch))
			})
		}
	})

	t.Run("clean prefix rejects target objects before creating dirty row", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER description_cn"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		var rows int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("refused clean-prefix drift wrote ledger rows: %d", rows)
		}
	})

	t.Run("dirty resume accepts exact target intermediate state", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		migration := schemaMigrations[1]
		insertDirtyMigrationRow(t, database, migration)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER description_cn"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
	})

	t.Run("epoch five dirty resume accepts empty bootstrap DDL boundary", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 4)
		migration := schemaMigrations[4]
		insertDirtyMigrationRow(t, database, migration)
		for _, statement := range authRBACTables()[:5] {
			if _, err := database.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var rows int
		if err := database.QueryRow("SELECT COUNT(*) FROM auth_bootstrap_state WHERE id = 1").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("resumed migration created %d bootstrap singleton rows, want 1", rows)
		}
	})

	t.Run("epoch five dirty resume rejects a skipped bootstrap singleton", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 4)
		migration := schemaMigrations[4]
		insertDirtyMigrationRow(t, database, migration)
		for _, statement := range authRBACTables() {
			if _, err := database.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "singleton id=1") {
			t.Fatalf("skipped bootstrap singleton problems = %v", status.ManifestDiffs)
		}
		var rows int
		if err := database.QueryRow("SELECT COUNT(*) FROM auth_bootstrap_state").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 0 {
			t.Fatalf("refused resume mutated empty bootstrap state: rows=%d", rows)
		}
	})

	t.Run("epoch five dirty resume rejects malformed bootstrap state", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 4)
		migration := schemaMigrations[4]
		insertDirtyMigrationRow(t, database, migration)
		for _, statement := range authRBACTables()[:5] {
			if _, err := database.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := database.Exec("INSERT INTO auth_bootstrap_state (id) VALUES (2)"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "singleton id=1") {
			t.Fatalf("malformed bootstrap singleton problems = %v", status.ManifestDiffs)
		}
	})

	t.Run("dirty resume rejects malformed target object before ledger write", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		migration := schemaMigrations[1]
		insertDirtyMigrationRow(t, database, migration)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN enabled VARCHAR(10) NULL"); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&startedAt); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "enabled") ||
			!containsProblem(status.ManifestDiffs, "允许的精确语句边界状态") {
			t.Fatalf("malformed dirty target problems = %v", status.ManifestDiffs)
		}
		var unchanged time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&unchanged); err != nil {
			t.Fatal(err)
		}
		if !unchanged.Equal(startedAt) {
			t.Fatalf("refused dirty target changed ledger timestamp: %s -> %s", startedAt, unchanged)
		}
	})

	t.Run("dirty resume rejects an impossible skipped statement state", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 1)
		migration := schemaMigrations[1]
		insertDirtyMigrationRow(t, database, migration)
		if _, err := database.Exec("ALTER TABLE env_configs ADD COLUMN sort_order INT NOT NULL DEFAULT 0 AFTER description_cn"); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&startedAt); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "允许的精确语句边界状态") {
			t.Fatalf("skipped dirty statement state problems = %v", status.ManifestDiffs)
		}
		var unchanged time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch=2").Scan(&unchanged); err != nil {
			t.Fatal(err)
		}
		if !unchanged.Equal(startedAt) {
			t.Fatalf("refused skipped state changed ledger timestamp: %s -> %s", startedAt, unchanged)
		}
	})

	t.Run("active environment codes remain canonical after epoch two", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO env_configs (env, description_cn) VALUES ('Preview', 'Preview')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('env-contract', '环境契约', 'owner', '负责人', 'golang', 'https://example.invalid/env')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type)
			SELECT app_id, ' PROD ', 'golang' FROM apps WHERE app_name='env-contract'`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "active environment codes") {
			t.Fatalf("non-canonical active environment codes were accepted: %+v", status)
		}
	})

	t.Run("active app environments must resolve to visible catalog entries", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO env_configs (env, description_cn)
			VALUES ('preview', 'Preview')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('catalog-contract', '环境目录契约', 'owner', '负责人', 'golang', 'https://example.invalid/catalog')`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO app_configs (app_id, env, code_package_type)
			SELECT app_id, 'preview', 'golang' FROM apps WHERE app_name = 'catalog-contract'`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("UPDATE env_configs SET deleted_at = CURRENT_TIMESTAMP WHERE env = 'preview'"); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "do not resolve to visible catalog entries") {
			t.Fatalf("soft-deleted environment reference was accepted: %+v", status)
		}
	})

	t.Run("index direction and visibility are contractual", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			statement string
			want      string
		}{
			{"invisible", "ALTER TABLE task_record ALTER INDEX idx_task_workflow_poll INVISIBLE", "VISIBLE=NO"},
			{"descending", `ALTER TABLE task_record DROP INDEX idx_task_workflow_poll,
				ADD INDEX operator_desc_poll (engine_version DESC, status, deleted_at, updated_at, task_id)`, "engine_version D"},
		} {
			t.Run(test.name, func(t *testing.T) {
				dsn, _ := harness.newDatabase(t)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
					t.Fatal(err)
				}
				database := openIntegrationDatabase(t, dsn)
				if _, err := database.Exec(test.statement); err != nil {
					t.Fatal(err)
				}
				status, err := InspectSchema(ctx, dsn, 45*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if status.Compatible() || !containsProblem(status.ManifestDiffs, test.want) {
					t.Fatalf("%s index drift was accepted: %+v", test.name, status)
				}
			})
		}
	})

	t.Run("character column collation is an exact epoch contract", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE apps MODIFY app_name
			VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci NOT NULL`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() ||
			!containsProblem(status.ManifestDiffs, "列 apps.app_name 排序规则") ||
			!containsProblem(status.ManifestDiffs, "utf8mb4_0900_ai_ci") {
			t.Fatalf("app_name collation drift was accepted: %+v", status)
		}
	})

	t.Run("epoch four rejects unowned schema objects visible to migrator", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		for _, statement := range []string{
			"CREATE VIEW operator_apps AS SELECT app_id FROM apps",
			"ALTER TABLE apps ADD CONSTRAINT operator_owner CHECK (CHAR_LENGTH(owner) > 0)",
			"CREATE TRIGGER operator_apps_trigger BEFORE INSERT ON apps FOR EACH ROW SET NEW.owner = NEW.owner",
			"CREATE EVENT operator_cleanup ON SCHEDULE EVERY 1 DAY DO DELETE FROM apps WHERE 1=0",
			"CREATE PROCEDURE operator_repair() SELECT 1",
		} {
			if _, err := database.Exec(statement); err != nil {
				t.Fatalf("create schema drift %q: %v", statement, err)
			}
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(status.ManifestDiffs, "\n")
		for _, want := range []string{"operator_apps", "operator_owner", "operator_apps_trigger", "operator_cleanup", "operator_repair"} {
			if !strings.Contains(joined, want) {
				t.Errorf("schema object %q was not rejected: %s", want, joined)
			}
		}
		if status.Compatible() {
			t.Fatalf("unowned schema objects were accepted: %+v", status)
		}
	})

	t.Run("AUTO_INCREMENT contract cannot be forged through a table comment", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec("ALTER TABLE apps AUTO_INCREMENT=1, COMMENT='AUTO_INCREMENT=10000'"); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "AUTO_INCREMENT") {
			t.Fatalf("comment-forged AUTO_INCREMENT was accepted: %+v", status)
		}
		result, err := database.Exec(`INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES ('auto-increment-probe', 'probe', 'probe', 'probe', 'golang', 'https://example.invalid/probe')`)
		if err != nil {
			t.Fatal(err)
		}
		insertedID, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if insertedID >= 10000 {
			t.Fatalf("test did not lower the real AUTO_INCREMENT value: inserted id=%d", insertedID)
		}
	})

	t.Run("schema diagnostics remove terminal control characters from database identifiers", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		unsafeTableName := "operator_\x1b[31m\u202e"
		if _, err := database.Exec("CREATE TABLE `" + unsafeTableName + "` (id BIGINT PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		rendered := status.String()
		for _, character := range rendered {
			if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
				t.Fatalf("status output retains unsafe rune U+%04X: %q", character, rendered)
			}
		}
		if status.Compatible() || !strings.Contains(rendered, "operator_") {
			t.Fatalf("unsafe-name schema drift was not reported safely: %s", rendered)
		}
	})

	t.Run("migration ledger rejects attached check constraints", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE schema_migrations
			ADD CONSTRAINT operator_dirty_must_be_clean CHECK (dirty = 0)`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.Problems, "不支持的 CHECK") {
			t.Fatalf("ledger CHECK drift was accepted: %+v", status)
		}
	})

	t.Run("foreign keys must reference the current schema", func(t *testing.T) {
		_, externalDatabase := harness.newDatabase(t)
		// Register the target database cleanup after the referenced database so
		// LIFO cleanup removes the foreign key owner first.
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(fmt.Sprintf(`CREATE TABLE %s.pipelines (
			job_name VARCHAR(100) NOT NULL PRIMARY KEY
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
			"`"+externalDatabase+"`")); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(fmt.Sprintf(`ALTER TABLE pipelines_job_combination
			DROP FOREIGN KEY fk_pipelines_ci_job,
			ADD CONSTRAINT operator_cross_schema_ci FOREIGN KEY (ci_job_name)
			REFERENCES %s.pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE`,
			"`"+externalDatabase+"`")); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "EXTERNAL SCHEMA") {
			t.Fatalf("cross-schema foreign key was accepted: %+v", status)
		}
	})

	t.Run("visible external inbound foreign keys to managed tables are rejected", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		// LIFO cleanup drops the external child schema before the referenced
		// Ares schema.
		_, externalDatabase := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(fmt.Sprintf(`CREATE TABLE %s.inbound_apps (
			id BIGINT NOT NULL PRIMARY KEY,
			app_id INT NOT NULL,
			CONSTRAINT operator_inbound_apps FOREIGN KEY (app_id)
			REFERENCES %s.apps (app_id) ON DELETE RESTRICT ON UPDATE CASCADE
		) ENGINE=InnoDB`, "`"+externalDatabase+"`", "`"+databaseName+"`")); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "反向引用") ||
			!containsProblem(status.ManifestDiffs, externalDatabase) {
			t.Fatalf("external inbound foreign key was accepted: %+v", status)
		}
	})

	t.Run("interrupted bootstrap rejects malformed fixed seed before inserts", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second}
		if err := session.createMigrationLedger(); err != nil {
			t.Fatal(err)
		}
		for _, table := range schemaBootstrapTables {
			if _, err := database.Exec(table.statement); err != nil {
				t.Fatal(err)
			}
		}
		const malformed = `{"allowed":["jar"],"default":"jar"}`
		if _, err := database.Exec("INSERT INTO dev_language_rules (dev_language, rules) VALUES ('java', ?)", malformed); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !strings.Contains(err.Error(), "语义不匹配") {
			t.Fatalf("malformed bootstrap seed error = %v", err)
		}
		var count int
		var rules string
		if err := database.QueryRow("SELECT COUNT(*), MAX(CAST(rules AS CHAR)) FROM dev_language_rules").Scan(&count, &rules); err != nil {
			t.Fatal(err)
		}
		gotCanonical, canonicalErr := canonicalJSON(rules)
		wantCanonical, _ := canonicalJSON(malformed)
		if count != 1 || canonicalErr != nil || gotCanonical != wantCanonical {
			t.Fatalf("refused bootstrap changed seeds: count=%d rules=%s", count, rules)
		}
	})

	t.Run("applied epoch drift is rejected before the next dirty row", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{
			ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
		}
		if err := session.adoptLegacyLedger(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("ALTER TABLE task_step_records DROP INDEX idx_step_status_uses"); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "idx_step_status_uses") &&
			!containsProblem(status.ManifestDiffs, "普通索引") {
			t.Fatalf("epoch-3 postcondition drift = %v", status.ManifestDiffs)
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("drifted epoch-3 schema gained a dirty row: ledger count=%d", count)
		}
	})

	t.Run("earlier applied postcondition drift is rejected before the next dirty row", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		database := openIntegrationDatabase(t, dsn)
		session := &migrationSession{
			ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
		}
		if err := session.adoptLegacyLedger(); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE apps SET description_cn = 'NULL' WHERE app_id = 12345`); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "non-canonical empty values") {
			t.Fatalf("epoch-1 postcondition drift = %v", status.ManifestDiffs)
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 3 {
			t.Fatalf("earlier postcondition drift gained a dirty row: ledger count=%d", count)
		}
	})

	t.Run("dirty migration requires an exact explicit resume", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		migration := schemaMigrations[len(schemaMigrations)-1]
		if _, err := database.Exec(`UPDATE schema_migrations
			SET dirty = 1, finished_at = NULL, last_error = 'simulated interruption'
			WHERE version = ?`, migration.version); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE version = ?", migration.version).Scan(&startedAt); err != nil {
			t.Fatal(err)
		}

		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if status.Dirty == nil || status.Dirty.Version != migration.version {
			t.Fatalf("dirty status = %+v, want %s", status.Dirty, migration.version)
		}
		var unchangedStartedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE version = ?", migration.version).Scan(&unchangedStartedAt); err != nil {
			t.Fatal(err)
		}
		if !unchangedStartedAt.Equal(startedAt) {
			t.Fatalf("refusing an implicit resume changed started_at: before=%s after=%s", startedAt, unchangedStartedAt)
		}

		_, err = MigrateUp(ctx, dsn, schemaMigrations[2].version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		status, err = MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var dirty bool
		var resumedStartedAt time.Time
		var finishedAt sql.NullTime
		var lastError sql.NullString
		if err := database.QueryRow(`SELECT dirty, started_at, finished_at, last_error
			FROM schema_migrations WHERE version = ?`, migration.version).
			Scan(&dirty, &resumedStartedAt, &finishedAt, &lastError); err != nil {
			t.Fatal(err)
		}
		if dirty || !finishedAt.Valid || lastError.Valid {
			t.Fatalf("resumed ledger state = dirty:%t finished:%v error:%v", dirty, finishedAt, lastError)
		}
		if !resumedStartedAt.Equal(startedAt) {
			t.Fatalf("successful resume changed started_at: before=%s after=%s", startedAt, resumedStartedAt)
		}
	})

	t.Run("dirty resume accepts the initial crash marker with null diagnostics", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		migration := schemaMigrations[len(schemaMigrations)-1]
		if _, err := database.Exec(`UPDATE schema_migrations
			SET dirty = 1, finished_at = NULL, last_error = NULL
			WHERE version = ?`, migration.version); err != nil {
			t.Fatal(err)
		}

		status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatalf("resume initial dirty marker: %v", err)
		}
		assertCompatibleStatus(t, status)
		var dirty bool
		var finishedAt sql.NullTime
		var lastError sql.NullString
		if err := database.QueryRow(`SELECT dirty, finished_at, last_error
			FROM schema_migrations WHERE version = ?`, migration.version).
			Scan(&dirty, &finishedAt, &lastError); err != nil {
			t.Fatal(err)
		}
		if dirty || !finishedAt.Valid || lastError.Valid {
			t.Fatalf("resumed initial dirty marker = dirty:%t finished:%v error:%v", dirty, finishedAt, lastError)
		}
	})

	t.Run("failed migration returns refreshed dirty status", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)

		original := schemaMigrations[3]
		t.Cleanup(func() { schemaMigrations[3] = original })
		schemaMigrations[3].up = func(*migrationSession) error {
			return errors.New("password=do-not-print forced migration failure")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err == nil || errors.Is(err, ErrSchemaState) {
			t.Fatalf("forced migration error = %v, want operational failure", err)
		}
		if strings.Contains(err.Error(), "do-not-print") {
			t.Fatalf("forced migration error leaked secret: %v", err)
		}
		if status.Dirty == nil || status.Dirty.Epoch != 4 || status.Dirty.Version != versionedSchemaMigrationVersion {
			t.Fatalf("returned status did not refresh dirty epoch 4: %+v", status)
		}
		if strings.Contains(status.Dirty.Error, "do-not-print") || !strings.Contains(status.Dirty.Error, "password=<redacted>") {
			t.Fatalf("returned dirty error was not sanitized: %q", status.Dirty.Error)
		}
	})

	t.Run("dirty resume validates all completed predecessors before writes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		database := migrateDatabaseToEpoch(t, dsn, 2)
		if _, err := database.Exec(`UPDATE schema_migrations
			SET dirty = 1, finished_at = NULL, last_error = 'simulated epoch-2 interruption'
			WHERE epoch = 2`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO apps
			(app_id, app_name, app_name_cn, owner, owner_cn, dev_language, description_cn, git_url)
			VALUES (10000, 'dirty-resume', 'Dirty Resume', 'owner', '负责人', 'golang', 'NULL', 'https://example.invalid/repo')`); err != nil {
			t.Fatal(err)
		}
		var startedAt time.Time
		if err := database.QueryRow("SELECT started_at FROM schema_migrations WHERE epoch = 2").Scan(&startedAt); err != nil {
			t.Fatal(err)
		}

		status, err := MigrateUp(ctx, dsn, schemaMigrations[1].version, 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.ManifestDiffs, "non-canonical empty values") {
			t.Fatalf("dirty predecessor drift = %v", status.ManifestDiffs)
		}
		var count int
		var dirty bool
		var unchangedStartedAt time.Time
		if err := database.QueryRow(`SELECT COUNT(*),
			MAX(epoch = 2 AND dirty = 1),
			MAX(CASE WHEN epoch = 2 THEN started_at END)
			FROM schema_migrations`).Scan(&count, &dirty, &unchangedStartedAt); err != nil {
			t.Fatal(err)
		}
		if count != 2 || !dirty || !unchangedStartedAt.Equal(startedAt) {
			t.Fatalf("refused dirty resume changed ledger: count=%d dirty=%t started=%s want=%s",
				count, dirty, unchangedStartedAt, startedAt)
		}
	})

	t.Run("checksum mismatch fails closed", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		const tamperedChecksum = "0000000000000000000000000000000000000000000000000000000000000000"
		if _, err := database.Exec("UPDATE schema_migrations SET checksum = ? WHERE epoch = 2", tamperedChecksum); err != nil {
			t.Fatal(err)
		}
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "checksum") {
			t.Fatalf("checksum mismatch status does not explain the problem: %+v", status)
		}
		var checksum string
		if err := database.QueryRow("SELECT checksum FROM schema_migrations WHERE epoch = 2").Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		if checksum != tamperedChecksum {
			t.Fatalf("fail-closed migration rewrote a tampered checksum: %s", checksum)
		}
	})

	t.Run("compatibility metadata mismatch fails closed", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`UPDATE schema_migrations
			SET compatible_min = 1, compatible_max = 99 WHERE epoch = 4`); err != nil {
			t.Fatal(err)
		}
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "兼容区间不匹配") {
			t.Fatalf("compatibility metadata mismatch is not explained: %+v", status)
		}
		var minimum, maximum uint64
		if err := database.QueryRow(`SELECT compatible_min, compatible_max
			FROM schema_migrations WHERE epoch = 4`).Scan(&minimum, &maximum); err != nil {
			t.Fatal(err)
		}
		if minimum != 1 || maximum != 99 {
			t.Fatalf("fail-closed migration rewrote compatibility metadata: [%d,%d]", minimum, maximum)
		}
	})

	t.Run("latest epoch retains earlier data invariants", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		loadSQLFixture(t, dsn, preW04FixturePath)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`UPDATE apps SET description_cn = 'NULL' WHERE app_id = 12345`); err != nil {
			t.Fatal(err)
		}

		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "non-canonical empty values") {
			t.Fatalf("latest epoch accepted an earlier data-invariant drift: %+v", status)
		}
	})

	t.Run("unknown epoch fails closed", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`INSERT INTO schema_migrations
			(version, epoch, description, checksum, dirty, started_at, finished_at,
			 compatible_min, compatible_max, last_error, legacy_adopted)
			VALUES ('20990101_001_unknown', ?, 'unknown future migration',
			'1111111111111111111111111111111111111111111111111111111111111111',
			0, NOW(6), NOW(6), ?, ?, NULL, 0)`,
			ApplicationSchemaEpoch+1, ApplicationSchemaEpoch+1, ApplicationSchemaEpoch+1); err != nil {
			t.Fatal(err)
		}
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		assertSchemaStateError(t, err)
		if !containsProblem(status.Problems, "未知 epoch") {
			t.Fatalf("unknown epoch status does not explain the problem: %+v", status)
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != len(schemaMigrations)+1 {
			t.Fatalf("fail-closed migration changed unknown ledger rows: got %d", count)
		}
	})

	t.Run("current manifest distinguishes primary keys from unique indexes", func(t *testing.T) {
		dsn, _ := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}
		database := openIntegrationDatabase(t, dsn)
		if _, err := database.Exec(`ALTER TABLE apps
			DROP PRIMARY KEY, ADD UNIQUE INDEX operator_app_id_unique (app_id)`); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(ctx, dsn, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !containsProblem(status.ManifestDiffs, "缺少主键") {
			t.Fatalf("primary-key drift was not rejected: %+v", status)
		}
	})

	t.Run("concurrent migrators converge without duplicate ledger rows", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		lockConnection, err := database.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer lockConnection.Close()
		lockName := integrationMigrationLockName(databaseName)
		var acquired int
		if err := lockConnection.QueryRowContext(context.Background(), "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if acquired != 1 {
			t.Fatalf("precondition lock acquisition = %d, want 1", acquired)
		}

		temporaryDirectory := t.TempDir()
		startFile := filepath.Join(temporaryDirectory, "start")
		commands := make([]*exec.Cmd, 0, 2)
		outputs := make([]bytes.Buffer, 2)
		for index := range 2 {
			readyFile := filepath.Join(temporaryDirectory, fmt.Sprintf("ready-%d", index))
			command := exec.Command(os.Args[0], "-test.run=^TestMigrationProcessHelper$")
			command.Env = append(os.Environ(),
				migrationProcessHelperEnv+"=1",
				"ARES_TEST_MIGRATION_PROCESS_DSN="+dsn,
				"ARES_TEST_MIGRATION_PROCESS_READY="+readyFile,
				"ARES_TEST_MIGRATION_PROCESS_START="+startFile,
			)
			command.Stdout = &outputs[index]
			command.Stderr = &outputs[index]
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			commands = append(commands, command)
		}
		deadline := time.Now().Add(15 * time.Second)
		for index := range 2 {
			readyFile := filepath.Join(temporaryDirectory, fmt.Sprintf("ready-%d", index))
			for {
				if _, err := os.Stat(readyFile); err == nil {
					break
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if time.Now().After(deadline) {
					t.Fatalf("migration helper %d did not become ready: %s", index, outputs[index].String())
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if err := os.WriteFile(startFile, []byte("start\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Both independent processes have completed driver startup and receive the
		// same start signal while this connection still owns the database lock.
		time.Sleep(250 * time.Millisecond)
		var released int
		if err := lockConnection.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released); err != nil {
			t.Fatal(err)
		}
		if released != 1 {
			t.Fatalf("precondition lock release = %d, want 1", released)
		}
		for index, command := range commands {
			if err := command.Wait(); err != nil {
				t.Fatalf("migration helper %d failed: %v\n%s", index, err, outputs[index].String())
			}
		}
		if got := len(readLedgerStamps(t, database)); got != len(schemaMigrations) {
			t.Fatalf("concurrent migration ledger rows = %d, want %d", got, len(schemaMigrations))
		}
	})

	t.Run("lock timeout is operational and writes no ledger", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		database := openIntegrationDatabase(t, dsn)
		lockConnection, err := database.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer lockConnection.Close()
		lockName := integrationMigrationLockName(databaseName)
		var acquired int
		if err := lockConnection.QueryRowContext(context.Background(), "SELECT GET_LOCK(?, 0)", lockName).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		if acquired != 1 {
			t.Fatalf("precondition lock acquisition = %d, want 1", acquired)
		}
		defer func() {
			var released sql.NullInt64
			_ = lockConnection.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", lockName).Scan(&released)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		started := time.Now()
		_, err = MigrateUp(ctx, dsn, "", 45*time.Second, 100*time.Millisecond)
		elapsed := time.Since(started)
		if err == nil || !strings.Contains(err.Error(), "timed out waiting for database migration lock") {
			t.Fatalf("lock timeout error = %v", err)
		}
		if elapsed < 75*time.Millisecond || elapsed > time.Second {
			t.Fatalf("lock timeout elapsed %s, want configured sub-second deadline", elapsed)
		}
		if errors.Is(err, ErrSchemaState) {
			t.Fatalf("lock timeout was classified as a schema state error: %v", err)
		}
		if got := harness.tableCount(t, databaseName); got != 0 {
			t.Fatalf("lock timeout created %d tables, want 0", got)
		}
	})

	t.Run("runtime account can inspect but cannot execute DDL", func(t *testing.T) {
		dsn, databaseName := harness.newDatabase(t)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second); err != nil {
			t.Fatal(err)
		}

		runtimeDSN := harness.newRuntimeUser(t, dsn, databaseName)
		status, err := InspectSchema(ctx, runtimeDSN, 45*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		database := openIntegrationDatabase(t, runtimeDSN)
		if err := CheckRuntimeCompatibility(ctx, database); err != nil {
			t.Fatalf("runtime compatibility check needs only read privileges: %v", err)
		}
		runtimeConfig, err := mysql.ParseDSN(runtimeDSN)
		if err != nil {
			t.Fatal(err)
		}
		var legacyPipelineGrantRows int
		if err := harness.admin.QueryRowContext(ctx, `SELECT COUNT(*) FROM mysql.tables_priv
			WHERE BINARY User = BINARY ? AND Host = '%' AND BINARY Db = BINARY ?
				AND Table_name IN ('pipelines', 'pipelines_job_combination')`,
			runtimeConfig.User, databaseName).Scan(&legacyPipelineGrantRows); err != nil {
			t.Fatal(err)
		}
		if legacyPipelineGrantRows != 0 {
			t.Fatalf("runtime account has %d unexpected table-level grants on read-only legacy pipeline tables",
				legacyPipelineGrantRows)
		}
		if _, err := database.Exec(`INSERT INTO integration_settings (provider, config_data)
			VALUES ('integration-test', '{"enabled":true}')`); err != nil {
			t.Fatalf("runtime account cannot perform normal business DML: %v", err)
		}
		userResult, err := database.Exec(`INSERT INTO auth_users
			(username, display_name, auth_source)
			VALUES ('runtime-auth-test', 'Runtime Auth Test', 'bootstrap')`)
		if err != nil {
			t.Fatalf("runtime account cannot insert auth user: %v", err)
		}
		userID, err := userResult.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`UPDATE auth_users SET last_login_at = NOW(6) WHERE user_id = ?`, userID); err != nil {
			t.Fatalf("runtime account cannot update auth user: %v", err)
		}
		if _, err := database.Exec(`INSERT INTO auth_identities
			(user_id, issuer, subject, identity_hash)
			VALUES (?, 'https://issuer.example.invalid', 'subject', UNHEX(REPEAT('01', 32)))`, userID); err != nil {
			t.Fatalf("runtime account cannot insert OIDC identity: %v", err)
		}
		if _, err := database.Exec(`INSERT INTO auth_sessions
			(session_hash, user_id, expires_at, last_seen_at)
			VALUES (UNHEX(REPEAT('02', 32)), ?, NOW(6) + INTERVAL 1 HOUR, NOW(6))`, userID); err != nil {
			t.Fatalf("runtime account cannot insert session: %v", err)
		}
		if _, err := database.Exec(`DELETE FROM auth_sessions
			WHERE session_hash = UNHEX(REPEAT('02', 32))`); err != nil {
			t.Fatalf("runtime account cannot delete expired session: %v", err)
		}
		if _, err := database.Exec(`INSERT INTO auth_oidc_flows
			(state_hash, nonce_hash, binding_hash, verifier_ciphertext, expires_at)
			VALUES (UNHEX(REPEAT('03', 32)), UNHEX(REPEAT('04', 32)),
				UNHEX(REPEAT('05', 32)), 'encrypted-verifier', NOW(6) + INTERVAL 5 MINUTE)`); err != nil {
			t.Fatalf("runtime account cannot insert OIDC flow: %v", err)
		}
		if _, err := database.Exec(`DELETE FROM auth_oidc_flows
			WHERE state_hash = UNHEX(REPEAT('03', 32))`); err != nil {
			t.Fatalf("runtime account cannot delete consumed OIDC flow: %v", err)
		}
		if _, err := database.Exec(`UPDATE auth_bootstrap_state SET completed_at = completed_at WHERE id = 1`); err != nil {
			t.Fatalf("runtime account cannot atomically update bootstrap singleton: %v", err)
		}
		if _, err := database.Exec(`INSERT INTO audit_events
			(actor_user_id, actor_username, actor_display_name, auth_source, action,
			 resource_type, resource_id, result, http_status, request_id)
			VALUES (?, 'runtime-auth-test', 'Runtime Auth Test', 'bootstrap',
				'integration.test', 'database', 'runtime', 'succeeded', 200, 'integration-request')`, userID); err != nil {
			t.Fatalf("runtime account cannot append audit event: %v", err)
		}
		for _, forbidden := range []string{
			`INSERT INTO pipelines (job_name, description_cn, url)
				VALUES ('runtime-must-not-write', 'forbidden', 'https://example.invalid/forbidden')`,
			"UPDATE pipelines_job_combination SET description_cn = description_cn WHERE id = 0",
			"DELETE FROM auth_users WHERE user_id = 0",
			"UPDATE auth_identities SET subject = subject WHERE identity_id = 0",
			"DELETE FROM auth_identities WHERE identity_id = 0",
			"INSERT INTO auth_bootstrap_state (id) VALUES (2)",
			"DELETE FROM auth_bootstrap_state WHERE id = 0",
			"UPDATE audit_events SET result = result WHERE audit_id = 0",
			"DELETE FROM audit_events WHERE audit_id = 0",
		} {
			_, err := database.Exec(forbidden)
			if err == nil {
				t.Fatalf("runtime account unexpectedly executed forbidden statement %q", forbidden)
			}
			var denied *mysql.MySQLError
			if !errors.As(err, &denied) || denied.Number != 1142 {
				t.Fatalf("forbidden statement %q error = %v, want MySQL command-denied 1142", forbidden, err)
			}
		}
		_, err = database.Exec("CREATE TABLE runtime_must_not_create_tables (id INT PRIMARY KEY)")
		if err == nil {
			t.Fatal("runtime account unexpectedly executed DDL")
		}
		var permissionError *mysql.MySQLError
		if !errors.As(err, &permissionError) || permissionError.Number != 1142 {
			t.Fatalf("runtime DDL error = %v, want MySQL command-denied error 1142", err)
		}
		_, err = database.Exec("UPDATE schema_migrations SET last_error = 'tampered' WHERE epoch = 4")
		if err == nil {
			t.Fatal("runtime account unexpectedly modified the migration ledger")
		}
		if !errors.As(err, &permissionError) || permissionError.Number != 1142 {
			t.Fatalf("runtime ledger write error = %v, want MySQL command-denied error 1142", err)
		}
	})
}

type mysqlIntegrationHarness struct {
	admin       *sql.DB
	adminConfig *mysql.Config
	next        int
}

func newMySQLIntegrationHarness(t *testing.T) *mysqlIntegrationHarness {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(mysqlIntegrationDSNEnv))
	if dsn == "" {
		t.Skipf("set %s to a MySQL 8.4 administrative DSN to run integration tests", mysqlIntegrationDSNEnv)
	}
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", mysqlIntegrationDSNEnv, err)
	}
	configuration.ParseTime = true
	admin, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		t.Fatalf("connect to MySQL integration server: %v", err)
	}
	var version string
	if err := admin.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "8.4.") {
		t.Fatalf("integration server version = %q, want MySQL 8.4.x", version)
	}
	return &mysqlIntegrationHarness{admin: admin, adminConfig: configuration}
}

func (h *mysqlIntegrationHarness) newDatabase(t *testing.T) (string, string) {
	t.Helper()
	h.next++
	databaseName := fmt.Sprintf("ares_w04_it_%d_%d", time.Now().UnixNano(), h.next)
	if !safeSQLIdentifier(databaseName) {
		t.Fatalf("generated unsafe database name %q", databaseName)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
		"CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", databaseName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := h.admin.ExecContext(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", databaseName)); err != nil {
			t.Errorf("drop integration database %s: %v", databaseName, err)
		}
	})
	target := h.adminConfig.Clone()
	target.DBName = databaseName
	target.ParseTime = true
	return target.FormatDSN(), databaseName
}

func (h *mysqlIntegrationHarness) tableCount(t *testing.T, databaseName string) int {
	t.Helper()
	var count int
	if err := h.admin.QueryRow(`SELECT COUNT(*) FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'`, databaseName).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (h *mysqlIntegrationHarness) newRuntimeUser(t *testing.T, targetDSN, databaseName string) string {
	t.Helper()
	h.next++
	username := fmt.Sprintf("aresrt_%x", time.Now().UnixNano()+int64(h.next))
	password := fmt.Sprintf("Ares-W04-%x", time.Now().UnixNano())
	account := sqlStringLiteral(username) + "@'%'"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.admin.ExecContext(ctx, "CREATE USER "+account+" IDENTIFIED BY "+sqlStringLiteral(password)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := h.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+account); err != nil {
			t.Errorf("drop integration runtime user %s: %v", username, err)
		}
	})
	var partialRevokes int
	if err := h.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
		t.Fatal(err)
	}
	grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
	if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
		"GRANT SELECT ON `%s`.* TO %s", grantPattern, account)); err != nil {
		t.Fatal(err)
	}
	for _, tableName := range sortedStringKeys(epoch5SemanticSchemaManifest.tables) {
		privileges := expectedRuntimeDMLPrivileges(tableName)
		if privileges == "" {
			continue
		}
		if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
			"GRANT %s ON `%s`.`%s` TO %s", privileges, databaseName, tableName, account)); err != nil {
			t.Fatalf("grant runtime DML on %s: %v", tableName, err)
		}
	}
	configuration, err := mysql.ParseDSN(targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	configuration.User = username
	configuration.Passwd = password
	configuration.ParseTime = true
	return configuration.FormatDSN()
}

func (h *mysqlIntegrationHarness) newGuardedMigrationUser(t *testing.T, targetDSN, databaseName string) (string, string) {
	t.Helper()
	h.next++
	username := fmt.Sprintf("aresmig_%x", time.Now().UnixNano()+int64(h.next))
	password := fmt.Sprintf("Aa1!AresW04%x", time.Now().UnixNano())
	account := sqlStringLiteral(username) + "@'%'"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.admin.ExecContext(ctx, "CREATE USER "+account+" IDENTIFIED BY "+sqlStringLiteral(password)+" ACCOUNT LOCK"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := h.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+account); err != nil {
			t.Errorf("drop integration migration user %s: %v", username, err)
		}
	})
	var partialRevokes int
	if err := h.admin.QueryRowContext(ctx, "SELECT @@GLOBAL.partial_revokes").Scan(&partialRevokes); err != nil {
		t.Fatal(err)
	}
	grantPattern := guardedMigrationDatabaseGrantPattern(databaseName, partialRevokes == 1)
	if _, err := h.admin.ExecContext(ctx, fmt.Sprintf(
		"GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES ON `%s`.* TO %s",
		grantPattern, account)); err != nil {
		t.Fatal(err)
	}
	configuration, err := mysql.ParseDSN(targetDSN)
	if err != nil {
		t.Fatal(err)
	}
	configuration.User = username
	configuration.Passwd = password
	configuration.ParseTime = true
	return configuration.FormatDSN(), username
}

func (h *mysqlIntegrationHarness) assertGuardedMigrationAccount(t *testing.T, username string, wantLocked bool, wantSessions int) {
	t.Helper()
	var locked string
	if err := h.admin.QueryRow(
		"SELECT account_locked FROM mysql.user WHERE BINARY User = BINARY ? AND Host = '%'", username).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	wantLockState := "N"
	if wantLocked {
		wantLockState = "Y"
	}
	if locked != wantLockState {
		t.Fatalf("migration account %s lock state = %s, want %s", username, locked, wantLockState)
	}
	var sessions int
	if err := h.admin.QueryRow(
		"SELECT COUNT(*) FROM information_schema.PROCESSLIST WHERE BINARY USER = BINARY ?", username).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != wantSessions {
		t.Fatalf("migration account %s session count = %d, want %d", username, sessions, wantSessions)
	}
}

func assertIntegrationDSNCannotConnect(t *testing.T, dsn string) {
	t.Helper()
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		return
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err == nil {
		t.Fatal("guarded migration credential unexpectedly established a new connection")
	}
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func integrationMigrationLockName(databaseName string) string {
	digest := sha256.Sum256([]byte(databaseName))
	return "ares_schema_migration_" + hex.EncodeToString(digest[:16])
}

func openIntegrationDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return database
}

func migrateDatabaseToEpoch(t *testing.T, dsn string, epoch uint64) *sql.DB {
	t.Helper()
	if epoch == 0 || epoch > uint64(len(schemaMigrations)) {
		t.Fatalf("invalid test epoch %d", epoch)
	}
	database := openIntegrationDatabase(t, dsn)
	session := &migrationSession{
		ctx: context.Background(), executor: database, operationTimeout: 45 * time.Second,
	}
	if err := session.createMigrationLedger(); err != nil {
		t.Fatal(err)
	}
	if err := session.bootstrapEmptySchema(); err != nil {
		t.Fatal(err)
	}
	for index := uint64(0); index < epoch; index++ {
		if err := session.executeDirtyMigration(schemaMigrations[index], false); err != nil {
			t.Fatalf("migrate test database to epoch %d: %v", index+1, err)
		}
	}
	status, err := inspectSchema(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Compatible() && len(status.Pending) == 0 {
		t.Fatalf("test database epoch %d is not a valid prefix: %+v", epoch, status)
	}
	if status.CurrentEpoch != epoch || len(status.ManifestDiffs) > 0 || len(status.Problems) > 0 {
		t.Fatalf("test database epoch %d status = %+v", epoch, status)
	}
	return database
}

func insertDirtyMigrationRow(t *testing.T, database *sql.DB, migration schemaMigration) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO schema_migrations
		(version, epoch, description, checksum, dirty, started_at, finished_at,
		 compatible_min, compatible_max, last_error, legacy_adopted)
		VALUES (?, ?, ?, ?, 1, NOW(6), NULL, ?, ?, 'simulated interruption', 0)`,
		migration.version, migration.epoch, migration.description, migration.checksum(),
		migration.compatibleMin, migration.compatibleMax); err != nil {
		t.Fatalf("insert dirty migration %s: %v", migration.version, err)
	}
}

func convertLedgerToLegacy(t *testing.T, database *sql.DB) {
	t.Helper()
	statement := `ALTER TABLE schema_migrations
		DROP INDEX uq_schema_migrations_epoch,
		DROP COLUMN epoch,
		DROP COLUMN description,
		DROP COLUMN checksum,
		DROP COLUMN dirty,
		DROP COLUMN started_at,
		DROP COLUMN finished_at,
		DROP COLUMN compatible_min,
		DROP COLUMN compatible_max,
		DROP COLUMN last_error,
		DROP COLUMN legacy_adopted`
	if _, err := database.Exec(statement); err != nil {
		t.Fatalf("convert migration ledger to legacy shape: %v", err)
	}
}

func loadSQLFixture(t *testing.T, dsn, path string) {
	t.Helper()
	content, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	configuration.MultiStatements = true
	configuration.ParseTime = true
	database := openIntegrationDatabase(t, configuration.FormatDSN())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := database.ExecContext(ctx, string(content)); err != nil {
		t.Fatalf("load fixture %s: %v", path, err)
	}
}

type ledgerStamp struct {
	Version    string
	Checksum   string
	Dirty      bool
	StartedAt  time.Time
	FinishedAt sql.NullTime
	AppliedAt  time.Time
}

func readLedgerStamps(t *testing.T, database *sql.DB) []ledgerStamp {
	t.Helper()
	rows, err := database.Query(`SELECT version, checksum, dirty, started_at, finished_at, applied_at
		FROM schema_migrations ORDER BY epoch`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]ledgerStamp, 0, len(schemaMigrations))
	for rows.Next() {
		var stamp ledgerStamp
		if err := rows.Scan(&stamp.Version, &stamp.Checksum, &stamp.Dirty, &stamp.StartedAt, &stamp.FinishedAt, &stamp.AppliedAt); err != nil {
			t.Fatal(err)
		}
		result = append(result, stamp)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func ledgerColumnNames(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query(`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'
		ORDER BY ORDINAL_POSITION`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertLegacyLedgerUnchanged(t *testing.T, database *sql.DB, wantRows int) {
	t.Helper()
	var columns, rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if columns != 2 || rows != wantRows {
		t.Fatalf("refused legacy adoption changed ledger: columns=%d rows=%d, want columns=2 rows=%d", columns, rows, wantRows)
	}
}

func assertCompatibleStatus(t *testing.T, status MigrationStatus) {
	t.Helper()
	if !status.Compatible() || status.CurrentEpoch != ApplicationSchemaEpoch || len(status.Applied) != len(schemaMigrations) {
		t.Fatalf("migration status is not current and compatible: %+v", status)
	}
}

func assertSchemaStateError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a fail-closed schema state error")
	}
	if !errors.Is(err, ErrSchemaState) {
		t.Fatalf("error = %v, want ErrSchemaState", err)
	}
}

func containsProblem(problems []string, fragment string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, fragment) {
			return true
		}
	}
	return false
}
