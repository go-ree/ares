# W11-B2：CI/CD 固定版本绑定存储

## 1. 本轮范围

B2 在 B1 只读预检之上增加**专用绑定存储与管理 API**：应用 CI 绑定、环境 CD 绑定、固定版本引用、绑定层参数、revision CAS、启停与改绑。绑定表不复用 `app_config_workflows`，也不写入任何旧工作流字段。

仍然**不创建任务、不启动执行器、不登记产物、不新增页面**。绑定成功只表示配置已保存；`executable` 恒为 `false`，直到 C 的产物能力与 B3 的运行上下文就绪。绑定写入事务会重新校验类型、模板、目标与环境状态，因此**任何跨请求复用旧预检或旧读取结果的做法都不被接受**。

## 2. 数据库结构与不可变边界

epoch 10（`20260916_001_pipeline_bindings`）新增两张表，追加在已发布的 epoch 1～9 之后，不修改历史迁移、v1 规范校验器或 epoch 9 三张模板表。

| 表 | 唯一键 | 外键（ON DELETE/UPDATE RESTRICT） | 语义 |
| --- | --- | --- | --- |
| `application_ci_bindings` | `app_id` | `apps(app_id)`、`pipeline_template_versions(version_id)` | 一个应用一个活动 CI 绑定 |
| `app_config_cd_bindings` | `config_id` | `app_configs(config_id)`、`pipeline_template_versions(version_id)` | 一个应用环境配置一个活动 CD 绑定 |

两表结构一致：`binding_id`、目标列、`version_id`、`parameters JSON`、`enabled`、`revision`、`created_by_user_id`、`updated_by_user_id`、`created_at`、`updated_at`。

设计取舍：

- **不存冗余的类型列**。CI 归属来自所引用版本的 `application_type`，CD 归属来自 `target_type`。写入时校验相等，读取时由版本行给出；重复一份归属会产生第二个事实来源。
- **不存 `kind` 列**。跨种类的改绑（CI 绑定指向 CD 版本）无法用外键表达：那需要在已冻结的 epoch 9 版本表上追加复合唯一索引。因此该约束由存储层写入校验与数据契约双重保证，两者都有测试。
- **启停即停用，不提供删除**。`enabled=0` 是"解除绑定"的表示；运行时不授予 `DELETE`，历史绑定不会被抹掉。
- 软删除的应用或环境配置**不会被自动清理**：读取仍返回绑定，写入拒绝。这样删除目标不会让已应用的 epoch 失效，也不会丢掉"曾经绑定了哪个版本"的审计事实。
- 绑定目标未创建、类型/模板/环境停用时拒绝**新建**，但已存在的绑定仍可读。

## 3. API、权限与审计

| 接口 | 权限 | 说明 |
| --- | --- | --- |
| PUT `/api/v1/apps/{app_id}/ci-binding` | `applications:write`，developer/admin | 创建或改绑（CAS），请求体含 `application_type` |
| GET `/api/v1/apps/{app_id}/ci-binding` | `applications:read` | 元数据，**不含参数** |
| GET `/api/v1/apps/{app_id}/ci-binding/parameters` | `applications:write` + 敏感读取审计 | 仅绑定层参数 |
| PUT `/api/v1/app-configs/{config_id}/cd-binding` | `app-configs:write`，developer/admin | 创建或改绑（CAS），请求体含 `target_type` |
| GET `/api/v1/app-configs/{config_id}/cd-binding` | `app-configs:read` | 元数据，**不含参数** |
| GET `/api/v1/app-configs/{config_id}/cd-binding/parameters` | `app-configs:write` + 敏感读取审计 | 仅绑定层参数 |

全部要求正式会话、Origin/CSRF，拒绝 legacy token；viewer/releaser 只读元数据，不能改绑定，也不能读参数。参数读取与写入同级（能改就能看），并以敏感读取审计记录。请求体与审计都不回显参数值；`PUT` 与敏感读取各记两条授权/结果审计，被拒绝的请求记 `denied`。

请求示例（ID 必须替换为实际存在的已发布版本）：

```json
{
  "version_id": "42",
  "application_type": "java",
  "parameters": {"repo": "https://example.invalid/group/app.git"},
  "enabled": true,
  "expected_revision": "3"
}
```

- `version_id` 是版本全局 ID（版本行 `version_id`），不是模板 ID、版本序号或 latest；无前导零的正十进制字符串。
- CI 不接受 `target_type`，CD 不接受 `application_type`；未知字段、重复字段、错类型一律 400。
- `enabled` 必填（指针语义），`parameters` 省略或 `null` 表示空绑定层。
- `expected_revision` 省略或为空表示"该目标当前不应存在绑定"（创建）；给出时必须是当前 revision（改绑/启停）。不接受 `"0"`。

成功返回 `persisted=true`、`executable=false`，创建 201、改绑 200。响应只包含本次写入的 revision，不做追加读取。

## 4. CAS 语义与锁顺序

创建与改绑是同一个操作：先按目标行做 `SELECT ... FOR UPDATE` 分类，再 `INSERT` 或 `UPDATE ... WHERE revision=?`。

| 情况 | 结果 |
| --- | --- |
| 无绑定 + 无 `expected_revision` | 创建，`revision=1`，201 |
| 无绑定 + 有 `expected_revision` | 404 `binding_resource_not_found` |
| 有绑定 + 无 `expected_revision` | 409 `binding_conflict`（不会分叉出第二个活动绑定） |
| 有绑定 + 陈旧或错误 revision | 409 `binding_conflict` |
| 有绑定 + 正确 revision | 改绑/启停，`revision+1`，200 |
| 并发创建同一目标 | 唯一键裁决，恰好一个成功，其余 409 |

**锁顺序固定为：`application_types` → `pipeline_templates` → 绑定行**（READ COMMITTED）。这与模板发布、模板停用使用的顺序一致，因此绑定写入会与发布/停用排队而不是相互竞争；CD 模板不属于语言类型，跳过类型锁。绑定表只有本服务写入，不存在反向加锁路径。参数校验在取锁之前完成，无效参数不会持有类型/模板锁。

失败整体回滚：写后校验与提交在同一事务内，触发器失败会同时回滚绑定行与 revision。

## 5. 参数与固定版本校验

- 存储的是**调用方声明的绑定层**，不是合并结果。模板默认值保留在不可变版本里，读取不会把默认值伪装成显式绑定值，因此 `parameters` 读取接口不会出现 `release` 这类默认值。
- 写入前用 `pipelinebinding.ResolveParameters` 重新解析一次：必填可满足、字段已声明、原生类型、整数精度、大小上限；未知字段即使值相同也拒绝。B1 冻结的规则不变。
- 绑定层的上限沿用 B1：最多 32 个参数、单值 4096 字节、字符串 2048 字节、输入 JSON 16 KiB；**存储后**再按 MySQL 规范化结果限制 64 KiB，并在同一事务内回读确认。
- 固定版本校验在写入事务内完成：读取指定不可变版本、核对 v1 规范、归属与 canonical SHA-256；草稿不参与。规范或摘要损坏返回 503，不会把损坏定义复制进绑定。
- 通用 Secret 引用仍待 W08；绑定参数是普通声明字段，不能承载凭据。

## 6. 错误码

| HTTP | code | 触发 |
| --- | --- | --- |
| 400 | `invalid_request` | ID/JSON/未知或重复字段/缺少 `enabled` |
| 404 | `binding_resource_not_found` | 目标、版本或绑定不存在；目标已软删除；有 `expected_revision` 但无绑定 |
| 409 | `binding_resource_disabled` | 类型、模板或 CD 环境停用 |
| 409 | `binding_conflict` | 重复创建、陈旧 revision、并发唯一键冲突、死锁/锁等待超时 |
| 422 | `binding_ownership_mismatch` | 阶段/类型/目标归属不匹配，或 `version_id` 归属与声明不符 |
| 422 | `invalid_binding_parameters` | 参数不满足契约或超出存储上限 |
| 413 / 415 | `invalid_request` | 请求过大 / Content-Type 不支持 |
| 503 | `binding_store_unavailable` | 存储不可用、超时、固定版本规范或摘要损坏 |

业务错误只用稳定分类，不返回数据库诊断、规范内容或参数值。

## 7. 数据契约与最小权限

epoch 10 声明数据契约 `ci-cd-bindings-v1`，与既有契约一起在每个 epoch 校验：

- CI/CD 绑定必须指向存在的目标与版本，且版本所属模板种类与表一致；`revision>0`、`enabled∈{0,1}`、操作者 ID 为正。
- `parameters` 必须是 JSON 对象、规范化后不超过 64 KiB、顶层值只能是 string/boolean/integer。
- 契约不要求目标处于启用或未删除状态：后续合法操作不得让已应用的 epoch 失败。

运行时账号新增 `INSERT, UPDATE ON application_ci_bindings` 与 `app_config_cd_bindings`；`SELECT` 来自库级授权。**不授予 `DELETE`**，也不改变 `pipeline_template_versions` 的只增权限。迁移账号、guarded 迁移与恢复路径不变。

## 8. 验证与后续

- 单测：绑定存储的创建/改绑/revision 冲突、12 路并发创建与并发 CAS 各只有一个成功、参数与存储上限回滚、跨种类与跨类型拒绝、停用与软删除分类、错误脱敏。
- API：四角色矩阵、匿名/CSRF/Origin/legacy、审计失败前置拒绝、非审计路由不产生审计、敏感读取审计、错误码与脱敏、创建与改绑状态码、参数不进入元数据与审计。
- MySQL 8.4：epoch 10 逐 DDL 中间态 dirty 恢复、数据契约对损坏行的 fail-closed、目标外键、每目标唯一绑定、最小权限主体真实 SQL 拒绝 `DELETE` 与版本改写、HTTP 层与组合 grant 联调、写后 schema 兼容。
- 未执行：不使用执行器、不创建任务或回执、不登记产物；前端与真实 Java/Python CI 仍未验收。

下一次 B3 接独立运行上下文、幂等回执与旧引擎隔离，复用现有 Worker/attempt；在 C 具备产物能力前不接纳不可执行任务。
