package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-ree/ares/internal/entity"
)

type schemaBootstrapTable struct {
	name      string
	statement string
}

type schemaBootstrapLanguageRule struct {
	language string
	rules    string
}

// schemaBootstrapTables is the immutable epoch-1 baseline. A fresh database is
// created at the same boundary as an upgraded installation, then epochs 2-4
// evolve it through their ordinary migrations. This keeps every ledger epoch
// meaningful and independently verifiable.
var schemaBootstrapTables = []schemaBootstrapTable{
	{
		name: entity.TableApps,
		statement: `CREATE TABLE IF NOT EXISTS apps (
			app_id INT NOT NULL AUTO_INCREMENT,
			app_name VARCHAR(255) NOT NULL,
			rundeck_app_name VARCHAR(255) NULL,
			app_name_cn VARCHAR(255) NOT NULL,
			owner VARCHAR(100) NOT NULL,
			owner_cn VARCHAR(100) NOT NULL,
			dev_language VARCHAR(100) NOT NULL,
			description_cn VARCHAR(255) NULL,
			git_url VARCHAR(255) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (app_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TableAppConfigs,
		statement: `CREATE TABLE IF NOT EXISTS app_configs (
			config_id INT NOT NULL AUTO_INCREMENT,
			app_id INT NOT NULL,
			env VARCHAR(100) NOT NULL,
			code_package_type VARCHAR(100) NOT NULL,
			code_package_path VARCHAR(255) NULL,
			code_package_name VARCHAR(255) NULL,
			base_image VARCHAR(255) NULL,
			pod_count INT DEFAULT 1,
			limits_memory INT DEFAULT 2,
			gpu_count INT DEFAULT 0,
			probe_type VARCHAR(100) DEFAULT 'TCP',
			probe_check_path VARCHAR(100) DEFAULT '/inside/checkup',
			probe_check_tcp_port INT NOT NULL DEFAULT 8080,
			probe_check_http_port INT NOT NULL DEFAULT 8080,
			probe_stop_check_http_port INT NOT NULL DEFAULT 8080,
			container_port INT NOT NULL DEFAULT 8080,
			pre_stop_type VARCHAR(100) DEFAULT 'TCP',
			pre_stop_check_path VARCHAR(100) DEFAULT '/inside/prestop',
			pre_stop_command VARCHAR(255) NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (config_id),
			KEY IDX_app_configs_app_id (app_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TableAppConfigDomains,
		statement: `CREATE TABLE IF NOT EXISTS app_config_domains (
			id BIGINT NOT NULL AUTO_INCREMENT,
			config_id INT NOT NULL,
			host VARCHAR(255) NOT NULL,
			path VARCHAR(255) NOT NULL DEFAULT '/',
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (id),
			UNIQUE KEY UQE_app_config_domains_config_id_host_path (config_id, host, path),
			KEY IDX_app_config_domains_config_id (config_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TableTaskRecord,
		statement: `CREATE TABLE IF NOT EXISTS task_record (
			task_id INT NOT NULL AUTO_INCREMENT,
			app_name VARCHAR(255) NOT NULL,
			rundeck_app_name VARCHAR(255) NULL,
			branch VARCHAR(100) NOT NULL,
			env VARCHAR(255) NOT NULL,
			publisher VARCHAR(255) NOT NULL,
			ci_build_id INT DEFAULT 0,
			cd_build_id INT DEFAULT 0,
			pipeline_param JSON NULL,
			status VARCHAR(100) DEFAULT 'init',
			message VARCHAR(255) NULL,
			ci_job_name VARCHAR(100) NULL,
			cd_job_name VARCHAR(100) NULL,
			auto_deploy TINYINT(1) DEFAULT 1,
			products VARCHAR(255) NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (task_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TableTaskRecordImages,
		statement: `CREATE TABLE IF NOT EXISTS task_record_images (
			id BIGINT NOT NULL AUTO_INCREMENT,
			task_id INT NOT NULL,
			img_type VARCHAR(32) NOT NULL,
			url VARCHAR(1024) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (id),
			UNIQUE KEY UQE_task_record_images_task_id_img_type (task_id, img_type)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TablePipelines,
		statement: `CREATE TABLE IF NOT EXISTS pipelines (
			id INT NOT NULL AUTO_INCREMENT,
			job_name VARCHAR(100) NOT NULL,
			description_cn VARCHAR(255) NOT NULL,
			url VARCHAR(255) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (id),
			UNIQUE KEY UQE_pipelines_job_name (job_name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TablePipelineJobs,
		statement: `CREATE TABLE IF NOT EXISTS pipelines_job_combination (
			id INT NOT NULL AUTO_INCREMENT,
			description_cn VARCHAR(255) NOT NULL,
			ci_job_name VARCHAR(100) NOT NULL,
			cd_job_name VARCHAR(100) NOT NULL,
			code_package_type VARCHAR(100) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (id),
			UNIQUE KEY UQE_pipelines_job_combination_uk_ci_cd_combination (ci_job_name, cd_job_name),
			UNIQUE KEY UQE_pipelines_job_combination_code_package_type (code_package_type),
			KEY IDX_pipelines_job_combination_idx_ci_job (ci_job_name),
			KEY IDX_pipelines_job_combination_idx_cd_job (cd_job_name),
			CONSTRAINT fk_pipelines_ci_job FOREIGN KEY (ci_job_name) REFERENCES pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE,
			CONSTRAINT fk_pipelines_cd_job FOREIGN KEY (cd_job_name) REFERENCES pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TableEnvConfigs,
		statement: `CREATE TABLE IF NOT EXISTS env_configs (
			id INT NOT NULL AUTO_INCREMENT,
			env VARCHAR(100) NOT NULL,
			cluster_name VARCHAR(255) NOT NULL,
			description_cn VARCHAR(255) NOT NULL,
			harbor_url VARCHAR(255) NOT NULL,
			harbor_project_name VARCHAR(255) NOT NULL,
			node_version VARCHAR(255) NOT NULL,
			maven_version VARCHAR(255) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (id),
			UNIQUE KEY UQE_env_configs_env (env)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TableIntegrationSettings,
		statement: `CREATE TABLE IF NOT EXISTS integration_settings (
			provider VARCHAR(64) NOT NULL,
			config_data MEDIUMTEXT NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (provider)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
	{
		name: entity.TableDevLanguageRules,
		statement: `CREATE TABLE IF NOT EXISTS dev_language_rules (
			dev_language VARCHAR(100) NOT NULL,
			rules JSON NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL,
			PRIMARY KEY (dev_language)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`,
	},
}

var schemaBootstrapLanguageRules = []schemaBootstrapLanguageRule{
	{language: "java", rules: `{"allowed":["jar","war"],"default":"jar"}`},
	{language: "python", rules: `{"allowed":["python","ai"],"default":"python"}`},
	{language: "node.js", rules: `{"allowed":["static","miniapp","node.js"],"default":"node.js"}`},
	{language: "golang", rules: `{"allowed":["golang"],"default":"golang"}`},
}

// bootstrapEmptySchema creates only a new or interrupted empty installation.
// It must never turn a partially recognized application database into a schema
// that merely looks current: unknown tables and business rows fail closed.
func (s *migrationSession) bootstrapEmptySchema() error {
	tables, problems, err := s.inspectBootstrapState()
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return &SchemaStateError{Problems: problems}
	}

	for _, table := range schemaBootstrapTables {
		if _, exists := tables[table.name]; exists {
			continue
		}
		ctx, cancel := s.operationContext()
		_, err = s.executor.ExecContext(ctx, table.statement)
		cancel()
		if err != nil {
			return fmt.Errorf("create bootstrap table %s: %w", table.name, err)
		}
		tables[table.name] = struct{}{}
	}

	for _, rule := range schemaBootstrapLanguageRules {
		ctx, cancel := s.operationContext()
		_, err = s.executor.ExecContext(ctx,
			`INSERT INTO dev_language_rules (dev_language, rules)
			 SELECT ?, ? WHERE NOT EXISTS (
				SELECT 1 FROM dev_language_rules WHERE dev_language = ?
			 )`,
			rule.language, rule.rules, rule.language)
		cancel()
		if err != nil {
			return fmt.Errorf("insert bootstrap language rule %s: %w", rule.language, err)
		}
	}
	ctx, cancel := s.operationContext()
	seedProblems, err := s.bootstrapLanguageRuleProblems(ctx, false)
	cancel()
	if err != nil {
		return err
	}
	if len(seedProblems) > 0 {
		return &SchemaStateError{Problems: seedProblems}
	}
	return nil
}

// inspectBootstrapState is a strictly read-only gate shared by normal
// bootstrap and empty-legacy-ledger adoption. It must run before any DDL when
// the database provenance cannot yet be proven by an applied migration.
func (s *migrationSession) inspectBootstrapState() (map[string]struct{}, []string, error) {
	ctx, cancel := s.operationContext()
	tables, err := s.databaseTables(ctx)
	cancel()
	if err != nil {
		return nil, nil, err
	}
	if problems := bootstrapTableSetProblems(tables); len(problems) > 0 {
		return tables, problems, nil
	}
	ctx, cancel = s.operationContext()
	schemaProblems, err := s.bootstrapExistingSchemaProblems(ctx, tables)
	cancel()
	if err != nil {
		return nil, nil, err
	}
	if len(schemaProblems) > 0 {
		return tables, schemaProblems, nil
	}
	if _, exists := tables[entity.TableDevLanguageRules]; exists {
		ctx, cancel = s.operationContext()
		seedProblems, seedErr := s.bootstrapLanguageRuleProblems(ctx, true)
		cancel()
		if seedErr != nil {
			return nil, nil, seedErr
		}
		if len(seedProblems) > 0 {
			return tables, seedProblems, nil
		}
	}

	ctx, cancel = s.operationContext()
	var ledgerRows int64
	err = s.executor.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&ledgerRows)
	cancel()
	if err != nil {
		return nil, nil, fmt.Errorf("count migration ledger before schema bootstrap: %w", err)
	}
	if ledgerRows != 0 {
		return tables, []string{
			fmt.Sprintf("空库 bootstrap 要求 schema_migrations 无记录，当前为 %d 条", ledgerRows),
		}, nil
	}

	nonEmpty, err := s.nonEmptyBootstrapTables(tables)
	if err != nil {
		return nil, nil, err
	}
	if len(nonEmpty) > 0 {
		return tables, []string{
			"空库 bootstrap 拒绝包含业务数据的受管表: " + strings.Join(nonEmpty, ","),
		}, nil
	}
	return tables, nil, nil
}

// bootstrapExistingSchemaProblems proves that any tables left by an
// interrupted bootstrap form the exact creation prefix and already satisfy
// the epoch-1 semantic contract. Merely recognizing a table name is not enough
// to authorize subsequent DDL against it.
func (s *migrationSession) bootstrapExistingSchemaProblems(
	ctx context.Context,
	tables map[string]struct{},
) ([]string, error) {
	manifest := semanticSchemaManifest{
		tables:              make(map[string]schemaTableManifest),
		strictTables:        true,
		strictColumns:       true,
		strictIndexes:       true,
		strictForeignKeys:   true,
		strictSchemaObjects: true,
	}
	problems := make([]string, 0)
	missingSeen := false
	for _, table := range schemaBootstrapTables {
		_, exists := tables[table.name]
		if !exists {
			missingSeen = true
			continue
		}
		if missingSeen {
			problems = append(problems,
				"空库 bootstrap 的已有受管表不是按固定顺序创建的连续前缀: "+table.name)
		}
		manifest.tables[table.name] = epoch1SemanticSchemaManifest.tables[table.name]
	}
	for _, foreignKey := range epoch1SemanticSchemaManifest.foreignKeys {
		if _, exists := manifest.tables[foreignKey.table]; exists {
			manifest.foreignKeys = append(manifest.foreignKeys, foreignKey)
		}
	}
	if _, exists := manifest.tables["pipelines_job_combination"]; exists {
		manifest.requirePipelineCollationAlignment = true
	}

	snapshot, err := readSchemaSnapshot(ctx, s.executor)
	if err != nil {
		return nil, err
	}
	problems = append(problems, compareSemanticSchema(snapshot, manifest)...)
	return finalizeSchemaDiffs(problems), nil
}

func bootstrapTableSetProblems(tables map[string]struct{}) []string {
	if _, exists := tables[schemaMigrationsTable]; !exists {
		return []string{"空库 bootstrap 要求 schema_migrations 已存在"}
	}
	managed := make(map[string]struct{}, len(schemaBootstrapTables)+1)
	managed[schemaMigrationsTable] = struct{}{}
	for _, table := range schemaBootstrapTables {
		managed[table.name] = struct{}{}
	}
	unknown := make(map[string]struct{})
	for table := range tables {
		if _, exists := managed[table]; !exists {
			unknown[table] = struct{}{}
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return []string{"空库 bootstrap 发现未知表: " + strings.Join(sortedKeys(unknown), ",")}
}

func (s *migrationSession) nonEmptyBootstrapTables(tables map[string]struct{}) ([]string, error) {
	nonEmpty := make(map[string]struct{})
	for _, table := range schemaBootstrapTables {
		if _, exists := tables[table.name]; !exists {
			continue
		}

		ctx, cancel := s.operationContext()
		var hasRows bool
		if table.name == entity.TableDevLanguageRules {
			cancel()
			// Exact seed semantics were already validated by inspectBootstrapState.
			// Valid partial seed rows are the only business rows allowed here.
			continue
		} else {
			err := s.executor.QueryRowContext(ctx,
				fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM `%s` LIMIT 1)", table.name)).Scan(&hasRows)
			cancel()
			if err != nil {
				return nil, fmt.Errorf("inspect bootstrap rows in %s: %w", table.name, err)
			}
		}
		if hasRows {
			nonEmpty[table.name] = struct{}{}
		}
	}
	return sortedKeys(nonEmpty), nil
}

func (s *migrationSession) bootstrapLanguageRuleProblems(
	ctx context.Context,
	allowMissing bool,
) ([]string, error) {
	expected := make(map[string]string, len(schemaBootstrapLanguageRules))
	for _, rule := range schemaBootstrapLanguageRules {
		canonical, err := canonicalJSON(rule.rules)
		if err != nil {
			return nil, fmt.Errorf("canonicalize compiled bootstrap language rule %s: %w", rule.language, err)
		}
		expected[rule.language] = canonical
	}
	rows, err := s.executor.QueryContext(ctx, `SELECT dev_language, rules, deleted_at
		FROM dev_language_rules ORDER BY dev_language`)
	if err != nil {
		return nil, fmt.Errorf("inspect bootstrap language rules: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]struct{}, len(expected))
	problems := make([]string, 0)
	for rows.Next() {
		var language string
		var rawRules []byte
		var deletedAt sql.NullTime
		if err := rows.Scan(&language, &rawRules, &deletedAt); err != nil {
			return nil, fmt.Errorf("scan bootstrap language rule: %w", err)
		}
		want, known := expected[language]
		if !known {
			problems = append(problems, "空库 bootstrap 发现未知语言规则: "+language)
			continue
		}
		seen[language] = struct{}{}
		if deletedAt.Valid {
			problems = append(problems, "空库 bootstrap 语言规则已软删除: "+language)
		}
		got, err := canonicalJSON(string(rawRules))
		if err != nil {
			problems = append(problems, "空库 bootstrap 语言规则 JSON 非法: "+language)
			continue
		}
		if got != want {
			problems = append(problems, "空库 bootstrap 语言规则语义不匹配: "+language)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bootstrap language rules: %w", err)
	}
	if !allowMissing {
		for _, rule := range schemaBootstrapLanguageRules {
			if _, exists := seen[rule.language]; !exists {
				problems = append(problems, "空库 bootstrap 缺少语言规则: "+rule.language)
			}
		}
	}
	return finalizeSchemaDiffs(problems), nil
}

func canonicalJSON(value string) (string, error) {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func bootstrapLanguageRuleNames() []any {
	names := make([]any, 0, len(schemaBootstrapLanguageRules))
	for _, rule := range schemaBootstrapLanguageRules {
		names = append(names, rule.language)
	}
	return names
}
