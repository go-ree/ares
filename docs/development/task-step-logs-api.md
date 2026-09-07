# 通用任务步骤日志 API

本文面向 Web 客户端和执行器开发者，说明 W03 起通用步骤日志的调用与续传契约。架构取舍见
[ADR-0003：执行器通用步骤日志与游标续传](../architecture/decisions/0003-generic-step-logs.md)，
身份与长连接安全边界见
[ADR-0002：OIDC、服务端会话、RBAC 与只增审计](../architecture/decisions/0002-authentication-rbac-audit.md)。

## 入口与权限

```http
GET /api/v1/tasks/:task_id/steps/:step_key/logs/stream?cursor=:cursor
```

- 必须使用已认证的同源 Cookie 会话，并拥有 `logs.read`。
- `task_id` 是正整数；`step_key` 使用工作流的稳定 key，格式为
  `^[a-z][a-z0-9._-]{0,62}$`。
- query 只允许一个可选 `cursor`。省略 cursor 表示从头读取；未知字段、重复 cursor 或显式空值
  返回 400。
- 客户端不得提交 `uses`、配置、external reference、Jenkins Job、Build ID 或服务地址。

步骤列表 `GET /api/v1/deploy/publish/query/:task_id/steps` 及 v2 任务详情会返回每个步骤由服务端
派生的 `capabilities`。只有 `capabilities.logs=true` 才展示日志入口；不能通过 CI/CD 类别、步骤
顺序、名称或兼容字段猜测能力。

## Cursor

cursor 是执行器拥有的 opaque UTF-8 字符串，表示下一段尚未确认日志的位置：

- 最多 256 bytes，不能包含 CR、LF 或 NUL；
- query cursor 与 `Last-Event-ID` 都可以作为续传输入；
- `Last-Event-ID` 只能出现一次，并适用相同校验；
- 两者同时存在时必须字节级一致，否则返回 `400 cursor_conflict`；
- Jenkins cursor 进一步限定为无符号十进制 int64；调用方不要对其他执行器的 cursor 做数值运算。

前端收到并处理一个 `log` 事件后保存该事件的 cursor。Ares Web 手工重建 fetch SSE transport 时
把最新值放入 `?cursor=`；其他合规 SSE 客户端也可以携带相同的 `Last-Event-ID`。不要提前保存
尚未处理的 cursor，也不要在错误后省略已有 cursor，否则可能重放大量日志。

## SSE 事件

### `log`

```text
event: log
id: 8192
data: {"content":"build output\n","cursor":"8192","eof":false}

```

`id` 必须与 payload 的 `cursor` 完全一致。`content` 是本次追加文本；客户端按到达顺序追加，
不得把内容作为 HTML。`eof=true` 后服务端还会发送 `end/completed`。

### `ping`

```text
event: ping
id: 8192
data: {}

```

ping 只证明连接仍活跃，不表示产生新日志。存在当前 cursor 时会重复同一 `id`；初始 cursor 为空
时不发送 `id`。

### `end`

```text
event: end
id: 8192
data: {"reason":"completed"}

```

| reason | 客户端行为 |
| ------ | ---------- |
| `completed` | 终态，关闭连接并停止该步骤的自动重连 |
| `max_duration` | 关闭旧连接，从最新 cursor 按有界退避重建 |
| `upstream_idle` | 显示暂时中断状态，从最新 cursor 按有界退避重试 |

### `stream-error`

```text
event: stream-error
data: {"code":"upstream_unavailable"}

```

允许的 code 为：

- `upstream_unavailable`：上游暂不可用；
- `invalid_log_chunk`：执行器返回了不满足 cursor/chunk 契约的结果；
- `executor_unavailable`：执行器或集成当前不可用；
- `log_source_mismatch`：历史引用与当前受信实例不匹配；
- `forbidden`：会话仍有效，但定期复验发现 `logs.read` 已被撤销。
- `session_revalidation_failed`：会话复验服务暂时不可用。

事件不会携带底层 message。前端只显示本地维护的稳定文案，并按错误类别决定是否提供手工重试，
不得显示 fetch、Axios 或上游原始错误。`forbidden` 只关闭日志流并刷新服务端最终权限，
不能清除仍有效的全局会话。

### `auth-expired`

```text
event: auth-expired
data: {"reason":"session_expired"}

```

收到后立即关闭 transport，取消所有重试和 timer，并让统一身份 store 收敛为匿名状态。建流 HTTP
401 直接执行相同逻辑；若日志请求/复验返回 403，只刷新权限并停止日志流，不触发全局登出。没有
HTTP 响应的网络错误会在重连前探测会话。

## 建流前错误

| HTTP | `error` | 前端处理 |
| ---- | ------- | -------- |
| 400 | `invalid_request` / `invalid_cursor` / `cursor_conflict` | 停止自动重试并报告请求状态无效 |
| 401 | `unauthenticated` | 清理身份并进入登录流程 |
| 403 | `forbidden` | 关闭日志 UI，不把它当作全局登出 |
| 404 | `task_or_step_not_found` | 停止自动重试并提示刷新任务详情；不区分任务或步骤哪一个不存在 |
| 409 | `logs_not_ready` | 步骤尚未得到日志引用，可以稍后有界重试 |
| 409 | `log_source_mismatch` / `legacy_task` | 停止自动重试并显示稳定说明 |
| 422 | `logs_unsupported` | 隐藏入口；这不是“空日志” |
| 429 | `stream_capacity_exceeded` | 尊重 `Retry-After`，不立即重连 |
| 502 | `upstream_unavailable` | 有界退避或提供手工重试 |
| 502 | `invalid_log_chunk` | 停止自动重试，避免错误 cursor 造成重放或丢失 |
| 503 | `executor_unavailable` | 保留页面其他能力，稍后重试日志 |
| 500 | `internal_error` | 显示通用内部错误，不展示响应细节 |

Ares Web 使用 fetch 建立并解析有界 SSE，因此能够在流提交前读取上述 HTTP 状态与稳定错误码，
并在 429 时读取 `Retry-After`。SSE 建立后 HTTP 状态不能再改变，只按上一节的事件分类处理。

## 前端生命周期

一个打开的任务详情只为当前选择且支持日志的步骤保留连接：

1. 打开详情后按服务端步骤顺序展示任意数量的步骤；
2. 选择步骤时关闭上一条流，再为新步骤建立连接；
3. 切换任务、关闭详情、路由卸载或权限失效时立即中止当前 transport 与 fetch；
4. 每个 `task_id + step_key` 独立保存 buffer、cursor、完成状态和共享的有限重连预算；
5. `completed` 在当前详情生命周期内不可被健康检查或 timer 重新打开；
6. 手工重试保留已确认内容和 cursor，不从头清空后静默重放；
7. 单步骤日志缓冲和当前详情内的全部步骤缓存都必须有内存上限；超出总量时按 LRU 淘汰旧步骤缓存，
   追加使用纯文本并分批渲染。

## 执行器要求

执行器通过 `LogReader` 返回一段有界内容：

```go
type LogReader interface {
    ReadLogs(context.Context, LogRequest) (LogChunk, error)
}
```

`LogRequest` 只包含服务端任务/步骤身份、存储的 external reference 和已校验 cursor。实现必须：

- 尊重 context 取消和 deadline；
- 在外连前严格验证自己的 external reference；
- 不使用当前工作流配置替换历史任务引用；
- 返回有效、非回退的下一 cursor，并为 EOF 给出确定结果；
- 限制单次上游响应和返回 chunk 大小；
- 不把 Secret、上游 Header/正文、URL 或原始错误写入公开错误或运行日志。

Descriptor 的 `capabilities.logs` 必须与是否实现 `LogReader` 一致，否则 Registry 拒绝注册。

## 旧接口

`/api/v1/job/stream/log` 和 `/api/v1/deploy/log/stream` 是 deprecated 兼容入口，只读取
`engine_version=1` 的历史任务，并继续限制为存储的 CI/CD 引用。它们的全部响应都包含
`Deprecation: true` 和
`Warning: 299 - "Legacy Jenkins log endpoint is deprecated; use task step logs"`；当前没有虚构
Sunset 日期或对 v1 不可用的 successor Link。v2 任务使用旧入口会被拒绝且绝不回退到兼容字段；
当前 Web 只为 v1 历史任务保留隔离的 task-scoped adapter，并且不会把 Job/Build 作为请求参数。

## 测试检查表

- task/step 不匹配、权限不足和 v1/v2 路由交叉使用不会发出外部请求；
- query/Header cursor 相同可续传，冲突、重复、超长和控制字符被拒绝；
- Jenkins folder Job、空增量、分段、EOF、offset 回退和上游超限均有测试；
- `log/ping/end/stream-error/auth-expired` 的 payload 与终止行为固定，单独撤销 `logs.read` 不会登出仍有效会话；
- 关闭、切换、会话撤销、最长时长和上游空闲后无残留连接、timer 或 goroutine；
- 前端只按 capabilities 展示入口，任意步骤数量与顺序均可用；
- 响应、审计和日志扫描不到测试 Secret、external reference、Job/Build 或上游正文。
