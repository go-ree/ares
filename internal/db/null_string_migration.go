package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"ares/internal/tool"
)

const (
	legacyNullStringMigrationVersion = "20260902_001_cleanup_legacy_null_strings"
	migrationBatchSize               = 500
)

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

func legacyRequiredTextBackfillReadyBeforeSync() (bool, error) {
	prerequisites := map[string]map[string]struct{}{
		"apps": {
			"app_id":       {},
			"app_name":     {},
			"app_name_cn":  {},
			"dev_language": {},
			"updated_at":   {},
		},
		"app_configs": {
			"config_id":         {},
			"app_id":            {},
			"code_package_type": {},
			"updated_at":        {},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	rows, err := migrationDatabase().QueryContext(ctx, `SELECT TABLE_NAME, COLUMN_NAME
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME IN ('apps', 'app_configs')`)
	if err != nil {
		return false, fmt.Errorf("inspect legacy null-string migration prerequisites: %w", err)
	}
	defer rows.Close()

	found := make(map[string]map[string]struct{})
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return false, fmt.Errorf("scan legacy null-string migration prerequisite: %w", err)
		}
		if found[table] == nil {
			found[table] = make(map[string]struct{})
		}
		found[table][column] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate legacy null-string migration prerequisites: %w", err)
	}
	if len(found) == 0 {
		return false, nil
	}

	missing := make([]string, 0)
	for table, columns := range prerequisites {
		for column := range columns {
			if _, exists := found[table][column]; !exists {
				missing = append(missing, table+"."+column)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		hasBusinessRows := false
		for _, table := range []string{"apps", "app_configs"} {
			if found[table] == nil {
				continue
			}
			var tableHasRows bool
			if err := queryScalar(fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM `%s` LIMIT 1)", table), &tableHasRows); err != nil {
				return false, fmt.Errorf("inspect partial legacy table %s: %w", table, err)
			}
			if tableHasRows {
				hasBusinessRows = true
				break
			}
		}
		if !hasBusinessRows {
			// Sync2 creates tables one at a time. If an initial empty-database
			// boot stopped midway, let the ordinary schema initializer resume.
			return false, nil
		}
		return false, fmt.Errorf("cannot safely migrate partial legacy schema; missing prerequisites: %s", strings.Join(missing, ", "))
	}
	return true, nil
}

func migrateLegacyNullStrings() error {
	languageDefaults, err := loadMigrationLanguageDefaults()
	if err != nil {
		return err
	}
	if err := validateRequiredTextBackfills(languageDefaults); err != nil {
		return err
	}
	if err := backfillRequiredTextColumns(languageDefaults); err != nil {
		return err
	}
	if err := ensureTextColumnDefinitions(optionalTextColumns); err != nil {
		return err
	}
	for _, table := range optionalTextTables {
		if err := cleanOptionalTextTable(table); err != nil {
			return err
		}
	}
	if err := ensureTextColumnDefinitions(requiredTextColumns); err != nil {
		return err
	}
	return verifyCanonicalTextValues()
}

type migrationLanguageRule struct {
	Allowed []string `json:"allowed"`
	Default string   `json:"default"`
}

func loadMigrationLanguageDefaults() (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	rows, err := migrationDatabase().QueryContext(ctx, `SELECT dev_language, rules
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

func validateRequiredTextBackfills(languageDefaults map[string]string) error {
	invalidAppIDs, err := queryInt64s(`SELECT app_id FROM apps
		WHERE ` + legacyNullishSQL("app_name_cn") + `
			AND ` + legacyNullishSQL("app_name") + `
		ORDER BY app_id LIMIT 21`)
	if err != nil {
		return fmt.Errorf("validate app_name_cn backfill: %w", err)
	}
	if len(invalidAppIDs) > 0 {
		return fmt.Errorf("cannot backfill app_name_cn because app_name is also empty for app_ids=%s", summarizeIDs(invalidAppIDs))
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	rows, err := migrationDatabase().QueryContext(ctx, `SELECT c.config_id, a.dev_language
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

func backfillRequiredTextColumns(languageDefaults map[string]string) error {
	appWhere := legacyNullishSQL("app_name_cn")
	if err := updateIDsInBatches("apps", "app_id", appWhere,
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
		lastID := int64(0)
		for {
			ids, err := queryInt64s(`SELECT c.config_id FROM app_configs c
				JOIN apps a ON a.app_id = c.app_id
				WHERE (`+where+`) AND c.config_id > ?
				ORDER BY c.config_id LIMIT `+fmt.Sprint(migrationBatchSize), language, lastID)
			if err != nil {
				return fmt.Errorf("select %s code_package_type backfill batch: %w", language, err)
			}
			if len(ids) == 0 {
				break
			}
			setArgs := []any{languageDefaults[language]}
			if err := updateRowsByIDs("app_configs", "config_id",
				"code_package_type = CASE WHEN "+legacyNullishSQL("code_package_type")+" THEN ? ELSE code_package_type END, updated_at = updated_at", setArgs, ids); err != nil {
				return fmt.Errorf("backfill %s code_package_type: %w", language, err)
			}
			lastID = ids[len(ids)-1]
		}
	}
	return nil
}

func cleanOptionalTextTable(spec optionalTextTableMigration) error {
	conditions := make([]string, 0, len(spec.columns))
	assignments := make([]string, 0, len(spec.columns)+1)
	for _, column := range spec.columns {
		condition := legacyNonNullTextSQL("`" + column + "`")
		conditions = append(conditions, condition)
		assignments = append(assignments,
			fmt.Sprintf("`%s` = CASE WHEN %s THEN NULL ELSE `%s` END", column, condition, column))
	}
	assignments = append(assignments, "updated_at = updated_at")
	return updateIDsInBatches(spec.table, spec.primaryKey, strings.Join(conditions, " OR "),
		strings.Join(assignments, ", "), nil)
}

func updateIDsInBatches(table, primaryKey, where, assignments string, assignmentArgs []any) error {
	total := int64(0)
	lastID := int64(0)
	for {
		ids, err := queryInt64s(fmt.Sprintf("SELECT `%s` FROM `%s` WHERE (%s) AND `%s` > ? ORDER BY `%s` LIMIT %d",
			primaryKey, table, where, primaryKey, primaryKey, migrationBatchSize), lastID)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			break
		}
		if err := updateRowsByIDs(table, primaryKey, assignments, assignmentArgs, ids); err != nil {
			return err
		}
		total += int64(len(ids))
		lastID = ids[len(ids)-1]
	}
	if total > 0 {
		slog.Info("normalized legacy null-string rows", "table", table, "rows", total)
	}
	return nil
}

func updateRowsByIDs(table, primaryKey, assignments string, assignmentArgs []any, ids []int64) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := fmt.Sprintf("UPDATE `%s` SET %s WHERE `%s` IN (%s)", table, assignments, primaryKey, placeholders)
	args := append([]any(nil), assignmentArgs...)
	for _, id := range ids {
		args = append(args, id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	if _, err := migrationDatabase().ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return nil
}

func ensureTextColumnDefinitions(columns []textColumnMigration) error {
	noBackslashEscapes, err := migrationUsesNoBackslashEscapes()
	if err != nil {
		return err
	}
	byTable := make(map[string][]string)
	for _, column := range columns {
		state, err := inspectTextColumn(column)
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
		ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
		_, err := migrationDatabase().ExecContext(ctx, fmt.Sprintf("ALTER TABLE `%s` %s", table, strings.Join(byTable[table], ", ")))
		cancel()
		if err != nil {
			return fmt.Errorf("normalize nullability for %s: %w", table, err)
		}
	}
	return nil
}

func migrationUsesNoBackslashEscapes() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	var sqlMode string
	if err := migrationDatabase().QueryRowContext(ctx, "SELECT @@SESSION.sql_mode").Scan(&sqlMode); err != nil {
		return false, fmt.Errorf("inspect SQL mode for schema migration: %w", err)
	}
	for _, mode := range strings.Split(sqlMode, ",") {
		if strings.EqualFold(strings.TrimSpace(mode), "NO_BACKSLASH_ESCAPES") {
			return true, nil
		}
	}
	return false, nil
}

func inspectTextColumn(column textColumnMigration) (*textColumnState, error) {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	var state textColumnState
	err := migrationDatabase().QueryRowContext(ctx, `SELECT COLUMN_TYPE, CHARACTER_SET_NAME,
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

func verifyCanonicalTextValues() error {
	if err := verifyTextColumns("required", requiredTextColumns, legacyNullishSQL); err != nil {
		return err
	}
	return verifyTextColumns("optional", optionalTextColumns, legacyNonNullTextSQL)
}

func verifyTextColumns(kind string, columns []textColumnMigration, condition func(string) string) error {
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
		if err := queryScalar(query, &exists); err != nil {
			return fmt.Errorf("verify %s text columns in %s: %w", kind, table, err)
		}
		if exists {
			return fmt.Errorf("%s text columns in %s still contain non-canonical empty values", kind, table)
		}
	}
	return nil
}

func queryInt64s(query string, args ...any) ([]int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	rows, err := migrationDatabase().QueryContext(ctx, query, args...)
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

func queryScalar(query string, target any, args ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout())
	defer cancel()
	return migrationDatabase().QueryRowContext(ctx, query, args...).Scan(target)
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
