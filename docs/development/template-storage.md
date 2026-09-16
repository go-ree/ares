# W11-A2：类型与模板存储契约

## 1. 本次交付边界

A2 拆为 A2a schema/迁移/权限和 A2b 事务存储/CAS，两者均已合并。A2b 的 `internal/templatecatalog` 内部服务由 A3 [管理 API](template-catalog-api.md) 接入，不新增应用绑定或执行路径，不把结构合法等同可执行。D 才接页面。

### 1.1 稳定身份与归属

- `application_types`：稳定小写 `type_key`（1～63 字符，字母开头，后续允许数字、下划线、连字符），显示名、启停、revision、时间。Java/Python 是可配置种子，不是枚举；不覆盖已有行，不把旧 dev_language 自动绑定到类型。
- `pipeline_templates`：全局唯一稳定 `template_key`、不可变 kind/归属、可变草稿 JSON、revision 与创建/更新时间。CI 引用类型，CD 引用 target_type，不绑定开发语言。停用只阻止后续选择，不删除历史定义。
- `pipeline_template_versions`：模板外键、模板内连续版本号、来源 revision、不可变规范、SHA-256、创建人和时间。唯一键 `(template_id, version_number)` 与 `(template_id, source_revision)` 防止同一草稿重复发布。没有应用私有模板副本。

### 1.2 事务约束（A2b）

类型/模板修改要求 expected_revision，原子 CAS 失败返回冲突；key/kind/归属不可变。发布锁定类型再锁模板，验证启用状态与草稿、分配下一版本号，并在同一事务插入版本和递增模板 revision。版本规范不接受 UPDATE/DELETE。禁用与发布使用相同锁序；重复发布相同 source_revision 不产生第二版。无执行器能力证明时不得启动 CI/CD。

### 1.3 内部服务行为

- `CreateType/GetType/UpdateType`、`CreateTemplate/GetTemplate/UpdateTemplate`、`Publish/GetVersion` 提供最小持久化能力。A3 补充三个有界 keyset 列表，SQL 不选择草稿/版本 JSON；HTTP 调用者先经权限与审计中间件授权，内部存储不自行鉴权。
- 更新 DTO 不接收稳定 key、kind 或归属字段的变更；草稿 Spec 必须与原归属一致。显示名/启停/草稿可以修改。类型停用后仍允许维护已有草稿，但不能创建该类型的新模板或发布；CD 不受语言类型启停影响。
- 创建 revision 为 1；每次成功更新或发布均加 1。发布使用当前 expected_revision 作为 source_revision，重复/陈旧请求返回 `ErrConflict`（不自动重放），调用方可读取固定版本。即使内容未改，使用新 revision 再发布也会生成新的连续版本号。
- 事务使用 READ COMMITTED：先读取不可变归属，再锁类型和模板；避免等待父锁后仍用旧快照分配版本号。停用先取得锁则发布拒绝；发布先取得锁则完整提交后停用。版本表仅普通读取，无需 UPDATE 权限。
- 结构、归属、非零 actor/revision 和 JSON 限额在写入前验证；数据库 JSON 排版后的 64 KiB 限额在提交前再检查。版本插入或 revision 更新任一步失败均回滚。
- 固定错误类别为 Invalid、NotFound、Conflict、Disabled、Storage；不暴露 SQL/草稿/参数。类型 CAS 未命中（包括不存在）返回 Conflict；按 ID 读取不存在返回 NotFound。死锁/锁等待超时映射 Conflict，调用方重新读取后决定是否重试；上下文取消/超时保留分类。
- 不改动 epoch 9 或其冻结依赖；发布只是登记定义，不证明执行器存在，不生成运行或产物。

## 2. Schema 与完整性

### 2.1 Epoch 9

追加 `20260911_002_pipeline_templates`，只新增上述三张表和 Java/Python 类型种子；epoch 1～8 保持不变。外键 RESTRICT，表使用 utf8mb4_unicode_ci。恢复边界覆盖三条 CREATE TABLE DDL；种子写入幂等。不迁移旧工作流或生成可信产物。

数据契约验证 key、归属、revision、外键关系、规范大小/结构、版本序号/来源 revision 和 checksum。规范最多 64 KiB；完整性摘要使用现有 canonicaljson 语义编码，不能依赖 MySQL JSON 原始排版。数据错误只报告固定说明，不回显规范或参数。

epoch 9 实现指纹包含 v1 Spec 校验器与 canonicaljson；未来规范演进必须保留 v1 校验语义并追加版本，不能修改已发布迁移所依赖的校验行为。

### 2.2 Epoch 10

2026-09-16 追加 `20260916_001_pipeline_bindings`，只新增 `application_ci_bindings` 与 `app_config_cd_bindings`；epoch 1～9 定义、校验器和指纹不变，不复用 `app_config_workflows`。绑定表结构、CAS、锁顺序与数据契约见[绑定存储契约](binding-storage.md)。绑定只引用不可变 `version_id`，不复制步骤或规范。

### 2.3 最小权限

runtime 对类型和草稿表仅 SELECT/INSERT/UPDATE，对版本表仅 SELECT/INSERT；对两张绑定表仅 SELECT/INSERT/UPDATE（无 DELETE）；不允许删除类型/模板或改写发布版本。migrator 继续按现有授权管理 DDL；应用启动只验证，不自动迁移。

## 3. 升级与恢复

### 3.1 部署顺序

停止旧 API/Worker → 备份并验证恢复 → migrator 升级至当前 epoch（A2a 为 9，B2 起为 10）→ 刷新 runtime 表级授权 → 启动匹配版本并检查健康、旧应用和历史数据。低于工作库 epoch 的二进制不得连接该库：epoch 8 二进制不得连接 epoch 9 工作库，epoch 9 二进制不得连接 epoch 10 工作库。

### 3.2 回退

保留故障库；恢复 epoch 8 备份到独立恢复库，并配套旧镜像和账号权限。禁止只回退镜像、修改 ledger 或删除新表伪造降级。恢复数据库不会撤销外部部署；本次迁移自身无外部执行副作用。
