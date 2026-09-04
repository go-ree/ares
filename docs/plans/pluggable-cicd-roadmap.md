# 可插拔 CI/CD 实施路线

> 状态：PR #4 已交付阶段 A～C 与阶段 D 主链路。阶段 D 的通用步骤日志和阶段 E 后续工作统一在 [开源化与生产能力开发计划](open-source-production-roadmap.md) 中跟踪。

本文是 [可插拔 CI/CD 与动态环境架构](../architecture/pluggable-cicd.md) 的实施计划。每一阶段都要求可独立验证、可升级并支持前向修复；数据库迁移后的旧镜像降级不等于安全回退，旧数据删除不属于当前阶段。

## 1. 交付原则

- 先固定契约和迁移边界，再改变运行行为。
- 数据库采用只增不删的兼容迁移。
- 数据库结构升级后禁止旧版 Xorm 进程继续写库；应用回退必须同时满足 schema 与 v2 Worker 兼容性。
- 新旧任务通过 `engine_version` 区分，禁止 shadow 双执行。
- Jenkins、Kubernetes、Redis 和 RabbitMQ 均不是 Ares 启动的强制依赖。
- 环境目录、AppConfig、流程定义、运行快照分别承担单一职责。
- 每阶段均通过中文 PR 提交，由维护者合并。

## 2. 阶段拆分

### 阶段 A：领域契约与动态环境

- [x] 审计 Jenkins 耦合、固定步骤状态机和三环境写死位置。
- [x] 确认 AppConfig 是环境专属流程的绑定点。
- [x] 演进 `env_configs`：新增 `enabled`、`sort_order`，旧工具链字段兼容可空。
- [x] 新增环境查询、创建、更新/停用 API。
- [x] 新应用不再自动创建 `dev/test/moni` 配置。
- [x] 后端环境校验统一走环境服务。
- [x] Kubernetes 客户端和集成设置改为动态环境映射。
- [x] 前端环境类型改为字符串，并统一从 API 读取。
- [x] 应用详情、发布、批量发布、日志和系统设置不再过滤自定义环境。

验收：创建 `qa-cn`，为应用新增配置并在所有相关页面正常显示；停用后不能新建配置或发布，但历史仍可读。

### 阶段 B：流程定义与步骤注册表

- [x] 新增流程、不可变版本、AppConfig 绑定和任务步骤表。
- [x] 定义版本化 WorkflowSpec 及完整校验。
- [x] 实现 Executor Registry 和描述符 API。
- [x] 实现 `builtin.noop@v1`。
- [x] 提供 AppConfig 流程查询、校验、发布新版本 API。
- [x] Demo 为每个示例应用环境绑定可直接执行的 Noop 流程。

验收：同一应用两个环境可保存不同的三步以上流程；修改后版本递增，旧版本不变；未知步骤无法保存。

### 阶段 C：通用串行编排

- [x] 创建任务时原子保存流程与上下文快照。
- [x] 通用 Worker 按顺序推进任意数量步骤。
- [x] 使用数据库 CAS 保证同一步骤只被认领一次；多副本租约放到阶段 E。
- [x] 失败策略支持 `stop` 和 `continue`。
- [x] 移除三小时扫描窗口。
- [x] 任务详情返回通用步骤状态。
- [x] 前端展示通用步骤时间线。

验收：Noop 流程无需 Jenkins 即可从 queued 运行到 succeeded；并发 Worker 不重复认领；步骤失败能得到确定终态。

### 阶段 D：Jenkins Adapter 与兼容切换

- [x] 把 Jenkins 调用封装为 `jenkins.job@v1`，发布核心不再 import Jenkins。
- [x] 外部引用同时容纳 Queue ID、Job 和 Build Number。
- [x] Jenkins 调用贯穿请求 context，不在 HTTP 请求中同步等待 Build Number。
- [x] 把旧两 Job 组合幂等导入为流程版本并绑定 AppConfig。
- [x] 新任务进入 v2；旧轮询器只续跑带可验证实例绑定的 v1 任务，历史未绑定在途任务 fail-closed。
- [x] 旧 CI/CD 字段只作为兼容投影。
- [ ] 日志改为通过任务步骤读取，限制任意 Job 访问。

验收：包含 Jenkins 步骤的任务行为与旧发布一致；关闭 Jenkins 后仅该类流程不可运行，Ares 其余功能和 Noop 流程正常。

### 阶段 E：可靠性与扩展生态

- [ ] 移除运行时 Xorm 结构同步，空库 bootstrap 与存量结构变化统一使用版本化迁移（W04 实现已落地，guarded migrator、旧 volume 升级和最终门禁仍在验收，继续保持未勾选）。
- [ ] 增加 attempt、有限重试、退避、超时和取消。
- [ ] 为多副本 Worker 增加 `next_poll_at`、owner/lease 和公平到期扫描。
- [ ] 发布 API 支持 `Idempotency-Key`。
- [ ] 增加 Secret Resolver，流程仅保存 Secret 引用。
- [ ] 提供执行器契约测试套件和开发模板。
- [ ] 评估 GitHub Actions、Webhook、Kubernetes 原生发布等执行器。
- [ ] 抽象任务通知接口，按规模选择 MySQL 轮询、Redis 或 RabbitMQ。

## 3. PR #4 已交付范围

2026-09-04 进度同步：W04 已把 epoch 1～4 拆为独立完整 schema/data 契约，运行时只读检查不再执行结构 DDL；未删除 AppConfig 必须对应未删除环境目录项。Compose 使用特权账号门禁、默认锁定的 migrator、管理员守护的唯一迁移会话和无 DDL runtime 账号；发现旧版/未知 schema grantee 时会在任何写入前拒绝，必须由 DBA 先撤权再升级。独立进程并发、真实 CLI 和账号日志/权限自动化已经补入，guarded Compose 全链路、最终门禁、中文 PR 与 GitHub CI 仍在进行，完成前本项保持未勾选。详细证据以[开源化与生产能力开发计划](open-source-production-roadmap.md) W04 记录为准。

PR #4 以形成可运行的第一条纵向闭环为目标，已经交付：

1. 完成阶段 A 的动态环境主链路。
2. 完成阶段 B 的流程数据模型、注册表、Noop 与配置 API。
3. 完成阶段 C 的 Noop 串行编排和步骤查询。
4. 完成 Jenkins Adapter 的核心解耦，保留旧任务查询兼容和严格受限的收尾路径。
5. 前端提供动态环境选择与基础步骤编辑能力。
6. 补齐单元测试、前端构建和 Docker Compose 空库验证。

可靠重试、取消、完整通用日志和更多第三方执行器进入 [后续开发计划](open-source-production-roadmap.md)，不以不完整实现扩大首版风险。

## 4. 数据迁移与发布步骤

1. 发布前备份 MySQL，并确认没有重复的 `(app_id, normalize(env))`。
2. 必须尽量等待旧的 `packaging`、自动部署中的 `packaged`、`deploying` 任务结束后升级；遗留未绑定任务会 fail-closed，升级后需人工重新发布。
3. 停止全部旧实例，由 DBA 审计并撤销旧 `MYSQL_USER` 等非 runtime/migrator 主体对 Ares schema 的授权；W04 不自动删除未知主体，未撤权时会零写入拒绝。
4. 使用管理员连接守护默认锁定 migrator 的单一会话执行 `ares migrate up`，成功且账号重新锁定、无会话后，再以无 DDL 权限的运行时账号启动新版；具体步骤见[数据库迁移与恢复手册](../operations/database-migrations.md)。
5. 检查导入流程和 AppConfig 绑定；Jenkins 集成存在时执行只读连通性检查。
6. 使用 Noop Demo 验证通用引擎，再为单个非关键应用环境切换 Jenkins 流程。
7. 观察任务和步骤状态，逐步扩大使用范围。
8. 如需回退，先冻结写入并停止创建新任务；已经启动的 v2 任务必须继续由 v2 Worker 收尾，不能交给旧引擎重触发。禁止直接让旧 `main` 镜像连接升级后的可写数据库，因为旧 Xorm 同步会删除新索引、但不会撤销迁移版本标记。优先前向修复；W04 epoch 4 回退必须把应用与数据库一起恢复到升级前备份。

## 5. 测试矩阵

### 后端

- 环境代码规范化、非法/重复代码、排序、启停、引用保护。
- 任意环境名 `qa-cn`、`prod-blue` 的应用配置创建和发布校验。
- 流程重复 key、未知 `uses`、空步骤、无效配置、版本冲突。
- Noop 同步成功、失败、`continue`、三个以上步骤。
- 两个调度循环并发时步骤 CAS 只成功一次。
- 流程更新后运行快照不变。
- Jenkins 未配置时 Noop 可用、Jenkins 步骤返回明确错误。
- 迁移幂等和旧终态任务查询兼容。

### 前端

- API 返回四个以上环境或未知历史环境时不丢数据。
- 空环境目录、停用环境和首次新增 AppConfig。
- 分支输入不再随环境名自动改写。
- 不同 AppConfig 编辑并保存不同步骤组合。
- 任务详情展示任意数量的步骤。
- TypeScript 类型检查和生产构建通过。

### 部署

- 空数据库 `docker compose up -d --build` 后健康检查通过。
- Demo 数据包含自定义环境和可运行流程。
- 不配置 Jenkins、Kubernetes、Redis、RabbitMQ 时核心功能正常。
- 重启不会重复插入目录、流程、步骤或重复触发运行。

## 6. 完成定义

- 架构文档、扩展指南、迁移说明与代码行为一致。
- 后端与前端不存在 `dev/test/moni` 三值业务枚举；Demo 数据中的示例值除外。
- 发布核心不以 Jenkins 是否配置作为全局入口条件。
- AppConfig 能绑定并运行自己的版本化串行步骤。
- 所有自动化测试、前端构建和 Compose 验证通过。
- 通过中文 PR 提交，不直接合并。
