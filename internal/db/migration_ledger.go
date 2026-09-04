package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type migrationLedgerColumnState struct {
	columnType   string
	nullable     string
	defaultValue sql.NullString
	extra        string
	charset      sql.NullString
	collation    sql.NullString
}

type migrationLedgerColumnContract struct {
	columnType   string
	nullable     string
	defaultValue sql.NullString
	extra        string
	charset      string
	collation    string
}

func ledgerDefault(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

var migrationLedgerContract = map[string]migrationLedgerColumnContract{
	"version": {
		columnType: "varchar(128)", nullable: "NO",
		charset: "utf8mb4", collation: "utf8mb4_unicode_ci",
	},
	"epoch":          {columnType: "bigint unsigned", nullable: "NO"},
	"description":    {columnType: "varchar(255)", nullable: "NO", charset: "utf8mb4", collation: "utf8mb4_unicode_ci"},
	"checksum":       {columnType: "char(64)", nullable: "NO", charset: "ascii", collation: "ascii_bin"},
	"dirty":          {columnType: "tinyint(1)", nullable: "NO"},
	"started_at":     {columnType: "datetime(6)", nullable: "NO"},
	"finished_at":    {columnType: "datetime(6)", nullable: "YES"},
	"compatible_min": {columnType: "bigint unsigned", nullable: "NO"},
	"compatible_max": {columnType: "bigint unsigned", nullable: "NO"},
	"last_error": {
		columnType: "text", nullable: "YES",
		charset: "utf8mb4", collation: "utf8mb4_unicode_ci",
	},
	"legacy_adopted": {columnType: "tinyint(1)", nullable: "NO", defaultValue: ledgerDefault("0")},
	"applied_at":     {columnType: "timestamp", nullable: "NO", defaultValue: ledgerDefault("CURRENT_TIMESTAMP")},
}

func (s *migrationSession) readMigrationLedgerColumns(ctx context.Context) (map[string]migrationLedgerColumnState, error) {
	rows, err := s.executor.QueryContext(ctx, `SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE,
		COLUMN_DEFAULT, EXTRA, CHARACTER_SET_NAME, COLLATION_NAME
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'
		ORDER BY ORDINAL_POSITION`)
	if err != nil {
		return nil, fmt.Errorf("inspect migration ledger column definitions: %w", err)
	}
	defer rows.Close()
	columns := make(map[string]migrationLedgerColumnState)
	for rows.Next() {
		var name string
		var state migrationLedgerColumnState
		if err := rows.Scan(&name, &state.columnType, &state.nullable,
			&state.defaultValue, &state.extra, &state.charset, &state.collation); err != nil {
			return nil, fmt.Errorf("scan migration ledger column definition: %w", err)
		}
		columns[name] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration ledger column definitions: %w", err)
	}
	return columns, nil
}

// migrationLedgerDefinitionProblems validates either the finalized W04 ledger
// or one of the narrowly supported adoption states. Adoption accepts only the
// exact legacy version/applied_at ledger plus nullable metadata columns that a
// previous W04 adoption attempt may already have appended.
func (s *migrationSession) migrationLedgerDefinitionProblems(
	ctx context.Context,
	columns map[string]migrationLedgerColumnState,
	adoption bool,
) ([]string, error) {
	problems := make([]string, 0)

	var engine, tableCollation sql.NullString
	if err := s.executor.QueryRowContext(ctx, `SELECT ENGINE, TABLE_COLLATION
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'schema_migrations'
			AND TABLE_TYPE = 'BASE TABLE'`).Scan(&engine, &tableCollation); err != nil {
		if err == sql.ErrNoRows {
			return []string{"schema_migrations 不是可支持的 BASE TABLE"}, nil
		}
		return nil, fmt.Errorf("inspect migration ledger table definition: %w", err)
	}
	if !strings.EqualFold(engine.String, "InnoDB") {
		problems = append(problems, "schema_migrations 必须使用 InnoDB")
	}
	if !strings.EqualFold(tableCollation.String, "utf8mb4_unicode_ci") {
		problems = append(problems, "schema_migrations 必须使用 utf8mb4_unicode_ci")
	}

	for name := range columns {
		if _, known := migrationLedgerContract[name]; !known {
			problems = append(problems, "schema_migrations 包含不支持的列 "+name)
		}
	}
	for _, required := range []string{"version", "applied_at"} {
		if _, exists := columns[required]; !exists {
			problems = append(problems, "schema_migrations 缺少旧 ledger 必需列 "+required)
		}
	}
	if !adoption {
		for _, required := range completeLedgerColumns {
			if _, exists := columns[required]; !exists {
				problems = append(problems, "schema_migrations 缺少列 "+required)
			}
		}
	}

	for name, actual := range columns {
		expected, known := migrationLedgerContract[name]
		if !known {
			continue
		}
		if normalizeColumnType(actual.columnType) != normalizeColumnType(expected.columnType) {
			problems = append(problems, fmt.Sprintf(
				"schema_migrations.%s 类型为 %s，期望 %s", name, actual.columnType, expected.columnType))
		}
		if !strings.EqualFold(actual.nullable, expected.nullable) {
			allowNullableAdoptionColumn := adoption && name != "version" && name != "applied_at" &&
				strings.EqualFold(actual.nullable, "YES") && strings.EqualFold(expected.nullable, "NO")
			if !allowNullableAdoptionColumn {
				problems = append(problems, fmt.Sprintf(
					"schema_migrations.%s IS_NULLABLE=%s，期望 %s", name, actual.nullable, expected.nullable))
			}
		}
		if !sameLedgerDefault(actual.defaultValue, expected.defaultValue, adoption && name == "legacy_adopted") {
			problems = append(problems, fmt.Sprintf(
				"schema_migrations.%s 默认值不受支持", name))
		}
		if normalizeLedgerExtra(actual.extra) != normalizeLedgerExtra(expected.extra) {
			problems = append(problems, fmt.Sprintf(
				"schema_migrations.%s EXTRA=%s，期望 %s",
				name, printableValue(actual.extra), printableValue(expected.extra)))
		}
		if !sameNullableString(actual.charset, expected.charset) {
			problems = append(problems, fmt.Sprintf("schema_migrations.%s 字符集不受支持", name))
		}
		if !sameNullableString(actual.collation, expected.collation) {
			problems = append(problems, fmt.Sprintf("schema_migrations.%s 排序规则不受支持", name))
		}
	}

	snapshot, err := readSchemaSnapshot(ctx, s.executor)
	if err != nil {
		return nil, err
	}
	actualIndexes := snapshot.indexes[schemaMigrationsTable]
	expectedIndexes := []schemaIndexManifest{primaryIndex("version")}
	if !adoption {
		expectedIndexes = append(expectedIndexes, uniqueIndex("epoch"))
	}
	indexDiffs := compareIndexDefinitions(schemaMigrationsTable, actualIndexes, expectedIndexes, true)
	if adoption {
		// The unique epoch index is added near the end of adoption. It is a
		// valid interrupted state, but no other additional index is accepted.
		withEpoch := []schemaIndexManifest{primaryIndex("version"), uniqueIndex("epoch")}
		if len(indexDiffs) > 0 && len(compareIndexDefinitions(
			schemaMigrationsTable, actualIndexes, withEpoch, true)) == 0 {
			indexDiffs = nil
		}
	}
	problems = append(problems, indexDiffs...)
	for _, check := range snapshot.checks {
		if check.table == schemaMigrationsTable {
			problems = append(problems,
				"schema_migrations 包含不支持的 CHECK "+check.name)
		}
	}
	for _, trigger := range snapshot.triggers {
		if trigger.table == schemaMigrationsTable {
			problems = append(problems,
				"schema_migrations 包含不支持的触发器 "+trigger.name)
		}
	}
	for _, foreignKey := range snapshot.foreignKeys {
		if foreignKey.table == schemaMigrationsTable || foreignKey.referencedTable == schemaMigrationsTable {
			problems = append(problems,
				"schema_migrations 包含不支持的关联外键")
		}
	}
	sort.Strings(problems)
	return problems, nil
}

func sameLedgerDefault(actual, expected sql.NullString, allowLegacyAdoptedNull bool) bool {
	if allowLegacyAdoptedNull && !actual.Valid {
		return true
	}
	if actual.Valid != expected.Valid {
		return false
	}
	if !actual.Valid {
		return true
	}
	return normalizeLedgerDefault(actual.String) == normalizeLedgerDefault(expected.String)
}

func normalizeLedgerDefault(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "CURRENT_TIMESTAMP") || strings.EqualFold(value, "CURRENT_TIMESTAMP()") {
		return "CURRENT_TIMESTAMP"
	}
	return value
}

func normalizeLedgerExtra(value string) string {
	parts := strings.Fields(strings.ToLower(value))
	filtered := parts[:0]
	for _, part := range parts {
		if part != "default_generated" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, " ")
}

func sameNullableString(actual sql.NullString, expected string) bool {
	if expected == "" {
		return !actual.Valid || strings.TrimSpace(actual.String) == ""
	}
	return actual.Valid && strings.EqualFold(strings.TrimSpace(actual.String), expected)
}

func (s *migrationSession) adoptionMetadataProblems(
	ctx context.Context,
	columns map[string]migrationLedgerColumnState,
	applied []MigrationInfo,
) ([]string, error) {
	metadataColumns := map[string]func(MigrationInfo) (string, []any){
		"epoch": func(info MigrationInfo) (string, []any) {
			return "(epoch IS NULL OR epoch = ?)", []any{info.Epoch}
		},
		"description": func(info MigrationInfo) (string, []any) {
			return "(description IS NULL OR BINARY description = BINARY ?)", []any{info.Description}
		},
		"checksum": func(info MigrationInfo) (string, []any) {
			return "(checksum IS NULL OR checksum = ?)", []any{info.Checksum}
		},
		"dirty": func(MigrationInfo) (string, []any) {
			return "(dirty IS NULL OR dirty = 0)", nil
		},
		"started_at": func(MigrationInfo) (string, []any) {
			return "(started_at IS NULL OR started_at = applied_at)", nil
		},
		"finished_at": func(MigrationInfo) (string, []any) {
			return "(finished_at IS NULL OR finished_at = applied_at)", nil
		},
		"compatible_min": func(info MigrationInfo) (string, []any) {
			return "(compatible_min IS NULL OR compatible_min = ?)", []any{info.CompatibleMin}
		},
		"compatible_max": func(info MigrationInfo) (string, []any) {
			return "(compatible_max IS NULL OR compatible_max = ?)", []any{info.CompatibleMax}
		},
		"last_error": func(MigrationInfo) (string, []any) {
			return "last_error IS NULL", nil
		},
		"legacy_adopted": func(MigrationInfo) (string, []any) {
			return "(legacy_adopted IS NULL OR legacy_adopted = 1)", nil
		},
	}

	columnNames := make([]string, 0, len(metadataColumns))
	for name := range metadataColumns {
		if _, exists := columns[name]; exists {
			columnNames = append(columnNames, name)
		}
	}
	sort.Strings(columnNames)
	if len(columnNames) == 0 {
		return nil, nil
	}

	problems := make([]string, 0)
	for _, info := range applied {
		conditions := make([]string, 0, len(columnNames))
		args := make([]any, 0, len(columnNames)+1)
		for _, name := range columnNames {
			condition, values := metadataColumns[name](info)
			conditions = append(conditions, condition)
			args = append(args, values...)
		}
		args = append(args, info.Version)
		query := `SELECT EXISTS(SELECT 1 FROM schema_migrations
			WHERE NOT (` + strings.Join(conditions, " AND ") + `) AND version = ?)`
		var mismatch bool
		if err := s.executor.QueryRowContext(ctx, query, args...).Scan(&mismatch); err != nil {
			return nil, fmt.Errorf("inspect partial adoption metadata for %s: %w", info.Version, err)
		}
		if mismatch {
			problems = append(problems,
				"旧 ledger 的 "+info.Version+" 已有元数据与编译迁移目录不一致")
		}
	}
	return problems, nil
}
