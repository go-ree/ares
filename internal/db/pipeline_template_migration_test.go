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

func TestTemplateMigrationKeepsHistoricalEpochsImmutable(t *testing.T) {
	states := epoch9ResumeSchemaStates()
	if len(states) != 4 || len(epoch9SemanticSchemaManifest.tables) != 26 {
		t.Fatal("incorrect epoch 9 boundaries")
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[0], epoch8SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatal(diffs)
	}
	if diffs := compareSemanticSchemaSnapshotManifests(states[3], epoch9SemanticSchemaManifest); len(diffs) != 0 {
		t.Fatal(diffs)
	}
	for _, table := range pipelineTemplateTables {
		if _, ok := epoch8SemanticSchemaManifest.tables[table.name]; ok {
			t.Fatal("mutated epoch 8")
		}
	}
}

const templateTestSpec = `{"schema_version":1,"kind":"ci","name":"示例","application_type":"java","steps":[{"key":"build","uses":"demo.build@v1","outputs":{"jar":{"kind":"file","media_type":"application/java-archive","simulated":true}}}],"outputs":{"jar":{"from":"steps.build.outputs.jar","type":{"kind":"file","media_type":"application/java-archive","simulated":true}}}}`

func seedTemplateVersion(t *testing.T, database *sql.DB) {
	t.Helper()
	result, err := database.Exec(`INSERT INTO pipeline_templates (template_key,kind,application_type,revision,draft,created_by_user_id) VALUES ('java-jar','ci','java',2,?,1)`, templateTestSpec)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.Canonicalize([]byte(templateTestSpec))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canonical)
	if _, err := database.Exec(`INSERT INTO pipeline_template_versions (template_id,version_number,source_revision,spec,checksum,created_by_user_id) VALUES (?,1,1,?,?,1)`, id, templateTestSpec, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
}

func TestMySQL84PipelineTemplates(t *testing.T) {
	h := newMySQLIntegrationHarness(t)
	for boundary := 0; boundary <= 3; boundary++ {
		t.Run(fmt.Sprintf("dirty-boundary-%d", boundary), func(t *testing.T) {
			dsn, _ := h.newDatabase(t)
			database := migrateDatabaseToEpoch(t, dsn, 8)
			migration := schemaMigrations[8]
			insertDirtyMigrationRow(t, database, migration)
			for i := 0; i < boundary; i++ {
				if _, err := database.Exec(pipelineTemplateTables[i].ddl); err != nil {
					t.Fatal(err)
				}
			}
			if boundary > 0 {
				if _, err := database.Exec("INSERT INTO application_types (type_key,name,enabled,revision) VALUES ('java','定制 Java',0,7),('rust','Rust',1,1)"); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			status, err := MigrateUp(ctx, dsn, migration.version, 45*time.Second, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			assertCompatibleStatus(t, status)
			var name string
			var enabled, revision int
			if err := database.QueryRow("SELECT name,enabled,revision FROM application_types WHERE type_key='java'").Scan(&name, &enabled, &revision); err != nil {
				t.Fatal(err)
			}
			if boundary > 0 && (name != "定制 Java" || enabled != 0 || revision != 7) {
				t.Fatal("seed overwrote user settings")
			}
			seedTemplateVersion(t, database)
			status, err = MigrateUp(ctx, dsn, "", 45*time.Second, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			assertCompatibleStatus(t, status)
			var count int
			if err := database.QueryRow("SELECT COUNT(*) FROM pipeline_template_versions").Scan(&count); err != nil || count != 1 {
				t.Fatalf("version lost/duplicated: %d %v", count, err)
			}
		})
	}
	t.Run("data-corruption-fails-closed", func(t *testing.T) {
		cases := []string{
			"UPDATE application_types SET type_key=CONCAT('python',CHAR(10)) WHERE type_key='python'",
			"UPDATE application_types SET name=CHAR(9) WHERE type_key='python'",
			"UPDATE pipeline_templates SET template_key=CONCAT(template_key,CHAR(10))",
			"UPDATE application_types SET revision=0 WHERE type_key='java'",
			"UPDATE application_types SET type_key='BadKey' WHERE type_key='python'",
			"UPDATE pipeline_templates SET kind='cd'",
			"UPDATE pipeline_templates SET draft=JSON_SET(draft,'$.application_type','python')",
			"UPDATE pipeline_templates SET draft=JSON_SET(draft,'$.unknown','private-marker')",
			"UPDATE pipeline_template_versions SET checksum=REPEAT('a',64)",
			"UPDATE pipeline_template_versions SET source_revision=2",
			"UPDATE pipeline_template_versions SET version_number=2",
		}
		for i, query := range cases {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				dsn, _ := h.newDatabase(t)
				database := migrateDatabaseToEpoch(t, dsn, 9)
				seedTemplateVersion(t, database)
				if _, err := database.Exec(query); err != nil {
					t.Fatal(err)
				}
				status, err := InspectSchema(context.Background(), dsn, 30*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				if status.Compatible() || !strings.Contains(status.String(), "模板存储数据") {
					t.Fatalf("accepted corrupt data: %s", status.String())
				}
				if strings.Contains(status.String(), "private-marker") {
					t.Fatal("leaked template content")
				}
			})
		}
	})
	t.Run("runtime-immutable-versions", func(t *testing.T) {
		dsn, name := h.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 9)
		runtime := openIntegrationDatabase(t, h.newRuntimeUser(t, dsn, name))
		seedTemplateVersion(t, runtime)
		for _, query := range []string{
			"UPDATE pipeline_template_versions SET checksum=REPEAT('a',64)",
			"DELETE FROM pipeline_template_versions",
			"DELETE FROM pipeline_templates",
			"DELETE FROM application_types",
		} {
			if _, err := runtime.Exec(query); err == nil {
				t.Fatalf("runtime accepted forbidden statement %s", query)
			}
		}
		if _, err := runtime.Exec("INSERT INTO application_types (type_key,name) VALUES ('custom','自定义')"); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Exec("UPDATE application_types SET enabled=0,revision=revision+1 WHERE type_key='custom'"); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.Exec("UPDATE pipeline_templates SET revision=revision+1"); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM pipeline_template_versions").Scan(&count); err != nil || count != 1 {
			t.Fatalf("immutable version changed: %d %v", count, err)
		}
	})
	t.Run("partial-schema-drift-is-rejected", func(t *testing.T) {
		dsn, _ := h.newDatabase(t)
		database := migrateDatabaseToEpoch(t, dsn, 8)
		insertDirtyMigrationRow(t, database, schemaMigrations[8])
		if _, err := database.Exec(pipelineTemplateTables[0].ddl); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec("ALTER TABLE application_types MODIFY name VARCHAR(10) NOT NULL"); err != nil {
			t.Fatal(err)
		}
		if _, err := MigrateUp(context.Background(), dsn, pipelineTemplateMigrationVersion, 30*time.Second, 10*time.Second); err == nil {
			t.Fatal("accepted malformed resume schema")
		}
	})
}
