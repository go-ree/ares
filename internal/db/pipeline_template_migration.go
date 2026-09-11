package db

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/go-ree/ares/internal/canonicaljson"
	"github.com/go-ree/ares/internal/pipelinetemplate"
)

const pipelineTemplateMigrationVersion = "20260911_002_pipeline_templates"

var pipelineTemplateTables = []struct {
	name, ddl, columns string
	indexes            []schemaIndexManifest
}{
	{"application_types", `CREATE TABLE IF NOT EXISTS application_types (
 type_key VARCHAR(63) NOT NULL,
 name VARCHAR(120) NOT NULL,
 enabled TINYINT(1) NOT NULL DEFAULT 1,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY (type_key)
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`type_key|varchar(63)|NO|<NULL>||
name|varchar(120)|NO|<NULL>||
enabled|tinyint(1)|NO|1||
revision|bigint unsigned|NO|1||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|
updated_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|`,
		[]schemaIndexManifest{primaryIndex("type_key")}},
	{"pipeline_templates", `CREATE TABLE IF NOT EXISTS pipeline_templates (
 template_id BIGINT NOT NULL AUTO_INCREMENT,
 template_key VARCHAR(63) NOT NULL,
 kind VARCHAR(2) NOT NULL,
 application_type VARCHAR(63) NULL,
 target_type VARCHAR(63) NULL,
 enabled TINYINT(1) NOT NULL DEFAULT 1,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 draft JSON NOT NULL,
 created_by_user_id BIGINT NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY (template_id),
 UNIQUE KEY uq_pipeline_template_key (template_key),
 KEY idx_pipeline_template_type (application_type),
 CONSTRAINT fk_pipeline_template_type FOREIGN KEY (application_type) REFERENCES application_types(type_key) ON DELETE RESTRICT ON UPDATE RESTRICT
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`template_id|bigint|NO|<NULL>|auto_increment|
template_key|varchar(63)|NO|<NULL>||
kind|varchar(2)|NO|<NULL>||
application_type|varchar(63)|YES|<NULL>||
target_type|varchar(63)|YES|<NULL>||
enabled|tinyint(1)|NO|1||
revision|bigint unsigned|NO|1||
draft|json|NO|<NULL>||
created_by_user_id|bigint|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|
updated_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|`,
		[]schemaIndexManifest{primaryIndex("template_id"), uniqueIndex("template_key"), regularIndex("application_type")}},
	{"pipeline_template_versions", `CREATE TABLE IF NOT EXISTS pipeline_template_versions (
 version_id BIGINT NOT NULL AUTO_INCREMENT,
 template_id BIGINT NOT NULL,
 version_number BIGINT UNSIGNED NOT NULL,
 source_revision BIGINT UNSIGNED NOT NULL,
 spec JSON NOT NULL,
 checksum CHAR(64) NOT NULL,
 created_by_user_id BIGINT NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY (version_id),
 UNIQUE KEY uq_pipeline_version_number (template_id, version_number),
 UNIQUE KEY uq_pipeline_version_source (template_id, source_revision),
 CONSTRAINT fk_pipeline_version_template FOREIGN KEY (template_id) REFERENCES pipeline_templates(template_id) ON DELETE RESTRICT ON UPDATE RESTRICT
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`version_id|bigint|NO|<NULL>|auto_increment|
template_id|bigint|NO|<NULL>||
version_number|bigint unsigned|NO|<NULL>||
source_revision|bigint unsigned|NO|<NULL>||
spec|json|NO|<NULL>||
checksum|char(64)|NO|<NULL>||
created_by_user_id|bigint|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|`,
		[]schemaIndexManifest{primaryIndex("version_id"), uniqueIndex("template_id", "version_number"), uniqueIndex("template_id", "source_revision")}},
}

func newPipelineTemplateSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 9, version: pipelineTemplateMigrationVersion,
		description: "建立应用类型与独立 CI/CD 模板存储", compatibleMin: 9, compatibleMax: 9,
		payload:          "pipeline-templates-v1|type-catalog|draft-revision|immutable-versions|canonical-checksum|no-legacy-conversion",
		implementationID: implementationID,
		preflight: func(s *migrationSession) error {
			if err := s.verifySemanticSchemaStates("模板存储迁移恢复状态", epoch9ResumeSchemaStates()); err != nil {
				return err
			}
			return s.verifyEpochDataContracts(8)
		},
		up: func(s *migrationSession) error {
			for _, table := range pipelineTemplateTables {
				if err := s.execMigrationStatement(table.ddl); err != nil {
					return err
				}
			}
			// Only absent seeds are inserted; retry never resets user-visible settings.
			return s.execMigrationStatement(`INSERT INTO application_types (type_key,name)
 SELECT 'java','Java' WHERE NOT EXISTS (SELECT 1 FROM application_types WHERE type_key='java')
 UNION ALL SELECT 'python','Python' WHERE NOT EXISTS (SELECT 1 FROM application_types WHERE type_key='python')`)
		},
		verify: func(s *migrationSession) error {
			if err := s.verifySemanticSchema("模板存储迁移后置条件", epoch9SemanticSchemaManifest); err != nil {
				return err
			}
			if err := s.verifyEpochDataContracts(9); err != nil {
				return err
			}
			return s.verifyStoredWorkflowChecksums()
		},
	}
}

func initializeEpoch9SemanticSchemaManifest() {
	epoch9SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch8SemanticSchemaManifest)
	for _, table := range pipelineTemplateTables {
		critical := mustParseFullColumnManifest(table.name, table.columns)
		names := sortedStringKeys(critical)
		for name, column := range critical {
			if isCharacterColumnType(column.columnType) {
				column.charset = "utf8mb4"
				column.collation = "utf8mb4_unicode_ci"
			}
			critical[name] = column
		}
		epoch9SemanticSchemaManifest.tables[table.name] = schemaTableManifest{columns: names, critical: critical, indexes: table.indexes, engine: "InnoDB", charset: "utf8mb4", collation: "utf8mb4_unicode_ci"}
	}
	epoch9SemanticSchemaManifest.foreignKeys = append(epoch9SemanticSchemaManifest.foreignKeys,
		schemaForeignKeyManifest{table: "pipeline_templates", columns: []string{"application_type"}, referencedTable: "application_types", referencedColumns: []string{"type_key"}, updateRule: "RESTRICT", deleteRule: "RESTRICT"},
		schemaForeignKeyManifest{table: "pipeline_template_versions", columns: []string{"template_id"}, referencedTable: "pipeline_templates", referencedColumns: []string{"template_id"}, updateRule: "RESTRICT", deleteRule: "RESTRICT"})
}

func epoch9ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch8SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}
	for _, table := range pipelineTemplateTables {
		current.tables[table.name] = cloneSchemaTableManifest(epoch9SemanticSchemaManifest.tables[table.name])
		for _, fk := range epoch9SemanticSchemaManifest.foreignKeys {
			if fk.table == table.name {
				current.foreignKeys = append(current.foreignKeys, fk)
			}
		}
		states = append(states, cloneSemanticSchemaManifest(current))
	}
	return states
}

func templateDataError() error {
	return &SchemaStateError{Problems: []string{"应用类型或模板存储数据不满足契约"}}
}

func (s *migrationSession) verifyPipelineTemplateRows() error {
	// Bound every document before loading it; error text never echoes parameters.
	checks := []string{
		`SELECT COUNT(*) FROM application_types WHERE NOT REGEXP_LIKE(type_key,'^[a-z][a-z0-9_-]{0,62}$','c') OR REGEXP_LIKE(type_key,'[^a-z0-9_-]','c') OR NOT REGEXP_LIKE(name,'[^[:space:]]') OR enabled NOT IN (0,1) OR revision=0`,
		`SELECT COUNT(*) FROM pipeline_templates t LEFT JOIN application_types a ON a.type_key=t.application_type
 WHERE NOT REGEXP_LIKE(t.template_key,'^[a-z][a-z0-9_-]{0,62}$','c') OR REGEXP_LIKE(t.template_key,'[^a-z0-9_-]','c') OR t.revision=0 OR t.enabled NOT IN (0,1) OR t.created_by_user_id<=0
 OR OCTET_LENGTH(t.draft)>65536
 OR NOT ((BINARY t.kind='ci' AND a.type_key IS NOT NULL AND t.target_type IS NULL)
 OR (BINARY t.kind='cd' AND t.application_type IS NULL AND t.target_type IS NOT NULL AND REGEXP_LIKE(t.target_type,'^[a-z][a-z0-9_-]{0,62}$','c') AND NOT REGEXP_LIKE(t.target_type,'[^a-z0-9_-]','c')))`,
		`SELECT COUNT(*) FROM pipeline_template_versions v LEFT JOIN pipeline_templates t ON t.template_id=v.template_id
 WHERE t.template_id IS NULL OR v.version_number=0 OR v.source_revision=0 OR v.source_revision>=t.revision
 OR v.created_by_user_id<=0 OR OCTET_LENGTH(v.spec)>65536 OR NOT REGEXP_LIKE(v.checksum,'^[a-f0-9]{64}$','c')`,
		`SELECT COUNT(*) FROM (SELECT template_id FROM pipeline_template_versions GROUP BY template_id HAVING MIN(version_number)<>1 OR MAX(version_number)<>COUNT(*)) broken`,
		`SELECT COUNT(*) FROM (SELECT source_revision, LAG(source_revision) OVER (PARTITION BY template_id ORDER BY version_number) previous_revision FROM pipeline_template_versions) ordered WHERE source_revision<=previous_revision`,
	}
	for _, query := range checks {
		var invalid int64
		if err := s.queryScalar(query, &invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return templateDataError()
		}
	}
	ctx, cancel := s.operationContext()
	defer cancel()
	rows, err := s.executor.QueryContext(ctx, `SELECT draft,kind,COALESCE(application_type,''),COALESCE(target_type,''),'' FROM pipeline_templates
 UNION ALL SELECT v.spec,t.kind,COALESCE(t.application_type,''),COALESCE(t.target_type,''),v.checksum
 FROM pipeline_template_versions v JOIN pipeline_templates t ON t.template_id=v.template_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var kind, applicationType, targetType, checksum string
		if err := rows.Scan(&raw, &kind, &applicationType, &targetType, &checksum); err != nil {
			return err
		}
		var spec pipelinetemplate.Spec
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&spec) != nil || decoder.Decode(new(any)) != io.EOF || len(pipelinetemplate.Validate(spec)) != 0 ||
			spec.Kind != kind || spec.ApplicationType != applicationType || spec.TargetType != targetType {
			return templateDataError()
		}
		if checksum != "" {
			canonical, err := canonicaljson.Canonicalize(raw)
			if err != nil {
				return templateDataError()
			}
			sum := sha256.Sum256(canonical)
			if hex.EncodeToString(sum[:]) != checksum {
				return templateDataError()
			}
		}
	}
	return rows.Err()
}
