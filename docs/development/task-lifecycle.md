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

## 3. 当前可用范围

W07-A 不包含 retry_wait、自动/手动重试、attempt 历史 API 和取消命令；这些分别由 W07-B/C 交付。完整设计、升级和回退边界见 [ADR-0006](../architecture/decisions/0006-task-lifecycle-recovery.md)，进度见[路线图](../plans/open-source-production-roadmap.md)。
