package db

import (
	"database/sql"
	"fmt"
	"strings"
)

// fullSemanticColumnCatalog is the immutable epoch-4 column contract. Column
// order is intentionally not semantic: MySQL may append columns while adopting
// an old installation, and Ares always names columns explicitly. Every managed
// column still has an exact normalized type, NULL, default, EXTRA and generated
// expression contract.
var fullSemanticColumnCatalog = map[string]string{
	"apps": `
app_id|int|NO|<NULL>|auto_increment|
app_name|varchar(255)|NO|<NULL>||
rundeck_app_name|varchar(255)|YES|<NULL>||
app_name_cn|varchar(255)|NO|<NULL>||
owner|varchar(100)|NO|<NULL>||
owner_cn|varchar(100)|NO|<NULL>||
dev_language|varchar(100)|NO|<NULL>||
description_cn|varchar(255)|YES|<NULL>||
git_url|varchar(255)|NO|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"app_configs": `
config_id|int|NO|<NULL>|auto_increment|
app_id|int|NO|<NULL>||
env|varchar(100)|NO|<NULL>||
active_env|varchar(100)|YES|<NULL>|stored generated|if(deleted_at is null,env,null)
code_package_type|varchar(100)|NO|<NULL>||
code_package_path|varchar(255)|YES|<NULL>||
code_package_name|varchar(255)|YES|<NULL>||
base_image|varchar(255)|YES|<NULL>||
pod_count|int|YES|1||
limits_memory|int|YES|2||
gpu_count|int|YES|0||
probe_type|varchar(100)|YES|TCP||
probe_check_path|varchar(100)|YES|/inside/checkup||
probe_check_tcp_port|int|NO|8080||
probe_check_http_port|int|NO|8080||
probe_stop_check_http_port|int|NO|8080||
container_port|int|NO|8080||
pre_stop_type|varchar(100)|YES|TCP||
pre_stop_check_path|varchar(100)|YES|/inside/prestop||
pre_stop_command|varchar(255)|YES|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"app_config_domains": `
id|bigint|NO|<NULL>|auto_increment|
config_id|int|NO|<NULL>||
host|varchar(255)|NO|<NULL>||
path|varchar(255)|NO|/||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"task_record": `
task_id|int|NO|<NULL>|auto_increment|
app_name|varchar(255)|NO|<NULL>||
rundeck_app_name|varchar(255)|YES|<NULL>||
branch|varchar(100)|NO|<NULL>||
env|varchar(255)|NO|<NULL>||
publisher|varchar(255)|NO|<NULL>||
ci_build_id|int|YES|0||
cd_build_id|int|YES|0||
pipeline_param|json|YES|<NULL>||
status|varchar(100)|YES|init||
message|varchar(255)|YES|<NULL>||
ci_job_name|varchar(100)|YES|<NULL>||
cd_job_name|varchar(100)|YES|<NULL>||
jenkins_address|text|YES|<NULL>||
auto_deploy|tinyint(1)|YES|1||
products|varchar(255)|YES|<NULL>||
engine_version|int|NO|1||
workflow_version_id|bigint|NO|0||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"task_record_images": `
id|bigint|NO|<NULL>|auto_increment|
task_id|int|NO|<NULL>||
img_type|varchar(32)|NO|<NULL>||
url|varchar(1024)|NO|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||`,
	"pipelines": `
id|int|NO|<NULL>|auto_increment|
job_name|varchar(100)|NO|<NULL>||
description_cn|varchar(255)|NO|<NULL>||
url|varchar(255)|NO|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"pipelines_job_combination": `
id|int|NO|<NULL>|auto_increment|
description_cn|varchar(255)|NO|<NULL>||
ci_job_name|varchar(100)|NO|<NULL>||
cd_job_name|varchar(100)|NO|<NULL>||
code_package_type|varchar(100)|NO|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"env_configs": `
id|int|NO|<NULL>|auto_increment|
env|varchar(100)|NO|<NULL>||
cluster_name|varchar(255)|YES|<NULL>||
description_cn|varchar(255)|NO|<NULL>||
enabled|tinyint(1)|NO|1||
sort_order|int|NO|0||
harbor_url|varchar(255)|YES|<NULL>||
harbor_project_name|varchar(255)|YES|<NULL>||
node_version|varchar(255)|YES|<NULL>||
maven_version|varchar(255)|YES|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"integration_settings": `
provider|varchar(64)|NO|<NULL>||
config_data|mediumtext|NO|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||`,
	"dev_language_rules": `
dev_language|varchar(100)|NO|<NULL>||
rules|json|NO|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP||
deleted_at|timestamp|YES|<NULL>||`,
	"release_workflows": `
workflow_id|bigint|NO|<NULL>|auto_increment|
name|varchar(120)|NO|<NULL>||
description|varchar(500)|YES|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP|on update current_timestamp|
deleted_at|timestamp|YES|<NULL>||`,
	"release_workflow_versions": `
version_id|bigint|NO|<NULL>|auto_increment|
workflow_id|bigint|NO|<NULL>||
version|int|NO|<NULL>||
spec|json|NO|<NULL>||
checksum|char(64)|NO|<NULL>||
created_by|varchar(100)|NO|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||`,
	"app_config_workflows": `
binding_id|bigint|NO|<NULL>|auto_increment|
app_config_id|int|NO|<NULL>||
workflow_id|bigint|NO|<NULL>||
version_id|bigint|NO|<NULL>||
revision|int|NO|1||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP|on update current_timestamp|`,
	"task_step_records": `
step_record_id|bigint|NO|<NULL>|auto_increment|
task_id|int|NO|<NULL>||
workflow_version_id|bigint|NO|<NULL>||
step_key|varchar(63)|NO|<NULL>||
name|varchar(120)|NO|<NULL>||
uses|varchar(120)|NO|<NULL>||
category|varchar(32)|YES|<NULL>||
position|int|NO|<NULL>||
config|json|NO|<NULL>||
timeout_seconds|int|NO|3600||
on_failure|varchar(16)|NO|stop||
status|varchar(32)|NO|pending||
attempt|int|NO|1||
external_ref|json|YES|<NULL>||
output|json|YES|<NULL>||
message|varchar(1000)|YES|<NULL>||
started_at|timestamp|YES|<NULL>||
finished_at|timestamp|YES|<NULL>||
created_at|timestamp|NO|CURRENT_TIMESTAMP||
updated_at|timestamp|NO|CURRENT_TIMESTAMP|on update current_timestamp|`,
}

var (
	epoch1SemanticSchemaManifest semanticSchemaManifest
	epoch2SemanticSchemaManifest semanticSchemaManifest
	epoch3SemanticSchemaManifest semanticSchemaManifest
)

const (
	canonicalTextValuesDataContractID        = "canonical-text-values-v1"
	normalizedEnvironmentCodesDataContractID = "normalized-active-environment-codes-v1"
	activeEnvironmentCatalogDataContractID   = "active-app-environments-resolve-to-catalog-v1"
)

// epochDataContractCatalog makes retained row-level invariants explicit at
// every published epoch. Entries are copied before use so callers cannot
// mutate the catalog accidentally. A new epoch must declare its complete set;
// there is deliberately no implicit "latest" fallback.
var epochDataContractCatalog = map[uint64][]string{
	1: {canonicalTextValuesDataContractID},
	2: {canonicalTextValuesDataContractID, normalizedEnvironmentCodesDataContractID, activeEnvironmentCatalogDataContractID},
	3: {canonicalTextValuesDataContractID, normalizedEnvironmentCodesDataContractID, activeEnvironmentCatalogDataContractID},
	4: {canonicalTextValuesDataContractID, normalizedEnvironmentCodesDataContractID, activeEnvironmentCatalogDataContractID},
}

func epochDataContractIDs(epoch uint64) []string {
	return append([]string(nil), epochDataContractCatalog[epoch]...)
}

func (s *migrationSession) verifyEpochDataContracts(epoch uint64) error {
	contractIDs, exists := epochDataContractCatalog[epoch]
	if !exists {
		return fmt.Errorf("epoch %d has no declared data-contract catalog", epoch)
	}
	for _, contractID := range contractIDs {
		switch contractID {
		case canonicalTextValuesDataContractID:
			if err := s.verifyCanonicalTextValues(); err != nil {
				return err
			}
		case normalizedEnvironmentCodesDataContractID:
			if err := s.verifyNormalizedActiveEnvironmentCodes(); err != nil {
				return err
			}
		case activeEnvironmentCatalogDataContractID:
			if err := s.verifyActiveAppEnvironmentsResolveToCatalog(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("epoch %d declares unknown data contract %q", epoch, contractID)
		}
	}
	return nil
}

func init() {
	for tableName, specification := range fullSemanticColumnCatalog {
		table, exists := epoch4SemanticSchemaManifest.tables[tableName]
		if !exists {
			panic("full schema column catalog references unknown table " + tableName)
		}
		table.engine = "InnoDB"
		table.charset = "utf8mb4"
		table.collation = publishedTableCollation(tableName)
		table.critical = mustParseFullColumnManifest(tableName, specification)
		for columnName, definition := range table.critical {
			if isCharacterColumnType(definition.columnType) {
				definition.charset = table.charset
				definition.collation = table.collation
			}
			table.critical[columnName] = definition
		}
		epoch4SemanticSchemaManifest.tables[tableName] = table
	}
	initializeHistoricalEpochManifests()
}

func publishedTableCollation(tableName string) string {
	switch tableName {
	case "release_workflows", "release_workflow_versions", "app_config_workflows", "task_step_records":
		return "utf8mb4_unicode_ci"
	default:
		return "utf8mb4_0900_ai_ci"
	}
}

func isCharacterColumnType(columnType string) bool {
	normalized := normalizeColumnType(columnType)
	return strings.HasPrefix(normalized, "char(") ||
		strings.HasPrefix(normalized, "varchar(") ||
		strings.Contains(normalized, "text") ||
		strings.HasPrefix(normalized, "enum(") ||
		strings.HasPrefix(normalized, "set(")
}

func initializeHistoricalEpochManifests() {
	// Every epoch receives an independent deep copy. This deliberately avoids
	// aliases between map/slice fields: a future epoch can only evolve through
	// a new manifest and cannot silently rewrite a published contract.
	epoch3SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch4SemanticSchemaManifest)
	epoch3Apps := epoch3SemanticSchemaManifest.tables["apps"]
	epoch3Apps.autoIncrementMin = 0
	epoch3SemanticSchemaManifest.tables["apps"] = epoch3Apps

	epoch2SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch3SemanticSchemaManifest)
	epoch2Task := epoch2SemanticSchemaManifest.tables["task_record"]
	epoch2Task = withoutManifestColumns(epoch2Task, "jenkins_address")
	epoch2Task.indexes = []schemaIndexManifest{primaryIndex("task_id")}
	epoch2SemanticSchemaManifest.tables["task_record"] = epoch2Task
	epoch2Steps := epoch2SemanticSchemaManifest.tables["task_step_records"]
	epoch2Steps.indexes = []schemaIndexManifest{
		primaryIndex("step_record_id"),
		uniqueIndex("task_id", "step_key"),
		regularIndex("task_id", "position"),
		regularIndex("task_id", "status"),
		regularIndex("workflow_version_id"),
	}
	epoch2SemanticSchemaManifest.tables["task_step_records"] = epoch2Steps

	epoch1SemanticSchemaManifest = cloneSemanticSchemaManifest(epoch2SemanticSchemaManifest)
	for _, table := range []string{
		"release_workflows", "release_workflow_versions", "app_config_workflows", "task_step_records",
	} {
		delete(epoch1SemanticSchemaManifest.tables, table)
	}
	epoch1AppConfigs := epoch1SemanticSchemaManifest.tables["app_configs"]
	epoch1AppConfigs = withoutManifestColumns(epoch1AppConfigs, "active_env")
	epoch1AppConfigs.indexes = []schemaIndexManifest{
		primaryIndex("config_id"),
		regularIndex("app_id"),
	}
	epoch1SemanticSchemaManifest.tables["app_configs"] = epoch1AppConfigs

	epoch1Environments := epoch1SemanticSchemaManifest.tables["env_configs"]
	epoch1Environments = withoutManifestColumns(epoch1Environments, "enabled", "sort_order")
	for _, column := range []string{
		"cluster_name", "harbor_url", "harbor_project_name", "node_version", "maven_version",
	} {
		definition := epoch1Environments.critical[column]
		definition.nullable = "NO"
		epoch1Environments.critical[column] = definition
	}
	epoch1SemanticSchemaManifest.tables["env_configs"] = epoch1Environments

	epoch1Task := epoch1SemanticSchemaManifest.tables["task_record"]
	epoch1Task = withoutManifestColumns(epoch1Task,
		"engine_version", "workflow_version_id")
	epoch1Task.indexes = []schemaIndexManifest{primaryIndex("task_id")}
	epoch1SemanticSchemaManifest.tables["task_record"] = epoch1Task
}

func cloneSemanticSchemaManifest(source semanticSchemaManifest) semanticSchemaManifest {
	result := source
	result.tables = make(map[string]schemaTableManifest, len(source.tables))
	for name, table := range source.tables {
		cloned := table
		cloned.columns = append([]string(nil), table.columns...)
		cloned.critical = make(map[string]schemaColumnManifest, len(table.critical))
		for column, definition := range table.critical {
			cloned.critical[column] = definition
		}
		cloned.indexes = make([]schemaIndexManifest, len(table.indexes))
		for index, definition := range table.indexes {
			cloned.indexes[index] = definition
			cloned.indexes[index].columns = append([]string(nil), definition.columns...)
			cloned.indexes[index].orders = append([]string(nil), definition.orders...)
		}
		result.tables[name] = cloned
	}
	result.foreignKeys = make([]schemaForeignKeyManifest, len(source.foreignKeys))
	for index, foreignKey := range source.foreignKeys {
		result.foreignKeys[index] = foreignKey
		result.foreignKeys[index].columns = append([]string(nil), foreignKey.columns...)
		result.foreignKeys[index].referencedColumns = append([]string(nil), foreignKey.referencedColumns...)
	}
	return result
}

func withoutManifestColumns(table schemaTableManifest, names ...string) schemaTableManifest {
	removed := stringSet(names)
	columns := make([]string, 0, len(table.columns))
	for _, column := range table.columns {
		if _, remove := removed[column]; remove {
			delete(table.critical, column)
			continue
		}
		columns = append(columns, column)
	}
	table.columns = columns
	return table
}

func epoch2ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch1SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}
	for _, item := range []struct{ table, column string }{
		{"env_configs", "enabled"},
		{"env_configs", "sort_order"},
		{"task_record", "engine_version"},
		{"task_record", "workflow_version_id"},
	} {
		addTargetManifestColumn(&current, epoch2SemanticSchemaManifest, item.table, item.column)
		states = append(states, cloneSemanticSchemaManifest(current))
	}
	for _, column := range []string{
		"cluster_name", "harbor_url", "harbor_project_name", "node_version", "maven_version",
	} {
		setTargetManifestColumn(&current, epoch2SemanticSchemaManifest, "env_configs", column)
	}
	states = append(states, cloneSemanticSchemaManifest(current))
	for _, table := range []string{
		"release_workflows", "release_workflow_versions", "app_config_workflows", "task_step_records",
	} {
		current.tables[table] = cloneSchemaTableManifest(epoch2SemanticSchemaManifest.tables[table])
		states = append(states, cloneSemanticSchemaManifest(current))
	}
	addTargetManifestColumn(&current, epoch2SemanticSchemaManifest, "app_configs", "active_env")
	states = append(states, cloneSemanticSchemaManifest(current))
	current.tables["app_configs"] = cloneSchemaTableManifest(epoch2SemanticSchemaManifest.tables["app_configs"])
	states = append(states, cloneSemanticSchemaManifest(current))
	return states
}

func epoch3ResumeSchemaStates() []semanticSchemaManifest {
	current := cloneSemanticSchemaManifest(epoch2SemanticSchemaManifest)
	states := []semanticSchemaManifest{cloneSemanticSchemaManifest(current)}
	addTargetManifestColumn(&current, epoch3SemanticSchemaManifest, "task_record", "jenkins_address")
	states = append(states, cloneSemanticSchemaManifest(current))
	currentTask := current.tables["task_record"]
	targetTask := epoch3SemanticSchemaManifest.tables["task_record"]
	currentTask.indexes = append(currentTask.indexes, cloneSchemaIndexManifest(targetTask.indexes[len(targetTask.indexes)-1]))
	current.tables["task_record"] = currentTask
	states = append(states, cloneSemanticSchemaManifest(current))
	currentSteps := current.tables["task_step_records"]
	targetSteps := epoch3SemanticSchemaManifest.tables["task_step_records"]
	currentSteps.indexes = append(currentSteps.indexes, cloneSchemaIndexManifest(targetSteps.indexes[len(targetSteps.indexes)-1]))
	current.tables["task_step_records"] = currentSteps
	states = append(states, cloneSemanticSchemaManifest(current))
	return states
}

func epoch4ResumeSchemaStates() []semanticSchemaManifest {
	return []semanticSchemaManifest{
		cloneSemanticSchemaManifest(epoch3SemanticSchemaManifest),
		cloneSemanticSchemaManifest(epoch4SemanticSchemaManifest),
	}
}

func addTargetManifestColumn(
	manifest *semanticSchemaManifest,
	target semanticSchemaManifest,
	tableName, columnName string,
) {
	table := manifest.tables[tableName]
	for _, existing := range table.columns {
		if existing == columnName {
			setTargetManifestColumn(manifest, target, tableName, columnName)
			return
		}
	}
	table.columns = append(table.columns, columnName)
	table.critical[columnName] = target.tables[tableName].critical[columnName]
	manifest.tables[tableName] = table
}

func setTargetManifestColumn(
	manifest *semanticSchemaManifest,
	target semanticSchemaManifest,
	tableName, columnName string,
) {
	table := manifest.tables[tableName]
	table.critical[columnName] = target.tables[tableName].critical[columnName]
	manifest.tables[tableName] = table
}

func cloneSchemaTableManifest(source schemaTableManifest) schemaTableManifest {
	result := source
	result.columns = append([]string(nil), source.columns...)
	result.critical = make(map[string]schemaColumnManifest, len(source.critical))
	for column, definition := range source.critical {
		result.critical[column] = definition
	}
	result.indexes = make([]schemaIndexManifest, len(source.indexes))
	for index, definition := range source.indexes {
		result.indexes[index] = cloneSchemaIndexManifest(definition)
	}
	return result
}

func cloneSchemaIndexManifest(source schemaIndexManifest) schemaIndexManifest {
	result := source
	result.columns = append([]string(nil), source.columns...)
	result.orders = append([]string(nil), source.orders...)
	return result
}

func mustParseFullColumnManifest(tableName, specification string) map[string]schemaColumnManifest {
	result := make(map[string]schemaColumnManifest)
	for lineNumber, line := range strings.Split(strings.TrimSpace(specification), "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 6 {
			panic(fmt.Sprintf("invalid schema manifest %s line %d: expected 6 fields", tableName, lineNumber+1))
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			panic(fmt.Sprintf("invalid schema manifest %s line %d: empty column", tableName, lineNumber+1))
		}
		if _, duplicate := result[name]; duplicate {
			panic(fmt.Sprintf("invalid schema manifest %s: duplicate column %s", tableName, name))
		}
		defaultValue := sql.NullString{}
		if parts[3] != "<NULL>" {
			defaultValue = sql.NullString{String: parts[3], Valid: true}
		}
		result[name] = schemaColumnManifest{
			columnType:   strings.TrimSpace(parts[1]),
			nullable:     strings.TrimSpace(parts[2]),
			defaultValue: defaultValue,
			checkDefault: true,
			extraExact:   strings.TrimSpace(parts[4]),
			checkExtra:   true,
			generation:   strings.TrimSpace(parts[5]),
		}
	}
	return result
}
