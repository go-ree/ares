package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/entity"
)

func TestSchemaBootstrapCatalog(t *testing.T) {
	wantOrder := []string{
		entity.TableApps,
		entity.TableAppConfigs,
		entity.TableAppConfigDomains,
		entity.TableTaskRecord,
		entity.TableTaskRecordImages,
		entity.TablePipelines,
		entity.TablePipelineJobs,
		entity.TableEnvConfigs,
		entity.TableIntegrationSettings,
		entity.TableDevLanguageRules,
	}
	if len(schemaBootstrapTables) != len(wantOrder) {
		t.Fatalf("bootstrap table count = %d, want %d", len(schemaBootstrapTables), len(wantOrder))
	}

	seen := make(map[string]struct{}, len(schemaBootstrapTables))
	for index, table := range schemaBootstrapTables {
		if table.name != wantOrder[index] {
			t.Errorf("bootstrap table %d = %s, want %s", index, table.name, wantOrder[index])
		}
		if _, duplicate := seen[table.name]; duplicate {
			t.Errorf("duplicate bootstrap table %s", table.name)
		}
		seen[table.name] = struct{}{}

		statement := compactBootstrapSQL(table.statement)
		if !strings.HasPrefix(statement, "CREATE TABLE IF NOT EXISTS "+table.name+" (") {
			t.Errorf("%s is not an idempotent explicit CREATE TABLE: %s", table.name, statement)
		}
		for _, required := range []string{"ENGINE=InnoDB", "DEFAULT CHARSET=utf8mb4", "COLLATE=utf8mb4_"} {
			if !strings.Contains(statement, required) {
				t.Errorf("%s missing %s", table.name, required)
			}
		}
		if strings.Contains(statement, "ares.") {
			t.Errorf("%s hard-codes the database name", table.name)
		}
		if strings.Contains(strings.ToLower(statement), "xorm") || strings.Contains(statement, "Sync") {
			t.Errorf("%s bootstrap depends on Xorm schema synchronization", table.name)
		}

		wantCollation := "COLLATE=utf8mb4_0900_ai_ci"
		if !strings.Contains(statement, wantCollation) {
			t.Errorf("%s collation does not match main@e2cfd2a: want %s", table.name, wantCollation)
		}
	}
}

func TestSchemaBootstrapCriticalDefinitions(t *testing.T) {
	statements := make(map[string]string, len(schemaBootstrapTables))
	for _, table := range schemaBootstrapTables {
		statements[table.name] = compactBootstrapSQL(table.statement)
	}

	wants := map[string][]string{
		entity.TableAppConfigs: {
			"env VARCHAR(100) NOT NULL",
			"KEY IDX_app_configs_app_id (app_id)",
		},
		entity.TableTaskRecord: {
			"env VARCHAR(255) NOT NULL",
			"pipeline_param JSON NULL",
		},
		entity.TablePipelineJobs: {
			"UNIQUE KEY UQE_pipelines_job_combination_code_package_type (code_package_type)",
			"FOREIGN KEY (ci_job_name) REFERENCES pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE",
			"FOREIGN KEY (cd_job_name) REFERENCES pipelines (job_name) ON DELETE RESTRICT ON UPDATE CASCADE",
		},
		entity.TableIntegrationSettings: {
			"config_data MEDIUMTEXT NOT NULL",
		},
		entity.TableDevLanguageRules: {
			"rules JSON NOT NULL",
		},
		entity.TableEnvConfigs: {
			"cluster_name VARCHAR(255) NOT NULL",
			"harbor_url VARCHAR(255) NOT NULL",
		},
	}
	for table, fragments := range wants {
		statement := statements[table]
		for _, fragment := range fragments {
			if !strings.Contains(statement, fragment) {
				t.Errorf("%s missing critical definition %q", table, fragment)
			}
		}
	}

	if strings.Contains(statements[entity.TableAppConfigs], "FOREIGN KEY") {
		t.Fatal("bootstrap must not add the init.sql-only app_configs foreign key")
	}
	if strings.Contains(statements[entity.TableTaskRecordImages], "KEY idx_task_id (task_id)") {
		t.Fatal("bootstrap must not add the init.sql-only task image index")
	}
	for table, fragments := range map[string][]string{
		entity.TableApps:       {"AUTO_INCREMENT=10000"},
		entity.TableAppConfigs: {"active_env", "uk_app_active_env"},
		entity.TableTaskRecord: {"jenkins_address", "engine_version", "workflow_version_id", "idx_task_workflow_poll"},
		entity.TableEnvConfigs: {"enabled", "sort_order"},
	} {
		for _, fragment := range fragments {
			if strings.Contains(statements[table], fragment) {
				t.Errorf("epoch-1 bootstrap %s unexpectedly contains later object %q", table, fragment)
			}
		}
	}
}

func TestBootstrapTableSetProblems(t *testing.T) {
	tests := []struct {
		name   string
		tables map[string]struct{}
		want   string
	}{
		{
			name:   "ledger only",
			tables: map[string]struct{}{schemaMigrationsTable: {}},
		},
		{
			name:   "interrupted managed prefix",
			tables: map[string]struct{}{schemaMigrationsTable: {}, entity.TableApps: {}, entity.TableAppConfigs: {}},
		},
		{
			name:   "known tables out of order remain a set-level candidate",
			tables: map[string]struct{}{schemaMigrationsTable: {}, entity.TableAppConfigs: {}},
		},
		{
			name:   "missing ledger",
			tables: map[string]struct{}{entity.TableApps: {}},
			want:   "schema_migrations 已存在",
		},
		{
			name: "unknown tables are sorted",
			tables: map[string]struct{}{
				schemaMigrationsTable: {},
				"z_operator_table":    {},
				"a_operator_table":    {},
			},
			want: "a_operator_table,z_operator_table",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problems := bootstrapTableSetProblems(test.tables)
			if test.want == "" {
				if len(problems) != 0 {
					t.Fatalf("unexpected problems: %v", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], test.want) {
				t.Fatalf("problems = %v, want substring %q", problems, test.want)
			}
		})
	}
}

func TestSchemaBootstrapLanguageRules(t *testing.T) {
	wantLanguages := []any{"java", "python", "node.js", "golang"}
	if got := bootstrapLanguageRuleNames(); !reflect.DeepEqual(got, wantLanguages) {
		t.Fatalf("bootstrap language names = %#v, want %#v", got, wantLanguages)
	}
	if len(schemaBootstrapLanguageRules) != 4 {
		t.Fatalf("bootstrap language rule count = %d, want 4", len(schemaBootstrapLanguageRules))
	}

	for _, item := range schemaBootstrapLanguageRules {
		var rule struct {
			Allowed []string `json:"allowed"`
			Default string   `json:"default"`
		}
		if err := json.Unmarshal([]byte(item.rules), &rule); err != nil {
			t.Fatalf("invalid %s rule JSON: %v", item.language, err)
		}
		foundDefault := false
		for _, allowed := range rule.Allowed {
			if allowed == rule.Default {
				foundDefault = true
				break
			}
		}
		if !foundDefault {
			t.Errorf("%s default %q is not allowed: %v", item.language, rule.Default, rule.Allowed)
		}
	}
}

func TestBootstrapEmptySchemaCreatesAndResumes(t *testing.T) {
	state := newBootstrapDriverState(schemaMigrationsTable)
	database := openBootstrapDriverDB(t, state)
	session := &migrationSession{executor: database, operationTimeout: time.Second}

	if err := session.bootstrapEmptySchema(); err != nil {
		t.Fatalf("bootstrap empty schema: %v", err)
	}
	state.mu.Lock()
	if len(state.tables) != len(schemaBootstrapTables)+1 {
		t.Errorf("created table count = %d, want %d", len(state.tables), len(schemaBootstrapTables)+1)
	}
	for _, rule := range schemaBootstrapLanguageRules {
		if state.languageRules[rule.language] != rule.rules {
			t.Errorf("language rule %s = %q, want %q", rule.language, state.languageRules[rule.language], rule.rules)
		}
	}
	createdAfterFirstRun := state.createCount
	state.languageRules["java"] = `{ "default":"jar", "allowed":["jar","war"] }`
	state.mu.Unlock()

	// A process may stop after all CREATE/seed statements but before epoch 1 is
	// recorded. Semantically identical JSON remains resumable and is not
	// overwritten merely because whitespace/key order differs.
	if err := session.bootstrapEmptySchema(); err != nil {
		t.Fatalf("resume completed bootstrap: %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.createCount != createdAfterFirstRun {
		t.Errorf("resume created %d additional tables", state.createCount-createdAfterFirstRun)
	}
	if got := state.languageRules["java"]; got != `{ "default":"jar", "allowed":["jar","war"] }` {
		t.Errorf("resume overwrote existing language rule: %s", got)
	}
}

func TestBootstrapEmptySchemaContinuesInterruptedPrefix(t *testing.T) {
	state := newBootstrapDriverState(schemaMigrationsTable, entity.TableApps, entity.TableAppConfigs)
	database := openBootstrapDriverDB(t, state)
	session := &migrationSession{executor: database, operationTimeout: time.Second}

	if err := session.bootstrapEmptySchema(); err != nil {
		t.Fatalf("resume interrupted bootstrap: %v", err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.createCount != len(schemaBootstrapTables)-2 {
		t.Fatalf("created %d tables, want %d", state.createCount, len(schemaBootstrapTables)-2)
	}
}

func TestBootstrapEmptySchemaFailsClosed(t *testing.T) {
	tests := []struct {
		name  string
		state *bootstrapDriverState
		want  string
	}{
		{
			name:  "unknown table",
			state: newBootstrapDriverState(schemaMigrationsTable, "operator_data"),
			want:  "未知表",
		},
		{
			name: "ledger row",
			state: func() *bootstrapDriverState {
				state := newBootstrapDriverState(schemaMigrationsTable)
				state.ledgerRows = 1
				return state
			}(),
			want: "schema_migrations 无记录",
		},
		{
			name: "business row",
			state: func() *bootstrapDriverState {
				state := newBootstrapDriverState(schemaMigrationsTable, entity.TableApps)
				state.rowCounts[entity.TableApps] = 1
				return state
			}(),
			want: "包含业务数据",
		},
		{
			name: "unknown language rule",
			state: func() *bootstrapDriverState {
				tables := []string{schemaMigrationsTable}
				for _, table := range schemaBootstrapTables[:10] {
					tables = append(tables, table.name)
				}
				state := newBootstrapDriverState(tables...)
				state.languageRules["rust"] = `{"allowed":["binary"],"default":"binary"}`
				return state
			}(),
			want: "未知语言规则",
		},
		{
			name: "malformed known language rule",
			state: func() *bootstrapDriverState {
				tables := []string{schemaMigrationsTable}
				for _, table := range schemaBootstrapTables {
					tables = append(tables, table.name)
				}
				state := newBootstrapDriverState(tables...)
				state.languageRules["java"] = `{"allowed":["jar"],"default":"jar"}`
				return state
			}(),
			want: "语义不匹配",
		},
		{
			name: "soft deleted known language rule",
			state: func() *bootstrapDriverState {
				tables := []string{schemaMigrationsTable}
				for _, table := range schemaBootstrapTables {
					tables = append(tables, table.name)
				}
				state := newBootstrapDriverState(tables...)
				state.languageRules["java"] = schemaBootstrapLanguageRules[0].rules
				state.deletedRules["java"] = true
				return state
			}(),
			want: "已软删除",
		},
		{
			name:  "out of order managed table",
			state: newBootstrapDriverState(schemaMigrationsTable, entity.TableAppConfigs),
			want:  "连续前缀",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := openBootstrapDriverDB(t, test.state)
			session := &migrationSession{executor: database, operationTimeout: time.Second}
			err := session.bootstrapEmptySchema()
			if !errors.Is(err, ErrSchemaState) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("bootstrap error = %v, want schema state containing %q", err, test.want)
			}
			test.state.mu.Lock()
			defer test.state.mu.Unlock()
			if test.state.createCount != 0 || test.state.insertCount != 0 {
				t.Fatalf("fail-closed bootstrap wrote create=%d insert=%d", test.state.createCount, test.state.insertCount)
			}
		})
	}
}

const bootstrapDriverName = "ares-schema-bootstrap-test"

var (
	registerBootstrapDriver sync.Once
	bootstrapDriverSequence atomic.Uint64
	bootstrapDriverStates   sync.Map
)

type bootstrapDriverState struct {
	mu            sync.Mutex
	tables        map[string]struct{}
	rowCounts     map[string]int64
	languageRules map[string]string
	deletedRules  map[string]bool
	ledgerRows    int64
	createCount   int
	insertCount   int
}

func newBootstrapDriverState(tables ...string) *bootstrapDriverState {
	state := &bootstrapDriverState{
		tables:        make(map[string]struct{}, len(tables)),
		rowCounts:     make(map[string]int64),
		languageRules: make(map[string]string),
		deletedRules:  make(map[string]bool),
	}
	for _, table := range tables {
		state.tables[table] = struct{}{}
	}
	return state
}

func openBootstrapDriverDB(t *testing.T, state *bootstrapDriverState) *sql.DB {
	t.Helper()
	registerBootstrapDriver.Do(func() {
		sql.Register(bootstrapDriverName, bootstrapSQLDriver{})
	})
	dsn := fmt.Sprintf("%s-%d", t.Name(), bootstrapDriverSequence.Add(1))
	bootstrapDriverStates.Store(dsn, state)
	database, err := sql.Open(bootstrapDriverName, dsn)
	if err != nil {
		t.Fatalf("open bootstrap test database: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
		bootstrapDriverStates.Delete(dsn)
	})
	return database
}

type bootstrapSQLDriver struct{}

func (bootstrapSQLDriver) Open(name string) (driver.Conn, error) {
	value, exists := bootstrapDriverStates.Load(name)
	if !exists {
		return nil, fmt.Errorf("unknown bootstrap test database %s", name)
	}
	return &bootstrapSQLConn{state: value.(*bootstrapDriverState)}, nil
}

type bootstrapSQLConn struct {
	state *bootstrapDriverState
}

func (*bootstrapSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by the bootstrap test driver")
}
func (*bootstrapSQLConn) Close() error { return nil }
func (*bootstrapSQLConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c *bootstrapSQLConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	compact := compactBootstrapSQL(query)
	switch {
	case compact == "SHOW CREATE TABLE `apps`":
		return &bootstrapDriverRows{
			columns: []string{"Table", "Create Table"},
			values:  [][]driver.Value{{"apps", schemaBootstrapTables[0].statement}},
		}, nil
	case strings.HasPrefix(compact, "SELECT t.TABLE_NAME, t.TABLE_TYPE, t.ENGINE, c.CHARACTER_SET_NAME, t.TABLE_COLLATION, t.AUTO_INCREMENT FROM information_schema.TABLES t"):
		return c.schemaTableRows(), nil
	case strings.HasPrefix(compact, "SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, EXTRA, GENERATION_EXPRESSION, CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS"):
		return c.schemaColumnRows(), nil
	case strings.HasPrefix(compact, "SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME, SUB_PART, EXPRESSION, COLLATION, INDEX_TYPE, IS_VISIBLE FROM information_schema.STATISTICS"):
		return c.schemaIndexRows(), nil
	case strings.HasPrefix(compact, "SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME, (BINARY k.REFERENCED_TABLE_SCHEMA = BINARY DATABASE()), k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE FROM information_schema.KEY_COLUMN_USAGE"):
		return c.schemaForeignKeyRows(), nil
	case strings.HasPrefix(compact, "SELECT tc.TABLE_NAME, tc.CONSTRAINT_NAME FROM information_schema.TABLE_CONSTRAINTS"):
		return &bootstrapDriverRows{columns: []string{"TABLE_NAME", "CONSTRAINT_NAME"}}, nil
	case strings.HasPrefix(compact, "SELECT EVENT_OBJECT_TABLE, TRIGGER_NAME FROM information_schema.TRIGGERS"):
		return &bootstrapDriverRows{columns: []string{"EVENT_OBJECT_TABLE", "TRIGGER_NAME"}}, nil
	case strings.HasPrefix(compact, "SELECT EVENT_NAME FROM information_schema.EVENTS"):
		return &bootstrapDriverRows{columns: []string{"EVENT_NAME"}}, nil
	case strings.HasPrefix(compact, "SELECT ROUTINE_NAME, ROUTINE_TYPE FROM information_schema.ROUTINES"):
		return &bootstrapDriverRows{columns: []string{"ROUTINE_NAME", "ROUTINE_TYPE"}}, nil
	case compact == "SELECT dev_language, rules, deleted_at FROM dev_language_rules ORDER BY dev_language":
		languages := make([]string, 0, len(c.state.languageRules))
		for language := range c.state.languageRules {
			languages = append(languages, language)
		}
		sort.Strings(languages)
		values := make([][]driver.Value, 0, len(languages))
		for _, language := range languages {
			var deletedAt driver.Value
			if c.state.deletedRules[language] {
				deletedAt = time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
			}
			values = append(values, []driver.Value{language, []byte(c.state.languageRules[language]), deletedAt})
		}
		return &bootstrapDriverRows{columns: []string{"dev_language", "rules", "deleted_at"}, values: values}, nil
	case strings.Contains(compact, "FROM information_schema.TABLES"):
		names := make([]string, 0, len(c.state.tables))
		for table := range c.state.tables {
			names = append(names, table)
		}
		sort.Strings(names)
		values := make([][]driver.Value, 0, len(names))
		for _, name := range names {
			values = append(values, []driver.Value{name})
		}
		return &bootstrapDriverRows{columns: []string{"TABLE_NAME"}, values: values}, nil
	case compact == "SELECT COUNT(*) FROM schema_migrations":
		return singleBootstrapDriverValue("COUNT(*)", c.state.ledgerRows), nil
	case strings.HasPrefix(compact, "SELECT EXISTS(SELECT 1 FROM `"):
		table, err := bootstrapQueryTable(compact)
		if err != nil {
			return nil, err
		}
		hasRows := int64(0)
		if c.state.rowCounts[table] > 0 {
			hasRows = 1
		}
		return singleBootstrapDriverValue("EXISTS", hasRows), nil
	default:
		return nil, fmt.Errorf("unexpected bootstrap test query: %s", compact)
	}
}

func (c *bootstrapSQLConn) schemaTableRows() driver.Rows {
	names := make([]string, 0, len(c.state.tables))
	for table := range c.state.tables {
		names = append(names, table)
	}
	sort.Strings(names)
	values := make([][]driver.Value, 0, len(names))
	for _, table := range names {
		var autoIncrement driver.Value
		values = append(values, []driver.Value{
			table, "BASE TABLE", "InnoDB", "utf8mb4", bootstrapTestCollation(table), autoIncrement,
		})
	}
	return &bootstrapDriverRows{
		columns: []string{
			"TABLE_NAME", "TABLE_TYPE", "ENGINE", "CHARACTER_SET_NAME", "TABLE_COLLATION", "AUTO_INCREMENT",
		},
		values: values,
	}
}

func (c *bootstrapSQLConn) schemaColumnRows() driver.Rows {
	values := make([][]driver.Value, 0)
	for _, table := range sortedStringKeys(epoch1SemanticSchemaManifest.tables) {
		if _, exists := c.state.tables[table]; !exists {
			continue
		}
		manifest := epoch1SemanticSchemaManifest.tables[table]
		for _, column := range manifest.columns {
			definition := manifest.critical[column]
			var defaultValue driver.Value
			if definition.defaultValue.Valid {
				defaultValue = definition.defaultValue.String
			}
			var generation driver.Value
			if definition.generation != "" {
				generation = definition.generation
			}
			var charset, collation driver.Value
			columnType := strings.ToLower(definition.columnType)
			if strings.HasPrefix(columnType, "char") || strings.HasPrefix(columnType, "varchar") ||
				strings.Contains(columnType, "text") {
				charset = "utf8mb4"
				collation = bootstrapTestCollation(table)
			}
			values = append(values, []driver.Value{
				table, column, definition.columnType, definition.nullable,
				defaultValue, definition.extraExact, generation, charset, collation,
			})
		}
	}
	return &bootstrapDriverRows{
		columns: []string{
			"TABLE_NAME", "COLUMN_NAME", "COLUMN_TYPE", "IS_NULLABLE",
			"COLUMN_DEFAULT", "EXTRA", "GENERATION_EXPRESSION",
			"CHARACTER_SET_NAME", "COLLATION_NAME",
		},
		values: values,
	}
}

func (c *bootstrapSQLConn) schemaIndexRows() driver.Rows {
	values := make([][]driver.Value, 0)
	for _, table := range sortedStringKeys(epoch1SemanticSchemaManifest.tables) {
		if _, exists := c.state.tables[table]; !exists {
			continue
		}
		for indexNumber, index := range epoch1SemanticSchemaManifest.tables[table].indexes {
			name := fmt.Sprintf("bootstrap_index_%d", indexNumber)
			if index.primary {
				name = "PRIMARY"
			}
			nonUnique := int64(1)
			if index.unique {
				nonUnique = 0
			}
			for sequence, column := range index.columns {
				values = append(values, []driver.Value{
					table, name, nonUnique, int64(sequence + 1), column, nil, nil,
					"A", index.indexType, index.visible,
				})
			}
		}
	}
	return &bootstrapDriverRows{
		columns: []string{
			"TABLE_NAME", "INDEX_NAME", "NON_UNIQUE", "SEQ_IN_INDEX",
			"COLUMN_NAME", "SUB_PART", "EXPRESSION", "COLLATION", "INDEX_TYPE", "IS_VISIBLE",
		},
		values: values,
	}
}

func (c *bootstrapSQLConn) schemaForeignKeyRows() driver.Rows {
	values := make([][]driver.Value, 0)
	for index, foreignKey := range epoch1SemanticSchemaManifest.foreignKeys {
		if _, exists := c.state.tables[foreignKey.table]; !exists {
			continue
		}
		for sequence := range foreignKey.columns {
			values = append(values, []driver.Value{
				foreignKey.table, fmt.Sprintf("bootstrap_fk_%d", index), int64(sequence + 1),
				foreignKey.columns[sequence], foreignKey.referencedTable, true,
				foreignKey.referencedColumns[sequence], foreignKey.updateRule, foreignKey.deleteRule,
			})
		}
	}
	return &bootstrapDriverRows{
		columns: []string{
			"TABLE_NAME", "CONSTRAINT_NAME", "ORDINAL_POSITION", "COLUMN_NAME",
			"REFERENCED_TABLE_NAME", "REFERENCED_CURRENT", "REFERENCED_COLUMN_NAME", "UPDATE_RULE", "DELETE_RULE",
		},
		values: values,
	}
}

func bootstrapTestCollation(table string) string {
	if table == schemaMigrationsTable {
		return "utf8mb4_unicode_ci"
	}
	for index, candidate := range schemaBootstrapTables {
		if candidate.name == table {
			if index < 10 {
				return "utf8mb4_0900_ai_ci"
			}
			return "utf8mb4_unicode_ci"
		}
	}
	return "utf8mb4_unicode_ci"
}

func (c *bootstrapSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	compact := compactBootstrapSQL(query)
	if strings.HasPrefix(compact, "CREATE TABLE IF NOT EXISTS ") {
		fields := strings.Fields(compact)
		if len(fields) < 6 {
			return nil, fmt.Errorf("invalid bootstrap CREATE: %s", compact)
		}
		c.state.tables[fields[5]] = struct{}{}
		c.state.createCount++
		return driver.RowsAffected(1), nil
	}
	if strings.HasPrefix(compact, "INSERT INTO dev_language_rules (dev_language, rules) SELECT ?, ? WHERE NOT EXISTS") {
		if len(args) != 3 {
			return nil, fmt.Errorf("language rule insert has %d arguments", len(args))
		}
		language, languageOK := args[0].Value.(string)
		rules, rulesOK := args[1].Value.(string)
		lookup, lookupOK := args[2].Value.(string)
		if !languageOK || !rulesOK || !lookupOK || lookup != language {
			return nil, errors.New("language rule arguments must be strings")
		}
		if _, exists := c.state.languageRules[language]; !exists {
			c.state.languageRules[language] = rules
		}
		c.state.insertCount++
		return driver.RowsAffected(1), nil
	}
	return nil, fmt.Errorf("unexpected bootstrap test exec: %s", compact)
}

func bootstrapQueryTable(query string) (string, error) {
	start := strings.Index(query, "`")
	if start < 0 {
		return "", fmt.Errorf("bootstrap row query has no table: %s", query)
	}
	end := strings.Index(query[start+1:], "`")
	if end < 0 {
		return "", fmt.Errorf("bootstrap row query has unterminated table: %s", query)
	}
	return query[start+1 : start+1+end], nil
}

func singleBootstrapDriverValue(column string, value driver.Value) driver.Rows {
	return &bootstrapDriverRows{columns: []string{column}, values: [][]driver.Value{{value}}}
}

type bootstrapDriverRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *bootstrapDriverRows) Columns() []string { return r.columns }
func (*bootstrapDriverRows) Close() error        { return nil }
func (r *bootstrapDriverRows) Next(destination []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(destination, r.values[r.index])
	r.index++
	return nil
}

func compactBootstrapSQL(statement string) string {
	return strings.Join(strings.Fields(statement), " ")
}
