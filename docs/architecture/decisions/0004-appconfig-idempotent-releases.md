# ADR-0004：以 AppConfig 为目标的原子幂等发布

- 状态：已接受
- 日期：2026-09-07
- 适用版本：W05 起
- 决策范围：发布目标、预检、请求幂等、批量原子性、任务快照与兼容接口

## 背景

Ares 已经把环境目录、AppConfig、不可变工作流版本和通用步骤执行器拆开，但历史发布入口仍以
`app_name + env + branch` 定位目标。一次请求会分开读取应用、环境、AppConfig、域名和工作流，
随后才在另一个事务中创建任务与步骤；并发修改可能让同一个任务混合多个时刻的数据。客户端在
超时后重试也会创建第二个任务。批量入口进一步把条目交给多个 goroutine 独立提交，进程崩溃时
无法判断哪些条目已经创建。

发布是可能触发外部副作用的命令。W05 必须在调用执行器前解决两类问题：用稳定 `config_id`
表达目标，并把客户端请求指纹、任务、工作流步骤快照和批量结果放入同一个数据库原子边界。
Jenkins、Kubernetes、Redis 或 RabbitMQ 都不是这项正确性的前提。

## 决策摘要

1. Canonical 单发入口为 `POST /api/v1/app-configs/:config_id/releases`，对应预检入口为
   `POST /api/v1/app-configs/:config_id/releases/preflight`。
2. Canonical 批量入口为 `POST /api/v1/releases/batch`，对应预检入口为
   `POST /api/v1/releases/batch/preflight`。批次数组有序，顺序属于请求语义。
3. 发布输入收敛为 `config_id + ref + inputs + expected_workflow_version_id`。应用名和环境名只是
   展示快照；工作流决定实际步骤，客户端不能用 `is_rundeck` 等开关选择执行器。
4. 两个创建入口都要求一个 `Idempotency-Key`。这是 Ares 自己的项目协议：一个未加引号的
   ASCII token，长度 16～128 bytes，匹配 `[A-Za-z0-9][A-Za-z0-9._~-]*`。本决策不宣称遵循
   尚未成为正式标准或已经过期的外部草案。
5. 唯一作用域是 `(actor_user_id, semantic_operation, key_digest)`。`actor_user_id` 来自服务端
   会话；不使用用户名、显示名、session、IP 或客户端字段。`config_id` 不进入唯一作用域，而是
   进入请求摘要，因此同一用户拿同一个 key 更换发布目标会冲突。
6. 请求摘要来自严格解码后的版本化意图 DTO，并使用规范 JSON 与 SHA-256。摘要保留 JSON 精确
   数值语义，不经过 `float64`。客户端提交的 `expected_workflow_version_id` 进入摘要；服务端解析的
   当前工作流、域名、时间戳、生成镜像名和发布人展示名不进入摘要。
7. Schema epoch 6 为 `task_record` 增加可空 `app_config_id`，并新增
   `release_idempotency_records`、`release_idempotency_items`。历史任务允许目标为空；所有 W05
   canonical 任务必须写入稳定 AppConfig ID。
8. 幂等记录、所有成功项的任务与步骤快照、全部有序结果项在同一个 MySQL 事务提交。数据库错误
   回滚整个命令；业务不可发布项可以作为批量结果的一部分与成功项一起提交。
9. 相同作用域下相同 key 与相同摘要只重放已提交结果；摘要不同返回
   `409 idempotency_key_conflict`。重放不重新预检，也不再次创建任务。
10. 旧单发和批量接口保留一个发布版本：带 key 的重放优先使用已有 receipt 的有序 `config_id`
    校验历史 alias，首次请求才解析活动 `app_name + env`，两者最终调用同一领域命令；旧接口缺少 key
    时仍是明确的非幂等兼容行为。Web 只调用 canonical API，不做静默回退。
11. 客户端 key 只标识 Ares 发布命令，绝不进入步骤配置、日志或外部系统。执行器继续使用独立的
    `task_id / step_key / attempt` 幂等键。

## Canonical 资源与输入

单发预检和创建共享以下 body：

```json
{
  "ref": "main",
  "inputs": {},
  "expected_workflow_version_id": "42"
}
```

批量接口共享以下 body：

```json
{
  "items": [
    {
      "config_id": 20001,
      "ref": "main",
      "inputs": {},
      "expected_workflow_version_id": "42"
    },
    {
      "config_id": 20002,
      "ref": "release/2026-09",
      "inputs": {"region": "cn-north"},
      "expected_workflow_version_id": "57"
    }
  ]
}
```

`ref` 是大小写敏感的一次发布输入，不由环境名称推导。`inputs` 省略时规范为 `{}`，显式 `null`
和非 object 值被拒绝。`expected_workflow_version_id` 可省略；省略表示在创建事务中接受当时的当前
版本，显式提交则必须与锁内版本相等。Ares Web 总是提交预检返回的版本，以便流程在确认到创建
之间切版时得到 `409 workflow_version_changed`，而不是无提示地发布另一套步骤。

本 ADR 新增的 W05 canonical 路由中，wire JSON 的 `workflow_id`、`workflow_version_id`、
`record_id` 等数据库 BIGINT 一律使用十进制字符串，避免浏览器越过 `2^53-1` 后丢失精度；
`config_id`、`app_id`、`task_id` 是数据库 INT，仍使用 JSON number。服务端把预期版本字符串严格
解析为正 int64 后，内部摘要模型才使用整数值。这不是对全部历史 `/api/v1` 响应的全局改线：
deprecated legacy 发布接口返回的 `TaskRecord` 兼容投影继续维持既有 JSON number 形状，canonical
Web 不得消费该投影。未来若要修正旧投影中的 BIGINT 精度风险，必须通过独立、版本化的兼容性决策。

Canonical JSON body 最大 1 MiB；单项规范化 `inputs` 最大 64 KiB、最大嵌套深度 16、最多
1000 个 JSON 节点。批量包含 1～100 项，`config_id` 不得重复，规范化总大小仍不得超过 1 MiB，
解析后的步骤快照总数不得超过 2000。超过限制在事务和执行器调用前失败。所有 object 都拒绝重复
key，DTO 拒绝未知字段，敏感 input key 沿用递归拒绝规则。

预检读取与创建相同的公开资格条件，但它只是提示，不预留 key、版本或资源。响应可以公开应用、
环境、工作流名称、版本 ID、有序步骤的 `key/name/uses/capabilities` 及稳定阻断原因；不得返回步骤
私有配置、Secret、external reference、Jenkins 地址或原始探测错误。创建事务必须重新校验，不能
把预检结果当作授权或一致性证明。

## Idempotency-Key 项目协议

创建请求必须只有一个 Header 值，例如：

```http
Idempotency-Key: 8e03978e-40d5-43e8-bc93-6894a57f9324
```

服务端拒绝缺失、空值、重复 Header、逗号折叠列表、引号、参数、空白、控制字符、非 ASCII、
不足 16 bytes、超过 128 bytes 或不匹配项目正则的值。key 大小写敏感，不 trim、不转小写，也不做
Unicode、URL 或百分号规范化。浏览器使用 `crypto.randomUUID()` 生成；每个新的用户意图只生成
一次，之后不主动复用。

数据库不保存原始 key，只保存带固定域分隔的 SHA-256 摘要。原始 key、key digest 和请求摘要都不
进入响应、应用日志、审计资源、执行器参数或外部请求。唯一作用域使用以下版本化操作名：

```text
release.create@v1
release.batch.create@v1
```

旧、新单发路由映射到同一个 `release.create@v1`；旧、新批量路由映射到同一个
`release.batch.create@v1`。不同用户可以安全使用相同原始 key，但查询和唯一约束始终同时包含
各自的稳定用户 ID，不能据此读取其他用户结果。

单发摘要的内部逻辑形式如下；wire 上的十进制 string 已在严格解码后转成 int64，因此这里有意
表现为 JSON number：

```json
{
  "schema": "release.create@v1",
  "config_id": 20001,
  "ref": "main",
  "inputs": {},
  "expected_workflow_version_id": 42
}
```

批量使用 `release.batch.create@v1` 并按请求顺序包含完整 items。省略的 `inputs` 先规范为
`{}`；省略的预期版本先规范为 `null`。JSON key 顺序、无意义空白和等价数字表达得到相同摘要，
数组顺序、ref 大小写、目标、输入或预期版本变化得到不同摘要。解码器必须使用 `json.Number` 或
原始 JSON 经过严格验证后再规范化；禁止把 `9007199254740992` 与 `9007199254740993` 先转为
`float64`。规范化整数继续遵守 ADR-0002 的 4096 位上限。

## 数据模型

Epoch 6 增加以下业务关系：

| 表/列 | 关键字段 | 职责 |
| ----- | -------- | ---- |
| `task_record.app_config_id` | `INT NULL`，索引 `idx_task_app_config` | 保存任务创建时解析出的稳定目标；旧任务保持 NULL |
| `release_idempotency_records` | `idempotency_id`、`actor_user_id`、`semantic_operation`、`key_digest BINARY(32)`、`request_digest BINARY(32)`、`item_count`、`created_at` | 每个已提交发布命令一条不可变 receipt |
| `release_idempotency_items` | `record_id`、`request_index`、`config_id`、可空 `task_id`、可空 `workflow_version_id`、`outcome`、可空 `error_code`、`created_at` | 按原请求位置保存成功或业务失败结果 |

`release_idempotency_records` 以 `uk_release_idempotency_scope_actor_key` 对
`(semantic_operation, actor_user_id, key_digest)` 建物理唯一索引；三列共同构成前述逻辑作用域，
列顺序不改变隔离语义。
`release_idempotency_items` 以 `(record_id, request_index)` 唯一定位，`request_index` 从 0 开始；
`item_count` 必须与完整连续的结果集合一致。`task_id` 只在成功创建时存在，并且每个任务只能属于
一个 receipt item。`outcome` 只允许 `accepted` 或 `rejected`：accepted 必须同时有 task/workflow、
没有 error；rejected 必须没有 task/workflow 且只有稳定 `error_code`。结果表不保存 `ref`、原始
`inputs`、错误详情、响应正文或凭据。表不使用级联外键删除历史，epoch 数据契约显式验证 record、
item、任务、主体、AppConfig ID 和工作流 ID 的逻辑关联。

这两张表是只增 receipt。一次提交不需要把持久记录从 pending 更新为 completed：reservation 行在
事务内先插入，随后插入任务、步骤和完整 items，只有全部完成才 commit；连接崩溃会回滚不可见的
reservation。W05 永久保留已提交记录，不提供自动过期、清理或删除入口，运行时对两张表只授予
`INSERT`（以及继承的全库 `SELECT`），不授予 `UPDATE` 或 `DELETE`。未来如需保留期与清理，必须
另立兼容性、安全和运维决策；客户端始终把 key 当作永不复用。结构校验发现 record 缺 item、索引
不连续、item 引用的任务缺失或摘要长度异常时必须 fail-closed，不能据此重新创建任务。

## 原子创建与并发

单发的数据库事务按以下逻辑完成：

1. 以服务端主体、语义操作和 key digest 插入 reservation；
2. 按统一锁顺序读取并锁定 AppConfig、应用、环境、当前流程绑定和域名；绑定指向的版本是
   append-only 不可变行，只做普通一致读取和 checksum 校验，不执行 `FOR UPDATE`；随后重新校验
   软删除、环境启用、预期版本与执行器可用性；每个当前 Executor 还必须重新 `Validate`
   已存储的 step `with`，以防二进制升级收紧配置契约后仍创建必然失败的任务；
3. 从同一快照构造运行上下文，插入带 `app_config_id` 的任务；
4. 插入该不可变工作流版本的全部步骤快照；
5. 插入 request index 为 0 的 receipt item；
6. commit 后才返回任务。

批量先保留原始顺序，再按 `app_configs -> apps -> env_configs -> app_config_workflows ->
release_workflows -> app_config_domains` 的全局表顺序，并在各表内按稳定 ID/代码顺序锁定所有目标
及其依赖；不可变 `release_workflow_versions` 只做普通读取。这样即使不同批次没有共同 config、却
交叉引用应用、环境或流程，也不会因图遍历顺序形成锁环。所有域名
create/overwrite/patch/delete 也在事务中先锁定同一 AppConfig 父行，再读写
`app_config_domains`；因此即使数据库使用 `READ COMMITTED`，空域名集合也不能在发布快照期间穿透插入。
可发布项的任务与步骤、所有成功/业务失败 items 和一条 batch record 在同一事务提交。业务失败
不会抹掉同批成功项；数据库、序列化或内部一致性错误会回滚整批，不留下部分任务或 receipt。
解析全部不可变步骤并确认 2000 条总预算后，才检查执行器可用性。批量事务不启动逐项 goroutine，
也不在持锁期间调用 Jenkins、Kubernetes 等远端服务。
`AvailabilityChecker` 在这个边界只能读取进程内已提交的集成快照，不得发起网络探测。任务提交后
仍由 Worker 异步执行。

唯一键竞争时，失败方等待数据库确定胜者事务结果，然后只读取已提交 record：

- 摘要相同：canonical 路由返回原始稳定 receipt，附加 `Idempotency-Replayed: true`；
- 摘要不同：返回 `409 idempotency_key_conflict`；
- 等待超过有界期限：返回 `409 idempotency_request_in_progress` 和整数秒 `Retry-After`；
- record 与 items 不满足结构不变量：返回 `500 internal_error`，不重建；
- commit 返回结果不明确：返回 `503 outcome_unknown`，要求客户端只以相同 key 和冻结 body 重试。

认证、授权、CSRF、Header、JSON 形状和批量级限制失败发生在 reservation 前，不消费 key。显式
preflight 从不消费 key。单发在锁内发现目标不可发布或预期版本变化时回滚且不创建 receipt；修复
配置后可以沿用原 key，但客户端通常应在重新预检、编辑意图后生成新 key。批量逐项业务失败属于
被接受的有序 batch receipt；重放不会因外部状态变化而重试失败项，重试失败项必须构造新命令和
新 key。

## 稳定错误边界

Canonical JSON 错误继续使用统一 envelope，只公开稳定分类：

| HTTP | `error` | 含义 |
| ---- | ------- | ---- |
| 400 | `invalid_request` | 路径、JSON、未知/重复字段或 ref 无效 |
| 400 | `idempotency_key_invalid` | key 缺失、重复、格式或长度无效 |
| 401 | `unauthenticated` | 会话无效 |
| 403 | `forbidden` | 缺少 `releases:create` 或读取预检所需权限 |
| 404 | `release_target_not_found` | AppConfig 或所属应用不存在/已删除 |
| 409 | `workflow_version_changed` | 当前工作流版本与预期版本不同 |
| 409 | `idempotency_key_conflict` | 同一作用域 key 已用于不同摘要 |
| 409 | `idempotency_request_in_progress` | 相同 key 的首个事务仍在处理 |
| 409 | `environment_disabled` | 目标环境当前禁止新发布 |
| 413 | `request_too_large` | 请求、inputs、节点或总步骤预算超限 |
| 415 | `invalid_request` | Content-Type 不是 JSON |
| 422 | `workflow_not_configured` | AppConfig 没有有效当前流程 |
| 422 | `workflow_invalid` | 当前流程版本无法安全读取或校验 |
| 422 | `executor_unavailable` | 流程所需执行器或可选集成当前不可用 |
| 500 | `internal_error` | 数据库结构或内部状态无法安全解释 |
| 503 | `outcome_unknown` | 数据库提交结果无法确认，只能原 key 重试 |

预检以 HTTP 200 返回逐项 `ready` 与可选 `error_code`，步骤也可带自己的可用性和稳定错误；只有
请求整体无效、未认证或无权限时使用失败 envelope。批量创建返回与原请求等长、按
`request_index` 排序的 items，每项以 `success` 区分结果：成功项包含
`task_id/workflow_version_id`，失败项包含稳定 `error_code`。内部错误、执行器探测正文和数据库
错误不进入响应。

生产入口可在上述 Ares 稳定 `error` 集合之外返回自身定义的 429。canonical create 客户端仍将其
视为结果未确认，保留原 key/body，并遵守整数秒 `Retry-After` 后再开放手动重试。

## 旧接口兼容

以下路由保留一个版本：

- `POST /api/v1/deploy/publish`
- `POST /api/v1/deploy/publish/batch`

Adapter 严格解析旧 DTO 并映射 `branch -> ref`、`extra_data -> inputs`。`is_rundeck` 只为旧请求
形状保留，不参与目标、摘要或步骤选择；实际执行器完全来自 AppConfig 当前工作流。

带 key 的旧请求先在 `(actor_user_id, semantic_operation, key_digest)` 作用域查找已有 receipt。
命中时，Adapter 以 receipt items 中按原请求顺序保存的 `config_id` 为目标身份，再用 Unscoped 的
历史应用/AppConfig 行验证原 `app_name + env` alias，随后进入 canonical 摘要比较与重放。目标停用、
软删除或活动 alias 后来变得歧义都不能遮蔽已提交结果；alias 与历史目标不一致时返回稳定冲突，
不会改用当前活动映射。只有 receipt 不存在时才以活动且唯一的 `app_name + env` 解析 `config_id`；
活动解析完成后再次检查相同作用域，若竞争请求在查询期间提交，则改用其历史 config IDs。若竞争
提交发生在二次检查之后，canonical 唯一键竞争与摘要比较仍会最终收敛，不会创建第二份结果。
解析不唯一、目标不存在或字段冲突时返回稳定错误，不猜测环境别名。

旧请求带合法 `Idempotency-Key` 时进入与 canonical 路由相同的 semantic operation 和原子服务；
缺失时为兼容旧客户端执行一次明确的非幂等创建，网络重试仍可能重复。所有旧响应携带
`Deprecation: true` 和 299 Warning，不虚构 Sunset 日期。新 Web 不调用旧路由，不在 404/5xx 后
自动 fallback。旧响应是基于稳定 receipt/task ID 重新读取的兼容投影，任务状态和步骤可能随执行
进度变化，不承诺逐字节冻结；若提交后详情暂时不可读，响应只返回已持久化 `task_id`，不会伪造
`queued`、零时间戳或把已提交结果改报失败。移除 adapter 需要独立评审、使用量证据和新的 contract
版本。

这项不新增 alias snapshot 列的兼容设计依赖 `apps.app_name` 与 `app_configs.env/app_id` 不可变，
并依赖应用和 AppConfig 只做软删除。未来支持应用重命名、AppConfig 换环境、换应用归属或硬删除前，
必须先通过独立 ADR 与迁移保存版本化 alias snapshot，并明确它与请求摘要、历史重放的关系；不能用
新的当前 alias 解释已经提交的旧 key。

## 安全与审计

- 所有发布与预检先认证、授权；Cookie 写请求还要通过 Origin/CSRF，之后才触碰幂等记录。
- 发布人及 `actor_user_id` 只来自当前 `Principal`。重放时重新鉴权；权限已经撤销的用户不能借
  历史 key 取得结果或创建任务。
- 审计通过路由 action、HTTP 状态和受限资源标识区分首次创建、replay、conflict、
  in-progress 与含 rejected 的批次。单发成功记录 receipt/config/task ID，批量只记录 receipt ID 和
  成败计数，逐项 ID 通过只增 receipt 关联；审计不记录 key、hash、摘要、ref、inputs 或步骤私有数据。
- 仅 deprecated legacy 无 key 的 partial 批量兼容路径会为各项创建独立 receipt；其审计只记录
  receipt 数量及首、末处理位置的有界 receipt 样本。样本不是连续 ID 范围，也不表示覆盖中间全部
  receipt；完整关联仍以只增 receipt 表为准。
- 请求摘要只能用于同一主体下常量时间比较，不能成为查询接口或暴露给客户端。
- Web 的 capabilities 与 preflight 只改善体验，服务端创建事务仍是最终授权和资格边界。
- 客户端请求 key 与执行器幂等键拥有不同命名空间、生成者和生命周期，任何 adapter 都不得混用。
- canonical create 一经发出，Web 对 401、403、429 也保持结果未确认状态，不清除冻结 key/body；429
  遵守 `Retry-After`。该请求跳过全局 401 跳转以保住仅存于内存的 pair；401 后在另一标签页恢复
  认证，原页面重试前刷新会话与 CSRF，并将当前 user ID 与冻结时的 `actor_user_id` 比较。主体为空、
  无法确认或不同均在创建请求前失败关闭并保留 pair；换账号会进入不同幂等作用域，必须先核对原任务
  而不能直接重试。
- Web 只在响应同时命中已知稳定错误码及其规定 HTTP 状态时，才把 4xx 视为确定拒绝并清除 pair；
  缺少 envelope、未知错误码或码/状态错配的 4xx（包括畸形 409/422）一律失败关闭为结果未确认。

## 发布、回退与验收

部署顺序为：停旧写入并备份，执行 epoch 6 迁移与 manifest/权限检查，部署支持 epoch 6 的后端，
最后部署只使用 canonical API 的前端。旧 adapter 让旧前端在一个版本内仍可工作，但不允许 epoch 5
后端连接 epoch 6 可写数据库。

回退前端不改变 schema；回退后端必须停止写入并恢复 epoch 6 迁移前备份及匹配的 epoch 5 应用，
不能只替换旧镜像。完整操作见[数据库迁移与恢复手册](../../operations/database-migrations.md)。

验收至少覆盖：

- 同一用户跨 session、跨进程并发提交相同 key/摘要，只产生一个 record、一个 task 和一组 steps；
- 同 key 换 config/ref/inputs/预期版本得到 409，不泄露原请求；不同用户同 key 互不影响；
- 超过 `2^53` 的相邻整数摘要不同，等价 JSON 表达摘要相同；
- reservation、task、部分 steps/items、commit 结果不明确等故障点都不会留下可见半成品；
- 批量混合结果保持输入顺序，数据库故障整批回滚，重启后相同 key 原样重放；
- keyed legacy 在目标停用/软删除或活动 alias 歧义后仍按 receipt 的有序 config IDs 重放；alias 与
  历史目标不符时稳定冲突，活动解析期间的竞争提交由二次 receipt 查询或后续 canonical 唯一键竞争收敛；
- 预检后切换工作流、停用环境或删除目标只会创建一致快照或返回稳定冲突；
- Web 在超时和 `outcome_unknown` 后使用原 key/冻结 body，在用户编辑后使用新 key；
- Web 在 401/403/429、网关 5xx 或连接中断等提交结果不明确场景同样保留原 key/冻结 body，不能生成
  新 key；429 遵守 `Retry-After`，401 后只允许同一 `actor_user_id` 恢复会话并重放；
- 原始 key、hash、摘要、inputs、Secret 和 Jenkins 地址不会出现在响应、日志或审计；
- 外部执行器收到的仍是 `task_id/step_key/attempt`，绝不是客户端 Header。

## 后果

发布命令获得了跨重试、跨进程和重启后的数据库级去重能力，任务也能稳定追溯到 AppConfig。
代价是批量创建需要一个有界但较大的事务，发布 DTO 和结果成为版本化协议，永久 receipt 会持续
增加两张只增表的存储。预检仍只是用户体验能力；任何真正创建都必须支付一次锁内权威校验成本。

永久保存也意味着 epoch 6 的 schema verifier 必须持续校验完整回执历史，启动与 `migrate status`
的时间和数据库资源开销会随 record/item 规模变化。本 ADR 不声明未经实测的固定性能数字；生产
上线、数据规模显著增长或数据库/查询条件变化前，必须按[数据库迁移与恢复手册](../../operations/database-migrations.md#永久-receipt-verifier-的容量基线与告警门槛)
在隔离的生产规模数据上重跑基线。相对于已审批的 schema 检查时限，p95 达到 50% 时告警并制定
容量处置计划，达到 80% 或发生超时、取消、校验失败时阻止发布；不得通过删除或改写永久回执绕过
该门禁。
