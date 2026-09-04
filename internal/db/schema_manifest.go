package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const maxSchemaManifestDiffs = 50

var tableAutoIncrementPattern = regexp.MustCompile(`(?i)\bAUTO_INCREMENT\s*=\s*([0-9]+)\b`)

type semanticSchemaManifest struct {
	tables                            map[string]schemaTableManifest
	foreignKeys                       []schemaForeignKeyManifest
	strictTables                      bool
	strictColumns                     bool
	strictIndexes                     bool
	strictForeignKeys                 bool
	strictSchemaObjects               bool
	requirePipelineCollationAlignment bool
}

type schemaTableManifest struct {
	columns          []string
	critical         map[string]schemaColumnManifest
	indexes          []schemaIndexManifest
	engine           string
	charset          string
	collation        string
	autoIncrementMin uint64
}

type schemaColumnManifest struct {
	columnType    string
	nullable      string
	defaultValue  sql.NullString
	checkDefault  bool
	extraContains string
	extraExact    string
	checkExtra    bool
	generation    string
	charset       string
	collation     string
}

type schemaIndexManifest struct {
	primary   bool
	unique    bool
	columns   []string
	orders    []string
	indexType string
	visible   string
}

type schemaForeignKeyManifest struct {
	table             string
	columns           []string
	referencedTable   string
	referencedColumns []string
	updateRule        string
	deleteRule        string
}

type schemaSnapshot struct {
	tables             map[string]schemaTableState
	views              map[string]struct{}
	columns            map[string]map[string]schemaColumnState
	indexes            map[string][]schemaIndexState
	foreignKeys        []schemaForeignKeyState
	inboundForeignKeys []schemaInboundForeignKeyState
	checks             []schemaNamedObjectState
	triggers           []schemaNamedObjectState
	events             []string
	routines           []schemaRoutineState
}

type schemaTableState struct {
	engine        string
	charset       string
	collation     string
	autoIncrement sql.NullInt64
}

type schemaColumnState struct {
	columnType   string
	nullable     string
	defaultValue sql.NullString
	extra        string
	generation   string
	charset      string
	collation    string
}

type schemaIndexState struct {
	primary   bool
	unique    bool
	columns   []string
	orders    []string
	indexType string
	visible   string
}

type schemaNamedObjectState struct {
	table string
	name  string
}

type schemaRoutineState struct {
	name string
	kind string
}

type schemaForeignKeyState struct {
	table             string
	columns           []string
	referencedTable   string
	referencedColumns []string
	referencedCurrent bool
	updateRule        string
	deleteRule        string
}

type schemaInboundForeignKeyState struct {
	tableSchema      string
	table            string
	constraint       string
	columns          []string
	referencedTable  string
	referencedColumn []string
}

// epoch4SemanticSchemaManifest is the immutable full schema snapshot for W04.
// Later epochs must add their own manifest instead of mutating this value.
var epoch4SemanticSchemaManifest = semanticSchemaManifest{
	strictTables:                      true,
	strictColumns:                     true,
	strictIndexes:                     true,
	strictForeignKeys:                 true,
	strictSchemaObjects:               true,
	requirePipelineCollationAlignment: true,
	tables: map[string]schemaTableManifest{
		"apps": {
			columns:          columnNames("app_id app_name rundeck_app_name app_name_cn owner owner_cn dev_language description_cn git_url created_at updated_at deleted_at"),
			autoIncrementMin: 10000,
			critical: map[string]schemaColumnManifest{
				"app_id": columnDefinition("int", "NO", "auto_increment", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("app_id"),
			},
		},
		"app_configs": {
			columns: columnNames("config_id app_id env active_env code_package_type code_package_path code_package_name base_image pod_count limits_memory gpu_count probe_type probe_check_path probe_check_tcp_port probe_check_http_port probe_stop_check_http_port container_port pre_stop_type pre_stop_check_path pre_stop_command created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"config_id":  columnDefinition("int", "NO", "auto_increment", ""),
				"app_id":     columnDefinition("int", "NO", "", ""),
				"env":        columnDefinition("varchar(100)", "NO", "", ""),
				"active_env": columnDefinition("varchar(100)", "YES", "stored generated", "if(deleted_at is null,env,null)"),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("config_id"),
				regularIndex("app_id"),
				uniqueIndex("app_id", "active_env"),
			},
		},
		"app_config_domains": {
			columns: columnNames("id config_id host path created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"id":        columnDefinition("bigint", "NO", "auto_increment", ""),
				"config_id": columnDefinition("int", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("id"),
				regularIndex("config_id"),
				uniqueIndex("config_id", "host", "path"),
			},
		},
		"task_record": {
			columns: columnNames("task_id app_name rundeck_app_name branch env publisher ci_build_id cd_build_id pipeline_param status message ci_job_name cd_job_name jenkins_address auto_deploy products engine_version workflow_version_id created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"task_id":             columnDefinition("int", "NO", "auto_increment", ""),
				"env":                 columnDefinition("varchar(255)", "NO", "", ""),
				"pipeline_param":      columnDefinition("json", "YES", "", ""),
				"jenkins_address":     columnDefinition("text", "YES", "", ""),
				"engine_version":      columnDefinition("int", "NO", "", ""),
				"workflow_version_id": columnDefinition("bigint", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("task_id"),
				regularIndex("engine_version", "status", "deleted_at", "updated_at", "task_id"),
			},
		},
		"task_record_images": {
			columns: columnNames("id task_id img_type url created_at updated_at"),
			critical: map[string]schemaColumnManifest{
				"id":      columnDefinition("bigint", "NO", "auto_increment", ""),
				"task_id": columnDefinition("int", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("id"),
				uniqueIndex("task_id", "img_type"),
			},
		},
		"pipelines": {
			columns: columnNames("id job_name description_cn url created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"id":       columnDefinition("int", "NO", "auto_increment", ""),
				"job_name": columnDefinition("varchar(100)", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("id"),
				uniqueIndex("job_name"),
			},
		},
		"pipelines_job_combination": {
			columns: columnNames("id description_cn ci_job_name cd_job_name code_package_type created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"id":                columnDefinition("int", "NO", "auto_increment", ""),
				"ci_job_name":       columnDefinition("varchar(100)", "NO", "", ""),
				"cd_job_name":       columnDefinition("varchar(100)", "NO", "", ""),
				"code_package_type": columnDefinition("varchar(100)", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("id"),
				regularIndex("ci_job_name"),
				regularIndex("cd_job_name"),
				uniqueIndex("ci_job_name", "cd_job_name"),
				uniqueIndex("code_package_type"),
			},
		},
		"env_configs": {
			columns: columnNames("id env cluster_name description_cn enabled sort_order harbor_url harbor_project_name node_version maven_version created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"id":                  columnDefinition("int", "NO", "auto_increment", ""),
				"env":                 columnDefinition("varchar(100)", "NO", "", ""),
				"cluster_name":        columnDefinition("varchar(255)", "YES", "", ""),
				"enabled":             columnDefinition("tinyint(1)", "NO", "", ""),
				"sort_order":          columnDefinition("int", "NO", "", ""),
				"harbor_url":          columnDefinition("varchar(255)", "YES", "", ""),
				"harbor_project_name": columnDefinition("varchar(255)", "YES", "", ""),
				"node_version":        columnDefinition("varchar(255)", "YES", "", ""),
				"maven_version":       columnDefinition("varchar(255)", "YES", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("id"),
				uniqueIndex("env"),
			},
		},
		"dev_language_rules": {
			columns: columnNames("dev_language rules created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"dev_language": columnDefinition("varchar(100)", "NO", "", ""),
				"rules":        columnDefinition("json", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("dev_language"),
			},
		},
		"integration_settings": {
			columns: columnNames("provider config_data created_at updated_at"),
			critical: map[string]schemaColumnManifest{
				"provider":    columnDefinition("varchar(64)", "NO", "", ""),
				"config_data": columnDefinition("mediumtext", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("provider"),
			},
		},
		"release_workflows": {
			columns: columnNames("workflow_id name description created_at updated_at deleted_at"),
			critical: map[string]schemaColumnManifest{
				"workflow_id": columnDefinition("bigint", "NO", "auto_increment", ""),
				"name":        columnDefinition("varchar(120)", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("workflow_id"),
			},
		},
		"release_workflow_versions": {
			columns: columnNames("version_id workflow_id version spec checksum created_by created_at"),
			critical: map[string]schemaColumnManifest{
				"version_id":  columnDefinition("bigint", "NO", "auto_increment", ""),
				"workflow_id": columnDefinition("bigint", "NO", "", ""),
				"version":     columnDefinition("int", "NO", "", ""),
				"spec":        columnDefinition("json", "NO", "", ""),
				"checksum":    columnDefinition("char(64)", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("version_id"),
				uniqueIndex("workflow_id", "version"),
				regularIndex("workflow_id"),
			},
		},
		"app_config_workflows": {
			columns: columnNames("binding_id app_config_id workflow_id version_id revision created_at updated_at"),
			critical: map[string]schemaColumnManifest{
				"binding_id":    columnDefinition("bigint", "NO", "auto_increment", ""),
				"app_config_id": columnDefinition("int", "NO", "", ""),
				"workflow_id":   columnDefinition("bigint", "NO", "", ""),
				"version_id":    columnDefinition("bigint", "NO", "", ""),
				"revision":      columnDefinition("int", "NO", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("binding_id"),
				uniqueIndex("app_config_id"),
				regularIndex("workflow_id"),
				regularIndex("version_id"),
			},
		},
		"task_step_records": {
			columns: columnNames("step_record_id task_id workflow_version_id step_key name uses category position config timeout_seconds on_failure status attempt external_ref output message started_at finished_at created_at updated_at"),
			critical: map[string]schemaColumnManifest{
				"step_record_id":      columnDefinition("bigint", "NO", "auto_increment", ""),
				"task_id":             columnDefinition("int", "NO", "", ""),
				"workflow_version_id": columnDefinition("bigint", "NO", "", ""),
				"step_key":            columnDefinition("varchar(63)", "NO", "", ""),
				"position":            columnDefinition("int", "NO", "", ""),
				"config":              columnDefinition("json", "NO", "", ""),
				"status":              columnDefinition("varchar(32)", "NO", "", ""),
				"external_ref":        columnDefinition("json", "YES", "", ""),
				"output":              columnDefinition("json", "YES", "", ""),
			},
			indexes: []schemaIndexManifest{
				primaryIndex("step_record_id"),
				uniqueIndex("task_id", "step_key"),
				regularIndex("task_id", "position"),
				regularIndex("task_id", "status"),
				regularIndex("workflow_version_id"),
				regularIndex("status", "uses", "task_id"),
			},
		},
	},
	foreignKeys: []schemaForeignKeyManifest{
		{
			table: "pipelines_job_combination", columns: []string{"ci_job_name"},
			referencedTable: "pipelines", referencedColumns: []string{"job_name"},
			updateRule: "CASCADE", deleteRule: "RESTRICT",
		},
		{
			table: "pipelines_job_combination", columns: []string{"cd_job_name"},
			referencedTable: "pipelines", referencedColumns: []string{"job_name"},
			updateRule: "CASCADE", deleteRule: "RESTRICT",
		},
	},
}

func columnNames(value string) []string {
	return strings.Fields(value)
}

func columnDefinition(columnType, nullable, extraContains, generation string) schemaColumnManifest {
	return schemaColumnManifest{
		columnType: columnType, nullable: nullable,
		extraContains: extraContains, generation: generation,
	}
}

func uniqueIndex(columns ...string) schemaIndexManifest {
	return schemaIndexManifest{unique: true, columns: columns, indexType: "BTREE", visible: "YES"}
}

func primaryIndex(columns ...string) schemaIndexManifest {
	return schemaIndexManifest{primary: true, unique: true, columns: columns, indexType: "BTREE", visible: "YES"}
}

func regularIndex(columns ...string) schemaIndexManifest {
	return schemaIndexManifest{columns: columns, indexType: "BTREE", visible: "YES"}
}

func (s *migrationSession) verifySemanticSchema(label string, manifest semanticSchemaManifest) error {
	ctx, cancel := s.operationContext()
	defer cancel()
	snapshot, err := readSchemaSnapshot(ctx, s.executor)
	if err != nil {
		return err
	}
	diffs := compareSemanticSchema(snapshot, manifest)
	if len(diffs) == 0 {
		return nil
	}
	problems := make([]string, 0, len(diffs))
	for _, diff := range diffs {
		problems = append(problems, label+": "+diff)
	}
	return &SchemaStateError{Problems: problems}
}

func (s *migrationSession) verifySemanticSchemaStates(
	label string,
	states []semanticSchemaManifest,
) error {
	ctx, cancel := s.operationContext()
	defer cancel()
	snapshot, err := readSchemaSnapshot(ctx, s.executor)
	if err != nil {
		return err
	}
	var closest []string
	for _, state := range states {
		diffs := compareSemanticSchema(snapshot, state)
		if len(diffs) == 0 {
			return nil
		}
		if closest == nil || len(diffs) < len(closest) {
			closest = diffs
		}
	}
	problems := []string{fmt.Sprintf(
		"%s: 当前结构不属于 %d 个允许的精确语句边界状态", label, len(states))}
	for _, diff := range closest {
		problems = append(problems, label+": "+diff)
	}
	return &SchemaStateError{Problems: finalizeSchemaDiffs(problems)}
}

func readSchemaSnapshot(ctx context.Context, executor sqlExecutor) (schemaSnapshot, error) {
	snapshot := schemaSnapshot{
		tables:  make(map[string]schemaTableState),
		views:   make(map[string]struct{}),
		columns: make(map[string]map[string]schemaColumnState),
		indexes: make(map[string][]schemaIndexState),
	}
	if err := readSchemaTables(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	if _, exists := snapshot.tables["apps"]; exists {
		if err := readAppsAutoIncrement(ctx, executor, &snapshot); err != nil {
			return schemaSnapshot{}, err
		}
	}
	if err := readSchemaColumns(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	if err := readSchemaIndexes(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	if err := readSchemaForeignKeys(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	if err := readSchemaChecks(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	if err := readVisibleSchemaTriggers(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	if err := readVisibleSchemaEvents(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	if err := readVisibleSchemaRoutines(ctx, executor, &snapshot); err != nil {
		return schemaSnapshot{}, err
	}
	return snapshot, nil
}

func readAppsAutoIncrement(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	var tableName, createStatement string
	if err := executor.QueryRowContext(ctx, "SHOW CREATE TABLE `apps`").Scan(&tableName, &createStatement); err != nil {
		return fmt.Errorf("inspect apps AUTO_INCREMENT contract: %w", err)
	}
	value, err := parseCreateTableAutoIncrement(createStatement)
	if err != nil {
		return fmt.Errorf("inspect apps AUTO_INCREMENT contract: %w", err)
	}
	state := snapshot.tables["apps"]
	if value > uint64(^uint64(0)>>1) {
		return fmt.Errorf("apps AUTO_INCREMENT %d exceeds signed inspection range", value)
	}
	state.autoIncrement = sql.NullInt64{Int64: int64(value), Valid: true}
	snapshot.tables["apps"] = state
	return nil
}

func parseCreateTableAutoIncrement(createStatement string) (uint64, error) {
	options, err := createTableOptions(createStatement)
	if err != nil {
		return 0, err
	}
	unquoted, err := maskQuotedSQLText(options)
	if err != nil {
		return 0, err
	}
	match := tableAutoIncrementPattern.FindStringSubmatch(unquoted)
	if len(match) != 2 {
		// MySQL omits AUTO_INCREMENT=1 from SHOW CREATE TABLE. Treating an
		// absent table option as one is both deterministic and fail-closed for
		// the epoch-4 minimum of 10000.
		return 1, nil
	}
	value, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse AUTO_INCREMENT %q: %w", match[1], err)
	}
	return value, nil
}

func createTableOptions(statement string) (string, error) {
	depth := 0
	started := false
	quote := byte(0)
	for index := 0; index < len(statement); index++ {
		character := statement[index]
		if quote != 0 {
			if character == '\\' && quote != '`' && index+1 < len(statement) {
				index++
				continue
			}
			if character == quote {
				if index+1 < len(statement) && statement[index+1] == quote {
					index++
					continue
				}
				quote = 0
			}
			continue
		}
		switch character {
		case '\'', '"', '`':
			quote = character
		case '(':
			depth++
			started = true
		case ')':
			if !started || depth == 0 {
				return "", errors.New("SHOW CREATE TABLE contains an unmatched closing parenthesis")
			}
			depth--
			if depth == 0 {
				return statement[index+1:], nil
			}
		}
	}
	if quote != 0 {
		return "", errors.New("SHOW CREATE TABLE contains an unterminated quoted value")
	}
	return "", errors.New("SHOW CREATE TABLE has no complete table definition")
}

func maskQuotedSQLText(value string) (string, error) {
	masked := []byte(value)
	quote := byte(0)
	for index := 0; index < len(masked); index++ {
		character := masked[index]
		if quote == 0 {
			if character == '\'' || character == '"' || character == '`' {
				quote = character
				masked[index] = ' '
			}
			continue
		}
		masked[index] = ' '
		if character == '\\' && quote != '`' && index+1 < len(masked) {
			index++
			masked[index] = ' '
			continue
		}
		if character == quote {
			if index+1 < len(masked) && masked[index+1] == quote {
				index++
				masked[index] = ' '
				continue
			}
			quote = 0
		}
	}
	if quote != 0 {
		return "", errors.New("SHOW CREATE TABLE options contain an unterminated quoted value")
	}
	return string(masked), nil
}

func readSchemaTables(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	rows, err := executor.QueryContext(ctx, `SELECT t.TABLE_NAME, t.TABLE_TYPE, t.ENGINE,
		c.CHARACTER_SET_NAME, t.TABLE_COLLATION, t.AUTO_INCREMENT
		FROM information_schema.TABLES t
		LEFT JOIN information_schema.COLLATION_CHARACTER_SET_APPLICABILITY c
			ON c.COLLATION_NAME = t.TABLE_COLLATION
		WHERE t.TABLE_SCHEMA = DATABASE()
		ORDER BY t.TABLE_NAME`)
	if err != nil {
		return fmt.Errorf("inspect schema manifest tables: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, tableType string
		var engine, charset, collation sql.NullString
		var autoIncrement sql.NullInt64
		if err := rows.Scan(&name, &tableType, &engine, &charset, &collation, &autoIncrement); err != nil {
			return fmt.Errorf("scan schema manifest table: %w", err)
		}
		if strings.EqualFold(tableType, "VIEW") {
			snapshot.views[name] = struct{}{}
			continue
		}
		snapshot.tables[name] = schemaTableState{
			engine: engine.String, charset: charset.String,
			collation: collation.String, autoIncrement: autoIncrement,
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema manifest tables: %w", err)
	}
	return nil
}

func readSchemaColumns(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	rows, err := executor.QueryContext(ctx, `SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE,
			IS_NULLABLE, COLUMN_DEFAULT, EXTRA, GENERATION_EXPRESSION, CHARACTER_SET_NAME, COLLATION_NAME
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, ORDINAL_POSITION`)
	if err != nil {
		return fmt.Errorf("inspect schema manifest columns: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, name, columnType, nullable, extra string
		var generation, charset, collation sql.NullString
		var defaultValue sql.NullString
		if err := rows.Scan(&table, &name, &columnType, &nullable, &defaultValue, &extra,
			&generation, &charset, &collation); err != nil {
			return fmt.Errorf("scan schema manifest column: %w", err)
		}
		if snapshot.columns[table] == nil {
			snapshot.columns[table] = make(map[string]schemaColumnState)
		}
		snapshot.columns[table][name] = schemaColumnState{
			columnType:   columnType,
			nullable:     nullable,
			defaultValue: defaultValue,
			extra:        extra,
			generation:   generation.String,
			charset:      charset.String,
			collation:    collation.String,
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema manifest columns: %w", err)
	}
	return nil
}

type schemaIndexBuilder struct {
	table     string
	primary   bool
	unique    bool
	columns   map[int]string
	orders    map[int]string
	indexType string
	visible   string
}

func readSchemaIndexes(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	rows, err := executor.QueryContext(ctx, `SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE,
			SEQ_IN_INDEX, COLUMN_NAME, SUB_PART, EXPRESSION, COLLATION, INDEX_TYPE, IS_VISIBLE
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE()
		ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`)
	if err != nil {
		return fmt.Errorf("inspect schema manifest indexes: %w", err)
	}
	defer rows.Close()
	builders := make(map[string]*schemaIndexBuilder)
	for rows.Next() {
		var table, name string
		var nonUnique, sequence int
		var column, expression, order sql.NullString
		var indexType, visible string
		var subPart sql.NullInt64
		if err := rows.Scan(&table, &name, &nonUnique, &sequence, &column, &subPart,
			&expression, &order, &indexType, &visible); err != nil {
			return fmt.Errorf("scan schema manifest index: %w", err)
		}
		key := table + "\x00" + name
		builder := builders[key]
		if builder == nil {
			builder = &schemaIndexBuilder{
				table: table, primary: strings.EqualFold(name, "PRIMARY"),
				unique: nonUnique == 0, columns: make(map[int]string),
				orders: make(map[int]string), indexType: indexType, visible: visible,
			}
			builders[key] = builder
		}
		token := column.String
		if token == "" {
			token = "expression:" + normalizeGenerationExpression(expression.String)
		}
		if subPart.Valid {
			token = fmt.Sprintf("%s(%d)", token, subPart.Int64)
		}
		builder.columns[sequence] = token
		builder.orders[sequence] = order.String
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema manifest indexes: %w", err)
	}
	for _, key := range sortedStringKeys(builders) {
		builder := builders[key]
		sequences := make([]int, 0, len(builder.columns))
		for sequence := range builder.columns {
			sequences = append(sequences, sequence)
		}
		sort.Ints(sequences)
		columns := make([]string, 0, len(sequences))
		orders := make([]string, 0, len(sequences))
		for _, sequence := range sequences {
			columns = append(columns, builder.columns[sequence])
			orders = append(orders, builder.orders[sequence])
		}
		snapshot.indexes[builder.table] = append(snapshot.indexes[builder.table], schemaIndexState{
			primary: builder.primary, unique: builder.unique, columns: columns,
			orders: orders, indexType: builder.indexType, visible: builder.visible,
		})
	}
	return nil
}

func readSchemaChecks(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	rows, err := executor.QueryContext(ctx, `SELECT tc.TABLE_NAME, tc.CONSTRAINT_NAME
		FROM information_schema.TABLE_CONSTRAINTS tc
		WHERE tc.CONSTRAINT_SCHEMA = DATABASE() AND tc.CONSTRAINT_TYPE = 'CHECK'
		ORDER BY tc.TABLE_NAME, tc.CONSTRAINT_NAME`)
	if err != nil {
		return fmt.Errorf("inspect schema manifest checks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item schemaNamedObjectState
		if err := rows.Scan(&item.table, &item.name); err != nil {
			return fmt.Errorf("scan schema manifest check: %w", err)
		}
		snapshot.checks = append(snapshot.checks, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema manifest checks: %w", err)
	}
	return nil
}

// MySQL filters these three information_schema views by TRIGGER, EVENT and
// routine metadata privileges. The Compose root-owned account initializer
// therefore performs the authoritative absence audit before it creates the
// least-privilege accounts. These readers still reject any object visible to
// an administrative/custom migrator, but callers must not interpret an empty
// result as proof that an underprivileged account can see every such object.
func readVisibleSchemaTriggers(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	rows, err := executor.QueryContext(ctx, `SELECT EVENT_OBJECT_TABLE, TRIGGER_NAME
		FROM information_schema.TRIGGERS
		WHERE TRIGGER_SCHEMA = DATABASE()
		ORDER BY EVENT_OBJECT_TABLE, TRIGGER_NAME`)
	if err != nil {
		return fmt.Errorf("inspect visible schema triggers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item schemaNamedObjectState
		if err := rows.Scan(&item.table, &item.name); err != nil {
			return fmt.Errorf("scan visible schema trigger: %w", err)
		}
		snapshot.triggers = append(snapshot.triggers, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate visible schema triggers: %w", err)
	}
	return nil
}

func readVisibleSchemaEvents(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	rows, err := executor.QueryContext(ctx, `SELECT EVENT_NAME
		FROM information_schema.EVENTS
		WHERE EVENT_SCHEMA = DATABASE()
		ORDER BY EVENT_NAME`)
	if err != nil {
		return fmt.Errorf("inspect visible schema events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("scan visible schema event: %w", err)
		}
		snapshot.events = append(snapshot.events, name)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate visible schema events: %w", err)
	}
	return nil
}

func readVisibleSchemaRoutines(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	rows, err := executor.QueryContext(ctx, `SELECT ROUTINE_NAME, ROUTINE_TYPE
		FROM information_schema.ROUTINES
		WHERE ROUTINE_SCHEMA = DATABASE()
		ORDER BY ROUTINE_TYPE, ROUTINE_NAME`)
	if err != nil {
		return fmt.Errorf("inspect visible schema routines: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item schemaRoutineState
		if err := rows.Scan(&item.name, &item.kind); err != nil {
			return fmt.Errorf("scan visible schema routine: %w", err)
		}
		snapshot.routines = append(snapshot.routines, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate visible schema routines: %w", err)
	}
	return nil
}

type schemaForeignKeyBuilder struct {
	tableSchema       string
	table             string
	constraint        string
	columns           map[int]string
	referencedTable   string
	referencedColumns map[int]string
	referencedCurrent bool
	inbound           bool
	updateRule        string
	deleteRule        string
}

func readSchemaForeignKeys(ctx context.Context, executor sqlExecutor, snapshot *schemaSnapshot) error {
	// The second half deliberately returns inbound cross-schema references in
	// the same result set. A least-privilege migration account can only see the
	// rows MySQL exposes to it, but it must still reject every visible external
	// child. Guarded migration repeats this invariant through the administrator
	// connection, whose global metadata privileges make the check authoritative.
	// A NUL marker is safe because MySQL identifiers cannot contain NUL bytes and
	// lets the Go side preserve the external schema without changing the result
	// shape consumed by the deterministic bootstrap test driver.
	rows, err := executor.QueryContext(ctx, `SELECT k.TABLE_NAME, k.CONSTRAINT_NAME,
			k.ORDINAL_POSITION, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME,
			(BINARY k.REFERENCED_TABLE_SCHEMA = BINARY DATABASE()),
			k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
			ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
			AND r.TABLE_NAME = k.TABLE_NAME
			AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		WHERE BINARY k.TABLE_SCHEMA = BINARY DATABASE()
			AND k.REFERENCED_TABLE_NAME IS NOT NULL
		UNION ALL
		SELECT CONCAT(CHAR(0), k.TABLE_SCHEMA, CHAR(0), k.TABLE_NAME), k.CONSTRAINT_NAME,
			k.ORDINAL_POSITION, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME,
			(BINARY k.REFERENCED_TABLE_SCHEMA = BINARY DATABASE()),
			k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
			ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
			AND r.TABLE_NAME = k.TABLE_NAME
			AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		WHERE BINARY k.REFERENCED_TABLE_SCHEMA = BINARY DATABASE()
			AND BINARY k.TABLE_SCHEMA <> BINARY DATABASE()
			AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY 1, 2, 3`)
	if err != nil {
		return fmt.Errorf("inspect schema manifest foreign keys: %w", err)
	}
	defer rows.Close()
	builders := make(map[string]*schemaForeignKeyBuilder)
	for rows.Next() {
		var table, name, column, referencedTable, referencedColumn, updateRule, deleteRule string
		var referencedCurrent bool
		var ordinal int
		if err := rows.Scan(&table, &name, &ordinal, &column, &referencedTable,
			&referencedCurrent, &referencedColumn, &updateRule, &deleteRule); err != nil {
			return fmt.Errorf("scan schema manifest foreign key: %w", err)
		}
		tableSchema, externalTable, inbound := parseInboundForeignKeyOwner(table)
		if inbound {
			table = externalTable
		}
		key := tableSchema + "\x00" + table + "\x00" + name
		builder := builders[key]
		if builder == nil {
			builder = &schemaForeignKeyBuilder{
				tableSchema: tableSchema, table: table, constraint: name,
				columns:         make(map[int]string),
				referencedTable: referencedTable, referencedColumns: make(map[int]string),
				referencedCurrent: referencedCurrent,
				inbound:           inbound,
				updateRule:        updateRule, deleteRule: deleteRule,
			}
			builders[key] = builder
		}
		builder.columns[ordinal] = column
		builder.referencedColumns[ordinal] = referencedColumn
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate schema manifest foreign keys: %w", err)
	}
	for _, key := range sortedStringKeys(builders) {
		builder := builders[key]
		ordinals := make([]int, 0, len(builder.columns))
		for ordinal := range builder.columns {
			ordinals = append(ordinals, ordinal)
		}
		sort.Ints(ordinals)
		columns := make([]string, 0, len(ordinals))
		referencedColumns := make([]string, 0, len(ordinals))
		for _, ordinal := range ordinals {
			columns = append(columns, builder.columns[ordinal])
			referencedColumns = append(referencedColumns, builder.referencedColumns[ordinal])
		}
		if builder.inbound {
			snapshot.inboundForeignKeys = append(snapshot.inboundForeignKeys, schemaInboundForeignKeyState{
				tableSchema: builder.tableSchema, table: builder.table,
				constraint: builder.constraint, columns: columns,
				referencedTable: builder.referencedTable, referencedColumn: referencedColumns,
			})
			continue
		}
		snapshot.foreignKeys = append(snapshot.foreignKeys, schemaForeignKeyState{
			table: builder.table, columns: columns,
			referencedTable: builder.referencedTable, referencedColumns: referencedColumns,
			referencedCurrent: builder.referencedCurrent,
			updateRule:        builder.updateRule, deleteRule: builder.deleteRule,
		})
	}
	return nil
}

func parseInboundForeignKeyOwner(encoded string) (schema, table string, inbound bool) {
	if !strings.HasPrefix(encoded, "\x00") {
		return "", encoded, false
	}
	separator := strings.IndexByte(encoded[1:], 0)
	if separator < 0 {
		return "", encoded, false
	}
	separator++
	return encoded[1:separator], encoded[separator+1:], true
}

func compareSemanticSchema(snapshot schemaSnapshot, manifest semanticSchemaManifest) []string {
	diffs := make([]string, 0)
	if manifest.strictTables {
		expectedTables := stringSet(sortedStringKeys(manifest.tables))
		// schema_migrations is engine-owned and has a separate exact definition
		// validator because legacy adoption must inspect its two-column form.
		expectedTables[schemaMigrationsTable] = struct{}{}
		actualTables := stringSet(sortedStringKeys(snapshot.tables))
		if extraTables := setDifference(actualTables, expectedTables); len(extraTables) > 0 {
			diffs = append(diffs, fmt.Sprintf(
				"数据库存在 manifest 外基础表 [%s]（请补充 migration/manifest 或移出 Ares 专用 schema）",
				strings.Join(extraTables, ",")))
		}
	}
	for _, tableName := range sortedStringKeys(manifest.tables) {
		expected := manifest.tables[tableName]
		actualTable, exists := snapshot.tables[tableName]
		if !exists {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s 缺失（请通过显式 migration 创建）", tableName))
			continue
		}
		if !strings.EqualFold(actualTable.engine, expected.engine) {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s 引擎为 %s，期望 %s（请新增显式 migration 转换）",
				tableName, printableValue(actualTable.engine), printableValue(expected.engine)))
		}
		if !strings.EqualFold(actualTable.charset, expected.charset) {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s 默认字符集为 %s，期望 %s（请新增显式 migration 对齐）",
				tableName, printableValue(actualTable.charset), printableValue(expected.charset)))
		}
		if !strings.EqualFold(actualTable.collation, expected.collation) {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s 排序规则为 %s，期望 %s（请新增显式 migration 对齐）",
				tableName, printableValue(actualTable.collation), printableValue(expected.collation)))
		}
		if expected.autoIncrementMin > 0 &&
			(!actualTable.autoIncrement.Valid || actualTable.autoIncrement.Int64 < int64(expected.autoIncrementMin)) {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s AUTO_INCREMENT=%s，期望不小于 %d（请通过显式 migration 修复）",
				tableName, printableNullInt64(actualTable.autoIncrement), expected.autoIncrementMin))
		}

		actualColumns := snapshot.columns[tableName]
		expectedColumnSet := stringSet(expected.columns)
		actualColumnSet := make(map[string]struct{}, len(actualColumns))
		for columnName := range actualColumns {
			actualColumnSet[columnName] = struct{}{}
		}
		missingColumns := setDifference(expectedColumnSet, actualColumnSet)
		if len(missingColumns) > 0 {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s 缺少列 [%s]（请通过显式 migration 添加）",
				tableName, strings.Join(missingColumns, ",")))
		}
		if manifest.strictColumns {
			extraColumns := setDifference(actualColumnSet, expectedColumnSet)
			if len(extraColumns) > 0 {
				diffs = append(diffs, fmt.Sprintf(
					"表 %s 存在 manifest 外列 [%s]（请补充 migration/manifest 或恢复正确 schema）",
					tableName, strings.Join(extraColumns, ",")))
			}
		}

		for _, columnName := range expected.columns {
			actual, exists := actualColumns[columnName]
			if !exists {
				continue
			}
			expectedDefinition := expected.critical[columnName]
			diffs = append(diffs, compareColumnDefinition(
				tableName, columnName, actual, expectedDefinition, manifest.strictColumns)...)
		}
		diffs = append(diffs, compareIndexDefinitions(
			tableName, snapshot.indexes[tableName], expected.indexes, manifest.strictIndexes)...)
	}

	diffs = append(diffs, compareForeignKeyDefinitions(snapshot, manifest)...)
	if manifest.strictSchemaObjects {
		diffs = append(diffs, compareUnexpectedSchemaObjects(snapshot, manifest)...)
	}
	if manifest.requirePipelineCollationAlignment {
		diffs = append(diffs, comparePipelineCollations(snapshot)...)
	}
	return finalizeSchemaDiffs(diffs)
}

func compareUnexpectedSchemaObjects(snapshot schemaSnapshot, manifest semanticSchemaManifest) []string {
	diffs := make([]string, 0)
	if views := sortedStringKeys(snapshot.views); len(views) > 0 {
		diffs = append(diffs, fmt.Sprintf(
			"数据库存在 manifest 外视图 [%s]（请移出 Ares 专用 schema）", strings.Join(views, ",")))
	}
	for _, check := range snapshot.checks {
		if _, managed := manifest.tables[check.table]; managed {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s 存在 manifest 外 CHECK %s（请补充 migration/manifest 或删除漂移约束）",
				check.table, check.name))
		}
	}
	for _, trigger := range snapshot.triggers {
		diffs = append(diffs, fmt.Sprintf(
			"表 %s 存在 manifest 外触发器 %s（请移出 Ares 专用 schema）",
			trigger.table, trigger.name))
	}
	if len(snapshot.events) > 0 {
		diffs = append(diffs, fmt.Sprintf(
			"数据库存在 manifest 外事件 [%s]（请移出 Ares 专用 schema）",
			strings.Join(snapshot.events, ",")))
	}
	for _, routine := range snapshot.routines {
		diffs = append(diffs, fmt.Sprintf(
			"数据库存在 manifest 外%s %s（请移出 Ares 专用 schema）",
			strings.ToUpper(routine.kind), routine.name))
	}
	return diffs
}

func printableNullInt64(value sql.NullInt64) string {
	if !value.Valid {
		return "<NULL>"
	}
	return fmt.Sprintf("%d", value.Int64)
}

func compareColumnDefinition(tableName, columnName string, actual schemaColumnState, expected schemaColumnManifest, strict bool) []string {
	diffs := make([]string, 0, 4)
	if expected.columnType != "" &&
		normalizeColumnType(actual.columnType) != normalizeColumnType(expected.columnType) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s 类型为 %s，期望 %s（请新增显式 migration 修改列）",
			tableName, columnName, printableValue(actual.columnType), expected.columnType))
	}
	if expected.nullable != "" && !strings.EqualFold(actual.nullable, expected.nullable) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s IS_NULLABLE=%s，期望 %s（请先治理数据再显式修改）",
			tableName, columnName, printableValue(actual.nullable), expected.nullable))
	}
	if expected.checkDefault && !sameSchemaDefault(actual.defaultValue, expected.defaultValue) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s 默认值为 %s，期望 %s（请新增显式 migration 修改）",
			tableName, columnName, printableSQLDefault(actual.defaultValue),
			printableSQLDefault(expected.defaultValue)))
	}
	if expected.checkExtra && normalizeSchemaExtra(actual.extra) != normalizeSchemaExtra(expected.extraExact) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s EXTRA=%s，期望 %s（请通过显式 migration 修复）",
			tableName, columnName, printableValue(actual.extra), printableValue(expected.extraExact)))
	}
	if expected.extraContains != "" &&
		!strings.Contains(strings.ToLower(actual.extra), strings.ToLower(expected.extraContains)) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s EXTRA=%s，期望包含 %s（请通过显式 migration 修复）",
			tableName, columnName, printableValue(actual.extra), expected.extraContains))
	}
	if expected.generation != "" &&
		normalizeGenerationExpression(actual.generation) != normalizeGenerationExpression(expected.generation) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s 生成表达式为 %s，期望 %s（请通过显式 migration 重建）",
			tableName, columnName, printableValue(actual.generation), expected.generation))
	}
	if strict && expected.generation == "" && strings.TrimSpace(actual.generation) != "" {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s 存在 manifest 外生成表达式 %s（请补充 migration/manifest 或恢复）",
			tableName, columnName, actual.generation))
	}
	if strict && !strings.EqualFold(actual.charset, expected.charset) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s 字符集为 %s，期望 %s（请新增显式 migration 对齐）",
			tableName, columnName, printableValue(actual.charset), printableValue(expected.charset)))
	}
	if strict && !strings.EqualFold(actual.collation, expected.collation) {
		diffs = append(diffs, fmt.Sprintf(
			"列 %s.%s 排序规则为 %s，期望 %s（请新增显式 migration 对齐）",
			tableName, columnName, printableValue(actual.collation), printableValue(expected.collation)))
	}
	return diffs
}

func sameSchemaDefault(actual, expected sql.NullString) bool {
	if actual.Valid != expected.Valid {
		return false
	}
	if !actual.Valid {
		return true
	}
	return normalizeSchemaDefault(actual.String) == normalizeSchemaDefault(expected.String)
}

func normalizeSchemaDefault(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "CURRENT_TIMESTAMP") || strings.EqualFold(value, "CURRENT_TIMESTAMP()") {
		return "CURRENT_TIMESTAMP"
	}
	return value
}

func normalizeSchemaExtra(value string) string {
	parts := strings.Fields(strings.ToLower(strings.ReplaceAll(value, "current_timestamp()", "current_timestamp")))
	filtered := parts[:0]
	for _, part := range parts {
		if part != "default_generated" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, " ")
}

func printableSQLDefault(value sql.NullString) string {
	if !value.Valid {
		return "<NULL>"
	}
	return value.String
}

func compareIndexDefinitions(table string, actual []schemaIndexState, expected []schemaIndexManifest, strict bool) []string {
	actualCounts := indexStateCounts(actual)
	expectedCounts := indexManifestCounts(expected)
	diffs := make([]string, 0)
	for _, key := range sortedStringKeys(expectedCounts) {
		if actualCounts[key] >= expectedCounts[key] {
			continue
		}
		for count := actualCounts[key]; count < expectedCounts[key]; count++ {
			diffs = append(diffs, fmt.Sprintf(
				"表 %s 缺少%s（请通过显式 migration 添加；索引名称可不同）",
				table, printableIndexKey(key)))
		}
	}
	if strict {
		for _, key := range sortedStringKeys(actualCounts) {
			if actualCounts[key] <= expectedCounts[key] {
				continue
			}
			for count := expectedCounts[key]; count < actualCounts[key]; count++ {
				diffs = append(diffs, fmt.Sprintf(
					"表 %s 存在 manifest 外%s（请补充 migration/manifest 或删除漂移索引）",
					table, printableIndexKey(key)))
			}
		}
	}
	return diffs
}

func indexStateCounts(indexes []schemaIndexState) map[string]int {
	result := make(map[string]int)
	for _, index := range indexes {
		result[indexSemanticKey(index.primary, index.unique, index.columns,
			index.orders, index.indexType, index.visible)]++
	}
	return result
}

func indexManifestCounts(indexes []schemaIndexManifest) map[string]int {
	result := make(map[string]int)
	for _, index := range indexes {
		result[indexSemanticKey(index.primary, index.unique, index.columns,
			index.orders, index.indexType, index.visible)]++
	}
	return result
}

func compareForeignKeyDefinitions(snapshot schemaSnapshot, manifest semanticSchemaManifest) []string {
	managedTables := make(map[string]struct{}, len(manifest.tables))
	for table := range manifest.tables {
		managedTables[table] = struct{}{}
	}
	actualCounts := make(map[string]int)
	for _, foreignKey := range snapshot.foreignKeys {
		if _, managed := managedTables[foreignKey.table]; !managed {
			continue
		}
		actualCounts[foreignKeySemanticKey(
			foreignKey.table, foreignKey.columns,
			foreignKey.referencedTable, foreignKey.referencedColumns,
			foreignKey.referencedCurrent,
			foreignKey.updateRule, foreignKey.deleteRule,
		)]++
	}
	expectedCounts := foreignKeyManifestCounts(manifest.foreignKeys)
	diffs := make([]string, 0)
	if manifest.strictForeignKeys {
		diffs = append(diffs, compareInboundForeignKeys(snapshot.inboundForeignKeys, managedTables)...)
	}
	for _, key := range sortedStringKeys(expectedCounts) {
		if actualCounts[key] >= expectedCounts[key] {
			continue
		}
		for count := actualCounts[key]; count < expectedCounts[key]; count++ {
			diffs = append(diffs, "缺少外键 "+key+"（请通过显式 migration 添加）")
		}
	}
	if manifest.strictForeignKeys {
		for _, key := range sortedStringKeys(actualCounts) {
			if actualCounts[key] <= expectedCounts[key] {
				continue
			}
			for count := expectedCounts[key]; count < actualCounts[key]; count++ {
				diffs = append(diffs, "存在 manifest 外外键 "+key+"（请补充 migration/manifest 或恢复）")
			}
		}
	}
	return diffs
}

func compareInboundForeignKeys(foreignKeys []schemaInboundForeignKeyState, managedTables map[string]struct{}) []string {
	diffs := make([]string, 0, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		if _, managed := managedTables[foreignKey.referencedTable]; !managed && foreignKey.referencedTable != schemaMigrationsTable {
			continue
		}
		diffs = append(diffs, fmt.Sprintf(
			"外部 schema %s 的表 %s 通过外键 %s(%s)->CURRENT SCHEMA.%s(%s) 反向引用 Ares 受管表（请先删除此外键）",
			foreignKey.tableSchema, foreignKey.table, foreignKey.constraint,
			strings.Join(foreignKey.columns, ","), foreignKey.referencedTable,
			strings.Join(foreignKey.referencedColumn, ",")))
	}
	return diffs
}

// validateNoInboundForeignKeys is the authoritative guarded-migration check.
// It must run with the already-validated administrator connection: ordinary
// least-privilege inspection catches any visible inbound references, while a
// global metadata-capable administrator proves there are none before account
// handoff and again before guarded migration reports success.
func validateNoInboundForeignKeys(ctx context.Context, executor sqlExecutor) error {
	snapshot := schemaSnapshot{}
	if err := readSchemaForeignKeys(ctx, executor, &snapshot); err != nil {
		return fmt.Errorf("inspect authoritative inbound foreign keys: %w", err)
	}
	managedTables := make(map[string]struct{}, len(epoch4SemanticSchemaManifest.tables))
	for table := range epoch4SemanticSchemaManifest.tables {
		managedTables[table] = struct{}{}
	}
	diffs := compareInboundForeignKeys(snapshot.inboundForeignKeys, managedTables)
	if len(diffs) == 0 {
		return nil
	}
	problems := make([]string, 0, len(diffs))
	for _, diff := range diffs {
		problems = append(problems, "管理员权威外键检查: "+diff)
	}
	return &SchemaStateError{Problems: finalizeSchemaDiffs(problems)}
}

func foreignKeyManifestCounts(foreignKeys []schemaForeignKeyManifest) map[string]int {
	result := make(map[string]int)
	for _, foreignKey := range foreignKeys {
		result[foreignKeySemanticKey(
			foreignKey.table, foreignKey.columns,
			foreignKey.referencedTable, foreignKey.referencedColumns,
			true,
			foreignKey.updateRule, foreignKey.deleteRule,
		)]++
	}
	return result
}

func comparePipelineCollations(snapshot schemaSnapshot) []string {
	references := []struct {
		table  string
		column string
	}{
		{table: "pipelines", column: "job_name"},
		{table: "pipelines_job_combination", column: "ci_job_name"},
		{table: "pipelines_job_combination", column: "cd_job_name"},
	}
	definitions := make([]schemaColumnState, 0, len(references))
	for _, reference := range references {
		column, exists := snapshot.columns[reference.table][reference.column]
		if !exists {
			return nil
		}
		definitions = append(definitions, column)
	}
	source := definitions[0]
	for index := 1; index < len(definitions); index++ {
		target := definitions[index]
		if strings.EqualFold(source.charset, target.charset) &&
			strings.EqualFold(source.collation, target.collation) {
			continue
		}
		return []string{fmt.Sprintf(
			"pipeline 外键字符列排序规则不一致：pipelines.job_name=%s/%s，%s.%s=%s/%s（请在添加外键前显式对齐）",
			printableValue(source.charset), printableValue(source.collation),
			references[index].table, references[index].column,
			printableValue(target.charset), printableValue(target.collation),
		)}
	}
	return nil
}

func indexSemanticKey(primary, unique bool, columns, orders []string, indexType, visible string) string {
	kind := "普通索引"
	if primary {
		kind = "主键"
	} else if unique {
		kind = "唯一索引"
	}
	normalized := make([]string, 0, len(columns))
	for index, column := range columns {
		order := "A"
		if index < len(orders) && strings.TrimSpace(orders[index]) != "" {
			order = strings.ToUpper(strings.TrimSpace(orders[index]))
		}
		normalized = append(normalized,
			strings.ToLower(strings.TrimSpace(column))+" "+order)
	}
	if strings.TrimSpace(indexType) == "" {
		indexType = "BTREE"
	}
	if strings.TrimSpace(visible) == "" {
		visible = "YES"
	}
	return kind + "\x00" + strings.Join(normalized, ",") + "\x00" +
		strings.ToUpper(strings.TrimSpace(indexType)) + "\x00" + strings.ToUpper(strings.TrimSpace(visible))
}

func printableIndexKey(key string) string {
	parts := strings.Split(key, "\x00")
	if len(parts) != 4 {
		return "索引 " + key
	}
	return fmt.Sprintf("%s (%s) TYPE=%s VISIBLE=%s", parts[0], parts[1], parts[2], parts[3])
}

func foreignKeySemanticKey(table string, columns []string, referencedTable string, referencedColumns []string, referencedCurrent bool, updateRule, deleteRule string) string {
	referenceScope := "EXTERNAL SCHEMA"
	if referencedCurrent {
		referenceScope = "CURRENT SCHEMA"
	}
	return fmt.Sprintf("%s(%s)->%s.%s(%s) ON UPDATE %s ON DELETE %s",
		strings.ToLower(table), strings.Join(lowerStrings(columns), ","),
		referenceScope,
		strings.ToLower(referencedTable), strings.Join(lowerStrings(referencedColumns), ","),
		normalizeForeignKeyRule(updateRule), normalizeForeignKeyRule(deleteRule))
}

func normalizeForeignKeyRule(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if normalized == "NO ACTION" {
		return "RESTRICT"
	}
	return normalized
}

func normalizeColumnType(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(value), " "))
	replacer := strings.NewReplacer(
		"bigint(20)", "bigint",
		"int(11)", "int",
		"mediumint(9)", "mediumint",
		"smallint(6)", "smallint",
	)
	return replacer.Replace(normalized)
}

func normalizeGenerationExpression(value string) string {
	value = strings.ToLower(value)
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '`' || r == '(' || r == ')' {
			return -1
		}
		return r
	}, value)
}

func lowerStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, strings.ToLower(strings.TrimSpace(value)))
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func setDifference(left, right map[string]struct{}) []string {
	result := make([]string, 0)
	for value := range left {
		if _, exists := right[value]; !exists {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func sortedStringKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func finalizeSchemaDiffs(diffs []string) []string {
	if len(diffs) == 0 {
		return nil
	}
	sort.Strings(diffs)
	if len(diffs) <= maxSchemaManifestDiffs {
		return diffs
	}
	visible := append([]string(nil), diffs[:maxSchemaManifestDiffs-1]...)
	visible = append(visible, fmt.Sprintf(
		"另有 %d 项 schema 差异未展示；请修复已列项目后重新检查",
		len(diffs)-len(visible)))
	return visible
}

func printableValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<空>"
	}
	return value
}
