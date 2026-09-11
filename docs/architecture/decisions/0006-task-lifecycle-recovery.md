# ADR-0006：任务超时、失败恢复与取消

## 1. 状态与范围

2026-09-11，W07 设计已确定。依赖已合并的 W06 task lease/fencing。W07 分三次交付，每次更新路线图并通过中文 PR 验收：

1. **W07-A（已合并 PR #37）**：步骤总时限、`timed_out` / `outcome_unknown` 终态、外部引用保留、查询异常退避、前端状态展示。
2. **W07-B（本次）**：版本化重试策略、独立 attempt 表、数据库时间退避、手动重试和尝试历史界面。
3. **W07-C**：持久化取消请求、执行器取消与确认协议、Jenkins Queue/Build 取消、前端能力入口。

拆分原因：先区分失败、时间耗尽与外部结果不明，再允许创建新的 attempt；取消需要在这些状态与历史记录之上保证幂等和可追查。W07-A 合并不代表完整 W07 完成。

## 2. W07-A 执行语义

`timeout_seconds` 是本次尝试自数据库 `started_at` 起的总预算，包含 Start、排队和全部 Reconcile，重启或换 Worker 不重置。Start 与 Reconcile 调用前均读取数据库剩余时间；上下文截止后返回的成功、输出和消息不再被接受，但返回的 opaque 外部引用仍需保留。

| 事件 | 步骤和任务处理 | 后续步骤 |
| --- | --- | --- |
| 执行器明确返回失败 | `failed`；沿用 `on_failure` | stop 停止，continue 继续 |
| 总预算耗尽，包括失联后到期 | `timed_out` | 无论 on_failure 均停止 |
| Start 普通错误，未能确认是否已提交 | `outcome_unknown` | 无论 on_failure 均停止 |
| Reconcile 普通错误 | 保持 running、保留引用，使用 Worker 持久化退避 | 等待至确认结果或总预算耗尽 |
| 执行器返回暂时 unknown | 保持 running、保留引用并退避 | 同上 |
| 执行器状态契约无效、外部实例不匹配 | `outcome_unknown` | 停止，人工核查 |
| Worker 被停止或租约失效 | 不保存业务终态；下一持有者继续读取 | 不重新 Start |

`timed_out` 表示 Ares 已停止推进，**不表示外部构建已停止**。`outcome_unknown` 表示无法确认外部结果，也不表示可以安全重试。两者都保留日志引用、完成时间，并清空任务租约和调度时间；不能通过 `on_failure: continue` 启动可能与仍在运行的构建冲突的后续步骤。API 和 Web 明确提示先核查外部执行状态。历史 failed 行不推断、不回写。

已领取但没有外部引用的 running 步骤继续等待到总时限，绝不再次 Start。错误正文和晚到输出不进入公共任务记录。终态步骤保存后进程崩溃，下个 Worker 必须从步骤终态恢复任务终态和跳过余下步骤。

## 3. W07-B 重试与历史契约

- 新 migration 建立以 `(step_record_id, attempt)` 唯一的历史记录，保存状态、开始/完成时间、外部引用、结果分类和稳定幂等键。步骤表保留当前尝试投影；两者与任务状态在同一持租约事务更新。
- 工作流版本冻结 `retry` 策略：总尝试数上限 5，默认 1；指数退避上下界 1～3600 秒，由数据库时间持久化 `retry_wait` 到期时间。
- 默认错误不可重试。执行器须显式证明未产生副作用，或明确报告外部执行已结束且允许重新执行，才能进入下一 attempt。网络错误、超时、未知结果和仅仅存在 external_ref 都不构成重试依据。
- 每次真正执行使用新的 attempt 与 `task_id/step_key/attempt` 幂等键；同一 attempt 的接管、轮询、请求重放不改变键。
- 手动重试接受预期 attempt，服务端检查权限、策略和结果分类，CAS 拒绝过期或并发命令。只恢复失败边界，不修改旧工作流快照、不回放成功的历史节点。
- 迁移只回填可证实的当前尝试，不虚构旧 attempt；未开始的 pending 不产生已执行历史。历史 API 不暴露配置、输出和外部引用，日志按精确 attempt 寻址。

### 3.1 W07-B 实现决策

- retry 为可选对象，省略保持一次尝试；字段为 max_attempts（1～5）、initial_delay_seconds / max_delay_seconds（1～3600，默认 1/60）、mode（automatic 或 manual，默认 automatic）。manual 只允许 on_failure=stop；自动和手动共用次数上限。
- 执行器能力增加 retry，Result 的内部 retry_class 仅接受 no_side_effect / completed_safe，且仅明确 failed 有效。Noop 声明无副作用并支持 fail_attempts（0～4）演示；Jenkins 保持不具备安全重试能力。
- epoch 8 增加步骤重试策略快照、retry_at、retry_class 和 task_step_attempts。task 仍为 running，retry_wait 仅为步骤状态，以保留已有 task 租约契约。attempt 在到期准备下一尝试时递增；ClaimStep 原子创建 running 历史，保存结果原子更新投影和当前历史。未调用/明确无副作用的 unavailable claim 可撤销其 running 记录，不消耗预算。
- 自动失败先写 failed 历史，再持租约排定 retry_wait；这两个事务间崩溃可从 failed 恢复。retry_at 只初始化一次，轮询或接管不会重置；未到期不会启动后续步骤。
- 手动 API 接受 expected_attempt，只有 failed 任务的 stop 失败边界、剩余预算、明确安全类别和执行器能力同时满足才接受。任务行锁串行化，排定 retry_wait 并恢复未执行的 skipped 后续步骤；重复或过期请求返回 409，不创建额外 attempt。
- 历史查询按 task/step 返回最多 5 条新尝试（历史迁移保留可证实的当前 attempt 编号），不返回引用/配置/输出。日志支持可选 attempt 参数并锁定对应历史引用；省略仍读取当前步骤引用。
- 升级必须停止旧 Worker，再运行 migrator 到 epoch 8，再启动新镜像。epoch 7 镜像拒绝 epoch 8；回退依赖升级前备份恢复，不直接回连新库。历史回填不推断 retry_class，不回写工作流版本或 checksum。

## 4. W07-C 取消契约

- API 只持久化取消意图并审计；持有租约的 Worker 执行外部操作。重复请求幂等；终态请求返回当前状态，不改写历史。
- queued、retry_wait 且未提交的步骤可直接取消；running 必须根据能力和外部引用发送取消并确认外部终态，不能把 HTTP 请求成功当成构建已停止。
- 原 `Canceller.Cancel(... ) error` 不足以表达已确认/待确认/未知，需要增量明确确认协议。没有能力的运行中执行器不提供取消入口，服务端同样拒绝。
- Jenkins 在原实例和 job/queue/build 身份校验后取消；处理 Queue 转 Build 的竞态，无法确认则继续有界查询并保留可诊断结果。未知提交不能通过取消变成可重试。
- 取消与重试按任务行锁串行化，取消先于新 attempt 提交；租约失效的 Worker 不能覆盖新持有者结果。

## 5. 验收与升级

W07-A 验证数据库剩余时限、晚到结果引用保留、Start 不明确错误、Reconcile 恢复、continue 安全停止、终态崩溃恢复、父上下文取消和过期 fencing，以及前端状态/日志可用性。W07-B 已通过真实 MySQL 的并发重试单赢家、重启退避、attempt 唯一性、预算上限和历史日志引用隔离；W07-C 再补取消与 Jenkins 竞态矩阵。

W07-A 无 DDL，沿用 epoch 7 的 VARCHAR 状态列。W07-B 升级至 epoch 8，须停止全部旧 API/Worker、验证备份、运行迁移和运行时账号任务后再启动新镜像；不支持新旧 Worker 混跑。回退必须恢复升级前备份并核查外部执行，不得单独降级镜像。具体步骤见[数据库迁移与恢复手册](../../operations/database-migrations.md)。W07-C 的取消存储与升级契约将在对应 PR 中补齐。
