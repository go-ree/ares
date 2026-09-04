package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestVersionedSchemaMigrationChecksumIsStable(t *testing.T) {
	migration := migrationByVersion(versionedSchemaMigrationVersion)
	if migration == nil {
		t.Fatal("epoch 4 migration is absent from schemaMigrations")
	}
	if migration.epoch != 4 {
		t.Fatalf("epoch = %d, want 4", migration.epoch)
	}
	const want = "0301b14dea0c3dacf2260dcdfd28fa2da486a596308ae43ccfeec47cb5638e01"
	if got := migration.checksum(); got != want {
		t.Fatalf("epoch 4 checksum = %s, want %s; published payload must remain immutable", got, want)
	}
}

func TestCurrentSemanticSchemaManifestOwnsFourteenTables(t *testing.T) {
	if got := len(epoch4SemanticSchemaManifest.tables); got != 14 {
		t.Fatalf("managed table count = %d, want 14", got)
	}
	for table, manifest := range epoch4SemanticSchemaManifest.tables {
		if len(manifest.columns) == 0 {
			t.Errorf("%s has no declared columns", table)
			continue
		}
		seen := make(map[string]struct{}, len(manifest.columns))
		for _, column := range manifest.columns {
			if _, duplicate := seen[column]; duplicate {
				t.Errorf("%s declares duplicate column %s", table, column)
			}
			seen[column] = struct{}{}
			definition, defined := manifest.critical[column]
			if !defined {
				t.Errorf("%s.%s has no semantic definition", table, column)
				continue
			}
			if definition.columnType == "" || definition.nullable == "" ||
				!definition.checkDefault || !definition.checkExtra {
				t.Errorf("%s.%s has an incomplete semantic definition: %+v", table, column, definition)
			}
		}
		if len(manifest.critical) != len(manifest.columns) {
			t.Errorf("%s has %d column definitions for %d managed columns",
				table, len(manifest.critical), len(manifest.columns))
		}
	}
}

func TestParseCreateTableAutoIncrementIgnoresQuotedContent(t *testing.T) {
	tests := []struct {
		name      string
		statement string
		want      uint64
	}{
		{
			name: "real table option",
			statement: "CREATE TABLE `apps` (`id` int NOT NULL AUTO_INCREMENT, PRIMARY KEY (`id`)) " +
				"ENGINE=InnoDB AUTO_INCREMENT=10000 COMMENT='AUTO_INCREMENT=1'",
			want: 10000,
		},
		{
			name: "comment cannot forge option",
			statement: "CREATE TABLE `apps` (`id` int NOT NULL AUTO_INCREMENT, PRIMARY KEY (`id`)) " +
				"ENGINE=InnoDB COMMENT='AUTO_INCREMENT=10000'",
			want: 1,
		},
		{
			name: "escaped comment quote cannot forge option",
			statement: "CREATE TABLE `apps` (`id` int NOT NULL AUTO_INCREMENT, PRIMARY KEY (`id`)) " +
				`ENGINE=InnoDB COMMENT='operator\'s AUTO_INCREMENT=10000'`,
			want: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseCreateTableAutoIncrement(test.statement)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("AUTO_INCREMENT = %d, want %d", got, test.want)
			}
		})
	}
}

func TestEpochSchemaManifestsAreCompleteAndIndependent(t *testing.T) {
	wants := []struct {
		name     string
		manifest semanticSchemaManifest
		tables   int
	}{
		{"epoch1", epoch1SemanticSchemaManifest, 10},
		{"epoch2", epoch2SemanticSchemaManifest, 14},
		{"epoch3", epoch3SemanticSchemaManifest, 14},
		{"epoch4", epoch4SemanticSchemaManifest, 14},
	}
	for _, want := range wants {
		t.Run(want.name, func(t *testing.T) {
			if len(want.manifest.tables) != want.tables {
				t.Fatalf("tables = %d, want %d", len(want.manifest.tables), want.tables)
			}
			if !want.manifest.strictTables || !want.manifest.strictColumns ||
				!want.manifest.strictIndexes || !want.manifest.strictForeignKeys ||
				!want.manifest.strictSchemaObjects {
				t.Fatalf("manifest is not exact: %+v", want.manifest)
			}
			for tableName, table := range want.manifest.tables {
				if len(table.columns) == 0 || len(table.columns) != len(table.critical) {
					t.Errorf("%s has incomplete columns: %d/%d", tableName, len(table.columns), len(table.critical))
				}
				if table.engine == "" || table.charset == "" || table.collation == "" {
					t.Errorf("%s has incomplete table encoding contract: %+v", tableName, table)
				}
				for columnName, definition := range table.critical {
					character := isCharacterColumnType(definition.columnType)
					if character && (definition.charset == "" || definition.collation == "") {
						t.Errorf("%s.%s has incomplete character encoding contract: %+v",
							tableName, columnName, definition)
					}
					if !character && (definition.charset != "" || definition.collation != "") {
						t.Errorf("%s.%s unexpectedly has character encoding metadata: %+v",
							tableName, columnName, definition)
					}
				}
			}
		})
	}

	clone := cloneSemanticSchemaManifest(epoch2SemanticSchemaManifest)
	definition := clone.tables["apps"].critical["app_name"]
	definition.columnType = "text"
	clone.tables["apps"].critical["app_name"] = definition
	clone.tables["apps"].indexes[0].columns[0] = "changed"
	if epoch2SemanticSchemaManifest.tables["apps"].critical["app_name"].columnType != "varchar(255)" ||
		epoch2SemanticSchemaManifest.tables["apps"].indexes[0].columns[0] != "app_id" {
		t.Fatal("deep-cloned manifest aliases a published epoch")
	}
}

func TestPublishedEpochManifestDigestsAreStable(t *testing.T) {
	wants := map[uint64]string{
		1: "ebc035f030b97548f6e51878616857ae02ad267cede51105483556e295288042",
		2: "8f57e0ea189e5c8a4bb5517a7749fce594cd1d7f6a406215d255dbb18ec0f98a",
		3: "f237bba7a8d41b55f67d5fd1b3eac459247ba20bc5322555ee6e537c18100aa9",
		4: "3777439f7d9f0dfe812f586e63dc4a1812713ba91bb8e4e548995db7e778c4fc",
	}
	for epoch, manifest := range publishedEpochSchemaManifests() {
		if got := semanticSchemaManifestDigest(manifest); got != wants[epoch] {
			t.Errorf("epoch %d manifest digest = %s, want %s; published epoch snapshots are immutable",
				epoch, got, wants[epoch])
		}
	}
}

func TestPublishedEpochDataContractDigestsAreStable(t *testing.T) {
	if got := len(epochDataContractCatalog); got != int(ApplicationSchemaEpoch) {
		t.Fatalf("data-contract epoch count = %d, want %d", got, ApplicationSchemaEpoch)
	}
	wants := map[uint64]string{
		1: "5b66874d093fc79ba55432b99866f6709423cf8fb655a2434acc22d8b551a40e",
		2: "47cc55f7586403f63044f83c88094bef79cb430ace9c4b42f4c033642ca6468a",
		3: "47cc55f7586403f63044f83c88094bef79cb430ace9c4b42f4c033642ca6468a",
		4: "47cc55f7586403f63044f83c88094bef79cb430ace9c4b42f4c033642ca6468a",
	}
	for epoch := uint64(1); epoch <= ApplicationSchemaEpoch; epoch++ {
		got := stringListDigest(epochDataContractIDs(epoch))
		if got != wants[epoch] {
			t.Errorf("epoch %d data-contract digest = %s, want %s; retained invariants are append-only",
				epoch, got, wants[epoch])
		}
	}
	copyOfEpoch2 := epochDataContractIDs(2)
	copyOfEpoch2[0] = "mutated-by-caller"
	if epochDataContractIDs(2)[0] != canonicalTextValuesDataContractID {
		t.Fatal("data-contract catalog aliases a caller-owned slice")
	}
}

func publishedEpochSchemaManifests() map[uint64]semanticSchemaManifest {
	return map[uint64]semanticSchemaManifest{
		1: epoch1SemanticSchemaManifest,
		2: epoch2SemanticSchemaManifest,
		3: epoch3SemanticSchemaManifest,
		4: epoch4SemanticSchemaManifest,
	}
}

func semanticSchemaManifestDigest(manifest semanticSchemaManifest) string {
	digest := sha256.New()
	_, _ = fmt.Fprintf(digest, "flags|%t|%t|%t|%t|%t|%t\n",
		manifest.strictTables, manifest.strictColumns, manifest.strictIndexes,
		manifest.strictForeignKeys, manifest.strictSchemaObjects,
		manifest.requirePipelineCollationAlignment)
	for _, tableName := range sortedStringKeys(manifest.tables) {
		table := manifest.tables[tableName]
		_, _ = fmt.Fprintf(digest, "table|%q|%q|%q|%q|%d\n",
			tableName, strings.ToUpper(strings.TrimSpace(table.engine)),
			strings.ToLower(strings.TrimSpace(table.charset)),
			strings.ToLower(strings.TrimSpace(table.collation)), table.autoIncrementMin)
		declaredColumns := append([]string(nil), table.columns...)
		slices.Sort(declaredColumns)
		for _, columnName := range declaredColumns {
			_, _ = fmt.Fprintf(digest, "declared-column|%q\n", columnName)
		}
		for _, columnName := range sortedStringKeys(table.critical) {
			column := table.critical[columnName]
			defaultValue := column.defaultValue.String
			if column.defaultValue.Valid {
				defaultValue = normalizeSchemaDefault(defaultValue)
			}
			_, _ = fmt.Fprintf(digest,
				"column|%q|%q|%q|%q|%t|%q|%q|%q|%t|%q|%q|%t\n",
				columnName, normalizeColumnType(column.columnType), strings.ToUpper(column.nullable),
				defaultValue, column.defaultValue.Valid,
				normalizeSchemaExtra(column.extraContains), normalizeSchemaExtra(column.extraExact),
				normalizeGenerationExpression(column.generation), column.checkDefault,
				strings.ToLower(strings.TrimSpace(column.charset)),
				strings.ToLower(strings.TrimSpace(column.collation)),
				column.checkExtra)
		}
		indexCounts := indexManifestCounts(table.indexes)
		for _, key := range sortedStringKeys(indexCounts) {
			_, _ = fmt.Fprintf(digest, "index|%q|%d\n", key, indexCounts[key])
		}
	}
	foreignKeyCounts := foreignKeyManifestCounts(manifest.foreignKeys)
	for _, key := range sortedStringKeys(foreignKeyCounts) {
		_, _ = fmt.Fprintf(digest, "foreign-key|%q|%d\n", key, foreignKeyCounts[key])
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func stringListDigest(values []string) string {
	digest := sha256.New()
	for _, value := range values {
		_, _ = fmt.Fprintf(digest, "%q\n", value)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func TestFullColumnManifestDetectsNonKeyColumnDrift(t *testing.T) {
	expected := epoch4SemanticSchemaManifest.tables["apps"].critical["app_name"]
	actual := schemaColumnState{
		columnType: "int", nullable: "YES",
		defaultValue: sql.NullString{String: "1", Valid: true},
		extra:        "auto_increment",
	}
	diffs := compareColumnDefinition("apps", "app_name", actual, expected, true)
	joined := strings.Join(diffs, "\n")
	for _, fragment := range []string{"类型", "IS_NULLABLE", "默认值", "EXTRA"} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("non-key column drift does not report %q: %v", fragment, diffs)
		}
	}
}

func TestNormalizeColumnType(t *testing.T) {
	tests := map[string]string{
		" INT(11) ":           "int",
		"BIGINT(20) UNSIGNED": "bigint unsigned",
		"smallint(6)":         "smallint",
		"tinyint(1)":          "tinyint(1)",
		"VARCHAR(100)":        "varchar(100)",
		"double   precision":  "double precision",
	}
	for input, want := range tests {
		if got := normalizeColumnType(input); got != want {
			t.Errorf("normalizeColumnType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeGenerationExpression(t *testing.T) {
	inputs := []string{
		"if((`deleted_at` is null),`env`,NULL)",
		"IF ( deleted_at IS NULL, env, NULL )",
		"if(((deleted_at is null)),(env),(null))",
	}
	want := normalizeGenerationExpression("if(deleted_at is null,env,null)")
	for _, input := range inputs {
		if got := normalizeGenerationExpression(input); got != want {
			t.Errorf("normalizeGenerationExpression(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCompareIndexDefinitionsUsesOrderedColumnsWithoutNames(t *testing.T) {
	actual := []schemaIndexState{
		{unique: true, columns: []string{"app_id", "active_env"}},
	}
	expected := []schemaIndexManifest{
		uniqueIndex("app_id", "active_env"),
	}
	if diffs := compareIndexDefinitions("app_configs", actual, expected, true); len(diffs) != 0 {
		t.Fatalf("same semantic index differs: %v", diffs)
	}

	reversed := []schemaIndexManifest{
		uniqueIndex("active_env", "app_id"),
	}
	diffs := compareIndexDefinitions("app_configs", actual, reversed, true)
	if len(diffs) != 2 {
		t.Fatalf("ordered-column mismatch produced %d diffs, want 2: %v", len(diffs), diffs)
	}
}

func TestCompareIndexDefinitionsIncludesDirectionVisibilityAndType(t *testing.T) {
	expected := []schemaIndexManifest{regularIndex("engine_version", "task_id")}
	for name, actual := range map[string][]schemaIndexState{
		"descending": {{columns: []string{"engine_version", "task_id"}, orders: []string{"D", "A"}, indexType: "BTREE", visible: "YES"}},
		"invisible":  {{columns: []string{"engine_version", "task_id"}, orders: []string{"A", "A"}, indexType: "BTREE", visible: "NO"}},
		"hash":       {{columns: []string{"engine_version", "task_id"}, orders: []string{"A", "A"}, indexType: "HASH", visible: "YES"}},
	} {
		t.Run(name, func(t *testing.T) {
			if diffs := compareIndexDefinitions("task_record", actual, expected, true); len(diffs) != 2 {
				t.Fatalf("metadata drift produced %d diffs, want 2: %v", len(diffs), diffs)
			}
		})
	}
}

func TestForeignKeyNoActionAndRestrictAreEquivalent(t *testing.T) {
	left := foreignKeySemanticKey("child", []string{"parent_id"}, "parent", []string{"id"}, true, "NO ACTION", "restrict")
	right := foreignKeySemanticKey("child", []string{"parent_id"}, "parent", []string{"id"}, true, "RESTRICT", "NO ACTION")
	if left != right {
		t.Fatalf("NO ACTION and RESTRICT differ: %q != %q", left, right)
	}
}

func TestInboundForeignKeyOwnerMarkerIsUnambiguous(t *testing.T) {
	for _, test := range []struct {
		encoded, schema, table string
		inbound                bool
	}{
		{encoded: "app_configs", table: "app_configs"},
		{encoded: "\x00external.release\x00app_configs", schema: "external.release", table: "app_configs", inbound: true},
		{encoded: "\x00malformed", table: "\x00malformed"},
	} {
		schema, table, inbound := parseInboundForeignKeyOwner(test.encoded)
		if schema != test.schema || table != test.table || inbound != test.inbound {
			t.Errorf("parseInboundForeignKeyOwner(%q) = (%q, %q, %t), want (%q, %q, %t)",
				test.encoded, schema, table, inbound, test.schema, test.table, test.inbound)
		}
	}
}

func TestExactManifestRejectsExternalInboundForeignKeysToManagedTables(t *testing.T) {
	snapshot := schemaSnapshot{inboundForeignKeys: []schemaInboundForeignKeyState{
		{
			tableSchema: "external", table: "deployments", constraint: "fk_external_app",
			columns: []string{"app_id"}, referencedTable: "apps", referencedColumn: []string{"app_id"},
		},
		{
			tableSchema: "external", table: "audit", constraint: "fk_external_ledger",
			columns: []string{"version"}, referencedTable: schemaMigrationsTable, referencedColumn: []string{"version"},
		},
		{
			tableSchema: "external", table: "operator_data", constraint: "fk_operator_extra",
			columns: []string{"id"}, referencedTable: "operator_extra", referencedColumn: []string{"id"},
		},
	}}
	manifest := semanticSchemaManifest{
		strictForeignKeys: true,
		tables:            map[string]schemaTableManifest{"apps": {}},
	}
	diffs := compareSemanticSchema(snapshot, manifest)
	joined := strings.Join(diffs, "\n")
	for _, fragment := range []string{"external", "deployments", "fk_external_app", "schema_migrations", "fk_external_ledger", "反向引用"} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("inbound foreign-key drift does not report %q: %v", fragment, diffs)
		}
	}
	if strings.Contains(joined, "fk_operator_extra") {
		t.Fatalf("foreign key to a non-managed table was reported as an Ares inbound dependency: %v", diffs)
	}
}

func TestExactManifestRejectsUnknownTablesAndSchemaObjects(t *testing.T) {
	snapshot := schemaSnapshot{
		tables: map[string]schemaTableState{
			"managed": {engine: "InnoDB", collation: "utf8mb4_unicode_ci"},
			"extra":   {engine: "InnoDB", collation: "utf8mb4_unicode_ci"},
		},
		views: map[string]struct{}{"operator_view": {}},
		columns: map[string]map[string]schemaColumnState{
			"managed": {"id": {columnType: "int", nullable: "NO"}},
		},
		indexes:  map[string][]schemaIndexState{},
		checks:   []schemaNamedObjectState{{table: "managed", name: "positive_id"}},
		triggers: []schemaNamedObjectState{{table: "managed", name: "audit_write"}},
		events:   []string{"nightly"},
		routines: []schemaRoutineState{{name: "repair", kind: "PROCEDURE"}},
	}
	manifest := semanticSchemaManifest{
		strictTables: true, strictColumns: true, strictIndexes: true,
		strictForeignKeys: true, strictSchemaObjects: true,
		tables: map[string]schemaTableManifest{
			"managed": {
				columns: []string{"id"},
				critical: map[string]schemaColumnManifest{
					"id": columnDefinition("int", "NO", "", ""),
				},
			},
		},
	}
	joined := strings.Join(compareSemanticSchema(snapshot, manifest), "\n")
	for _, fragment := range []string{"extra", "operator_view", "positive_id", "audit_write", "nightly", "repair"} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("unknown object %q was not rejected: %s", fragment, joined)
		}
	}
}

func TestCompareIndexDefinitionsDistinguishesPrimaryFromUnique(t *testing.T) {
	actual := []schemaIndexState{
		{unique: true, columns: []string{"app_id"}},
	}
	expected := []schemaIndexManifest{
		primaryIndex("app_id"),
	}
	diffs := compareIndexDefinitions("apps", actual, expected, true)
	if len(diffs) != 2 {
		t.Fatalf("primary-to-unique drift produced %d diffs, want 2: %v", len(diffs), diffs)
	}
	joined := strings.Join(diffs, "\n")
	for _, fragment := range []string{"缺少主键", "manifest 外唯一索引"} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("primary-to-unique drift does not report %q: %v", fragment, diffs)
		}
	}
}

func TestCompareSemanticSchemaReportsSortedActionableDiffs(t *testing.T) {
	snapshot := schemaSnapshot{
		tables: map[string]schemaTableState{
			"example": {engine: "MyISAM", collation: "latin1_swedish_ci"},
		},
		columns: map[string]map[string]schemaColumnState{
			"example": {
				"actual_only": {columnType: "text", nullable: "YES", charset: "latin1", collation: "latin1_swedish_ci"},
				"id":          {columnType: "varchar(10)", nullable: "YES"},
			},
		},
		indexes: map[string][]schemaIndexState{},
	}
	manifest := semanticSchemaManifest{
		strictColumns: true,
		strictIndexes: true,
		tables: map[string]schemaTableManifest{
			"example": {
				columns:   []string{"id", "missing"},
				engine:    "InnoDB",
				charset:   "utf8mb4",
				collation: "utf8mb4_0900_ai_ci",
				critical: map[string]schemaColumnManifest{
					"id": columnDefinition("int", "NO", "auto_increment", ""),
				},
				indexes: []schemaIndexManifest{uniqueIndex("id")},
			},
		},
	}
	diffs := compareSemanticSchema(snapshot, manifest)
	if len(diffs) < 7 {
		t.Fatalf("got too few semantic diffs: %v", diffs)
	}
	if !slices.IsSorted(diffs) {
		t.Fatalf("diffs are not sorted: %v", diffs)
	}
	joined := strings.Join(diffs, "\n")
	for _, fragment := range []string{
		"请通过显式 migration",
		"manifest 外列",
		"期望 InnoDB",
		"期望 utf8mb4_0900_ai_ci",
	} {
		if !strings.Contains(joined, fragment) {
			t.Errorf("diffs do not contain actionable fragment %q: %v", fragment, diffs)
		}
	}
}

func TestPipelineCollationsMustMatchEvenWhenBothAreAllowed(t *testing.T) {
	snapshot := schemaSnapshot{columns: map[string]map[string]schemaColumnState{
		"pipelines": {
			"job_name": {charset: "utf8mb4", collation: "utf8mb4_unicode_ci"},
		},
		"pipelines_job_combination": {
			"ci_job_name": {charset: "utf8mb4", collation: "utf8mb4_unicode_ci"},
			"cd_job_name": {charset: "utf8mb4", collation: "utf8mb4_0900_ai_ci"},
		},
	}}
	if diffs := comparePipelineCollations(snapshot); len(diffs) != 1 {
		t.Fatalf("collation mismatch produced %d diffs, want 1: %v", len(diffs), diffs)
	}
	snapshot.columns["pipelines_job_combination"]["cd_job_name"] = schemaColumnState{
		charset: "utf8mb4", collation: "utf8mb4_unicode_ci",
	}
	if diffs := comparePipelineCollations(snapshot); len(diffs) != 0 {
		t.Fatalf("aligned pipeline collations differ: %v", diffs)
	}
}

func TestFinalizeSchemaDiffsSortsAndLimitsOutput(t *testing.T) {
	diffs := make([]string, 0, 80)
	for index := 79; index >= 0; index-- {
		diffs = append(diffs, fmt.Sprintf("差异-%03d", index))
	}
	got := finalizeSchemaDiffs(diffs)
	if len(got) != maxSchemaManifestDiffs {
		t.Fatalf("diff count = %d, want %d", len(got), maxSchemaManifestDiffs)
	}
	if got[0] != "差异-000" || got[maxSchemaManifestDiffs-2] != "差异-048" {
		t.Fatalf("visible diffs are not sorted/truncated as expected: first=%q last=%q",
			got[0], got[maxSchemaManifestDiffs-2])
	}
	if !strings.Contains(got[len(got)-1], "另有 31 项") {
		t.Fatalf("summary = %q, want omitted count 31", got[len(got)-1])
	}
}

func TestCurrentSchemaManifestAgainstMySQL84(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ARES_SCHEMA_MANIFEST_TEST_DSN"))
	if dsn == "" {
		t.Skip("set ARES_SCHEMA_MANIFEST_TEST_DSN to run the MySQL 8.4 manifest test")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	session := &migrationSession{executor: database, operationTimeout: 30 * time.Second}
	diffs, err := session.epoch4SchemaDiffs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 0 {
		t.Fatalf("MySQL schema differs from the epoch 4 semantic manifest:\n%s", strings.Join(diffs, "\n"))
	}
}
