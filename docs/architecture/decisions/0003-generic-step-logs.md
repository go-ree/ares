# ADR-0003：执行器通用步骤日志与游标续传

- 状态：已接受
- 日期：2026-09-07
- 适用版本：W03 起
- 决策范围：任务步骤日志定位、执行器分派、SSE 协议、兼容路由与资源边界

## 背景

Ares 的 v2 发布任务已经把每个步骤的 `step_key`、`uses`、配置快照和 opaque
`external_ref` 保存到 `task_step_records`，执行器注册表也已经预留 `LogReader` 和
`capabilities.logs`。但是当前日志入口仍以 Jenkins 的固定 `ci` / `cd` 阶段为中心：前端通过
任务兼容字段判断 Job 与 Build，服务端旧路由再把它们映射为 Jenkins progressive log 请求。
这使日志展示无法覆盖任意数量、任意顺序的步骤，也让新的执行器不得不复制 Jenkins 专用入口。

[ADR-0002](0002-authentication-rbac-audit.md) 已经确定流式接口使用同源 HttpOnly Cookie、
`logs.read` 权限、定期会话复验、有限写入期限和有界重连。本 ADR 在该安全边界上补全执行器
分派和 cursor 协议，不改变认证模型。

## 决策摘要

1. 通用日志的唯一 canonical 入口为
   `GET /api/v1/tasks/:task_id/steps/:step_key/logs/stream`。
2. 路径只标识 Ares 自己的任务和步骤。客户端不能提交 `uses`、执行器配置、external reference、
   Jenkins Job、Build ID 或服务地址。
3. 服务端以 `(task_id, step_key)` 读取不可变步骤快照，从快照取得 `uses` 与 `external_ref`，再由
   Registry 找到对应执行器并分派 `LogReader`。通用层不解释执行器私有引用。
4. `Descriptor.Capabilities.Logs=true` 与实现 `LogReader` 必须同时成立；两者不一致时执行器注册
   失败。`builtin.noop@v1` 等不支持日志的执行器明确返回 `logs=false`，不能伪造空日志。
5. canonical 入口只接受一个可选 query 字段 `cursor`。未知或重复 query 字段均返回 400。
6. `cursor` 是执行器拥有、服务端透传的续传位置。通用边界要求它不超过 256 bytes、是有效
   UTF-8，且不包含 CR、LF 或 NUL；执行器可以继续收紧格式。`jenkins.job@v1` 只接受无符号
   十进制 int64 offset。
7. 首次读取通过省略 `cursor` 表示从执行器定义的起点开始；显式空 query 或 Header 无效。Ares
   Web 使用可观察建流 HTTP 状态的同源 fetch SSE transport，并在手工重连时把最后确认值放入
   `cursor` query；其他合规客户端也可以发送 `Last-Event-ID`。Header 必须是单值并满足同一
   cursor 校验；query 与 Header 同时存在时必须字节级一致，否则返回 400。
8. cursor 表示“下一段尚未确认内容的位置”。每个 `log` 事件的 `id` 和 payload cursor 都是该段
   内容之后的续传位置；客户端只有在处理该事件后才能保存 cursor。以该值重连不得重复或跳过
   已确认内容。
9. SSE 只使用 `log`、`ping`、`end`、`stream-error` 和 `auth-expired` 五种事件。普通 API
   envelope、执行器错误正文和上游响应不得混入流。
10. 服务端在发送 200 前完成身份/权限、参数、任务步骤归属、日志能力、引用就绪和日志来源
    一致性检查，并在第一次 LogReader/上游请求前取得连接配额。Jenkins 地址或实例不匹配必须在
    任何外部网络请求前失败。
11. 通用流处理器统一负责连接准入、总时长、上游空闲时间、心跳、滚动写 deadline、会话复验、
    backpressure 和 context 取消。执行器只按 cursor 有界读取日志，不创建脱离请求生命周期的
    后台流。
12. 日志读取是敏感读取：需要 `logs.read`。每次鉴权通过的连接请求写入有界的 `authorized` 与
    最终 `succeeded/failed` 审计事件，不按 chunk 写审计。路由层形成 `task_id/step_key` 资源，
    不记录 cursor、external reference、上游 URL、日志正文或凭据。
13. 旧 `/api/v1/job/stream/log` 与 `/api/v1/deploy/log/stream` 标记 deprecated，只服务
    `engine_version=1` 的历史任务。v2 任务必须使用 canonical 步骤入口；旧入口仍只能根据存储的
    v1 任务字段解析 CI/CD 引用，不能恢复任意 Job/Build 查询。
14. W03 不保存日志、不保存 cursor、不增加表或列，也不修改 epoch 1～5 的 migration、manifest、
    checksum 或运行时数据库权限。

## API 与资源解析

请求示例：

```http
GET /api/v1/tasks/12001/steps/build-image/logs/stream?cursor=4096 HTTP/1.1
Accept: text/event-stream
Cookie: ares_session=...
```

服务端按以下顺序处理，并在前置检查全部成功后才发送 SSE 响应头：

1. 认证当前会话并检查 `logs.read`；
2. 校验正整数 `task_id`、工作流 step key 格式、query 和 cursor；
3. 获取 generic/legacy 共用的连接配额；
4. 同时确认任务存在、是 v2 任务，且该 `step_key` 确实属于这个任务；
5. 从步骤快照取得 `uses` 与 opaque `external_ref`；
6. 查询 Registry，核对 descriptor 日志能力和 `LogReader` 实现；
7. 确认外部引用已经足以定位日志；
8. 由执行器严格验证私有引用和当前集成实例，且在验证通过前不发送外部请求；
9. 完成第一次有界读取或返回稳定前置错误，再开始 SSE。

取得连接配额后、第一次执行器读取前必须先清除普通 HTTP `WriteTimeout`；否则合法的集成读取
超时可能长于普通接口写期限，使尚未提交的 502 等稳定错误也无法写回。整个预读和后续流仍受
SSE `max_duration` context 约束，正式写入每个响应头或事件时再使用滚动 write deadline。

步骤列表和任务详情中的 `capabilities` 是服务端根据当前已注册的步骤执行器派生的响应字段，
不是任务可写输入或数据库状态。执行器临时不可用不改变其静态日志能力；用户仍可以看到日志入口，
并收到稳定的“执行器不可用”结果。

## SSE 协议

日志数据事件：

```text
event: log
id: 8192
data: {"content":"build output\n","cursor":"8192","eof":false}

```

`content` 是本段日志文本，单次读取和单个事件都必须有固定字节上限。`cursor` 必须与 SSE `id`
完全相同。`eof=true` 表示执行器已确认没有后续内容；服务端随后发送 `end/completed` 并关闭连接。
空内容不能用来冒充“不支持日志”，但执行器可以在尚无新增内容时返回同 cursor 的非终态 chunk，
由通用层按有界间隔继续读取。

心跳不代表 cursor 前进：

```text
event: ping
id: 8192
data: {}

```

当前 cursor 为空时 `ping` 不发送 `id`。正常结束事件为：

```text
event: end
id: 8192
data: {"reason":"completed"}

```

`end.reason` 只使用：

- `completed`：执行器确认 EOF，是唯一业务终态；前端停止自动重连；
- `max_duration`：本次连接达到最长时长，前端可以从最新 cursor 有界重连；
- `upstream_idle`：在空闲期限内没有收到有效上游进展，前端按有界退避决定是否重连。

流建立后的稳定错误事件为：

```text
event: stream-error
data: {"code":"upstream_unavailable"}

```

允许的 `code` 只有 `upstream_unavailable`、`invalid_log_chunk`、`executor_unavailable`、
`log_source_mismatch`、`forbidden` 和 `session_revalidation_failed`。定期复验发现会话仍有效但 `logs.read` 已被撤销时发送
`stream-error/forbidden` 并关闭当前日志流；前端只收起日志能力，不能把它当作全局登出。事件不
携带内部 message、上游正文或 URL。只有会话到期、用户禁用或会话撤销时才尽力发送：

```text
event: auth-expired
data: {"reason":"session_expired"}

```

随后立即关闭。前端收到 `auth-expired` 或建流 HTTP 401，必须终止该日志生命周期的全部连接、
timer 和重连并收敛全局身份；建流 HTTP 403 时只停止日志流并刷新最终权限。真正没有收到 HTTP
响应的网络失败会在重连前探测会话，防止失效身份进入无界重试。
canonical fetch 不发送页面 Referer；错误正文最多读取 16 KiB，超限或取消失败都不能覆盖已经取得的
HTTP 状态与稳定错误分类。

## HTTP 失败契约

SSE 响应头发送前使用以下稳定分类：

| HTTP | `error` | 含义 |
| ---- | ------- | ---- |
| 400 | `invalid_request` | 路径、未知/重复 query 或 Header 形状无效 |
| 400 | `invalid_cursor` | cursor 超长、编码或字符不合法，或执行器拒绝其格式 |
| 400 | `cursor_conflict` | query cursor 与 `Last-Event-ID` 不一致 |
| 401 | `unauthenticated` | 未登录或会话失效 |
| 403 | `forbidden` | 当前主体没有日志读取权限 |
| 404 | `task_or_step_not_found` | 任务不存在，或步骤不属于该任务 |
| 409 | `logs_not_ready` | 执行器支持日志，但步骤尚无可用日志引用 |
| 409 | `log_source_mismatch` | 存储引用与当前受信执行器实例不一致 |
| 409 | `legacy_task` | v1 任务误用 canonical v2 步骤入口 |
| 422 | `logs_unsupported` | 该步骤类型没有日志能力 |
| 429 | `stream_capacity_exceeded` | 当前主体或进程的日志连接容量已满；响应携带 `Retry-After` |
| 502 | `upstream_unavailable` / `invalid_log_chunk` | 第一次读取时上游不可用，或执行器返回无效 chunk |
| 503 | `executor_unavailable` | 执行器未注册或集成未启用 |
| 500 | `internal_error` | 无法安全读取内部任务/步骤状态 |

404 不区分“任务不存在”和“task 存在但 step 不属于它”，避免把错误组合变成额外枚举接口。
响应正文仍使用统一 API envelope，但只返回表中的稳定 `error`，不回显底层错误。

## Jenkins Adapter

`jenkins.job@v1` 从步骤快照的 external reference 读取 integration、规范化地址、folder Job 和
Build ID。引用必须是严格的单一 JSON 对象，未知字段、缺失 Build ID、非法 Job、地址缺失或实例
不匹配全部在 progressiveText 请求前拒绝。Queue ID 已存在但 Build ID 尚未产生时返回
`logs_not_ready`。

Jenkins progressiveText 的 byte offset 映射为十进制 cursor。Adapter 保留 256 KiB 上游读取
硬上限；达到上限时只在安全的 UTF-8 rune 边界切分并按实际返回字节推进 offset。每个有界 chunk
由通用层编码为一个有界 SSE 事件。上游 offset 回退、缺失或超出 int64 均属于
`invalid_log_chunk`，不能通过把 cursor 重置为 0 静默重放整份日志。

## 兼容、发布与回退

canonical API 是只读增量能力，不修改任务、步骤或外部构建。发布顺序为先部署后端，再部署使用
步骤日志的新前端。回退时先回退前端，再回退后端；因为没有 schema 或数据变更，不需要恢复
数据库备份。

旧日志路由继续保护 v1 历史任务，但所有响应（包括 4xx/5xx）都发送 `Deprecation: true` 和
`Warning: 299 - "Legacy Jenkins log endpoint is deprecated; use task step logs"`，并拒绝 v2
任务。当前不虚构 Sunset 日期，也不发送无法供 v1 任务直接使用的 successor Link。v2 前端只按
步骤 `capabilities.logs` 使用 canonical 入口，绝不回退到兼容字段；v1 历史任务仍由隔离的
task-scoped adapter 判断 CI/CD 历史引用是否存在，向旧路由只提交 task ID、日志类别和 cursor，
不会提交 Job/Build。移除旧路由仍需单独评审；W03 只收紧其用途，不删除 v1 历史读取能力。

## 验收

- v2 Jenkins folder Job 可以按任意步骤 key 读取，客户端无法覆盖 Job、Build ID 或地址。
- task/step 组合篡改、日志越权、非法 cursor 和来源不匹配都在外连前失败。
- 断线后从 query cursor 或一致的 `Last-Event-ID` 续传，不重复、不丢失已确认日志。
- Noop 和其他无日志能力步骤明确返回 `logs_unsupported`，前端不显示日志入口。
- 关闭详情、切换任务/步骤、权限撤销、会话失效及所有终止路径都释放上游请求、连接、timer 和 goroutine；单独撤销 `logs.read` 不会登出仍有效的会话。
- 日志、错误、审计和结构化运行日志扫描不到测试 Secret、external reference 或上游正文。
- Jenkins 未启用时 Ares、Noop 工作流和非日志功能仍正常。

## 后果

执行器只需实现有界、可恢复的 chunk reader，认证与长连接复杂度集中在通用 API 层。代价是
cursor 成为版本化执行器契约的一部分；改变 cursor 语义必须发布新的执行器版本或提供显式兼容，
不能让历史任务在升级后从不确定位置继续读取。
