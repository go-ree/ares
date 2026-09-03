package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

const pluggableCICDMigrationVersion = "20260903_001_pluggable_cicd"

// migratePluggableCICD is an expand-only migration. Legacy pipeline tables and
// task_record CI/CD columns intentionally remain available while existing
// runs are drained by the v1 worker.
func migratePluggableCICD() error {
	if err := ensurePluggableCICDColumns(); err != nil {
		return err
	}
	if err := normalizeEnvironmentCatalog(); err != nil {
		return err
	}
	for _, statement := range pluggableCICDTables() {
		if err := execMigrationStatement(statement); err != nil {
			return err
		}
	}
	if err := importLegacyWorkflowBindings(); err != nil {
		return err
	}
	if err := ensureActiveAppEnvironmentUniqueness(); err != nil {
		return err
	}
	return nil
}

type legacyWorkflowImport struct {
	configID int
	appName  string
	env      string
	ciJob    string
	cdJob    string
}

type importedWorkflowSpec struct {
	SchemaVersion int                    `json:"schema_version"`
	Name          string                 `json:"name"`
	Steps         []importedWorkflowStep `json:"steps"`
}

type importedWorkflowStep struct {
	Key            string         `json:"key"`
	Name           string         `json:"name"`
	Uses           string         `json:"uses"`
	Category       string         `json:"category"`
	With           map[string]any `json:"with"`
	TimeoutSeconds int            `json:"timeout_seconds"`
	OnFailure      string         `json:"on_failure"`
}

// importLegacyWorkflowBindings turns the historical package-type CI/CD pair
// into an ordinary two-slot workflow. No external system is contacted here.
// Existing bindings always win, making the import safe to resume.
func importLegacyWorkflowBindings() error {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()

	rows, err := migrationDatabase().QueryContext(ctx, `SELECT c.config_id, a.app_name, c.env,
		combination.ci_job_name, combination.cd_job_name
		FROM app_configs c
		JOIN apps a ON a.app_id = c.app_id AND a.deleted_at IS NULL
		JOIN pipelines_job_combination combination
			ON combination.code_package_type = c.code_package_type
			AND combination.deleted_at IS NULL
		LEFT JOIN app_config_workflows binding ON binding.app_config_id = c.config_id
		WHERE c.deleted_at IS NULL AND binding.binding_id IS NULL
		ORDER BY c.config_id`)
	if err != nil {
		return fmt.Errorf("query legacy workflow bindings: %w", err)
	}
	imports := make([]legacyWorkflowImport, 0)
	for rows.Next() {
		var item legacyWorkflowImport
		if err := rows.Scan(&item.configID, &item.appName, &item.env, &item.ciJob, &item.cdJob); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy workflow binding: %w", err)
		}
		imports = append(imports, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate legacy workflow bindings: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy workflow rows: %w", err)
	}
	if len(imports) == 0 {
		return nil
	}

	tx, err := migrationDatabase().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy workflow import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range imports {
		name := legacyWorkflowName(item.appName, item.env, item.configID)
		workflowResult, err := tx.ExecContext(ctx,
			"INSERT INTO release_workflows (name, description) VALUES (?, ?)",
			name, "由 pipelines_job_combination 自动迁移；可在 Web 中发布新版本")
		if err != nil {
			return fmt.Errorf("insert legacy workflow for config %d: %w", item.configID, err)
		}
		workflowID, err := workflowResult.LastInsertId()
		if err != nil {
			return fmt.Errorf("read workflow id for config %d: %w", item.configID, err)
		}
		spec := importedWorkflowSpec{
			SchemaVersion: 1,
			Name:          name,
			Steps: []importedWorkflowStep{
				{Key: "build", Name: "构建", Uses: "jenkins.job@v1", Category: "build", With: map[string]any{"job": item.ciJob, "parameters": map[string]string{}}, TimeoutSeconds: 3600, OnFailure: "stop"},
				{Key: "deploy", Name: "部署", Uses: "jenkins.job@v1", Category: "deploy", With: map[string]any{"job": item.cdJob, "parameters": map[string]string{}}, TimeoutSeconds: 3600, OnFailure: "stop"},
			},
		}
		specJSON, err := json.Marshal(spec)
		if err != nil {
			return fmt.Errorf("encode legacy workflow for config %d: %w", item.configID, err)
		}
		digest := sha256.Sum256(specJSON)
		versionResult, err := tx.ExecContext(ctx, `INSERT INTO release_workflow_versions
			(workflow_id, version, spec, checksum, created_by) VALUES (?, 1, ?, ?, 'migration')`,
			workflowID, specJSON, hex.EncodeToString(digest[:]))
		if err != nil {
			return fmt.Errorf("insert legacy workflow version for config %d: %w", item.configID, err)
		}
		versionID, err := versionResult.LastInsertId()
		if err != nil {
			return fmt.Errorf("read workflow version id for config %d: %w", item.configID, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_config_workflows
			(app_config_id, workflow_id, version_id, revision) VALUES (?, ?, ?, 1)`,
			item.configID, workflowID, versionID); err != nil {
			return fmt.Errorf("bind legacy workflow for config %d: %w", item.configID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy workflow import: %w", err)
	}
	return nil
}

func legacyWorkflowName(appName, env string, configID int) string {
	const maxNameRunes = 120
	name := fmt.Sprintf("%s/%s Jenkins 兼容流程", appName, env)
	runes := []rune(name)
	if len(runes) <= maxNameRunes {
		return name
	}
	suffix := []rune(fmt.Sprintf(" #%d", configID))
	prefixLength := maxNameRunes - len(suffix)
	if prefixLength < 0 {
		prefixLength = 0
	}
	return string(runes[:prefixLength]) + string(suffix)
}

func ensurePluggableCICDColumns() error {
	columns := []struct {
		table, column, definition string
	}{
		{"env_configs", "enabled", "TINYINT(1) NOT NULL DEFAULT 1 AFTER `description_cn`"},
		{"env_configs", "sort_order", "INT NOT NULL DEFAULT 0 AFTER `enabled`"},
		{"task_record", "engine_version", "INT NOT NULL DEFAULT 1 AFTER `products`"},
		{"task_record", "workflow_version_id", "BIGINT NOT NULL DEFAULT 0 AFTER `engine_version`"},
	}
	for _, column := range columns {
		if err := ensureMigrationColumn(column.table, column.column, column.definition); err != nil {
			return err
		}
	}

	// These values belonged to the old Jenkins parameter builder. They are no
	// longer properties required to create an environment, but remain readable
	// until their consumers have migrated to step configuration.
	legacyEnvironmentColumns := make([]textColumnMigration, 0, 5)
	for _, column := range []string{"cluster_name", "harbor_url", "harbor_project_name", "node_version", "maven_version"} {
		legacyEnvironmentColumns = append(legacyEnvironmentColumns, textColumnMigration{
			table: "env_configs", column: column, nullable: true,
		})
	}
	if err := ensureTextColumnDefinitions(legacyEnvironmentColumns); err != nil {
		return fmt.Errorf("make legacy environment tool fields optional: %w", err)
	}
	return nil
}

func normalizeEnvironmentCatalog() error {
	invalidCodes, err := findInvalidActiveEnvironmentCodes()
	if err != nil {
		return err
	}
	if len(invalidCodes) > 0 {
		return fmt.Errorf(
			"cannot normalize environment catalog; active environment codes must match ^[a-z][a-z0-9._-]{0,62}$ after trimming/lowercasing: %s",
			strings.Join(invalidCodes, ", "),
		)
	}

	var duplicate string
	err = queryScalar(`SELECT COALESCE(GROUP_CONCAT(CONCAT(app_id, ':', normalized_env) ORDER BY app_id, normalized_env SEPARATOR ', '), '')
		FROM (
			SELECT app_id, LOWER(TRIM(env)) AS normalized_env
			FROM app_configs
			WHERE deleted_at IS NULL
			GROUP BY app_id, LOWER(TRIM(env))
			HAVING COUNT(*) > 1
		) duplicates`, &duplicate)
	if err != nil {
		return fmt.Errorf("inspect duplicate app environments: %w", err)
	}
	if duplicate != "" {
		return fmt.Errorf("cannot add active app/environment uniqueness; duplicate normalized values: %s", duplicate)
	}

	var duplicateEnvironment string
	err = queryScalar(`SELECT COALESCE(GROUP_CONCAT(normalized_env ORDER BY normalized_env SEPARATOR ', '), '')
		FROM (
			SELECT LOWER(TRIM(env)) AS normalized_env
			FROM env_configs
			GROUP BY LOWER(TRIM(env))
			HAVING COUNT(*) > 1
		) duplicates`, &duplicateEnvironment)
	if err != nil {
		return fmt.Errorf("inspect duplicate environment codes: %w", err)
	}
	if duplicateEnvironment != "" {
		return fmt.Errorf("cannot normalize environment catalog; duplicate normalized values: %s", duplicateEnvironment)
	}

	for _, statement := range []string{
		"UPDATE env_configs SET env = LOWER(TRIM(env)), updated_at = updated_at WHERE BINARY env <> BINARY LOWER(TRIM(env))",
		"UPDATE app_configs SET env = LOWER(TRIM(env)), updated_at = updated_at WHERE BINARY env <> BINARY LOWER(TRIM(env))",
	} {
		if err := execMigrationStatement(statement); err != nil {
			return fmt.Errorf("normalize environment codes: %w", err)
		}
	}

	// Existing catalog rows are operator-managed and stay enabled. Codes only
	// found in historical/application data are visible but disabled until an
	// administrator explicitly enables them.
	backfill := `INSERT INTO env_configs
		(env, description_cn, enabled, sort_order, cluster_name, harbor_url, harbor_project_name, node_version, maven_version)
		SELECT source.env, source.env, 0, 0, NULL, NULL, NULL, NULL, NULL
		FROM (
			SELECT DISTINCT LOWER(TRIM(env)) AS env
			FROM app_configs
			WHERE deleted_at IS NULL AND TRIM(env) <> ''
			UNION
			SELECT DISTINCT LOWER(TRIM(env)) AS env
			FROM task_record
			WHERE TRIM(env) <> ''
				AND CHAR_LENGTH(LOWER(TRIM(env))) <= 63
				AND LOWER(TRIM(env)) REGEXP '^[a-z][a-z0-9._-]{0,62}$'
		) source
		LEFT JOIN env_configs existing ON existing.env = source.env
		WHERE existing.id IS NULL`
	if err := execMigrationStatement(backfill); err != nil {
		return fmt.Errorf("backfill environment catalog: %w", err)
	}
	return nil
}

// findInvalidActiveEnvironmentCodes fails fast instead of letting MySQL
// truncate an old app/task environment into env_configs. Historical task rows
// remain readable even when their value is no longer a valid catalog code;
// active catalog and app-config rows must be manageable through the current API.
func findInvalidActiveEnvironmentCodes() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	rows, err := migrationDatabase().QueryContext(ctx, `SELECT source
		FROM (
			SELECT CONCAT('env_configs:', id, '=', env) AS source
			FROM env_configs
			WHERE deleted_at IS NULL AND (
				CHAR_LENGTH(LOWER(TRIM(env))) NOT BETWEEN 1 AND 63
				OR LOWER(TRIM(env)) NOT REGEXP '^[a-z][a-z0-9._-]{0,62}$'
			)
			UNION ALL
			SELECT CONCAT('app_configs:', config_id, '=', env) AS source
			FROM app_configs
			WHERE deleted_at IS NULL AND (
				CHAR_LENGTH(LOWER(TRIM(env))) NOT BETWEEN 1 AND 63
				OR LOWER(TRIM(env)) NOT REGEXP '^[a-z][a-z0-9._-]{0,62}$'
			)
		) invalid
		ORDER BY source
		LIMIT 21`)
	if err != nil {
		return nil, fmt.Errorf("inspect invalid active environment codes: %w", err)
	}
	defer rows.Close()
	values := make([]string, 0, 21)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan invalid active environment code: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invalid active environment codes: %w", err)
	}
	if len(values) > 20 {
		values = append(values[:20], "...")
	}
	return values, nil
}

func ensureActiveAppEnvironmentUniqueness() error {
	if err := ensureMigrationColumn("app_configs", "active_env",
		"VARCHAR(100) GENERATED ALWAYS AS (IF(`deleted_at` IS NULL, `env`, NULL)) STORED AFTER `env`"); err != nil {
		return err
	}
	var exists bool
	if err := queryScalar(`SELECT EXISTS(
		SELECT 1 FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'app_configs'
			AND INDEX_NAME = 'uk_app_active_env'
	)`, &exists); err != nil {
		return fmt.Errorf("inspect app environment unique index: %w", err)
	}
	if exists {
		return nil
	}
	return execMigrationStatement("ALTER TABLE app_configs ADD UNIQUE INDEX uk_app_active_env (app_id, active_env)")
}

func ensureMigrationColumn(table, column, definition string) error {
	if !safeSQLIdentifier(table) || !safeSQLIdentifier(column) {
		return fmt.Errorf("unsafe migration column identifier %s.%s", table, column)
	}
	var exists bool
	if err := queryScalar(`SELECT EXISTS(
		SELECT 1 FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?
	)`, &exists, table, column); err != nil {
		return fmt.Errorf("inspect migration column %s.%s: %w", table, column, err)
	}
	if exists {
		return nil
	}
	return execMigrationStatement(fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN `%s` %s", table, column, definition))
}

func execMigrationStatement(statement string) error {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	if _, err := migrationDatabase().ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("execute migration statement %q: %w", compactSQL(statement), err)
	}
	return nil
}

func compactSQL(statement string) string {
	return strings.Join(strings.Fields(statement), " ")
}

func pluggableCICDTables() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS release_workflows (
			workflow_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			name VARCHAR(120) NOT NULL,
			description VARCHAR(500) NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			deleted_at TIMESTAMP NULL DEFAULT NULL
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS release_workflow_versions (
			version_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			workflow_id BIGINT NOT NULL,
			version INT NOT NULL,
			spec JSON NOT NULL,
			checksum CHAR(64) NOT NULL,
			created_by VARCHAR(100) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE KEY uk_workflow_version (workflow_id, version),
			INDEX idx_workflow_versions_workflow (workflow_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS app_config_workflows (
			binding_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			app_config_id INT NOT NULL,
			workflow_id BIGINT NOT NULL,
			version_id BIGINT NOT NULL,
			revision INT NOT NULL DEFAULT 1,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uk_app_config_workflow (app_config_id),
			INDEX idx_app_config_workflow (workflow_id),
			INDEX idx_app_config_workflow_version (version_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS task_step_records (
			step_record_id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
			task_id INT NOT NULL,
			workflow_version_id BIGINT NOT NULL,
			step_key VARCHAR(63) NOT NULL,
			name VARCHAR(120) NOT NULL,
			uses VARCHAR(120) NOT NULL,
			category VARCHAR(32) NULL,
			position INT NOT NULL,
			config JSON NOT NULL,
			timeout_seconds INT NOT NULL DEFAULT 3600,
			on_failure VARCHAR(16) NOT NULL DEFAULT 'stop',
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			attempt INT NOT NULL DEFAULT 1,
			external_ref JSON NULL,
			output JSON NULL,
			message VARCHAR(1000) NULL,
			started_at TIMESTAMP NULL DEFAULT NULL,
			finished_at TIMESTAMP NULL DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uk_task_step_key (task_id, step_key),
			INDEX idx_task_position (task_id, position),
			INDEX idx_task_status (task_id, status),
			INDEX idx_task_workflow_version (workflow_version_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	}
}
