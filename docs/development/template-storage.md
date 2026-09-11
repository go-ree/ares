# W11-A2：类型与模板存储契约

## 1. 本次交付边界

A2 拆为 A2a schema/迁移/权限和 A2b 事务存储/CAS。本次仅 A2a，不新增 HTTP 管理接口、应用绑定或执行路径，不把结构合法等同可执行。A3 才接管理 API，D 才接页面。

### 1.1 稳定身份与归属

- `application_types`：稳定小写 `type_key`（1～63 字符，字母开头，后续允许数字、下划线、连字符），显示名、启停、revision、时间。Java/Python 是可配置种子，不是枚举；不覆盖已有行，不把旧 dev_language 自动绑定到类型。
- `pipeline_templates`：全局唯一稳定 `template_key`、不可变 kind/归属、可变草稿 JSON、revision 与创建/更新时间。CI 引用类型，CD 引用 target_type，不绑定开发语言。停用只阻止后续选择，不删除历史定义。
- `pipeline_template_versions`：模板外键、模板内连续版本号、来源 revision、不可变规范、SHA-256、创建人和时间。唯一键 `(template_id, version_number)` 与 `(template_id, source_revision)` 防止同一草稿重复发布。没有应用私有模板副本。

### 1.2 后续事务约束（A2b，尚未实现）

类型/模板修改要求 expected_revision，原子 CAS 失败返回冲突；key/kind/归属不可变。发布锁定类型再锁模板，验证启用状态与草稿、分配下一版本号，并在同一事务插入版本和递增模板 revision。版本规范不接受 UPDATE/DELETE。禁用与发布使用相同锁序；重复发布相同 source_revision 不产生第二版。无执行器能力证明时不得启动 CI/CD。

## 2. Schema 与完整性

### 2.1 Epoch 9

追加 `20260911_002_pipeline_templates`，只新增上述三张表和 Java/Python 类型种子；epoch 1～8 保持不变。外键 RESTRICT，表使用 utf8mb4_unicode_ci。恢复边界覆盖三条 CREATE TABLE DDL；种子写入幂等。不迁移旧工作流或生成可信产物。

数据契约验证 key、归属、revision、外键关系、规范大小/结构、版本序号/来源 revision 和 checksum。规范最多 64 KiB；完整性摘要使用现有 canonicaljson 语义编码，不能依赖 MySQL JSON 原始排版。数据错误只报告固定说明，不回显规范或参数。

epoch 9 实现指纹包含 v1 Spec 校验器与 canonicaljson；未来规范演进必须保留 v1 校验语义并追加版本，不能修改已发布迁移所依赖的校验行为。

### 2.2 最小权限

runtime 对类型和草稿表仅 SELECT/INSERT/UPDATE，对版本表仅 SELECT/INSERT；不允许删除类型/模板或改写发布版本。migrator 继续按现有授权管理 DDL；应用启动只验证，不自动迁移。

## 3. 升级与恢复

### 3.1 部署顺序

停止旧 API/Worker → 备份并验证恢复 → migrator 升级至 epoch 9 → 刷新 runtime 表级授权 → 启动匹配版本并检查健康、旧应用和历史数据。epoch 8 二进制不得连接 epoch 9 工作库。

### 3.2 回退

保留故障库；恢复 epoch 8 备份到独立恢复库，并配套旧镜像和账号权限。禁止只回退镜像、修改 ledger 或删除新表伪造降级。恢复数据库不会撤销外部部署；本次迁移自身无外部执行副作用。
