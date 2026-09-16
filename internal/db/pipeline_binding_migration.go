package db

// Epoch 10 persists the fixed-version CI/CD bindings that B1 only preflighted.
// Bindings point at an immutable template version and never copy step lists, so
// the legacy app_config_workflows table stays untouched and unpublished.

const pipelineBindingMigrationVersion = "20260916_001_pipeline_bindings"

// A binding row is unique per target: rebinding and disable/enable are updates
// under revision CAS, so no target can accumulate competing active bindings.
var pipelineBindingTables = []struct {
	name, ddl, columns string
	indexes            []schemaIndexManifest
}{
	{"application_ci_bindings", `CREATE TABLE IF NOT EXISTS application_ci_bindings (
 binding_id BIGINT NOT NULL AUTO_INCREMENT,
 app_id INT NOT NULL,
 version_id BIGINT NOT NULL,
 parameters JSON NOT NULL,
 enabled TINYINT(1) NOT NULL DEFAULT 1,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 created_by_user_id BIGINT NOT NULL,
 updated_by_user_id BIGINT NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY (binding_id),
 UNIQUE KEY uq_application_ci_binding (app_id),
 KEY idx_application_ci_binding_version (version_id),
 CONSTRAINT fk_ci_binding_app FOREIGN KEY (app_id) REFERENCES apps(app_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
 CONSTRAINT fk_ci_binding_version FOREIGN KEY (version_id) REFERENCES pipeline_template_versions(version_id) ON DELETE RESTRICT ON UPDATE RESTRICT
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`binding_id|bigint|NO|<NULL>|auto_increment|
app_id|int|NO|<NULL>||
version_id|bigint|NO|<NULL>||
parameters|json|NO|<NULL>||
enabled|tinyint(1)|NO|1||
revision|bigint unsigned|NO|1||
created_by_user_id|bigint|NO|<NULL>||
updated_by_user_id|bigint|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|
updated_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|`,
		[]schemaIndexManifest{primaryIndex("binding_id"), uniqueIndex("app_id"), regularIndex("version_id")}},
	{"app_config_cd_bindings", `CREATE TABLE IF NOT EXISTS app_config_cd_bindings (
 binding_id BIGINT NOT NULL AUTO_INCREMENT,
 config_id INT NOT NULL,
 version_id BIGINT NOT NULL,
 parameters JSON NOT NULL,
 enabled TINYINT(1) NOT NULL DEFAULT 1,
 revision BIGINT UNSIGNED NOT NULL DEFAULT 1,
 created_by_user_id BIGINT NOT NULL,
 updated_by_user_id BIGINT NOT NULL,
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 PRIMARY KEY (binding_id),
 UNIQUE KEY uq_app_config_cd_binding (config_id),
 KEY idx_app_config_cd_binding_version (version_id),
 CONSTRAINT fk_cd_binding_config FOREIGN KEY (config_id) REFERENCES app_configs(config_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
 CONSTRAINT fk_cd_binding_version FOREIGN KEY (version_id) REFERENCES pipeline_template_versions(version_id) ON DELETE RESTRICT ON UPDATE RESTRICT
 ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`binding_id|bigint|NO|<NULL>|auto_increment|
config_id|int|NO|<NULL>||
version_id|bigint|NO|<NULL>||
parameters|json|NO|<NULL>||
enabled|tinyint(1)|NO|1||
revision|bigint unsigned|NO|1||
created_by_user_id|bigint|NO|<NULL>||
updated_by_user_id|bigint|NO|<NULL>||
created_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|
updated_at|datetime(6)|NO|CURRENT_TIMESTAMP(6)|DEFAULT_GENERATED|`,
		[]schemaIndexManifest{primaryIndex("binding_id"), uniqueIndex("config_id"), regularIndex("version_id")}},
}

func newPipelineBindingSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 10, version: pipelineBindingMigrationVersion,
		description: "建立应用 CI 与环境下 CD 固定版本绑定存储", compatibleMin: 10, compatibleMax: 10,
		payload:          "pipeline-bindings-v1|fixed-version|binding-parameters|revision-cas|no-legacy-workflow-reuse",
		implementationID: implementationID,
		preflight: func(s *migrationSession) error {
			if err := s.verifySemanticSchemaStates("绑定存储迁移恢复状态", epoch10ResumeSchemaStates()); err != nil {
				return err
			}
			return s.verifyEpochDataContracts(9)
		},
		up: func(s *migrationSession) error {
			for _, table := range pipelineBindingTables {
				if err := s.execMigrationStatement(table.ddl); err != nil {
					return err
				}
			}
			// Bindings are authored by an explicit opt-in operation; migration
			// never guesses a type or binding from legacy workflows or Output.
			return nil
		},
		verify: func(s *migrationSession) error {
			if err := s.verifySemanticSchema("绑定存储迁移后置条件", epoch10SemanticSchemaManifest); err != nil {
				return err
			}
			if err := s.verifyEpochDataContracts(10); err != nil {
				return err
			}
			return s.verifyStoredWorkflowChecksums()
		},
	}
}

func initializeEpoch10SemanticSchemaManifest() {
	epoch10SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch9SemanticSchemaManifest)
	for _, table := range pipelineBindingTables {
		critical := mustParseFullColumnManifest(table.name, table.columns)
		names := sortedStringKeys(critical)
		for name, column := range critical {
			if isCharacterColumnType(column.columnType) {
				column.charset = "utf8mb4"
				column.collation = "utf8mb4_unicode_ci"
			}
			critical[name] = column
		}
		epoch10SemanticSchemaManifest.tables[table.name] = schemaTableManifest{columns: names, critical: critical, indexes: table.indexes, engine: "InnoDB", charset: "utf8mb4", collation: "utf8mb4_unicode_ci"}
	}
	epoch10SemanticSchemaManifest.foreignKeys = append(epoch10SemanticSchemaManifest.foreignKeys,
		schemaForeignKeyManifest{table: "application_ci_bindings", columns: []string{"app_id"}, referencedTable: "apps", referencedColumns: []string{"app_id"}, updateRule: "RESTRICT", deleteRule: "RESTRICT"},
		schemaForeignKeyManifest{table: "application_ci_bindings", columns: []string{"version_id"}, referencedTable: "pipeline_template_versions", referencedColumns: []string{"version_id"}, updateRule: "RESTRICT", deleteRule: "RESTRICT"},
		schemaForeignKeyManifest{table: "app_config_cd_bindings", columns: []string{"config_id"}, referencedTable: "app_configs", referencedColumns: []string{"config_id"}, updateRule: "RESTRICT", deleteRule: "RESTRICT"},
		schemaForeignKeyManifest{table: "app_config_cd_bindings", columns: []string{"version_id"}, referencedTable: "pipeline_template_versions", referencedColumns: []string{"version_id"}, updateRule: "RESTRICT", deleteRule: "RESTRICT"})
}

func epoch10ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch9SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}
	for _, table := range pipelineBindingTables {
		current.tables[table.name] = cloneSchemaTableManifest(epoch10SemanticSchemaManifest.tables[table.name])
		for _, fk := range epoch10SemanticSchemaManifest.foreignKeys {
			if fk.table == table.name {
				current.foreignKeys = append(current.foreignKeys, fk)
			}
		}
		states = append(states, cloneSemanticSchemaManifest(current))
	}
	return states
}

func bindingDataError() error {
	return &SchemaStateError{Problems: []string{"CI/CD 绑定存储数据不满足契约"}}
}

func (s *migrationSession) verifyPipelineBindingRows() error {
	// Required columns already prevent missing rows and NULL flags. These queries
	// prove the retained ownership and stored-shape invariants that the runtime
	// store enforces on write, including after an interrupted migration. A
	// soft-deleted app, config or environment deliberately stays valid: deleting
	// a target later must not invalidate an applied epoch.
	checks := []string{
		`SELECT COUNT(*) FROM application_ci_bindings b
 LEFT JOIN apps a ON a.app_id=b.app_id
 LEFT JOIN pipeline_template_versions v ON v.version_id=b.version_id
 LEFT JOIN pipeline_templates t ON t.template_id=v.template_id
 WHERE a.app_id IS NULL OR v.version_id IS NULL OR t.template_id IS NULL OR BINARY t.kind<>'ci'
 OR t.application_type IS NULL OR b.app_id<=0 OR b.revision=0 OR b.enabled NOT IN (0,1)
 OR b.created_by_user_id<=0 OR b.updated_by_user_id<=0
 OR JSON_TYPE(b.parameters)<>'OBJECT' OR OCTET_LENGTH(b.parameters)>65536`,
		`SELECT COUNT(*) FROM app_config_cd_bindings b
 LEFT JOIN app_configs c ON c.config_id=b.config_id
 LEFT JOIN pipeline_template_versions v ON v.version_id=b.version_id
 LEFT JOIN pipeline_templates t ON t.template_id=v.template_id
 WHERE c.config_id IS NULL OR v.version_id IS NULL OR t.template_id IS NULL OR BINARY t.kind<>'cd'
 OR t.target_type IS NULL OR b.config_id<=0 OR b.revision=0 OR b.enabled NOT IN (0,1)
 OR b.created_by_user_id<=0 OR b.updated_by_user_id<=0
 OR JSON_TYPE(b.parameters)<>'OBJECT' OR OCTET_LENGTH(b.parameters)>65536`,
		// Only declared scalars may be stored; arrays, objects and null never
		// reach the table through the store, and a direct write must not survive.
		`SELECT COUNT(*) FROM (SELECT b.binding_id FROM application_ci_bindings b,
 JSON_TABLE(b.parameters,'$.*' COLUMNS(value JSON PATH '$')) AS p
 WHERE JSON_TYPE(p.value) NOT IN ('STRING','BOOLEAN','INTEGER')) broken`,
		`SELECT COUNT(*) FROM (SELECT b.binding_id FROM app_config_cd_bindings b,
 JSON_TABLE(b.parameters,'$.*' COLUMNS(value JSON PATH '$')) AS p
 WHERE JSON_TYPE(p.value) NOT IN ('STRING','BOOLEAN','INTEGER')) broken`,
	}
	for _, query := range checks {
		var invalid int64
		if err := s.queryScalar(query, &invalid); err != nil {
			return err
		}
		if invalid != 0 {
			return bindingDataError()
		}
	}
	return nil
}
