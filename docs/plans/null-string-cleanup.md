# “NULL 字符串”治理方案

本方案治理历史上用字符串 `"NULL"` 代表空值的问题。数据库中的 SQL `NULL`、软删除条件 `deleted_at IS NULL` 以及 JSON 自身的 `null` 均是合法语义，不在清理范围内。

## 字段语义

### 必填字段

- `apps.app_name_cn`：历史空值回填为同一行的 `app_name`，最终为 `NOT NULL` 且无伪默认值。
- `app_configs.code_package_type`：按应用开发语言对应的 `dev_language_rules.rules.default` 回填，最终为 `NOT NULL` 且无伪默认值。缺少应用、规则或合法默认值时迁移会停止并报告 `config_id`，不会猜测数据。

### 可选字段

以下字段统一使用 SQL `NULL`，数据库不再保存空字符串、纯空白或大小写不同的 `"NULL"`：

- `apps.description_cn`
- `apps.rundeck_app_name`
- `app_configs.code_package_path`
- `app_configs.code_package_name`
- `app_configs.base_image`
- `app_configs.pre_stop_command`
- `task_record.message`
- `task_record.rundeck_app_name`
- `task_record.ci_job_name`
- `task_record.cd_job_name`
- `task_record.products`

除既有的 Rundeck 指针字段外，Go 实体暂时保留普通字符串字段；Xorm 读取 SQL `NULL` 后，这些字段对外 JSON 仍返回空字符串，避免破坏现有 API 和前端契约。

## 迁移与兼容

- 迁移版本：`20260902_001_cleanup_legacy_null_strings`
- W04 起，本迁移作为 epoch 1 由独立的 `ares migrate up` 在数据库级锁内执行；`serve` 不再运行 Xorm 结构同步或迁移。全新空库先由显式 bootstrap 建表并补齐默认语言规则，再按 epoch 校验；受支持的旧两列 ledger 会在验证后置条件后被收养。
- migrator 会在第一条操作前写入包含固定 checksum 的 dirty 记录；MySQL DDL 可能隐式提交，因此每一步都设计为可重复执行，成功通过后置校验后才清除 dirty。
- 版本已记录后，后续启动只做只读 ledger 和 schema manifest 检查，不再扫描 `apps` / `app_configs` 做预回填。
- 数据按主键分批清理，并显式保持 `updated_at` 不变；首批不设置人为下界，因而 `0`、负 INT 和最小 BIGINT 等完整带符号主键范围都不会被 keyset 游标跳过。
- 前后端写入会把空串、纯空白及大小写不敏感的 `"NULL"` 统一视为空；可选字段写 SQL `NULL`，必填字段拒绝空值。
- 历史 `pipeline_param` 不直接批量重写；读取或重新触发任务时，仅对白名单中的遗留可空参数做兼容归一。

当前迁移命令、停机升级和 dirty 恢复流程以[数据库迁移与恢复手册](../operations/database-migrations.md)为准。

## 部署前检查

迁移会调整 `apps`、`app_configs`、`task_record` 的列定义并清理历史数据。生产升级前应完成数据库备份，并确认有效（未软删除）的 `dev_language_rules` 覆盖了所有历史应用的开发语言。

升级应安排短暂维护窗口：先停止所有旧版本 Ares 实例，再启动新版本。数据库初始化锁只能协调已经包含该锁的实例，无法阻止旧版本在迁移完成后重新写入字符串 `"NULL"`；因此不支持新旧版本同时写库的滚动升级。
