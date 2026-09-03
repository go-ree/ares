# ADR-0001：版本化数据库迁移与运行时兼容性检查

- 状态：已接受
- 日期：2026-09-03
- 适用版本：W04 起
- 决策范围：MySQL 8.4、Ares 服务端启动、Docker Compose 部署与数据库恢复

## 背景

当前启动流程会调用 Xorm 的 `Sync2`、执行未版本化的 `ALTER TABLE`，并在应用进程中补做迁移后置条件。这会带来以下问题：

- 运行时账号必须持有 DDL 权限，权限边界过大；
- 实体结构变化可能在服务重启时隐式改变数据库；
- 迁移只有 `version` 与 `applied_at` 两列，不能识别脚本被修改、执行中断或版本兼容区间；
- MySQL DDL 会触发隐式提交，单个事务不能覆盖完整迁移；
- 多实例同时启动时，进程内互斥锁不能阻止跨进程重复执行；
- 数据库异常时，服务可能边修复边启动，失败状态和恢复动作不明确。

W04 将数据库变更从应用运行期拆出，并将数据库状态变成可检查、可追踪、默认拒绝不确定状态的发布前置条件。

## 决策摘要

1. 数据库 DDL 只允许由独立 migrator 执行；`serve` 启动过程只做只读兼容性检查，不创建、修改或修复表结构。
2. 保留无参数启动兼容性，并新增明确的 `serve`、`migrate status`、`migrate up` 命令。
3. 将已有三条迁移固定映射为 epoch 1～3，W04 收口迁移为 epoch 4；W04 应用声明自身 schema epoch 为 4。
4. 空库由显式 bootstrap 建立 epoch 0 起始结构；bootstrap 仅可用于真正空库，且不占用迁移版本。
5. 旧版两列 `schema_migrations` ledger 通过受控收养升级为完整 ledger；未知版本、断档和 checksum 不一致均 fail-closed。
6. migrator 使用同一条专用 `*sql.Conn` 获取并持有数据库级 `GET_LOCK`，迁移完成后显式释放。
7. 每条迁移先持久化 `dirty=1`，成功后才写 `finished_at` 并清除 dirty；失败只能通过指定版本的显式 resume 继续。
8. 不提供 `down`、`force clean` 或自动清除 dirty 的能力。不可安全前滚时，从已验证备份恢复。
9. 运行时账号与迁移账号分离；应用容器不能获得迁移账号连接串。
10. 版本 ledger 与实际 schema manifest 必须同时通过校验，才允许应用启动。

## 命令行契约

### 命令

```text
ares
ares -config /path/to/config.yaml
ares serve
ares serve --config /path/to/config.yaml
ares migrate status --config /path/to/config.yaml
ares migrate up --config /path/to/config.yaml
ares migrate up --resume-dirty 20260903_003_versioned_migrations --config /path/to/config.yaml
```

规则如下：

- `ares` 与 `ares -config ...` 等价于 `ares serve`，用于兼容既有启动方式；
- `-config` 与 `--config` 均可用，配置参数可位于子命令前后，但歧义或未知参数必须报错；
- `migrate status` 严格只读：不得创建 ledger、不得取得写锁、不得执行 seed，也不得初始化集成、任务调度或 Web 服务；
- `migrate up` 执行全部待处理迁移，不提供跳版本和目标版本参数；
- `--resume-dirty <version>` 只接受当前唯一 dirty 行的精确版本号；
- 命令输出包含当前 epoch、兼容区间、pending、dirty、checksum 与 manifest 结论，但不得输出数据库密码或完整 DSN。

### 退出码

| 退出码 | 含义 | 典型场景 |
| --- | --- | --- |
| `0` | 成功 | schema 已兼容、`up` 完成、帮助信息正常输出 |
| `1` | 未分类内部错误 | 仅用于未被正常错误路径捕获的内部故障 |
| `2` | 命令用法错误 | 未知命令、缺少参数、非法 `--resume-dirty` |
| `3` | 数据库状态不允许继续 | pending、dirty、未知版本、版本断档、checksum 不一致、兼容区间或 manifest 不匹配 |
| `5` | 操作性故障 | 配置错误、连接失败、锁超时、迁移执行失败 |

`migrate status` 发现待迁移状态时返回 `3`；`migrate up` 中某条迁移执行失败时返回 `5`，同时保留 dirty 记录。`serve` 的只读检查失败时返回 `3`，且不得启动任何后台任务或监听端口。

## 迁移目录与 epoch

迁移清单是有序、编译期固定且只追加的。已经发布的版本号、描述、操作清单和 checksum 不得修改。

| Epoch | 版本 | 说明 | 完成后的兼容区间 |
| --- | --- | --- | --- |
| 1 | `20260902_001_cleanup_legacy_null_strings` | 历史 NULL 字符串治理 | `[1,1]` |
| 2 | `20260903_001_pluggable_cicd` | 可插拔 CI/CD 结构 | `[2,2]` |
| 3 | `20260903_002_cicd_runtime_hardening` | CI/CD 运行时收口 | `[3,3]` |
| 4 | `20260903_003_versioned_migrations` | W04 schema 与迁移边界收口 | `[4,4]` |

兼容区间描述“该数据库结构允许哪些应用 schema epoch 连接”。当前 W04 应用的 epoch 为 `4`，因此只有最新一条干净迁移的区间包含 `4`，且 manifest 校验成功时，`serve` 才能启动。

未来采用 expand/contract 时可将 expand 阶段记录为跨版本区间，例如 `[4,5]`；contract 完成后再收紧为 `[5,5]`。兼容区间不用于放宽未知版本检查：ledger 中出现本二进制不认识的版本时仍然 fail-closed。

## 空库 bootstrap

bootstrap 是迁移引擎自身的初始化步骤，不是编号迁移，不写入 epoch 行。

`migrate up` 在 ledger 不存在时先查询当前 `DATABASE()` 下的用户表：

1. 表数量为零，才能执行 bootstrap；
2. bootstrap 通过仓库内固定的显式 SQL 建立 epoch 1 所需的 epoch 0 基础结构、完整 ledger 结构，以及历史迁移依赖的最小内置参考数据；
3. bootstrap 完成后仍按顺序执行并记录 epoch 1～4；
4. 任一用户表已存在但 ledger 不存在时，判定为“未受管理数据库”，返回 `3`，不得猜测、`Sync2` 或自动接管；
5. `migrate status` 对空库只报告“未初始化且存在 4 条 pending”，不得执行 bootstrap。

bootstrap SQL 与迁移一样必须幂等、可审查并纳入 checksum/测试资产，但它没有版本号，不能被用来绕开已发布迁移。空库最终必须经过与升级库相同的 epoch 1～4 路径。

## Ledger 结构与状态机

`schema_migrations` 至少包含以下字段：

| 字段 | 类型 | 语义 |
| --- | --- | --- |
| `version` | `VARCHAR(128) PRIMARY KEY` | 不可变迁移标识 |
| `epoch` | `BIGINT UNSIGNED UNIQUE` | 单调递增的结构代次 |
| `description` | `VARCHAR(255)` | 人类可读说明 |
| `checksum` | `CHAR(64)` | 小写十六进制 SHA-256 |
| `dirty` | `TINYINT(1)` | 已开始但未确认完成 |
| `started_at` | `DATETIME(6)` | 首次开始时间 |
| `finished_at` | `DATETIME(6) NULL` | 成功完成时间 |
| `compatible_min` | `BIGINT UNSIGNED` | 允许的最小应用 epoch |
| `compatible_max` | `BIGINT UNSIGNED` | 允许的最大应用 epoch |
| `last_error` | `TEXT NULL` | 截断并脱敏后的最近错误 |
| `legacy_adopted` | `TINYINT(1)` | 是否由旧两列 ledger 收养 |
| `applied_at` | 保留旧类型 | 仅为旧版兼容保留，不再作为完成状态依据 |

checksum 来自稳定的规范化迁移描述，至少覆盖版本、epoch、描述、兼容区间和有序操作标识/SQL；不得使用 Git commit、Go 函数地址、构建时间或格式不稳定的 `SHOW CREATE TABLE` 文本。每条已发布迁移的 checksum 以 golden test 固定，修改旧迁移必须导致测试失败，并通过新增迁移完成变更。

每条迁移遵循以下状态机：

```text
pending
  -> INSERT dirty=1, started_at=NOW(6), finished_at=NULL
  -> 执行可重复的 Up
  -> 校验该 epoch 的 schema manifest
  -> UPDATE dirty=0, finished_at=NOW(6), last_error=NULL

任一步失败
  -> 保持 dirty=1
  -> 写入脱敏、限长的 last_error
  -> 拒绝普通 up 与 serve
```

dirty 行必须在第一条 DDL 前单独持久化。MySQL 的 DDL 会触发隐式提交，不能把整条迁移伪装成一个原子事务；因此所有迁移操作必须可重入，并在每个 `CREATE`、`ALTER`、数据回填动作后验证后置条件。该行为以 [MySQL 8.4 隐式提交说明](https://dev.mysql.com/doc/refman/8.4/en/implicit-commit.html) 为准。

## 旧两列 ledger 收养

既有数据库的 ledger 只有 `version` 和 `applied_at`。`migrate up` 在持有数据库锁后执行可重入的 ledger 元数据升级：

1. 只读识别当前列集合；缺失的新列先以可空形式逐列添加；
2. 只允许出现上表中 epoch 1～3 的版本，并且必须是从 epoch 1 开始的连续前缀；
3. 对每个旧版本校验固定 checksum 对应的迁移后置条件及该 epoch manifest；
4. 使用固定映射回填 epoch、描述、checksum、兼容区间，将 `started_at`/`finished_at` 从 `applied_at` 派生，设置 `dirty=0`、`legacy_adopted=1`；
5. 全部回填和校验成功后再收紧非空约束与唯一约束；
6. 重新读取完整 ledger，再决定是否执行 epoch 4。

ledger 元数据升级不占用业务 epoch，并且每个步骤都必须能在进程中断后重入。以下状态不得自动收养：

- ledger 中存在未知版本、重复 epoch 或版本断档；
- 已知版本对应的实际 schema 后置条件不成立；
- ledger 不存在但数据库并非空库；
- ledger 部分升级后的值与固定目录不一致。

`migrate status` 识别旧两列 ledger 后仅报告“需要收养”并返回 `3`，不能修改表。

## Checksum、版本连续性与 fail-closed

每次 `status`、`up` 和 `serve` 都必须检查：

1. ledger 版本是当前二进制迁移目录的连续前缀；
2. version 与 epoch 一一对应，无重复、无跳号；
3. 所有已完成行 `dirty=0` 且 `finished_at` 非空；
4. ledger checksum 与编译期固定值完全一致；
5. 最新兼容区间合法，且运行应用 epoch 位于区间内；
6. 对应 epoch 的实际 schema manifest 成立。

出现未知版本时，即便该行宣称的兼容区间包含当前应用，也必须 fail-closed。当前二进制无法验证未知迁移的 checksum、操作语义和 manifest，不能信任数据库自行声明的兼容性。

## 跨进程锁与连接生命周期

`migrate up` 打开专用 `*sql.Conn`，并在这条连接上依次完成：

1. 根据数据库名生成长度不超过 64 字符的稳定锁名；
2. `SELECT GET_LOCK(?, timeout_seconds)`；
3. 取得锁后重新读取 ledger，不使用取锁前的 pending 结果；
4. bootstrap、ledger 收养、迁移与最终校验；
5. defer 中执行 `SELECT RELEASE_LOCK(?)`，随后关闭连接。

锁名必须区分数据库，且不得包含密码或完整 DSN。`GET_LOCK` 返回 `0` 或 `NULL` 都视为失败；锁超时返回退出码 `5`，且不写 dirty 行。迁移全过程不得切换到连接池中的另一条连接。

MySQL named lock 是会话级锁，不会因事务提交或回滚而释放；连接结束时也会释放。实现和测试以 [MySQL 8.4 锁函数说明](https://dev.mysql.com/doc/refman/8.4/en/locking-functions.html) 为准。进程内互斥锁不能替代该锁。

## Dirty 恢复

普通 `migrate up` 遇到 dirty 行必须返回 `3`，不得自动重试或清理。恢复步骤为：

1. 使用 `migrate status` 确认唯一 dirty 版本、checksum 和 manifest 差异；
2. 查明失败原因并完成数据库备份；
3. 修复外部原因，例如权限、空间、非法历史数据；
4. 执行 `migrate up --resume-dirty <精确版本>`；
5. migrator 校验 dirty 行版本和 checksum 与当前目录一致；
6. 从该迁移开头重入执行，验证后置条件后清除 dirty，再继续后续迁移。

指定错误版本、存在多个 dirty 行、checksum 不一致或后置条件不可安全判断时均拒绝 resume。系统不提供 `--force`、手工标记完成、自动清 dirty 或 `down` 命令。无法证明可安全前滚时，唯一支持的恢复方式是恢复迁移前备份。

## Schema manifest

版本库为每个受支持 epoch 保存规范化 schema manifest。校验器通过只读查询 `information_schema` 检查 Ares 管理的对象：

- 表名、存储引擎、默认字符集和排序规则；
- 所有受管列的顺序、规范化类型、可空性、默认值、`EXTRA` 与生成列表达式；
- 索引名称、唯一性及有序列集合；
- 外键名称、列映射、引用表与 `ON UPDATE`/`ON DELETE` 规则。

`AUTO_INCREMENT` 当前计数、统计信息、物理页布局不参与比较。数据库中的非 Ares 表可以忽略；Ares 管理表上的未知列、索引或约束必须由 manifest 明确允许，否则视为漂移。

校验策略：

- `migrate status`：按 ledger 最新 epoch 只读校验并展示差异；
- `migrate up`：每条 Up 后校验目标 epoch，只有通过才清除 dirty；
- `serve`：校验 ledger、兼容区间和当前 epoch manifest，只报告问题，不执行修复；
- schema 漂移只能由新增的显式迁移修复，不能由实体定义或启动钩子修复。

因此，修改 Xorm 实体本身不会改变数据库；实体变更必须同时新增迁移、manifest 和测试，否则 CI 不允许合并。

## 运行时与迁移账号

配置增加独立迁移连接：

- `db.conn_str` / `ARES_DB_CONN_STR`：只供 `serve` 和只读 `migrate status` 使用；
- `db.migration_conn_str` / `ARES_DB_MIGRATION_CONN_STR`：只供 `migrate up` 使用，缺失时不得回退到运行时连接。

运行时账号仅授予 Ares schema 上的 `SELECT`、`INSERT`、`UPDATE`、`DELETE`。它不拥有 `CREATE`、`ALTER`、`DROP`、`INDEX` 或 `REFERENCES`。

W04 migrator 按当前迁移所需授予 `SELECT`、`INSERT`、`UPDATE`、`DELETE`、`CREATE`、`ALTER`、`INDEX`、`REFERENCES`。W04 不使用 `DROP`；未来 contract 迁移确需 `DROP` 时，必须在单独 ADR/发布窗口中临时授权并审计。权限语义以 [MySQL 8.4 GRANT 说明](https://dev.mysql.com/doc/refman/8.4/en/grant.html) 为准。

应用容器和日志不得包含迁移 DSN。migrator 的错误输出必须脱敏；测试必须证明运行时账号能正常启动和处理业务 DML，同时任何 DDL 都被数据库拒绝。

## Docker Compose 启动顺序

Compose 固定为以下依赖链：

```text
mysql healthy
    -> migrate: ares migrate up（一次性任务，成功退出）
        -> ares: ares serve（仅运行时账号）
            -> web
```

约束如下：

- `migrate` 使用迁移账号，`restart: "no"`；
- `ares` 仅注入运行时账号，并通过 `depends_on.condition: service_completed_successfully` 等待 migrator；
- migrator 失败或返回非零时，应用不得启动；
- MySQL 健康检查成功后才允许迁移；
- 对现有 volume 重启时，`migrate up` 必须幂等并在无 pending 时快速返回 `0`；
- Dockerfile 以 `ENTRYPOINT ["/app/ares"]` 配合默认 `CMD ["serve"]`，Compose 只覆盖子命令；
- 生产编排遵循同一顺序：备份/维护窗口、一次性迁移 Job、成功后启动或滚动应用。

## 升级、恢复与回退

### 从 W03/main 升级到 W04

epoch 4 的兼容区间为 `[4,4]`，旧应用仍可能执行 Xorm DDL，因此本次是需要短暂停机的边界收口：

1. 备份数据库并完成一次可用性验证或恢复演练；
2. 停止所有旧 Ares 应用实例，确认没有旧二进制连接数据库；
3. 使用 W04 镜像和迁移账号执行 `migrate status`/`migrate up`；
4. `up` 成功后，以仅 DML 的运行时账号启动 W04；
5. 检查健康状态、关键业务读写和 `migrate status`；
6. 移除旧应用账号的 DDL 权限。

不可在旧实例仍运行时执行 epoch 4。

### 迁移失败

迁移失败保留 dirty 行。优先修复根因后使用精确版本 resume 前滚。不得直接修改 ledger、重复运行普通 `up` 或重新启动服务尝试“自愈”。如果后置条件无法确认，恢复迁移前备份并重新评估迁移。

### 应用回退

只有数据库最新兼容区间包含目标旧应用 epoch 时，才允许只回退二进制。epoch 4 不兼容 epoch 3，所以迁移到 W04 后不能直接启动 W03 二进制。W04 应用发布失败时可选择修复并前滚；若必须回退，则恢复迁移前数据库备份后再部署 W03。

未来 expand 迁移应先发布跨版本兼容区间，使应用可以回退；contract 迁移必须在所有旧应用下线且回退窗口关闭后执行。

## 实现边界

- 从 `serve` 路径删除 `Sync`/`Sync2`、`CREATE TABLE`、`ALTER TABLE`、迁移执行和结构后置修复；
- reference/demo seed 可以在兼容性检查通过后以运行时账号执行，但必须只有 DML 且幂等；
- migrator 不初始化 Jenkins、Kubernetes、RabbitMQ、Redis、任务调度或 Web；
- 所有迁移使用显式、可重入操作，执行器不依赖包级活动数据库全局变量；
- 迁移锁、查询和 DDL 使用带超时的 context；错误包含阶段与版本，但必须脱敏；
- 不再把历史 `init.sql` 当作权威 schema；空库入口只有 bootstrap，旧库回归入口使用固定的 W04 前主线 fixture；
- 支持的数据库基线为 MySQL 8.4，行为差异必须通过真实 MySQL 集成测试确认，不能只依赖 mock。

## 验收测试矩阵

| 场景 | 前置状态 | 操作 | 必须结果 |
| --- | --- | --- | --- |
| 空库 status | 无用户表 | `migrate status` | 返回 `3`，列出 epoch 1～4，数据库仍为空 |
| 空库 up | 无用户表 | `migrate up` | bootstrap 后顺序完成 1～4，ledger/manifest 正确 |
| 空库重跑 | 已完成 epoch 4 | 再次 `migrate up` | 返回 `0`，无 DDL、无数据重复 |
| 旧库升级 | 固定的 `main@e2cfd2a` schema/data fixture、两列 ledger | `migrate up` | 收养 1～3、执行 4、业务数据保持 |
| 旧库重启 | 升级完成 | 多次 `serve` | 只读检查稳定通过，schema 不变化 |
| 中断/失败 | Up 已开始并注入 DDL 后故障 | 普通 `up`/`serve` | dirty 保留，两者均拒绝继续 |
| 正确 resume | 唯一 dirty 且 checksum 一致 | 精确 `--resume-dirty` | 可重入完成并清 dirty |
| 错误 resume | 版本错误、多个 dirty 或 checksum 不符 | `--resume-dirty` | 返回 `3`，不修改 schema/ledger |
| Checksum 篡改 | 已完成行 checksum 被改 | `status`/`serve`/`up` | 全部 fail-closed |
| 未知/断档版本 | 插入未知版本或删除中间 epoch | 任一命令 | 返回 `3`，不得执行 DDL |
| 并发迁移 | 两个独立进程同时 `up` | 等待完成 | 只有一个持锁执行；另一个取锁后重读并无重复 |
| 锁超时 | 第三方持有相同 named lock | `migrate up` | 返回 `5`，不新增 dirty 行 |
| Manifest 漂移 | 改列类型、索引或外键 | `status`/`serve` | 返回 `3` 并给出规范化差异，不自动修复 |
| 实体误改 | 只修改 Xorm entity | 启动 `serve` | 数据库零变化；CI 因迁移/manifest 不一致失败 |
| 最小权限 | 运行时账号无 DDL | `serve` 与业务读写 | 正常；直接 DDL 被 MySQL 拒绝 |
| Compose 新部署 | 新 volume | `docker compose up` | mysql → migrate → ares → web，最终健康 |
| Compose 旧 volume | W04 前 fixture | `docker compose up` | 自动执行一次升级，重启幂等 |
| 备份恢复 | epoch 4 后模拟必须回退 | 恢复备份并启动 W03 | 数据库回到 epoch 3，旧应用通过 |

真实 MySQL 8.4 集成检查必须作为独立、稳定名称的 CI 必需检查。测试既要比较 ledger 和业务数据，也要导出并比较规范化 manifest；并发测试必须使用独立进程/连接，不能被进程内互斥锁掩盖。

## 被否决的方案

- **继续在应用启动时运行 Xorm Sync**：无法保证只新增对象，也无法建立最小权限和可预测回退边界。
- **只记录版本号**：不能识别已发布迁移被修改或执行中断。
- **把每条 MySQL DDL 包在一个事务中**：DDL 隐式提交，不具备所需原子性。
- **发现 dirty 后自动重跑或清理**：可能把部分完成的 DDL 误判为成功，扩大损坏。
- **提供通用 down migration**：数据丢失和不可逆 DDL 无法可靠自动回滚。
- **信任未知迁移自报的兼容区间**：当前二进制无法验证其语义与真实 schema。
- **迁移账号回退到运行时 DSN**：会重新引入生产运行账号持有 DDL 权限的诱因。

## 结果

正向结果是数据库变更可审计、并发安全、失败可诊断，应用运行账号可以移除 DDL 权限，实体变化也不会在重启时隐式修改生产 schema。

代价是发布流程必须显式运行 migrator；epoch 4 首次升级需要停机边界；每次 schema 变更都必须同时维护迁移、checksum、manifest、恢复说明和真实 MySQL 测试。这些成本是开源部署可预测性和生产安全边界的一部分。
