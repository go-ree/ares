# ADR-0005：多副本 Worker 的任务租约与 fencing

- 状态：已接受
- 日期：2026-09-08
- 适用版本：W06 起
- 决策范围：v2 任务调度、租约、故障接管、陈旧写隔离、进程退出、集成设置收敛与 v1 排空

## 背景

W05 已保证一次发布命令只原子创建一份任务和步骤快照，但现有后台任务仍由每个 Ares 进程中的
Cron 独立扫描。`pending -> running` 的步骤 CAS 只能阻止两个实例同时首次认领同一步骤，不能阻止
多个实例同时 Reconcile 已经 running 的步骤，也不能在持有者故障后区分旧、新实例的数据库写入。
扫描依赖改写 `task_record.updated_at` 把任务移到队尾，既污染业务更新时间，也不能给临时不可用的
执行器提供稳定退避。

W06 需要让 MySQL 成为默认的调度事实源，在不要求 Redis 或 RabbitMQ 的情况下支持多个 Ares
副本。同时必须承认外部调用与数据库事务无法形成一个原子提交：租约可以选出唯一有效数据库
写入者，却不能撤回租约失效前已经发出的 HTTP 请求，也不能消除“外部系统已接受请求、引用尚未
落库时进程退出”的不确定窗口。

## 决策摘要

1. Ares 对 v2 任务提供 **at-least-once 调度与唯一有效数据库写入权**，不承诺跨外部系统
   exactly-once。执行器必须使用 Ares 已提供的稳定 `task_id/step_key/attempt` 幂等键。
2. 租约归属 `task_record`，覆盖一次任务的读取、步骤认领、Start/Reconcile、状态保存和下一次调度；
   不为同一串行任务的各步骤建立相互独立的所有权。
3. epoch 7 为任务增加 `next_poll_at`、`lease_owner`、`lease_expires_at`、单调
   `lease_fencing_token` 和持久化调度失败计数，并为 `integration_settings` 增加数据库 revision。
4. 所有到期判断和新租约期限都使用 MySQL `UTC_TIMESTAMP(6)`；进程时钟只负责本地定时器，不能
   决定租约是否合法。
5. 领取使用 `READ COMMITTED` 短事务和 `FOR UPDATE SKIP LOCKED`，按
   `(next_poll_at, task_id)` 公平排序；事务提交后才允许调用执行器。
6. 领取、续租、释放、步骤认领、结果保存、跳过剩余步骤和任务终态写入全部校验
   `(task_id, lease_owner, lease_fencing_token, lease_expires_at > database_now)`。token 不匹配的旧实例
   永远不能覆盖新持有者。
7. Worker 在执行期间周期续租；续租失败立即取消该任务的本地 context 并停止后续写入。执行器必须
   传播 context，但 Ares 不假设远端请求一定随取消停止。
8. `next_poll_at` 是唯一调度时间，业务 `updated_at` 不再承担轮转职责。成功轮询使用基础间隔加
   jitter，执行器/集成暂不可用和可恢复的协调失败使用持久化、有上限的指数退避加 jitter。
9. 进程收到退出信号后先停止领取新任务，继续为在途任务续租并等待有界 drain；超时后取消本地
   context，未能完成的租约由数据库时间自然过期并被其他实例接管。
10. 集成设置以数据库 revision 为事实源，写入使用 CAS；每个副本周期读取 revision 并原子替换
    本地运行快照。Redis/RabbitMQ 可以在未来作为通知优化，但不参与 W06 正确性。
11. v1 遗留状态机不进入 v2 租约协议。每轮 v1 排空必须持有 MySQL 会话级 leader lock；连接断开
    自动释放，同一数据库在任一时刻最多一个旧状态机执行。

## 数据模型与迁移

epoch 7 只增加列和索引，不删除历史字段：

| 表 | 字段 | 契约 |
| --- | --- | --- |
| `task_record` | `next_poll_at DATETIME(6) NULL` | v2 活跃任务下一次可领取时间；终态和 v1 为 NULL |
| `task_record` | `lease_owner VARBINARY(64)` | 当前进程随机生成的非敏感实例 ID；未持有时为 NULL |
| `task_record` | `lease_expires_at DATETIME(6)` | 当前租约截止时间；未持有时为 NULL |
| `task_record` | `lease_fencing_token BIGINT UNSIGNED` | 每次成功领取加一，永不回退或复用 |
| `task_record` | `poll_failure_count INT UNSIGNED` | 连续调度失败次数；成功推进后归零，用于有界退避 |
| `integration_settings` | `revision BIGINT UNSIGNED` | provider 配置的数据库 CAS 与跨副本收敛版本 |

`idx_task_worker_due(engine_version, deleted_at, next_poll_at, task_id)` 支持有界到期扫描和全局
`next_poll_at/task_id` 顺序，不因 queued/running 两个状态分段而破坏公平性。迁移仅把已有、未删除、
queued/running v2 任务的 `next_poll_at` 初始化为迁移时数据库时间；终态、软删除和 v1 任务保持 NULL。
已有 integration row 从 revision 1 开始，新 row 也从 1 开始。迁移后 schema 与数据契约必须拒绝：

- v2 活跃任务缺少 `next_poll_at`；
- v1、终态或软删除任务保留 `next_poll_at`；
- owner、expiry 只存在其一；
- 持有租约但 fencing token 为 0；
- 终态或软删除任务仍保留租约；
- revision 为 0；
- failure count 超过实现允许的饱和值。

epoch 7 与 epoch 6 应用不并行写同一数据库。升级前停止 epoch 6 实例并备份，执行独立 migrator，
验证 manifest 与最小权限后再启动多副本。回退必须冻结写入并恢复 epoch 6 备份及匹配镜像，不提供
down migration，也不能让旧 Worker 写 epoch 7。

## 领取与公平性

每个副本启动时使用 CSPRNG 生成不超过 64 字节的 owner；owner 不从主机名、Pod 名或用户配置读取，
避免副本重建后复用身份。一个 owner 在进程生命周期内固定。

领取事务采用以下语义：

1. 以 `READ COMMITTED` 开启短事务。
2. 查找 `engine_version = 2`、未删除、状态为 queued/running、`next_poll_at <= UTC_TIMESTAMP(6)`，
   且无租约或租约已过期的任务。
3. 按 `next_poll_at ASC, task_id ASC` 排序，按当前空闲并发数限制批量大小，并以
   `FOR UPDATE SKIP LOCKED` 跳过其他领取事务持有的行锁。
4. 对选中行设置 owner、数据库计算的 expiry，把 next poll 同步推到 expiry，并令 lease fencing
   token 原子加一；token 达到
   `BIGINT UNSIGNED` 上限时 fail-closed，不回绕。
5. 提交后返回 task ID、token、期限和 failure count；事务内不读取步骤配置、不探测集成，也不做
   任何外部网络调用。

任务被领取但进程在调用执行器前退出时，其他实例在 expiry 后接管。`next_poll_at` 与 expiry 相同，
所以接管无需额外恢复扫描。超过单批上限的旧任务保持更早的 next poll 时间，会先于本轮被释放到
未来时间的任务被领取，从而避免头部 200 个长任务造成饥饿。

## fencing 与状态写入

`TaskLease` 是进程内不可变句柄，至少包含 task ID、owner 和 token。Coordinator 不再提供只凭
task ID 推进生产任务的入口。每个执行存储方法都接收同一句柄，并在 SQL 中连接或校验父任务：

```text
task_id = lease.task_id
lease_owner = lease.owner
lease_fencing_token = lease.token
lease_expires_at > UTC_TIMESTAMP(6)
engine_version = 2
deleted_at IS NULL
status IN ('queued', 'running')
```

零行更新必须区分“步骤状态已经变化”和“租约已丢失”；租约无效统一返回 `ErrLeaseLost`。即使旧
实例中的执行器忽略取消并晚返回，其 SaveStepResult、任务终态和 next poll 更新也会被 token 拒绝。
读取任务上下文与步骤快照同样校验租约，避免在已知失去所有权后继续发出新的外部调用。

步骤首次 Start 前仍先以当前 token 把 `pending -> running` 持久化。这样进程在远端接受请求后、
external reference 落库前退出时，接管者不会再次把该步骤当成 pending 直接 Start；它保留 running
且无引用的不确定状态，并沿用当前超时后的安全失败策略。W07 会在 attempt 历史中进一步明确重试
和人工处置，W06 不进行盲目自动重触发。

## 续租、执行与故障接管

lease duration、renew interval、并发数、扫描批量和 drain timeout 是启动配置，必须有安全默认值与
上下界；renew interval 必须显著小于 lease duration。续租 SQL 只延长仍未过期且 token 匹配的租约，
不能复活过期租约。每个在途任务至多一个续租 goroutine，并在任务释放后立即结束。

正常执行流程为：

1. Worker 领取任务并创建独立 task context。
2. 续租器按固定间隔续租；Coordinator 在同一 context 下运行到异步边界或终态。
3. 成功推进后清零失败计数，按普通轮询间隔加 jitter 写 next poll 并释放 owner/expiry；终态在同一
   fenced 更新中把 next poll、owner 和 expiry 全部清空。
4. 执行器/集成暂不可用或可恢复协调错误时增加饱和 failure count，使用有上限指数退避加 full
   jitter 后释放；数据库不可写时不伪造释放，等待原租约自然过期。
5. 续租返回零行、连接失败或超过续租操作时限时，取消 task context；之后所有数据库写仍须通过
   fencing，因此取消是否被执行器及时遵守不影响新持有者的写安全。

运行中的 Reconcile 与 Start 具有相同租约要求。故障测试必须包含 Reconcile 阻塞超过一个租期、
故意阻断续租和第二实例接管；允许观察到两个外部读取/调用短暂重叠，但只有新 token 能提交结果。
有业务副作用的 Mock 必须按稳定执行器幂等键去重并最终只产生一次副作用。

## 退避与 jitter

Worker 不以紧循环轮询数据库或不可用集成：

- 没有到期任务时，扫描循环等待基础 poll interval 加随机 jitter，context 取消立即唤醒。
- running 且远端仍运行时，使用普通 next poll 间隔加 jitter，不累计 failure count。
- 执行器/集成不可用和可恢复协调失败按 failure count 指数退避，并限制最大时长与移位上限。
- jitter 由进程本地随机源产生，只影响未来调度时间；正确性仍由数据库时间、lease 和 token 决定。
- 测试注入确定性随机源和时钟边界，生产日志不输出 owner、token 或外部凭据。

## 进程生命周期

后台 Worker 直接接入 `signal.NotifyContext` 派生的服务 context，不再以
`context.Background()` 从 Cron 启动。退出分为两个阶段：

1. context 取消后立即停止扫描和领取，已经领取的任务继续执行和续租。
2. 在 drain timeout 内等待在途任务到达可释放边界；到期后取消 task contexts 并停止续租，Worker
   返回，剩余租约由数据库时间过期。

退出不把 context cancellation 误写成执行器业务失败。对于已经 running 但结果未知的 Start，保留
安全失败状态而不是自动回到 pending。进程不得无限等待不遵守 context 的第三方执行器；超过有界
退出窗口后允许进程结束，由 fencing 保证晚到 goroutine 不能写库。

## 集成设置的多副本收敛

`integration_settings.revision` 是每个 provider 的单调数据库版本。更新流程读取当前 revision，完成
输入校验和有界连接探测后，以 `WHERE provider = ? AND revision = ?` 更新配置并令 revision 加一；
零行表示其他副本已经提交，当前请求返回稳定冲突并重新加载，不能覆盖胜者。首次插入通过主键竞争
收敛为同样结果。

每个实例运行 context-aware 的低频同步器，只读取 provider、revision 和密文配置。revision 较新时，
先在候选对象中解密、校验和构造运行时，再以现有进程内门闩原子替换；慢探测完成前必须重新读取
数据库 revision，不能让旧候选覆盖新设置。远端临时不可用时保存安全错误并有界退避重试，不阻塞
核心 API readiness，也不回显上游正文。禁用配置无需网络即可快速收敛。

数据库 revision 提供最终自动收敛和并发写 CAS，不宣称外部平台配置切换与在途 HTTP 请求线性一致。
Jenkins external reference 已固定实例地址；地址更换继续要求先排空活动步骤，窄竞态将在执行前同步
检查和地址匹配中 fail-closed。若未来需要在线无缝迁移 provider，应另立带 generation pinning 的
任务快照决策，不能弱化当前引用校验。

## v1 遗留任务

v1 没有通用步骤快照、稳定执行器幂等键或 fencing 字段语义，不能被包装成看似安全的 v2 任务。
所有副本可以启动 v1 排空循环，但每一轮必须先在专用 MySQL 连接上以零等待获取命名 leader lock；
只有持锁连接对应的实例运行旧状态机，并在整轮结束后显式释放。进程崩溃或连接断开由 MySQL 自动
释放锁。锁名按数据库身份做不可逆摘要并限制长度，日志不输出 DSN。

该约束只用于排空，不为 v1 提供故障中 exactly-once。新发布永远创建 v2；待生产确认不存在活动
v1 后，可以在后续独立 PR 删除旧轮询器和 leader lock。

## 安全与可观测边界

- owner 是随机进程标识，不使用主机元数据，也不进入 API、审计资源或公开日志。
- fencing token、租约期限和 failure count 是内部调度数据，不加入现有 TaskRecord 公共 JSON；实体
  字段必须标记 `json:"-"`。
- SQL 和错误日志只记录 task ID 与稳定错误分类，不输出步骤 config、external reference、集成密文、
  owner 或 token。
- Worker 获取租约不授予业务权限；任务只能来自 W05 已鉴权、已审计的原子创建入口。
- runtime 账号继续只需要 DML；epoch 7 DDL 仅由独立 migrator 执行。

W10 将增加租约领取数、续租失败、接管延迟、到期队列深度和调度延迟指标。本阶段只使用结构化、
低基数日志，不提前开放匿名 metrics。

## 验收与故障测试

W06 至少验证：

1. 三个 Worker 并发领取同一批任务，任一任务同时只有一个未过期合法 lease，token 每次接管递增。
2. 旧 token 对步骤结果、任务状态、释放和 next poll 的全部写入均返回 `ErrLeaseLost`。
3. kill 当前持有者后，第二实例在数据库租期后接管；旧实例恢复或晚返回不能覆盖新结果。
4. Reconcile 阻塞超过一个租期且续租失败时，本地 context 被取消；允许外部调用重叠，但幂等 Mock
   只有一个业务副作用。
5. 任务数超过单次扫描上限时，按 next poll/task ID 最终全部被领取，没有通过 updated_at 轮转。
6. 执行器和数据库临时不可用时退避有上限、有 jitter，恢复后继续执行且不形成请求风暴。
7. SIGTERM 后不再领取新任务；正常在途任务于 drain 窗口完成，超时任务被取消并可到期接管。
8. 两个实例并发修改或加载 integration settings 时 revision/CAS 只保留一个胜者，其他实例自动收敛。
9. 多实例同时运行 v1 排空循环时只有命名锁持有者访问旧 Jenkins；断开连接后另一实例可以接管。
10. MySQL 8.4 migration 的空库、epoch 6 升级、每个 DDL 中断边界、重复执行、manifest、数据契约和
    最小权限账号均通过；隔离 Compose 至少运行三个 API/Worker 副本完成 Noop 发布与故障接管。

## 后果

正面结果是任务推进不再依赖单进程 Cron 假设，调度时间、所有权和故障恢复都成为可验证的持久化
契约；默认部署继续只要求 MySQL。代价是所有 Coordinator 写入都必须携带租约，测试和 migration
矩阵明显扩大，运维升级不能滚动混跑 epoch 6/7。租约也不会解决外部副作用的原子性，执行器幂等、
W07 尝试历史和结果不明确处置仍是完整可靠性模型的一部分。
