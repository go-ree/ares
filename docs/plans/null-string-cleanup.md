# “NULL 字符串”治理清单（第一版）

本清单用于治理：**用字符串 `"NULL"` 代表空值** 的历史设计问题。  
来源：扫描 `init.sql` 与 `internal/entity/*.go` 中 `DEFAULT 'NULL' / default 'NULL'` 相关字段。

## 需要治理的表与字段（按表分组）

### ares.apps
- **`app_name_cn`**：`DEFAULT 'NULL'`
- **`description_cn`**：`DEFAULT 'NULL'`

### ares.app_configs
- **`code_package_type`**：`DEFAULT 'NULL'`
- **`code_package_path`**：`DEFAULT 'NULL'`
- **`code_package_name`**：`DEFAULT 'NULL'`
- **`base_image`**：`DEFAULT 'NULL'`
- **`pre_stop_command`**：`DEFAULT 'NULL'`

### ares.task_record
- **`message`**：`DEFAULT 'NULL'`
- **`ci_job_name`**：`DEFAULT 'NULL'`
- **`cd_job_name`**：`DEFAULT 'NULL'`
- **`products`**：`DEFAULT 'NULL'`

## 对应的 Go 结构体字段（便于定位写入/读取路径）

### `internal/entity/app.go`
- `Apps.AppNameCn`
- `Apps.DescriptionCN`
- `AppConfigs.CodePackageType`
- `AppConfigs.CodePackagePath`
- `AppConfigs.CodePackageName`
- `AppConfigs.BaseImage`
- `AppConfigs.PreStopCommand`

### `internal/entity/publish.go`
- `TaskRecord.Message`
- `TaskRecord.CiJobName`
- `TaskRecord.CdJobName`
- `TaskRecord.Products`

## 下一步建议（仅提纲）

- **兼容期（双读）**：读到 `NULL/"NULL"/""/空白` 统一视为“空”
- **止血（单写）**：禁止再写入 `"NULL"`，空值写 `NULL`（或业务默认值）
- **DB 迁移**：批量把历史 `"NULL"` 清洗为 `NULL`，再按业务收紧/调整列的默认值与可空性
