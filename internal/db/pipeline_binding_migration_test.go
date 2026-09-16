package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/canonicaljson"
)

func TestBindingMigrationKeepsHistoricalEpochsImmutable(t *testing.T) {
	states := epoch10ResumeSchemaStates()
	if len(states) != 3 || len(epoch10SemanticSchemaManifest.tables) != 28 {
		t.Fatal("incorrect epoch 10 boundaries")
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[0], epoch9SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatal(diffs)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[2], epoch10SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatal(diffs)
	}
	for _, table := range pipelineBindingTables {
		if _, ok := epoch9SemanticSchemaManifest.tables[table.name]; ok {
			t.Fatal("mutated epoch 9")
		}
	}
}

const deploymentTestSpec = `{"schema_version":1,"kind":"cd","name":"示例部署","target_type":"kubernetes","inputs":{"image":{"kind":"oci_image","media_type":"application/vnd.oci.image.manifest.v1+json","simulated":true}},"steps":[{"key":"deploy","uses":"demo.deploy@v1","inputs":{"image":{"from":"inputs.image","type":{"kind":"oci_image","media_type":"application/vnd.oci.image.manifest.v1+json","simulated":true}}}}]}`

func insertReturningID(t *testing.T, database *sql.DB, query string, arguments ...any) int64 {
	t.Helper()
	result, err := database.Exec(query, arguments...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func insertTemplateVersion(t *testing.T, database *sql.DB, key, kind, applicationType, targetType, spec string) int64 {
	t.Helper()
	templateID := insertReturningID(t, database, `INSERT INTO pipeline_templates
		(template_key,kind,application_type,target_type,revision,draft,created_by_user_id)
		VALUES (?,?,NULLIF(?,''),NULLIF(?,''),2,?,1)`, key, kind, applicationType, targetType, spec)
	canonical, err := canonicaljson.Canonicalize([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	return insertReturningID(t, database, `INSERT INTO pipeline_template_versions
		(template_id,version_number,source_revision,spec,checksum,created_by_user_id) VALUES (?,1,1,?,?,1)`,
		templateID, spec, hex.EncodeToString(sum[:]))
}

// seedBindingTargets creates one Java CI version and one Kubernetes CD version
// plus the app, environment and configuration a binding may point at. It never
// creates a binding itself: every test chooses its own binding state.
func seedBindingTargets(t *testing.T, database *sql.DB) (int64, int64, int64, int64) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO env_configs (env, description_cn, enabled)
		SELECT 'dev','绑定测试环境',1 WHERE NOT EXISTS (SELECT 1 FROM env_configs WHERE env='dev')`); err != nil {
		t.Fatal(err)
	}
	appID := insertReturningID(t, database, `INSERT INTO apps
		(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
		VALUES ('binding-app','绑定应用','integration-test','集成测试','java','https://example.invalid/binding-app')`)
	configID := insertReturningID(t, database, `INSERT INTO app_configs (app_id, env, code_package_type)
		VALUES (?, 'dev', 'java')`, appID)
	ciVersion := insertTemplateVersion(t, database, "binding-ci", "ci", "java", "", templateTestSpec)
	cdVersion := insertTemplateVersion(t, database, "binding-cd", "cd", "", "kubernetes", deploymentTestSpec)
	return appID, configID, ciVersion, cdVersion
}

func insertCIBinding(t *testing.T, database *sql.DB, appID, versionID int64) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO application_ci_bindings
		(app_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
		VALUES (?, ?, JSON_OBJECT(), 1, 1, 1)`, appID, versionID); err != nil {
		t.Fatal(err)
	}
}

func insertCDBinding(t *testing.T, database *sql.DB, configID, versionID int64) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO app_config_cd_bindings
		(config_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
		VALUES (?, ?, JSON_OBJECT(), 1, 1, 1)`, configID, versionID); err != nil {
		t.Fatal(err)
	}
}

func TestMySQL84PipelineBindings(t *testing.T) {
	h := newMySQLIntegrationHarness(t)
	t.Run("dirty-boundary-resume", func(t *testing.T) {
		for boundary := 0; boundary <= len(pipelineBindingTables); boundary++ {
			t.Run(fmt.Sprintf("boundary-%d", boundary), func(t *testing.T) {
				dsn, _ := h.newDatabase(t)
				database := migrateDatabaseToEpoch(t, dsn, 9)
				insertDirtyMigrationRow(t, database, schemaMigrations[9])
				for i := 0; i < boundary; i++ {
					if _, err := database.Exec(pipelineBindingTables[i].ddl); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				status, err := MigrateUp(ctx, dsn, pipelineBindingMigrationVersion, 45*time.Second, 10*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				assertCompatibleStatus(t, status)
				// A resumed migration must not invent bindings for existing apps.
				var count int
				if err := database.QueryRow("SELECT COUNT(*) FROM application_ci_bindings").Scan(&count); err != nil || count != 0 {
					t.Fatalf("resume created bindings: %d %v", count, err)
				}
			})
		}
	})
	t.Run("binding-rows-survive-later-migrations", func(t *testing.T) {
		dsn, _ := h.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 10)
		appID, configID, ciVersion, cdVersion := seedBindingTargets(t, database)
		if _, err := database.Exec(`INSERT INTO application_ci_bindings
			(app_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
			VALUES (?, ?, JSON_OBJECT('repo','https://example.invalid/repo.git'), 1, 1, 1)`, appID, ciVersion); err != nil {
			t.Fatal(err)
		}
		insertCDBinding(t, database, configID, cdVersion)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		status, err := MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		assertCompatibleStatus(t, status)
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM application_ci_bindings").Scan(&count); err != nil || count != 1 {
			t.Fatalf("binding lost: %d %v", count, err)
		}
	})
	t.Run("data-corruption-fails-closed", func(t *testing.T) {
		cases := []string{
			"UPDATE application_ci_bindings SET revision=0",
			"UPDATE application_ci_bindings SET enabled=2",
			"UPDATE application_ci_bindings SET created_by_user_id=0",
			"UPDATE application_ci_bindings SET updated_by_user_id=0",
			"UPDATE application_ci_bindings SET parameters=JSON_ARRAY(1)",
			"UPDATE application_ci_bindings SET parameters=JSON_OBJECT('nested',JSON_OBJECT('deep','private-marker'))",
			"UPDATE application_ci_bindings SET parameters=JSON_OBJECT('items',JSON_ARRAY('private-marker'))",
			"UPDATE application_ci_bindings SET version_id=(SELECT version_id FROM pipeline_template_versions WHERE spec->>'$.kind'='cd')",
			"UPDATE app_config_cd_bindings SET revision=0",
			"UPDATE app_config_cd_bindings SET enabled=2",
			"UPDATE app_config_cd_bindings SET parameters=JSON_OBJECT('items',JSON_ARRAY(1))",
			"UPDATE app_config_cd_bindings SET version_id=(SELECT version_id FROM pipeline_template_versions WHERE spec->>'$.kind'='ci')",
		}
		for i, query := range cases {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				dsn, _ := h.newDatabase(t)
				database := migrateDatabaseToEpoch(t, dsn, 10)
				appID, configID, ciVersion, cdVersion := seedBindingTargets(t, database)
				insertCIBinding(t, database, appID, ciVersion)
				insertCDBinding(t, database, configID, cdVersion)
				if _, err := database.Exec(query); err != nil {
					t.Fatal(err)
				}
				status, err := InspectSchema(context.Background(), dsn, 30*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if status.Compatible() || !strings.Contains(status.String(), "CI/CD 绑定存储数据") {
					t.Fatalf("accepted corrupt data: %s", status.String())
				}
				if strings.Contains(status.String(), "private-marker") {
					t.Fatal("leaked binding content")
				}
			})
		}
	})
	t.Run("runtime-cannot-delete-or-fake-bindings", func(t *testing.T) {
		dsn, name := h.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 10)
		appID, _, ciVersion, cdVersion := seedBindingTargets(t, database)
		runtime := openIntegrationDatabase(t, h.newRuntimeUser(t, dsn, name))
		if _, err := runtime.Exec(`INSERT INTO application_ci_bindings
			(app_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
			VALUES (?, ?, JSON_OBJECT('profile','release'), 1, 1, 1)`, appID, ciVersion); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Exec(`UPDATE application_ci_bindings
			SET parameters=JSON_OBJECT('profile','debug'), revision=revision+1, updated_by_user_id=1 WHERE app_id=?`, appID); err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{
			"DELETE FROM application_ci_bindings",
			"DELETE FROM app_config_cd_bindings",
			"UPDATE pipeline_template_versions SET checksum=REPEAT('a',64)",
		} {
			if _, err := runtime.Exec(query); err == nil {
				t.Fatalf("runtime accepted forbidden statement %s", query)
			}
		}
		// A kind-crossing rebind cannot be blocked by a foreign key without
		// altering the frozen epoch 9 version table, so the binding store rejects
		// it on write and this contract catches a direct DML write. Proving both
		// layers here is why the binding table keeps no denormalized kind column.
		if _, err := runtime.Exec(`UPDATE application_ci_bindings
			SET version_id=?, revision=revision+1, updated_by_user_id=1 WHERE app_id=?`, cdVersion, appID); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSchema(context.Background(), dsn, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if status.Compatible() || !strings.Contains(status.String(), "CI/CD 绑定存储数据") {
			t.Fatalf("kind-crossing binding passed the data contract: %s", status.String())
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM application_ci_bindings").Scan(&count); err != nil || count != 1 {
			t.Fatalf("binding lost: %d %v", count, err)
		}
	})
	t.Run("binding-target-foreign-keys-are-enforced", func(t *testing.T) {
		dsn, _ := h.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 10)
		appID, _, ciVersion, _ := seedBindingTargets(t, database)
		if _, err := database.Exec(`INSERT INTO application_ci_bindings
			(app_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
			VALUES (?, ?, JSON_OBJECT(), 1, 1, 1)`, appID, ciVersion+1000); err == nil {
			t.Fatal("accepted a binding to an unknown version")
		}
		if _, err := database.Exec(`INSERT INTO application_ci_bindings
			(app_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
			VALUES (?, ?, JSON_OBJECT(), 1, 1, 1)`, appID+1000, ciVersion); err == nil {
			t.Fatal("accepted a binding to an unknown application")
		}
	})
	t.Run("one-active-binding-per-target", func(t *testing.T) {
		dsn, _ := h.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 10)
		appID, configID, ciVersion, cdVersion := seedBindingTargets(t, database)
		insertCIBinding(t, database, appID, ciVersion)
		if _, err := database.Exec(`INSERT INTO application_ci_bindings
			(app_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
			VALUES (?, ?, JSON_OBJECT(), 1, 1, 1)`, appID, ciVersion); err == nil {
			t.Fatal("accepted two CI bindings for one application")
		}
		insertCDBinding(t, database, configID, cdVersion)
		if _, err := database.Exec(`INSERT INTO app_config_cd_bindings
			(config_id, version_id, parameters, revision, created_by_user_id, updated_by_user_id)
			VALUES (?, ?, JSON_OBJECT(), 1, 1, 1)`, configID, cdVersion); err == nil {
			t.Fatal("accepted two CD bindings for one application configuration")
		}
	})
}
