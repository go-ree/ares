package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/go-ree/ares/internal/tool"
)

const (
	legacyNullStringMigrationVersion = "20260902_001_cleanup_legacy_null_strings"
	migrationBatchSize               = 500
)

// newLegacyNullStringSchemaMigration keeps the immutable epoch metadata and
// the concrete Up/verify wiring beside their implementation. This file (and
// its cross-package normalizer) is source-fingerprinted by the catalog test.
func newLegacyNullStringSchemaMigration(implementationID string) schemaMigration {
	return schemaMigration{
		epoch: 1, version: legacyNullStringMigrationVersion,
		description: "治理历史 NULL 字符串", compatibleMin: 1, compatibleMax: 1,
		payload:          "null-string-algorithm-v1|required-and-optional-text-columns|batch-size-500",
		implementationID: implementationID,
		preflight:        func(session *migrationSession) error { return session.verifyLegacyNullStringResumeState() },
		up:               func(session *migrationSession) error { return session.migrateLegacyNullStrings() },
		verify:           func(session *migrationSession) error { return session.verifyLegacyNullStringPostconditions() },
	}
}

func (s *migrationSession) verifyLegacyNullStringResumeState() error {
	return s.verifySemanticSchema("NULL 字符串迁移恢复状态", epoch1SemanticSchemaManifest)
}

func (s *migrationSession) verifyLegacyNullStringPostconditions() error {
	if err := s.verifySemanticSchema("NULL 字符串迁移后置条件", epoch1SemanticSchemaManifest); err != nil {
		return err
	}
	return s.verifyEpochDataContracts(1)
}

type textColumnMigration struct {
	table    string
	column   string
	nullable bool
}

type textColumnState struct {
	columnType   string
	characterSet sql.NullString
	collation    sql.NullString
	comment      string
	extra        string
	nullable     string
	defaultValue sql.NullString
}

type optionalTextTableMigration struct {
	table      string
	primaryKey string
	columns    []string
}

var requiredTextColumns = []textColumnMigration{
	{table: "apps", column: "app_name_cn"},
	{table: "app_configs", column: "code_package_type"},
}

var optionalTextColumns = []textColumnMigration{
	{table: "apps", column: "rundeck_app_name", nullable: true},
	{table: "apps", column: "description_cn", nullable: true},
	{table: "app_configs", column: "code_package_path", nullable: true},
	{table: "app_configs", column: "code_package_name", nullable: true},
	{table: "app_configs", column: "base_image", nullable: true},
	{table: "app_configs", column: "pre_stop_command", nullable: true},
	{table: "task_record", column: "message", nullable: true},
	{table: "task_record", column: "rundeck_app_name", nullable: true},
	{table: "task_record", column: "ci_job_name", nullable: true},
	{table: "task_record", column: "cd_job_name", nullable: true},
	{table: "task_record", column: "products", nullable: true},
}

var optionalTextTables = []optionalTextTableMigration{
	{table: "apps", primaryKey: "app_id", columns: []string{"rundeck_app_name", "description_cn"}},
	{table: "app_configs", primaryKey: "config_id", columns: []string{"code_package_path", "code_package_name", "base_image", "pre_stop_command"}},
	{table: "task_record", primaryKey: "task_id", columns: []string{"rundeck_app_name", "message", "ci_job_name", "cd_job_name", "products"}},
}

func (s *migrationSession) migrateLegacyNullStrings() error {
	languageDefaults, err := s.loadMigrationLanguageDefaults()
	if err != nil {
		return err
	}
	if err := s.validateRequiredTextBackfills(languageDefaults); err != nil {
		return err
	}
	if err := s.backfillRequiredTextColumns(languageDefaults); err != nil {
		return err
	}
	if err := s.ensureTextColumnDefinitions(optionalTextColumns); err != nil {
		return err
	}
	for _, table := range optionalTextTables {
		if err := s.cleanOptionalTextTable(table); err != nil {
			return err
		}
	}
	if err := s.ensureTextColumnDefinitions(requiredTextColumns); err != nil {
		return err
	}
	return s.verifyCanonicalTextValues()
}

type migrationLanguageRule struct {
	Allowed []string `json:"allowed"`
	Default string   `json:"default"`
}

func (s *migrationSession) loadMigrationLanguageDefaults() (map[string]string, error) {
	ctx, cancel := s.operationContext()
	defer cancel()
	rows, err := s.executor.QueryContext(ctx, `SELECT dev_language, rules
		FROM dev_language_rules WHERE deleted_at IS NULL ORDER BY dev_language`)
	if err != nil {
		return nil, fmt.Errorf("load language rules for null-string migration: %w", err)
	}
	defer rows.Close()

	defaults := make(map[string]string)
	invalidLanguages := make(map[string]struct{})
	for rows.Next() {
		var devLanguage string
		var rawRule []byte
		if err := rows.Scan(&devLanguage, &rawRule); err != nil {
			return nil, fmt.Errorf("scan language rule for null-string migration: %w", err)
		}
		language := strings.ToLower(strings.TrimSpace(devLanguage))
		if language == "" {
			continue
		}
		if _, invalid := invalidLanguages[language]; invalid {
			continue
		}
		var rule migrationLanguageRule
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			slog.Warn("ignoring invalid language rule during null-string migration",
				"dev_language", devLanguage,
				"reason", err)
			invalidLanguages[language] = struct{}{}
			delete(defaults, language)
			continue
		}
		defaultValue := tool.NormalizeNullableText(rule.Default)
		if defaultValue == "" {
			slog.Warn("ignoring invalid language rule during null-string migration",
				"dev_language", devLanguage,
				"reason", "default package type is empty")
			invalidLanguages[language] = struct{}{}
			delete(defaults, language)
			continue
		}
		allowed := false
		for _, value := range rule.Allowed {
			if strings.TrimSpace(value) == defaultValue {
				allowed = true
				break
			}
		}
		if !allowed {
			slog.Warn("ignoring invalid language rule during null-string migration",
				"dev_language", devLanguage,
				"reason", fmt.Sprintf("default %q is not in its allowed list", defaultValue))
			invalidLanguages[language] = struct{}{}
			delete(defaults, language)
			continue
		}
		if existing, duplicate := defaults[language]; duplicate && existing != defaultValue {
			slog.Warn("ignoring conflicting language rules during null-string migration",
				"dev_language", language,
				"defaults", []string{existing, defaultValue})
			invalidLanguages[language] = struct{}{}
			delete(defaults, language)
			continue
		}
		defaults[language] = defaultValue
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate language rules for null-string migration: %w", err)
	}
	return defaults, nil
}

func (s *migrationSession) validateRequiredTextBackfills(languageDefaults map[string]string) error {
	invalidAppIDs, err := s.queryInt64s(`SELECT app_id FROM apps
		WHERE ` + legacyNullishSQL("app_name_cn") + `
			AND ` + legacyNullishSQL("app_name") + `
		ORDER BY app_id LIMIT 21`)
	if err != nil {
		return fmt.Errorf("validate app_name_cn backfill: %w", err)
	}
	if len(invalidAppIDs) > 0 {
		return fmt.Errorf("cannot backfill app_name_cn because app_name is also empty for app_ids=%s", summarizeIDs(invalidAppIDs))
	}

	ctx, cancel := s.operationContext()
	defer cancel()
	rows, err := s.executor.QueryContext(ctx, `SELECT c.config_id, a.dev_language
		FROM app_configs c
		LEFT JOIN apps a ON a.app_id = c.app_id
		WHERE `+legacyNullishSQL("c.code_package_type")+`
		ORDER BY c.config_id`)
	if err != nil {
		return fmt.Errorf("inspect code_package_type backfills: %w", err)
	}
	defer rows.Close()

	invalidIDs := make([]int64, 0, 21)
	invalidCount := 0
	for rows.Next() {
		var configID int64
		var devLanguage sql.NullString
		if err := rows.Scan(&configID, &devLanguage); err != nil {
			return fmt.Errorf("scan code_package_type backfill: %w", err)
		}
		language := strings.ToLower(strings.TrimSpace(devLanguage.String))
		if _, ok := languageDefaults[language]; ok && devLanguage.Valid {
			continue
		}
		invalidCount++
		if len(invalidIDs) < 21 {
			invalidIDs = append(invalidIDs, configID)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate code_package_type backfills: %w", err)
	}
	if invalidCount > 0 {
		return fmt.Errorf("cannot backfill code_package_type for %d configs; missing or invalid language rules, config_ids=%s", invalidCount, summarizeIDs(invalidIDs))
	}
	return nil
}

func (s *migrationSession) backfillRequiredTextColumns(languageDefaults map[string]string) error {
	appWhere := legacyNullishSQL("app_name_cn")
	if err := s.updateIDsInBatches("apps", "app_id", appWhere,
		"app_name_cn = CASE WHEN "+appWhere+" THEN REGEXP_REPLACE(app_name, '^[[:space:]]+|[[:space:]]+$', '') ELSE app_name_cn END, updated_at = updated_at", nil); err != nil {
		return fmt.Errorf("backfill apps.app_name_cn: %w", err)
	}

	languages := make([]string, 0, len(languageDefaults))
	for language := range languageDefaults {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	for _, language := range languages {
		where := legacyNullishSQL("c.code_package_type") +
			" AND LOWER(REGEXP_REPLACE(a.dev_language, '^[[:space:]]+|[[:space:]]+$', '')) = ?"
		var lastID *int64
		for {
			query := `SELECT c.config_id FROM app_configs c
				JOIN apps a ON a.app_id = c.app_id
				WHERE (` + where + `)`
			args := []any{language}
			if lastID != nil {
				query += " AND c.config_id > ?"
				args = append(args, *lastID)
			}
			query += " ORDER BY c.config_id LIMIT " + fmt.Sprint(migrationBatchSize)
			ids, err := s.queryInt64s(query, args...)
			if err != nil {
				return fmt.Errorf("select %s code_package_type backfill batch: %w", language, err)
			}
			if len(ids) == 0 {
				break
			}
			setArgs := []any{languageDefaults[language]}
			if err := s.updateRowsByIDs("app_configs", "config_id",
				"code_package_type = CASE WHEN "+legacyNullishSQL("code_package_type")+" THEN ? ELSE code_package_type END, updated_at = updated_at", setArgs, ids); err != nil {
				return fmt.Errorf("backfill %s code_package_type: %w", language, err)
			}
			cursor := ids[len(ids)-1]
			lastID = &cursor
		}
	}
	return nil
}

func (s *migrationSession) cleanOptionalTextTable(spec optionalTextTableMigration) error {
	conditions := make([]string, 0, len(spec.columns))
	assignments := make([]string, 0, len(spec.columns)+1)
	for _, column := range spec.columns {
		condition := legacyNonNullTextSQL("`" + column + "`")
		conditions = append(conditions, condition)
		assignments = append(assignments,
			fmt.Sprintf("`%s` = CASE WHEN %s THEN NULL ELSE `%s` END", column, condition, column))
	}
	assignments = append(assignments, "updated_at = updated_at")
	return s.updateIDsInBatches(spec.table, spec.primaryKey, strings.Join(conditions, " OR "),
		strings.Join(assignments, ", "), nil)
}

func (s *migrationSession) updateIDsInBatches(table, primaryKey, where, assignments string, assignmentArgs []any) error {
	total := int64(0)
	// A nil cursor deliberately means "no lower bound". Starting at zero or
	// math.MinInt64 would skip valid signed primary keys at or below the
	// sentinel and could leave an epoch permanently dirty during verification.
	var lastID *int64
	for {
		query := fmt.Sprintf("SELECT `%s` FROM `%s` WHERE (%s)", primaryKey, table, where)
		args := make([]any, 0, 1)
		if lastID != nil {
			query += fmt.Sprintf(" AND `%s` > ?", primaryKey)
			args = append(args, *lastID)
		}
		query += fmt.Sprintf(" ORDER BY `%s` LIMIT %d", primaryKey, migrationBatchSize)
		ids, err := s.queryInt64s(query, args...)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		if err := s.updateRowsByIDs(table, primaryKey, assignments, assignmentArgs, ids); err != nil {
			return err
		}
		total += int64(len(ids))
		cursor := ids[len(ids)-1]
		lastID = &cursor
	}
	if total > 0 {
		slog.Info("normalized legacy null-string rows", "table", table, "rows", total)
	}
	return nil
}

func (s *migrationSession) updateRowsByIDs(table, primaryKey, assignments string, assignmentArgs []any, ids []int64) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := fmt.Sprintf("UPDATE `%s` SET %s WHERE `%s` IN (%s)", table, assignments, primaryKey, placeholders)
	args := append([]any(nil), assignmentArgs...)
	for _, id := range ids {
		args = append(args, id)
	}
	ctx, cancel := s.operationContext()
	defer cancel()
	if _, err := s.executor.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return nil
}

func (s *migrationSession) ensureTextColumnDefinitions(columns []textColumnMigration) error {
	noBackslashEscapes, err := s.migrationUsesNoBackslashEscapes()
	if err != nil {
		return err
	}
	byTable := make(map[string][]string)
	for _, column := range columns {
		state, err := s.inspectTextColumn(column)
		if err != nil {
			return err
		}
		wantNullable := "NO"
		if column.nullable {
			wantNullable = "YES"
		}
		if state.nullable != wantNullable {
			definition, err := preservedTextColumnDefinition(*state, column.nullable, noBackslashEscapes)
			if err != nil {
				return fmt.Errorf("preserve definition for %s.%s: %w", column.table, column.column, err)
			}
			byTable[column.table] = append(byTable[column.table],
				fmt.Sprintf("MODIFY COLUMN `%s` %s", column.column, definition))
			continue
		}
		if state.defaultValue.Valid {
			byTable[column.table] = append(byTable[column.table],
				fmt.Sprintf("ALTER COLUMN `%s` DROP DEFAULT", column.column))
		}
	}
	tables := make([]string, 0, len(byTable))
	for table := range byTable {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		ctx, cancel := s.operationContext()
		_, err := s.executor.ExecContext(ctx, fmt.Sprintf("ALTER TABLE `%s` %s", table, strings.Join(byTable[table], ", ")))
		cancel()
		if err != nil {
			return fmt.Errorf("normalize nullability for %s: %w", table, err)
		}
	}
	return nil
}

func (s *migrationSession) migrationUsesNoBackslashEscapes() (bool, error) {
	ctx, cancel := s.operationContext()
	defer cancel()
	var sqlMode string
	if err := s.executor.QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&sqlMode); err != nil {
		return false, fmt.Errorf("inspect SQL mode for schema migration: %w", err)
	}
	for _, mode := range strings.Split(sqlMode, ",") {
		if strings.EqualFold(strings.TrimSpace(mode), "NO_BACKSLASH_ESCAPES") {
			return true, nil
		}
	}
	return false, nil
}

func (s *migrationSession) inspectTextColumn(column textColumnMigration) (*textColumnState, error) {
	ctx, cancel := s.operationContext()
	defer cancel()
	var state textColumnState
	err := s.executor.QueryRowContext(ctx, `SELECT COLUMN_TYPE, CHARACTER_SET_NAME,
			COLLATION_NAME, COLUMN_COMMENT, EXTRA, IS_NULLABLE, COLUMN_DEFAULT
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
		column.table, column.column).Scan(
		&state.columnType,
		&state.characterSet,
		&state.collation,
		&state.comment,
		&state.extra,
		&state.nullable,
		&state.defaultValue,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect %s.%s: %w", column.table, column.column, err)
	}
	return &state, nil
}

func preservedTextColumnDefinition(state textColumnState, nullable, noBackslashEscapes bool) (string, error) {
	columnType := strings.ToLower(strings.TrimSpace(state.columnType))
	if !strings.HasPrefix(columnType, "varchar(") || !strings.HasSuffix(columnType, ")") {
		return "", fmt.Errorf("unsupported column type %q", state.columnType)
	}
	length := strings.TrimSuffix(strings.TrimPrefix(columnType, "varchar("), ")")
	if length == "" {
		return "", fmt.Errorf("invalid column type %q", state.columnType)
	}
	for _, character := range length {
		if character < '0' || character > '9' {
			return "", fmt.Errorf("invalid column type %q", state.columnType)
		}
	}

	parts := []string{columnType}
	if state.characterSet.Valid {
		if !safeSQLIdentifier(state.characterSet.String) {
			return "", fmt.Errorf("invalid character set %q", state.characterSet.String)
		}
		parts = append(parts, "CHARACTER SET "+state.characterSet.String)
	}
	if state.collation.Valid {
		if !safeSQLIdentifier(state.collation.String) {
			return "", fmt.Errorf("invalid collation %q", state.collation.String)
		}
		parts = append(parts, "COLLATE "+state.collation.String)
	}
	if nullable {
		parts = append(parts, "NULL")
	} else {
		parts = append(parts, "NOT NULL")
	}
	if state.comment != "" {
		parts = append(parts, "COMMENT "+quoteMigrationSQLString(state.comment, noBackslashEscapes))
	}
	if strings.Contains(strings.ToUpper(state.extra), "INVISIBLE") {
		parts = append(parts, "INVISIBLE")
	}
	return strings.Join(parts, " "), nil
}

func quoteMigrationSQLString(value string, noBackslashEscapes bool) string {
	if !noBackslashEscapes {
		value = strings.ReplaceAll(value, `\`, `\\`)
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func safeSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func (s *migrationSession) verifyCanonicalTextValues() error {
	if err := s.verifyTextColumnDefinitions(append(
		append([]textColumnMigration(nil), requiredTextColumns...), optionalTextColumns...)); err != nil {
		return err
	}
	if err := s.verifyTextColumns("required", requiredTextColumns, legacyNullishSQL); err != nil {
		return err
	}
	return s.verifyTextColumns("optional", optionalTextColumns, legacyNonNullTextSQL)
}

func (s *migrationSession) verifyTextColumnDefinitions(columns []textColumnMigration) error {
	problems := make([]string, 0)
	for _, column := range columns {
		state, err := s.inspectTextColumn(column)
		if err != nil {
			return err
		}
		columnType := strings.ToLower(strings.TrimSpace(state.columnType))
		if !strings.HasPrefix(columnType, "varchar(") || !strings.HasSuffix(columnType, ")") {
			problems = append(problems, fmt.Sprintf(
				"列 %s.%s 类型为 %s，epoch 1 仅支持 VARCHAR", column.table, column.column, state.columnType))
		}
		wantNullable := "NO"
		if column.nullable {
			wantNullable = "YES"
		}
		if !strings.EqualFold(state.nullable, wantNullable) {
			problems = append(problems, fmt.Sprintf(
				"列 %s.%s IS_NULLABLE=%s，epoch 1 期望 %s",
				column.table, column.column, state.nullable, wantNullable))
		}
		if state.defaultValue.Valid {
			problems = append(problems, fmt.Sprintf(
				"列 %s.%s 仍保留默认值 %s，epoch 1 期望无默认值",
				column.table, column.column, state.defaultValue.String))
		}
		if strings.Contains(strings.ToLower(state.extra), "generated") {
			problems = append(problems, fmt.Sprintf(
				"列 %s.%s 不应为生成列", column.table, column.column))
		}
	}
	if len(problems) > 0 {
		return &SchemaStateError{Problems: problems}
	}
	return nil
}

func (s *migrationSession) verifyTextColumns(kind string, columns []textColumnMigration, condition func(string) string) error {
	byTable := make(map[string][]string)
	for _, column := range columns {
		byTable[column.table] = append(byTable[column.table], column.column)
	}
	tables := make([]string, 0, len(byTable))
	for table := range byTable {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		conditions := make([]string, 0, len(byTable[table]))
		for _, column := range byTable[table] {
			conditions = append(conditions, condition("`"+column+"`"))
		}
		var exists bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM `%s` WHERE %s LIMIT 1)",
			table, strings.Join(conditions, " OR "))
		if err := s.queryScalar(query, &exists); err != nil {
			return fmt.Errorf("verify %s text columns in %s: %w", kind, table, err)
		}
		if exists {
			return &SchemaStateError{Problems: []string{fmt.Sprintf(
				"%s text columns in %s still contain non-canonical empty values", kind, table)}}
		}
	}
	return nil
}

func (s *migrationSession) queryInt64s(query string, args ...any) ([]int64, error) {
	ctx, cancel := s.operationContext()
	defer cancel()
	rows, err := s.executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]int64, 0)
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *migrationSession) queryScalar(query string, target any, args ...any) error {
	ctx, cancel := s.operationContext()
	defer cancel()
	return s.executor.QueryRowContext(ctx, query, args...).Scan(target)
}

func legacyNullishSQL(expression string) string {
	return fmt.Sprintf("(%[1]s IS NULL OR %[1]s = '' OR %[1]s REGEXP '^[[:space:]]+$' OR LOWER(%[1]s) REGEXP '^[[:space:]]*null[[:space:]]*$')", expression)
}

func legacyNonNullTextSQL(expression string) string {
	return fmt.Sprintf("(%[1]s IS NOT NULL AND (%[1]s = '' OR %[1]s REGEXP '^[[:space:]]+$' OR LOWER(%[1]s) REGEXP '^[[:space:]]*null[[:space:]]*$'))", expression)
}

func summarizeIDs(ids []int64) string {
	const maxIDs = 20
	displayed := ids
	truncated := false
	if len(displayed) > maxIDs {
		displayed = displayed[:maxIDs]
		truncated = true
	}
	parts := make([]string, 0, len(displayed))
	for _, id := range displayed {
		parts = append(parts, fmt.Sprint(id))
	}
	result := strings.Join(parts, ",")
	if truncated {
		result += ",..."
	}
	return result
}
