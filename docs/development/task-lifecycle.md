# 任务状态与恢复边界

## 1. v2 任务与步骤状态

W07-A 在原有 `failed`、`cancelled` 等状态之外增加两个状态；任务详情、步骤查询和发布记录列表保持原接口路径与响应结构。

| 状态 | 含义 | 操作 |
| --- | --- | --- |
| timed_out | 从 started_at 起的步骤总时限耗尽，Ares 已停止推进 | 先到外部平台确认任务是否仍在运行 |
| outcome_unknown | 无法确认提交/执行结果，或无法安全识别来源 | 按任务与步骤身份核查外部记录，避免重复发布 |

两者均是 Ares 终态：填写 finished_at、跳过 pending 步骤、释放任务租约并停止调度，即使 on_failure 为 continue 也不继续。它们不代表外部任务已取消，API 不提供安全重试承诺。执行器具备日志能力且引用有效时，原步骤日志接口仍可访问。

## 2. 执行器行为

- Start 普通 error 无法证明“没有提交”，进入 outcome_unknown；结果中已返回的 ExternalReference 会保留，错误正文不公开。
- ErrExecutorUnavailable 仅可用于提交前无副作用的情况，且不能同时返回已创建资源引用；该情况仍可释放 pending claim 等待集成可用。
- Reconcile 普通 error 或 ResultUnknown 只代表查询不确定，保留当前 attempt 和最新引用并使用 Worker 持久化轮询退避。
- Reconcile 若发现来源不匹配或引用无效，应返回 ResultOutcomeUnknown；外部明确失败才返回 ResultFailed。
- 调用截止后的结果不作为成功接受，但仍保留已返回的引用。执行器必须响应 context；进程停机或租约丢失不会被写为 timed_out。
- 超时使用原始总时限，不因轮询失败、重启或换 Worker 重新计时。

## 3. W07-B：重试与尝试历史

工作流步骤可以保存如下重试策略，默认省略即只执行一次：

```json
{
  "key": "verify",
  "name": "重试演示",
  "uses": "builtin.noop@v1",
  "with": {"fail_attempts": 1},
  "on_failure": "stop",
  "retry": {"max_attempts": 3, "initial_delay_seconds": 2, "max_delay_seconds": 60, "mode": "automatic"}
}
```

总尝试数为 1～5，等待以指数增长但不超过 max_delay_seconds（1～3600 秒）。`manual` 模式只允许失败即停；失败后由具有 releases:create 权限的用户请求重试。两种模式共用 max_attempts 上限。Noop 支持 fail_attempts=0～4，在前 N 次确定失败但无副作用，之后按 outcome 执行。

执行器须声明 capabilities.retry，并仅在明确失败时返回内部 RetryClass=`no_side_effect` 或 `completed_safe`。Jenkins 未声明该能力，配置 max_attempts>1 会被拒绝。timeout、unknown、普通 error 或只有外部引用均不构成安全重试条件。执行器新增重试能力前需为自己的外部副作用负责。

| 接口 | 权限 | 行为 |
| --- | --- | --- |
| GET /api/v1/tasks/{task_id}/steps/{step_key}/attempts | tasks:read | 最新 5 条尝试，按编号倒序；仅编号、状态、消息、起止时间 |
| POST /api/v1/tasks/{task_id}/steps/{step_key}/retry | releases:create + CSRF | JSON `{"expected_attempt":1}`，成功 202；状态/编号/预算/安全条件不符返回 409 |
| GET /api/v1/tasks/{task_id}/steps/{step_key}/logs/stream?attempt=1 | logs:read | 固定到历史尝试的日志引用；不带 attempt 保留当前步骤兼容语义 |

retry_wait 是步骤状态，task 保持 running。数据库保存 retry_at；等待中重启或切换 Worker 不重置它。到期后才递增 attempt 并准备下一次执行，每次 ClaimStep 与 running 历史在同一事务提交。完成历史不被重写；未真正提交且明确 unavailable 的 claim 可撤销，不消耗重试预算。请求超时后请先刷新状态，重复 expected_attempt 会返回 409，不会额外重试。

## 4. 后续范围

取消命令与 Jenkins Queue/Build 取消确认由 W07-C 交付。完整设计、升级和回退边界见 [ADR-0006](../architecture/decisions/0006-task-lifecycle-recovery.md)，进度见[路线图](../plans/open-source-production-roadmap.md)。
