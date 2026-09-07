# AppConfig 发布 API

本文是 W05 起发布预检、单发、批量和请求重放的前后端联调契约。架构原因、事务和数据模型见
[ADR-0004：以 AppConfig 为目标的原子幂等发布](../architecture/decisions/0004-appconfig-idempotent-releases.md)。

## 1. 通用约定

- BasePath：`/api/v1`。
- 所有接口使用服务端会话；本页五个接口均需要 `releases:create`。
- Cookie 鉴权的 POST 同时要求合法 Origin 和当前会话的 `X-CSRF-Token`。
- JSON 使用 `application/json`、严格 DTO、单一 JSON 值；未知字段、重复 key 和尾随内容被拒绝。
- 成功与失败沿用 `code/result/error/message/help` envelope；`error` 只返回本文列出的稳定机器码。
- 对本文列出的 W05 canonical 路由，`config_id`、`app_id`、`task_id` 是正 INT 并使用 JSON number；
  `workflow_id`、`workflow_version_id`、`record_id` 是 BIGINT，在 wire JSON 中必须使用仅含十进制
  数字的 string。canonical 前后端不得把这些 BIGINT ID 经过 JavaScript number/`float64`。
- 上述 BIGINT 规则不是全部历史 `/api/v1` 模型的全局契约。deprecated legacy 发布接口的
  `TaskRecord` 兼容投影仍维持既有 JSON number 形状；新 Web 不得消费它，旧客户端也不得据此
  推断 canonical 字段类型。
- 创建响应含 `Cache-Control: no-store`；每个响应继续携带 `X-Request-ID`。

Canonical 路由：

| 方法 | 路径 | 用途 | 是否要求 `Idempotency-Key` |
| ---- | ---- | ---- | -------------------------- |
| GET | `/releases/targets?env=:code&q=:keyword&page_num=1&page_size=20` | 按环境分页选择 AppConfig 目标 | 不适用 |
| POST | `/app-configs/:config_id/releases/preflight` | 单个目标预检 | 否，也不消费 key |
| POST | `/app-configs/:config_id/releases` | 创建一个发布任务 | 是 |
| POST | `/releases/batch/preflight` | 有序批量预检 | 否，也不消费 key |
| POST | `/releases/batch` | 原子创建有序批量结果 | 是 |

预检不是预留或锁定操作。客户端必须把预检返回的工作流版本带入创建请求，服务端仍会在创建事务
中重新校验授权、目标、环境、绑定、步骤、执行器和输入。

## 2. 输入模型

### 2.1 单发输入

路径中的 `config_id` 是唯一发布目标。预检与创建使用相同 body：

```json
{
  "ref": "main",
  "inputs": {},
  "expected_workflow_version_id": "42"
}
```

字段约束：

| 字段 | 必填 | 约束 |
| ---- | :--: | ---- |
| `ref` | 是 | trim 后不变、1～100 个 Unicode 字符的有效 UTF-8，不含控制字符；大小写敏感，不根据环境改写 |
| `inputs` | 否 | JSON object；省略规范为 `{}`，显式 `null` 或其他类型拒绝 |
| `expected_workflow_version_id` | 否 | 正 int64 的十进制 JSON string；Web 必须原样使用最近一次成功预检返回的值，JSON number 会被拒绝 |

规范化后的单项 `inputs` 不超过 64 KiB，嵌套深度不超过 16，JSON 节点不超过 1000。敏感字段名
继续递归拒绝，包括 password、token、secret、credential、authorization、cookie、key 及其常见
camelCase/复数形式。调用方只能传普通发布参数，凭据必须使用工作流配置中的 Secret 引用。

### 2.2 批量输入

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

`items` 必须有 1～100 项，每项遵守单发字段约束。一个请求内 `config_id` 不得重复；批次数组顺序
属于请求意图，响应始终以同一 `request_index` 顺序返回。整个 body 和规范化意图都不超过
1 MiB，所有可发布项合计最多生成 2000 条步骤快照。

## 3. 目标与预检

### 3.1 目标列表

`GET /api/v1/releases/targets` 要求 `env`，可选 `q`，并使用 1-based 的 `page_num/page_size`；页长
上限为 100。它返回该环境下的应用行：已有 AppConfig 时包含 `config_id`，没有时保留应用展示并以
稳定 `unavailable_code` 标记不可发布。环境、AppConfig、流程或执行器不可用都不能让前端自行猜测。

```json
{
  "code": 1,
  "message": "发布目标查询成功",
  "result": {
    "total": 1,
    "page_num": 1,
    "page_size": 20,
    "total_pages": 1,
    "targets": [
      {
        "config_id": 20001,
        "app_id": 10001,
        "app_name": "demo-api",
        "app_name_cn": "示例 API",
        "env": "qa-cn",
        "workflow_version_id": "42",
        "available": true,
        "steps": [
          {
            "key": "prepare",
            "name": "准备",
            "uses": "builtin.noop@v1",
            "available": true,
            "capabilities": {"logs": false, "cancel": false}
          }
        ]
      }
    ]
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

目标列表是选择器投影，不代替 preflight；`available` 也不是创建授权。响应不得包含 AppConfig 私有
运行参数、工作流 `with` 或集成配置。

### 3.2 单发预检

```http
POST /api/v1/app-configs/20001/releases/preflight HTTP/1.1
Content-Type: application/json
X-CSRF-Token: ...

{"ref":"main","inputs":{}}
```

成功读取返回 HTTP 200。`ready=false` 仍是成功的预检结果，不用失败 envelope 表达业务资格：

```json
{
  "code": 1,
  "message": "预检完成",
  "result": {
    "request_index": 0,
    "config_id": 20001,
    "app_id": 10001,
    "app_name": "demo-api",
    "app_name_cn": "示例 API",
    "env": "qa-cn",
    "workflow_id": "9",
    "workflow_version_id": "42",
    "workflow_version": 3,
    "workflow_revision": 3,
    "ready": true,
    "steps": [
      {
        "key": "prepare",
        "name": "准备",
        "uses": "builtin.noop@v1",
        "available": true,
        "capabilities": {"logs": false, "cancel": false}
      }
    ]
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

不可发布示例：

```json
{
  "code": 1,
  "message": "预检完成",
  "result": {
    "request_index": 0,
    "config_id": 20001,
    "app_id": 10001,
    "app_name": "demo-api",
    "app_name_cn": "示例 API",
    "env": "qa-cn",
    "ready": false,
    "error_code": "workflow_not_configured",
    "steps": []
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

`error_code` 只使用稳定分类。每个 step 也可用 `available=false/error_code=executor_unavailable`
或 `workflow_invalid` 说明具体阻断位置。服务端会使用当前 Executor 重新校验不可变版本中的
step `with`；校验细节不公开。响应不包含工作流 `with`、Secret、执行器外部引用、Jenkins
地址或上游错误。
AppConfig 不存在仍返回 404 `release_target_not_found`，不会用一个伪造 target 做资格结果。

### 3.3 批量预检

`POST /api/v1/releases/batch/preflight` 返回：

```json
{
  "code": 1,
  "message": "预检完成",
  "result": {
    "total_count": 2,
    "ready_count": 1,
    "failure_count": 1,
    "items": [
      {
        "request_index": 0,
        "config_id": 20001,
        "app_id": 10001,
        "app_name": "demo-api",
        "app_name_cn": "示例 API",
        "env": "qa-cn",
        "workflow_id": "9",
        "ready": true,
        "workflow_version_id": "42",
        "workflow_version": 3,
        "workflow_revision": 3,
        "steps": [
          {
            "key": "prepare",
            "name": "准备",
            "uses": "builtin.noop@v1",
            "available": true,
            "capabilities": {"logs": false, "cancel": false}
          }
        ]
      },
      {
        "request_index": 1,
        "config_id": 20002,
        "app_id": 10002,
        "app_name": "demo-worker",
        "app_name_cn": "示例 Worker",
        "env": "qa-cn",
        "ready": false,
        "steps": [],
        "error_code": "workflow_not_configured"
      }
    ]
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

批量预检逐项报告业务资格；JSON、重复目标、条目数或整体预算无效时整个请求失败。空的可选字段
可以按 JSON `omitempty` 省略，前端不能依赖空字符串或显式 null。预检不写幂等表、任务或步骤；
通用安全中间件仍会为该 POST 记录授权与最终结果审计。

## 4. Idempotency-Key

两个创建入口必须发送恰好一个未加引号的 ASCII token：

```http
Idempotency-Key: 8e03978e-40d5-43e8-bc93-6894a57f9324
```

项目契约是 16～128 bytes 且匹配 `[A-Za-z0-9][A-Za-z0-9._~-]*`。重复 Header、逗号列表、
引号、参数、前后空白、非 ASCII 或控制字符均返回 400 `idempotency_key_invalid`。它是 Ares
自有协议；客户端不要按任何过期草案或尚未发布的 RFC 猜测另一种语法。

客户端以一次用户确认后的不可变意图为单位生成 key。相同用户、相同操作、相同 key：

- body 规范摘要相同：服务端不重新预检或创建，原样返回已提交 receipt，并增加
  `Idempotency-Replayed: true`；
- body 摘要不同：返回 409 `idempotency_key_conflict`；
- 首个事务尚未结束且有界等待超时：返回 409 `idempotency_request_in_progress`，同时返回
  `Retry-After: 1`；
- commit 结果无法确认：返回 503 `outcome_unknown`，只能使用原 key 和逐字节冻结的原 body
  再试，不能生成新 key。

首次响应不发送 `Idempotency-Replayed`，重放时值固定为 `true`。原始 key 不会被响应回显。
W05 永久保留已提交 receipt，不自动过期或清理；客户端仍必须把每个 key 当作永不复用。

## 5. 创建发布

### 5.1 单发

```http
POST /api/v1/app-configs/20001/releases HTTP/1.1
Content-Type: application/json
Idempotency-Key: 8e03978e-40d5-43e8-bc93-6894a57f9324
X-CSRF-Token: ...

{"ref":"main","inputs":{},"expected_workflow_version_id":"42"}
```

首次创建与重放均返回相同 HTTP 状态和 body；成功为 HTTP 200，并以任务 URL 返回 `Location`：

```http
Location: /api/v1/deploy/publish/query/31001
```

```json
{
  "code": 1,
  "message": "发布任务已接纳",
  "result": {
    "record_id": "9001",
    "total_count": 1,
    "success_count": 1,
    "failure_count": 0,
    "items": [
      {
        "request_index": 0,
        "config_id": 20001,
        "success": true,
        "task_id": 31001,
        "workflow_version_id": "42"
      }
    ]
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

HTTP 请求只原子持久化任务和步骤，不等待执行器。随后使用既有任务详情和通用步骤接口观察运行。
创建事务发现目标不可发布时返回稳定失败，不留下 receipt、task 或 steps；客户端修复配置后应重新
预检，并为改变后的用户操作生成新 key。

### 5.2 批量

批量成功接纳返回 HTTP 200；业务上部分不可发布不改变整体 HTTP 成功状态：

```json
{
  "code": 1,
  "message": "批量发布任务已接纳",
  "result": {
    "record_id": "9002",
    "total_count": 2,
    "success_count": 1,
    "failure_count": 1,
    "items": [
      {
        "request_index": 0,
        "config_id": 20001,
        "success": true,
        "task_id": 31002,
        "workflow_version_id": "42"
      },
      {
        "request_index": 1,
        "config_id": 20002,
        "success": false,
        "error_code": "executor_unavailable"
      }
    ]
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

items 长度和顺序必须与请求完全相同。`success=true` 项的 task/steps 与所有结果项在同一事务提交；
数据库或内部错误使整批回滚。`success=false` 是已提交 receipt 的组成部分，相同 key 重放时不会重新
尝试；用户只重试失败项时必须创建新的单发/批量 body 和 key。

## 6. 稳定错误

| HTTP | `error` | 处理建议 |
| ---- | ------- | -------- |
| 400 | `invalid_request` | 修正路径、JSON、字段或 ref |
| 400 | `idempotency_key_invalid` | 为创建请求生成符合项目协议的新 key |
| 401 | `unauthenticated` | 收敛会话；canonical create 已发出时保留原 key/body，使用同一主体重新登录 |
| 403 | `forbidden` | 刷新权限；canonical create 已发出时保留原 key/body，不要登出仍有效会话 |
| 404 | `release_target_not_found` | 刷新 AppConfig 列表 |
| 409 | `workflow_version_changed` | 重新预检，用户确认后生成新 key |
| 409 | `idempotency_key_conflict` | 不得覆盖原意图；为新意图生成新 key |
| 409 | `idempotency_request_in_progress` | 按 `Retry-After` 用原 key/body 有界重试 |
| 409 | `environment_disabled` | 刷新环境和目标状态 |
| 413 | `request_too_large` | 缩小 inputs、批次或步骤规模 |
| 415 | `invalid_request` | 使用 `application/json` Content-Type |
| 422 | `workflow_not_configured` | 配置工作流后重新预检 |
| 422 | `workflow_invalid` | 修复无法安全读取或校验的流程版本 |
| 422 | `executor_unavailable` | 配置对应集成或选择其他可用工作流 |
| 500 | `internal_error` | 停止自动重试，但保留原 key/body；用 request ID 排障后只能重试原冻结请求 |
| 503 | `outcome_unknown` | 只能原 key/body 重试 |

代理可能在后端已经提交后生成没有 Ares envelope 的 `502/503/504`。Web 将所有 5xx 和网络无响应
都视为“提交结果可能不明确”：保留冻结的 key/body、停止自动重试，并只允许用户用同一对数据手动
重试。即使服务端实际已回滚，复用同一 key 也是安全的；生成新 key 则可能创建重复任务。
生产入口也可能返回自身定义的 429（不属于 Ares 稳定 `error` 集合）；canonical create 客户端同样
保留原 key/body，并按整数秒 `Retry-After` 等待后再开放手动重试。

批量已接纳后的逐项 `error_code` 使用同一稳定集合，但不会携带内部 message。重复目标和 inputs
形状/敏感键返回 400 `invalid_request`；body、inputs、节点、条目数或总步骤预算超限返回
413 `request_too_large`。服务端不得把 SQL、Xorm、
执行器配置、远端响应正文或地址放入 `error/message/help`。

## 7. 旧接口适配期

旧入口保留一个发布版本：

- `POST /api/v1/deploy/publish`
- `POST /api/v1/deploy/publish/batch`

Adapter 映射 `branch -> ref`、`extra_data -> inputs`；`is_rundeck` 不再控制发布步骤，只为旧 DTO
解析保留。旧、新单发共享幂等作用域；旧、新批量也共享幂等作用域。带 key 的请求会先按当前
`actor_user_id + semantic_operation + key digest` 查询已有 receipt：命中时，以 receipt items 中按原请求
顺序保存的 `config_id` 为目标身份，再使用包含软删除行的历史 AppConfig 与应用记录校验原
`app_name + env` alias，最后进入 canonical 摘要比较与重放。这样目标后来被停用、软删除，或活动
alias 变得歧义时，仍不能遮蔽已经提交的任务；alias 与冻结目标不匹配则返回 409，而不会改用当前
活动目标。

只有不存在已有 receipt 时，Adapter 才把活动且唯一的 `app_name + env` 解析为 `config_id`。活动
解析完成后会再次检查相同幂等作用域；命中在 alias 查询期间提交的竞争者时，改用其历史目标完成
摘要比较，不信任刚得到的活动映射。若竞争提交发生在二次检查之后，canonical 唯一键竞争与摘要
比较仍会最终收敛，不会创建第二份结果。

旧调用带合法 key 时获得与 canonical 相同的原子去重；不带 key 时继续执行一次非幂等兼容创建，
无法保护网络重试。所有旧响应包含 `Deprecation: true` 和 299 Warning，不承诺尚未评审的移除日期。
Ares Web 不得调用旧路由，也不得在 canonical 请求失败时 fallback。旧响应基于稳定 receipt/task ID
重新读取实时兼容投影，状态和步骤可能变化，不保证重放 body 逐字节相同；详情读取暂时失败时只
返回持久化 `task_id`，不伪造任务状态或时间。该 `TaskRecord` 兼容投影保留历史 JSON number
序列化，包括其中的 BIGINT 字段；这是 deprecated adapter 的限时兼容例外，不改变本文 canonical
路由的十进制 string 契约。

无 key 的 legacy 批量仍按历史 partial 语义逐项产生独立 receipt。审计中的首、末 receipt 仅是按
处理位置选取的有界样本，不是连续 ID 范围，也不代表中间所有 receipt；完整结果应以持久化 receipt
关联为准。

该无新 schema 的兼容方案依赖 `apps.app_name` 以及 `app_configs` 的 `env/app_id` 在记录生命周期内
不可变，并依赖应用与 AppConfig 只做软删除。未来若支持应用重命名、AppConfig 换环境或换应用归属，
或允许硬删除上述记录，必须先设计版本化 alias snapshot，并把它纳入 receipt 与请求摘要的兼容迁移；
不能继续依赖可变当前行猜测旧请求身份。

## 8. 前端请求状态机

前端提交时必须：

1. 根据当前表单构造严格 DTO，调用 preflight；
2. 展示目标、环境、工作流版本、有序步骤和逐项稳定 `error_code`；严格校验响应顺序与目标，只保留
   `ready=true` 且已回填精确工作流版本的项；
3. 零通过时禁止创建；否则在用户确认后生成一次 `crypto.randomUUID()`，冻结规范 body 与 key；
4. HTTP 2xx 必须先校验 receipt 的 ID、计数、顺序、目标、版本和逐项不变量；校验通过才清除冻结 pair 并展示
   每个 task 链接；
5. 网络无响应、401、403、408、429、所有 5xx、`outcome_unknown` 或
   `idempotency_request_in_progress` 都保留冻结 pair，
   停止自动重试并只允许用原 pair 有界手动重试；
6. 用户编辑目标、ref、inputs，或重新接受新的工作流版本时，废弃旧 pair，重新预检并生成新 key。

一旦 canonical create 已发出，401、403 和 429 都不能证明此前使用同一 pair 的尝试没有提交：认证、
授权或流量控制可能在幂等记录查询之前拒绝后续重放，网关也可能返回自己的响应。因此 Web 不得据此
清除 pair。429 按整数秒 `Retry-After`（前端等待上限 30 秒）再允许手动重试；401 必须由**同一个
`actor_user_id`** 恢复会话后重试。canonical create 跳过全局 401 跳转，以保留当前页面的内存冻结
pair；页面提示在另一标签页重新登录，重试按钮先刷新会话、取得新 CSRF，并核对当前 user ID 与冻结
ID 完全相同。主体为空、会话无法确认或 ID 不同都在发出创建请求前本地拒绝，同时继续保留 pair。
403 必须等待同一主体恢复权限。不同账号不得重放该 pair，因为服务端幂等作用域随
`actor_user_id` 改变，换账号会成为新的发布命令并可能造成重复任务。其他确定的 4xx/业务失败结束
本次提交。

“确定的 4xx”必须同时命中本文稳定错误表中的 HTTP 状态与 `error` 组合。缺少 Ares envelope、未知
错误码或码/状态错配（包括畸形 409/422）都可能由代理替换响应产生，Web 必须继续视为结果不明确并
保留 pair，不能生成新 key。

提交中或结果不明确时，Web 阻止路由离开并在
刷新/关闭前警告；W05 不把 key/body 写入 localStorage/sessionStorage，也不把 key 放进 URL。用户强制离开后必须先
核对任务/审计记录，不得盲目使用新 key 重提。任务进度
只根据服务端任务与步骤状态计算；后端没有步骤统计时显示不确定进度，不能伪造 10%/50%/100%。

## 9. 最小验收矩阵

- Header 的缺失、重复、列表、引号、非 ASCII、15/16/128/129 bytes 和大小写差异。
- inputs 省略与 `{}` 摘要相同，显式 `null` 拒绝；相邻超 `2^53` 整数摘要不同。
- 同用户跨 session/进程重放，不同用户同 key 隔离，同 key 更换任意意图字段返回冲突。
- keyed legacy 在目标停用/软删除或活动 alias 歧义后仍优先按 receipt 的有序 config IDs 重放；旧
  `app_name + env` 与历史目标不匹配时稳定冲突，并覆盖 alias 解析复查及 canonical 唯一键竞争的
  并发收敛路径。
- 单发 20～100 并发只有一个任务；批量混合结果、最大批次和重启后重放保持顺序。
- 预检后流程切版、环境停用、AppConfig/域名变化不会创建撕裂快照。
- 任意事务故障没有半条 record、item、task 或 step；commit 不明确可由原 key 收敛。
- Web 超时、401/403/429 或网关 5xx 后重试不换 key；429 遵守 `Retry-After`，401 只由同一
  `actor_user_id` 恢复会话；编辑后不复用 key，不回退 legacy，不展示假进度。
- 响应、审计、应用日志和外部执行器均不泄露客户端 key、摘要、inputs 或内部集成信息。
