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
4. 空库由显式 bootstrap 建立 epoch 1 的固定起始结构；首次执行仅允许真正空库，后续只允许恢复可严格证明由同一 bootstrap 留下的连续中断前缀，bootstrap 不占用迁移版本。
5. 旧版两列 `schema_migrations` ledger 通过受控收养升级为完整 ledger；未知版本、断档和 checksum 不一致均 fail-closed。
6. migrator 使用同一条专用 `*sql.Conn` 获取并持有数据库级 `GET_LOCK`，迁移完成后显式释放。
7. 每条迁移先持久化 `dirty=1`，成功后才写 `finished_at` 并清除 dirty；失败只能通过指定版本的显式 resume 继续。
8. 不提供 `down`、`force clean` 或自动清除 dirty 的能力。不可安全前滚时，从已验证备份恢复。
9. 运行时账号与迁移账号分离；应用容器不能获得迁移账号连接串。
10. 版本 ledger 与实际 schema manifest 必须同时通过校验，才允许应用启动。
11. 迁移账号常态保持锁定；一次性迁移作业通过独立管理员连接建立且仅保留一条已认证迁移会话，随后立即重新锁号，迁移结束时关闭会话并复核无残留连接。
12. Ares schema 只能向声明的 runtime/migrator 身份授权；旧版或未知授权主体必须在任何账号、schema 或 ledger 写入前由 DBA 处置。

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
- YAML 配置必须只有一个文档且所有字段名已知；顶层或嵌套字段拼写错误、多文档输入都在应用环境变量覆盖前失败，不能因错误地漏掉管理员 DSN 而静默降级迁移保护；
- `migrate status` 严格只读：不得创建 ledger、不得取得写锁、不得执行 seed，也不得初始化集成、任务调度或 Web 服务；
- `migrate up` 执行全部待处理迁移，不提供跳版本和目标版本参数；
- `--resume-dirty <version>` 只接受当前唯一 dirty 行的精确版本号；
- 命令输出包含当前 epoch、兼容区间、pending、dirty、checksum 与 manifest 结论，但不得输出数据库密码或完整 DSN。

### 退出码

| 退出码 | 含义                 | 典型场景                                                                        |
| ------ | -------------------- | ------------------------------------------------------------------------------- |
| `0`    | 成功                 | schema 已兼容、`up` 完成、帮助信息正常输出                                      |
| `2`    | 命令用法错误         | 未知命令、缺少参数、非法 `--resume-dirty`                                       |
| `3`    | 数据库状态不允许继续 | pending、dirty、未知版本、版本断档、checksum 不一致、兼容区间或 manifest 不匹配 |
| `5`    | 操作性故障           | 配置错误、连接失败、锁超时、迁移执行失败                                        |

`migrate status` 发现待迁移状态时返回 `3`。`migrate up` 在迁移前或执行过程中发现可识别的 schema 状态、恢复前置条件或 manifest 不满足时返回 `3`；连接、权限、锁等待和普通 SQL 执行故障返回 `5`。故障若发生在 dirty 行持久化之后，两类错误都可能保留 dirty 记录。`serve` 的只读检查失败时返回 `3`，且不得启动任何后台任务或监听端口。

## 迁移目录与 epoch

迁移清单是有序、编译期固定且只追加的。已经发布的版本号、描述、操作清单和 checksum 不得修改。

| Epoch | 版本                                       | 说明                      | 完成后的兼容区间 |
| ----- | ------------------------------------------ | ------------------------- | ---------------- |
| 1     | `20260902_001_cleanup_legacy_null_strings` | 历史 NULL 字符串治理      | `[1,1]`          |
| 2     | `20260903_001_pluggable_cicd`              | 可插拔 CI/CD 结构         | `[2,2]`          |
| 3     | `20260903_002_cicd_runtime_hardening`      | CI/CD 运行时收口          | `[3,3]`          |
| 4     | `20260903_003_versioned_migrations`        | W04 schema 与迁移边界收口 | `[4,4]`          |

兼容区间描述“该数据库结构允许哪些应用 schema epoch 连接”。当前 W04 应用的 epoch 为 `4`，因此只有最新一条干净迁移的区间包含 `4`，且 manifest 校验成功时，`serve` 才能启动。

当前迁移引擎只信任本二进制内置目录能够逐条验证的已知 epoch，并要求数据库最终到达该目录的最新 epoch；兼容区间不能让旧二进制接受未知版本。未来若要让两个应用版本在 expand 阶段同时工作，必须先用新的 ADR 定义可由双方独立证明的跨版本协议、manifest 和发布顺序，不能仅依赖数据库中的 `[4,5]` 自声明。ledger 中出现本二进制不认识的版本时始终 fail-closed。

每个 epoch 的 verifier 是该 epoch 完成状态所承诺的完整、自包含且发布后不可变的后置契约。检查一个已应用连续前缀时，只运行最高已应用 epoch 的 verifier；更早 epoch 是历史快照，不能累积套用到可能已被后续迁移合法改变的结构。后续仍应保持的旧约束必须显式纳入新 verifier。epoch 4 的契约包含 14 张受管表的完整结构 manifest，并保留 epoch 1 的规范化文本值，以及 epoch 2 起的活动环境代码规范化和“每条未删除 AppConfig 的环境必须对应未删除环境目录项”约束。ledger 的版本、epoch、描述、兼容区间和 checksum 仍对每一行逐项校验。

## 空库 bootstrap

bootstrap 是迁移引擎自身的初始化步骤，不是编号迁移，不写入 epoch 行。它建立固定的 10 表 epoch 1 基线，随后由普通 epoch 迁移扩展到 14 表的 epoch 4。

`migrate up` 在 ledger 不存在时先查询当前 `DATABASE()` 下的用户表：

1. 首次进入 bootstrap 前数据库必须没有用户表；migrator 先创建合法的空 ledger，再按固定顺序建立 epoch 1 基线；
2. MySQL DDL 会隐式提交，因此重跑允许恢复 bootstrap 自身留下的中断状态，但已有对象必须严格构成固定建表顺序的连续前缀：ledger 合法且无记录、已有表的完整语义定义与 epoch 1 manifest 一致、不存在未知或乱序对象，并且没有业务数据；
3. 唯一允许存在的参考数据是 bootstrap 自身管理的四个语言规则键；已有行必须是未软删除的固定 JSON 语义，缺失的固定项才可继续补齐；
4. 空两列旧 ledger 也必须先通过同一只读来源门禁，才能执行 ledger `ALTER`；任一条件不满足均在继续 DDL、收养或写 dirty 行前返回 `3`；
5. bootstrap 完成后仍按顺序执行并记录 epoch 1～4；任一用户表已存在但 ledger 不存在时视为未受管理数据库，不得猜测、`Sync2` 或自动接管；
6. `migrate status` 对真正空库只报告“未初始化且存在 4 条 pending”，不得执行 bootstrap。

bootstrap SQL 与迁移一样必须幂等、可审查，并由独立源码指纹和 golden test 固定；它没有 ledger checksum，不能被用来绕开已发布迁移。空库最终必须经过与升级库相同的 epoch 1～4 路径。

## Ledger 结构与状态机

`schema_migrations` 至少包含以下字段：

| 字段             | 类型                       | 语义                                   |
| ---------------- | -------------------------- | -------------------------------------- |
| `version`        | `VARCHAR(128) PRIMARY KEY` | 不可变迁移标识                         |
| `epoch`          | `BIGINT UNSIGNED UNIQUE`   | 单调递增的结构代次                     |
| `description`    | `VARCHAR(255)`             | 人类可读说明                           |
| `checksum`       | `CHAR(64)`                 | 小写十六进制 SHA-256                   |
| `dirty`          | `TINYINT(1)`               | 已开始但未确认完成                     |
| `started_at`     | `DATETIME(6)`              | 首次开始时间                           |
| `finished_at`    | `DATETIME(6) NULL`         | 成功完成时间                           |
| `compatible_min` | `BIGINT UNSIGNED`          | 允许的最小应用 epoch                   |
| `compatible_max` | `BIGINT UNSIGNED`          | 允许的最大应用 epoch                   |
| `last_error`     | `TEXT NULL`                | 截断并脱敏后的最近错误                 |
| `legacy_adopted` | `TINYINT(1)`               | 是否由旧两列 ledger 收养               |
| `applied_at`     | 保留旧类型                 | 仅为旧版兼容保留，不再作为完成状态依据 |

checksum 来自稳定的规范化迁移描述，覆盖版本、epoch、描述、兼容区间、有序操作标识，以及迁移 resume preflight、`Up` 和后置校验依赖文件的源码指纹；不得使用 Git commit、Go 函数地址、构建时间或格式不稳定的 `SHOW CREATE TABLE` 文本。每条已发布迁移的 checksum、实现源码指纹和规范化 manifest/data-contract 摘要均由 golden test 固定，修改旧迁移实现或借未来 epoch 改写历史 manifest 必须导致测试失败，并通过新增迁移完成变更。共享迁移引擎另设独立源码指纹，覆盖 runner、ledger 收养，以及共享 manifest 比较、迁移目录调度和 dirty 恢复路径；这允许安全修复引擎，但要求显式更新审计基线且不重写已落库的历史 checksum。bootstrap 不占用 epoch，但它的显式建表实现同样具有独立源码指纹和 golden test，防止空库起点被静默改写。

每条迁移遵循以下状态机：

```text
pending
  -> INSERT dirty=1, started_at=NOW(6), finished_at=NULL
  -> 执行可重复的 Up
  -> 校验该 epoch 的 schema manifest
  -> UPDATE dirty=0, finished_at=NOW(6), last_error=NULL

Up 或后置校验失败
  -> 保持 dirty=1
  -> 尽力写入脱敏、限长的 last_error
  -> 拒绝普通 up 与 serve
```

dirty 行必须在第一条 DDL 前单独持久化。MySQL 的 DDL 会触发隐式提交，不能把整条迁移伪装成一个原子事务；因此所有迁移操作必须可重入，并在每个 `CREATE`、`ALTER`、数据回填动作后验证后置条件。该行为以 [MySQL 8.4 隐式提交说明](https://dev.mysql.com/doc/refman/8.4/en/implicit-commit.html) 为准。

`started_at` 始终保留该 dirty 行首次开始时间，显式 resume 不重写它。Up 或后置校验失败时 migrator 会尽力记录 `last_error`；若数据库连接或 ledger 写入本身失败，dirty 可能存在但 `last_error` 为空，此时以脱敏后的 migrator 日志和现场备份为准。失败返回前会尽力重新只读检查，使命令输出反映实际留下的 dirty/manifest 状态；刷新失败不能覆盖原始错误。

## 旧两列 ledger 收养

既有数据库的 ledger 只有 `version` 和 `applied_at`。`migrate up` 在持有数据库锁后执行可重入的 ledger 元数据升级：

1. 只读识别当前列集合；缺失的新列先以可空形式逐列添加；
2. 只允许出现上表中 epoch 1～3 的版本，并且必须是从 epoch 1 开始的连续前缀；
3. 对连续前缀中的每一行校验固定目录元数据，并使用最高已应用 epoch 的完整 verifier 验证当前数据库；不得把较早 epoch 的 exact snapshot 依次套用到较新结构；
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

生产/Compose 的 `migrate up` 同时接收迁移 DSN 与独立管理员 DSN。guarded preflight 要求两条物理连接最终选择同一数据库，迁移用户名不能是 root 或与管理员同名；同时拒绝迁移用户名的其他 Host 影子身份、任意入/出向角色、PROXY/被代理关系和 DEFINER 对象。数据库身份以管理员物理连接返回的 `DATABASE()` 规范值为准，不能直接比较 DSN 原始字符串；这兼容 `lower_case_table_names` 的服务端归一化，同时仍会拒绝 migrator 实际连接到不同 schema。管理员物理连接先按迁移用户名摘要取得稳定的账号级 named lock，再锁定迁移账号并清理旧会话，然后为本次执行设置随机一次性密码、短暂解锁并建立唯一的迁移 `*sql.Conn`；该连接必须以 `CURRENT_USER()=<migrator>@%` 命中，并与管理员连接的规范数据库名和 MySQL `server_uuid` 一致。连接建立后立即重新锁定账号、轮换掉一次性密码并清理其他会话。账号被锁定不会中断已经认证的连接，因此后续 migration 只能在这条受控连接内继续，外部无法再建立新会话。账号锁 watchdog 每两秒复核锁仍由原管理员连接持有，丢失时取消迁移；已经锁定但仍有活动会话的状态视为既有 guarded 执行，不允许新任务写入或接管。

受控迁移连接依次完成：

1. 根据数据库名生成长度不超过 64 字符的稳定锁名；
2. `SELECT GET_LOCK(?, timeout_seconds)`；
3. 取得锁后重新读取 ledger，不使用取锁前的 pending 结果；
4. bootstrap、ledger 收养、迁移与最终校验；
5. defer 中执行 `SELECT RELEASE_LOCK(?)`，随后关闭连接；
6. 管理员连接最终再次锁定账号、终止并复核该用户名没有残留会话；清理使用独立的 30 秒后台 context，不因调用方取消而跳过。原管理员连接失效时，在同一服务器身份复核后新建管理员连接，并只用 `GET_LOCK(account_lock, 0)` 非阻塞重取账号锁；若已有后续 owner 则旧任务不得排队或修改账号。清理或复核无法证明成功时整个迁移作业按操作性故障失败。

数据库迁移锁必须区分数据库，账号锁必须区分精确用户名；两者都不得包含密码或完整 DSN。`GET_LOCK` 返回 `0` 或 `NULL` 都视为失败；锁超时返回退出码 `5`，且不写 dirty 行。迁移全过程不得切换到连接池中的另一条连接。迁移账号在作业前后都必须是 `ACCOUNT LOCK` 且无活动会话；仅拥有迁移密码不能在作业窗口外登录。

MySQL named lock 是会话级锁，不会因事务提交或回滚而释放；连接结束时也会释放。实现和测试以 [MySQL 8.4 锁函数说明](https://dev.mysql.com/doc/refman/8.4/en/locking-functions.html) 为准。进程内互斥锁不能替代该锁。

`GET_LOCK` 只在当前 MySQL 服务端实例内有效，不是集群级分布式锁。一次账号任务或迁移作业涉及的 root、管理员、migrator 连接，以及所有并发 Ares 迁移 Job，必须固定到同一个 MySQL 8.4 single-writer 端点；不支持把这些连接交给会按连接、事务或语句分流到多个 writer 的 Router、ProxySQL、DNS 轮询或 active-active 集群。确需多写拓扑时，部署编排必须在 MySQL 之外提供覆盖全部 writer 的租约/CAS 串行机制。HA 切换导致物理连接或 `server_uuid` 变化时，本次作业按操作性故障失败；操作方应先核对账号仍已锁定、会话已清空及 dirty 状态，再从账号任务开始重跑。

## Dirty 恢复

普通 `migrate up` 遇到 dirty 行必须返回 `3`，不得自动重试或清理。恢复步骤为：

1. 使用 `migrate status` 确认唯一 dirty 版本、checksum 和 manifest 差异；
2. 查明失败原因并完成数据库备份；
3. 修复外部原因，例如权限、空间、非法历史数据；
4. 执行 `migrate up --resume-dirty <精确版本>`；
5. migrator 校验 dirty 行版本和 checksum 与当前目录一致；
6. 在更新 `started_at`、清理错误或执行任何 DML/DDL 前，先运行该迁移专属的 resume preflight；通过后才从该迁移开头重入，验证最终后置条件、清除 dirty 并继续后续迁移。

指定错误版本、存在多个 dirty 行、checksum 不一致或后置条件不可安全判断时均拒绝 resume。系统不提供 `--force`、手工标记完成、自动清 dirty 或 `down` 命令。无法证明可安全前滚时，唯一支持的恢复方式是恢复迁移前备份。

dirty 行表示目标迁移可能只完成了部分动作，因此检查阶段运行该迁移定义的 resume preflight，而不是要求部分执行后的数据库已经满足目标 epoch。preflight 接受的状态只能是“上一干净 epoch 的完整契约”以及按迁移语句顺序枚举的精确中间边界；已存在目标列、表或索引也必须验证完整定义，不能只按名称跳过。preflight、ledger 或 checksum 任一不通过时，在任何新的 ledger 或业务写入前返回 `3`。

## Schema manifest

版本库为每个受支持 epoch 保存规范化 schema manifest。校验器通过只读查询 `information_schema` 检查 Ares 管理的对象：

- 基础表名、存储引擎、精确默认字符集和排序规则；Ares 使用专用 schema，未知基础表与视图都属于漂移；
- 所有受管列的名称、规范化类型、可空性、默认值、`EXTRA`、生成列表达式及字符列的精确字符集/排序规则；列的物理排列顺序不属于语义契约；
- 索引的主键/唯一/普通类型、有序列集合、升降序、可见性和索引类型；除 `PRIMARY` 的主键语义外，索引名称不属于语义契约；
- 外键的列映射、引用表与 `ON UPDATE`/`ON DELETE` 规则；约束名称不属于语义契约；Ares 受管表指向外部 schema，或外部 schema 的子表反向引用 Ares 受管表/ledger，都属于不允许的漂移。
- 受管表上的 CHECK 必须由 manifest 声明；当前 epoch 1～4 不允许额外 CHECK。

除 `apps` 在 epoch 4 要求不小于 `10000` 外，`AUTO_INCREMENT` 当前计数、统计信息和物理页布局不参与比较。Ares 使用独占 schema；未知基础表、视图、受管列、索引、外键或 CHECK 都必须由对应 epoch manifest 明确允许，否则视为漂移。InnoDB 中语义等价的 `NO ACTION`/`RESTRICT` 外键动作会规范化比较，操作方自定义的索引/约束名称不会造成伪漂移。

MySQL 会按 `TRIGGER`、`EVENT`、routine 和外键相关元数据权限过滤 `information_schema`，最小权限 runtime/migrator 看到的空结果不能证明这些对象或外部反向引用不存在。Compose 的 root 一次性账号任务会在任何账号或 schema 写入前权威检查目标 schema 不含 trigger、event、routine、view，并拒绝目标账号在任意 schema 中仍作为对象 DEFINER；guarded Go 迁移还会在账号 handoff 前、账号收敛后和成功返回前使用管理员连接权威检查外部入向外键。非 Compose 部署必须由 DBA 执行同等特权 preflight；之后若特权管理员绕过发布流程创建对象，属于对专用 schema 所有权边界的破坏。Go verifier 仍会拒绝当前连接可见的此类对象，但不要求给运行时账号授予危险的对象管理权限。

校验策略：

- `migrate status`：按 ledger 最新 epoch 只读校验并展示差异；
- `migrate up`：每条 Up 后校验目标 epoch，只有通过才清除 dirty；
- `serve`：校验 ledger、兼容区间和当前 epoch manifest，只报告问题，不执行修复；
- schema 漂移只能由新增的显式迁移修复，不能由实体定义或启动钩子修复。

因此，修改 Xorm 实体本身不会改变数据库；实体变更必须同时新增迁移、manifest 和测试，否则 CI 不允许合并。

## 运行时与迁移账号

配置区分三类连接：

- `db.conn_str` / `ARES_DB_CONN_STR`：供 `serve` 和只读 `migrate status` 使用；应用容器中的值必须是运行时账号；
- `db.migration_conn_str` / `ARES_DB_MIGRATION_CONN_STR`：只供 `migrate up` 的唯一受控会话使用，缺失时不得回退到运行时连接；
- `db.migration_admin_conn_str` / `ARES_DB_MIGRATION_ADMIN_CONN_STR`：启用内置 guarded 模式，只供一次性迁移作业锁号、轮换一次性密码、终止/复核会话和权威元数据检查，不得注入应用容器。该 DSN 必须精确命中配置的管理员用户，并由其直接持有全局 `PROCESS`、`CREATE USER`、`SELECT`、`TRIGGER`、`EVENT`、`SHOW VIEW`，以及 `CONNECTION_ADMIN` 或 `SUPER`；`mysql.user.User_attributes.$.Restrictions` 必须为空，不能依赖角色间接授权或受部分权限限制的身份。未配置时二进制保留普通迁移入口，生产部署必须由外部编排或 DBA 提供等价账号生命周期和权威元数据检查。

运行时账号授予 Ares schema 全库 `SELECT`，以便只读检查 ledger 和 manifest；`INSERT`、`UPDATE`、`DELETE` 只逐表授予 14 张受管业务表，不授予 `schema_migrations` 写权限。它不拥有 `CREATE`、`ALTER`、`DROP`、`INDEX` 或 `REFERENCES`。

W04 migrator 按当前迁移所需授予 `SELECT`、`INSERT`、`UPDATE`、`DELETE`、`CREATE`、`ALTER`、`INDEX`、`REFERENCES`。W04 不使用 `DROP`；未来 contract 迁移确需 `DROP` 时，必须在单独 ADR/发布窗口中临时授权并审计。MySQL 在 `@@GLOBAL.partial_revokes=0` 时把数据库级授权名称解释为 LIKE pattern，因此账号任务必须转义 `\\`、`%`、`_`，guarded preflight 也必须核对 `mysql.db.Db` 是当前模式下唯一安全的字面 pattern；切换 `partial_revokes` 后必须重建并复验授权。权限语义以 [MySQL 8.4 GRANT 说明](https://dev.mysql.com/doc/refman/8.4/en/grant.html) 和 [partial revokes 说明](https://dev.mysql.com/doc/refman/8.4/en/partial-revokes.html) 为准。

自动账号任务只管理配置的 `<user>@'%'` 身份，并要求身份归当前 Ares schema 独占。首次写入前要求：服务端是 MySQL 8.4、`@@GLOBAL.mandatory_roles` 为空、实例不存在匿名账号和目标同名非 `%` Host 身份、目标身份没有作为 role/PROXY/DEFINER 向第三方扩散权限或持有其他 schema/全局权限、目标 schema 不含可执行对象或视图，并且 MySQL grant 表中不存在 runtime/migrator 之外仍获目标 schema 权限的主体。后者包括旧 Compose 的宽权限 `MYSQL_USER`；任务只报告并拒绝，不猜测其所有权或自动撤权。

账号任务同样使用上述账号级锁，且 named lock、全部 root 预检、账号 DDL、授权、KILL 和终态验证必须共用一条显式禁用自动重连的物理连接。migrator 任务持有迁移身份锁；runtime 任务按锁名全局字节序同时持有迁移身份锁和 runtime 身份锁，避免撞上迁移窗口或交叉配置形成反向锁序。持锁连接失效后，fail-closed 收敛只有在新连接非阻塞拿齐相同锁集合时才可继续。

门禁通过后，任务锁定目标账号，轮换密码并丢弃 secondary password，清理默认/可激活角色、直接授权、`GRANT OPTION` 与入向 PROXY，再终止并复核旧会话。runtime 获得白名单权限后才解锁，并以目标密码回连验证 `CURRENT_USER()` 精确为 `<user>@'%'`，且 `SET ROLE ALL` 后 `CURRENT_ROLE()` 为 `NONE`；migrator 获得迁移白名单权限后仍保持锁定，由上节的受控作业负责单会话生命周期。运行时任务还必须先证明 migrator 已锁定且会话数为零。连接和整个任务都有有限超时，任何查询、撤销、断连或验证失败都会阻止应用启动。

账号脚本在启用 `NO_BACKSLASH_ESCAPES` 后把经过单引号转义的密码直接放入 `CREATE USER` / `ALTER USER`，不经用户变量、十六进制变量或动态 `PREPARE` 中转；这是为了让 MySQL 8.4 的 `general_log` 对密码语句自动写成 `<secret>`，避免记录可逆中间值。该保证属于本决策固定的 MySQL 8.4 行为和自动化验收项；等价脚本、代理审计层或其他数据库实现必须先证明相同的日志脱敏语义。

共享或托管 MySQL 若不能满足上述条件，不能绕过错误继续启动。应由 DBA 处理身份匹配、继承/代理/DEFINER 关系和旧会话，或用受审计的等价 Job 手工建号并逐项验证有效权限；应用容器不得持有 root 管理凭据。

应用容器和日志不得包含迁移 DSN 或管理员 DSN。migrator 的错误输出必须脱敏；测试必须证明运行时账号能正常启动和处理业务 DML，同时数据库拒绝其执行 DDL 或写入 `schema_migrations`。

## Docker Compose 启动顺序

Compose 固定为以下依赖链：

```text
mysql healthy
    -> database-migrator-user: 创建/收紧迁移账号并保持锁定（一次性任务，成功退出）
        -> migrate: 管理员连接守护单一迁移会话，执行 ares migrate up（一次性任务，成功退出）
            -> database-runtime-user: 创建/收紧运行时账号（一次性任务，成功退出）
                -> ares: ares serve（仅运行时账号）
                    -> web
```

约束如下：

- 两个账号任务使用 root 管理连接，分别在迁移前准备默认锁定的 migrator、迁移后按业务表授予 runtime DML；它们不与应用共享凭据，成功后即退出；
- `migrate` 使用迁移账号和仅该容器可见的管理员连接守护单一会话，`restart: "no"`；容器内执行 `migrate status` 时，`ARES_DB_CONN_STR` 使用管理员只读检查 DSN，避免依赖尚未配置的 runtime 账号；
- `ares` 仅注入运行时账号，并通过 `depends_on.condition: service_completed_successfully` 等待 migrator；
- 账号初始化或 migrator 失败/返回非零时，应用不得启动；
- MySQL 健康检查成功后才允许初始化账号和迁移；
- 升级旧 volume 时，root Secret 必须匹配数据库内的实际密码；仅修改环境变量不会轮换 MySQL root 密码。旧版/未知 schema grantee 必须先由 DBA 审计撤权，否则第一个账号任务会在任何写入前失败；
- 对现有 volume 重启时，`migrate up` 必须幂等并在无 pending 时快速返回 `0`；
- Dockerfile 以 `ENTRYPOINT ["/app/ares"]` 配合默认 `CMD ["serve"]`，Compose 只覆盖子命令；
- 生产编排遵循同一顺序：备份/维护窗口、一次性迁移 Job、成功后启动或滚动应用。

## 升级、恢复与回退

### 从 W04 前主线基线（`main@e2cfd2a`）升级到 W04

epoch 4 的兼容区间为 `[4,4]`，旧应用仍可能执行 Xorm DDL，因此本次是需要短暂停机的边界收口：

1. 备份数据库并完成一次可用性验证或恢复演练；
2. 停止所有旧 Ares 应用实例，确认没有旧二进制连接数据库；
3. 由 DBA 枚举目标 schema 的 grantee，确认旧 `MYSQL_USER` 等主体不再被任何实例使用后撤权或删除，并保留审计记录；
4. 使用 W04 一次性迁移作业的管理员连接执行只读 `migrate status`，再由其建立唯一 migrator 会话执行 `migrate up`；
5. `up` 成功并复核 migrator 已锁定、无会话后，以仅 DML 的运行时账号启动 W04；
6. 检查健康状态、关键业务读写和运行时 `migrate status`。

不可在旧实例仍运行时执行 epoch 4。

### 迁移失败

迁移失败保留 dirty 行。优先修复根因后使用精确版本 resume 前滚。不得直接修改 ledger、重复运行普通 `up` 或重新启动服务尝试“自愈”。如果后置条件无法确认，恢复迁移前备份并重新评估迁移。

### 应用回退

只有数据库最新兼容区间包含目标旧应用 epoch 时，才允许只回退二进制。epoch 4 不兼容 epoch 3，所以迁移到 W04 后不能直接启动创建迁移前备份的 W04 前二进制。W04 应用发布失败时可选择修复并前滚；若必须回退，则恢复迁移前数据库备份后再部署与该备份精确匹配的旧版本。路线图工作包 W03 与 schema epoch 3 不是同一概念。

未来 expand/contract 若要支持跨二进制版本回退，必须先通过独立 ADR 定义双方均可验证的兼容协议和发布顺序；contract 迁移只能在所有旧应用下线且回退窗口关闭后执行。

## 实现边界

- 从 `serve` 路径删除 `Sync`/`Sync2`、`CREATE TABLE`、`ALTER TABLE`、迁移执行和结构后置修复；
- reference/demo seed 可以在兼容性检查通过后以运行时账号执行，但必须只有 DML 且幂等；
- migrator 不初始化 Jenkins、Kubernetes、RabbitMQ、Redis、任务调度或 Web；
- 所有迁移使用显式、可重入操作，执行器不依赖包级活动数据库全局变量；
- 生产迁移入口必须在管理员连接守护下使用唯一 migrator 物理连接，退出路径无论成功或失败都重新锁号、清理并复核会话；
- 迁移锁、查询和 DDL 使用带超时的 context；错误包含阶段与版本，但必须脱敏；
- 不再把历史 `init.sql` 当作权威 schema；空库入口只有 bootstrap，旧库回归入口使用固定的 W04 前主线 fixture；
- 仓库通过 `.gitattributes` 将 Go、Shell、SQL、YAML、Markdown 和 JSON 文本固定为 LF，避免跨平台换行改写源码指纹或迁移审计基线；
- 配置文件使用严格 YAML 解码，只接受单文档和已知字段；`status` 与 runtime 兼容性检查的全部多查询读取均固定在各自唯一物理连接上；
- 仅支持 MySQL 8.4.x；`status`、`up` 和 `serve` 都会读取服务端版本并对其他 MySQL 版本或 MariaDB fail-closed。行为差异必须通过真实 MySQL 8.4 集成测试确认，不能只依赖 mock。

## 验收测试矩阵

| 场景                | 前置状态                                                  | 操作                                                    | 必须结果                                                                                |
| ------------------- | --------------------------------------------------------- | ------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| 空库 status         | 无用户表                                                  | `migrate status`                                        | 返回 `3`，列出 epoch 1～4，数据库仍为空                                                 |
| 空库 up             | 无用户表                                                  | `migrate up`                                            | bootstrap 后顺序完成 1～4，ledger/manifest 正确                                         |
| 空库重跑            | 已完成 epoch 4                                            | 再次 `migrate up`                                       | 返回 `0`，无 DDL、无数据重复                                                            |
| 旧库升级            | 固定的 `main@e2cfd2a` schema/data fixture、两列 ledger    | `migrate up`                                            | 收养 1～3、执行 4、业务数据保持                                                         |
| 旧库重启            | 升级完成                                                  | 多次 `serve`                                            | 只读检查稳定通过，schema 不变化                                                         |
| 中断/失败           | Up 已开始并注入 DDL 后故障                                | 普通 `up`/`serve`                                       | dirty 保留，两者均拒绝继续                                                              |
| 正确 resume         | 唯一 dirty 且 checksum 一致（包括初始 `last_error=NULL`） | 精确 `--resume-dirty`                                   | 可重入完成并清 dirty，保留首次 `started_at`                                             |
| 错误 resume         | 版本错误、多个 dirty 或 checksum 不符                     | `--resume-dirty`                                        | 返回 `3`，不修改 schema/ledger                                                          |
| Checksum 篡改       | 已完成行 checksum 被改                                    | `status`/`serve`/`up`                                   | 全部 fail-closed                                                                        |
| 未知/断档版本       | 插入未知版本或删除中间 epoch                              | 任一命令                                                | 返回 `3`，不得执行 DDL                                                                  |
| 并发迁移            | 两个独立进程经同一 MySQL 实例同时 `up`                    | 等待完成                                                | 只有一个持锁执行；另一个取锁后重读并无重复                                              |
| 锁超时              | 第三方持有相同 named lock                                 | `migrate up`                                            | 返回 `5`，不新增 dirty 行                                                               |
| Manifest 漂移       | 改列类型、索引或外键                                      | `status`/`serve`                                        | 返回 `3` 并给出规范化差异，不自动修复                                                   |
| 结构先于数据诊断    | 删除数据契约依赖的表或列                                  | `status`/`serve`                                        | 返回 `3` 和结构差异，不误报 SQL 操作故障，ledger 零写                                   |
| 外部入向外键        | 外部 schema 子表引用 Ares 受管表或 ledger                 | guarded `migrate up`                                    | 管理员在账号凭据变更前权威拒绝，并指出应删除的外键                                      |
| 非正历史主键        | NULL 历史行使用 `0`、负 INT 或最小 BIGINT 主键            | epoch 1 迁移                                            | 所有批次均被治理，不因 keyset 游标跳行                                                  |
| 环境目录漂移        | 未删除 AppConfig 指向不存在或已软删除的环境目录项         | `status`/`serve`/`up`                                   | 返回 `3`，不得自动猜测或回填目录                                                        |
| 实体误改            | 只修改 Xorm entity                                        | 启动 `serve`                                            | 数据库零变化；CI 因迁移/manifest 不一致失败                                             |
| 最小权限            | 运行时账号无 DDL                                          | `serve` 与业务读写                                      | 正常；直接 DDL 被 MySQL 拒绝                                                            |
| 迁移账号生命周期    | migrator 默认锁定且无会话                                 | 受管理员连接守护的 `migrate up`                         | 仅建立一条迁移会话；立即重新锁号，成功/失败后均无残留会话且旧密码不可登录               |
| 管理员元数据受限    | 缺任一直接全局元数据权限或存在 partial Restrictions       | guarded `migrate up` / Compose 账号任务                 | 在账号/schema/ledger 写入前拒绝，不接受角色间接授权                                     |
| 未知 schema grantee | 旧账号仍持有目标 schema 权限                              | 任一 Compose 账号任务                                   | 在账号/schema/ledger 写入前拒绝，DBA 撤权后才能继续                                     |
| 密码日志            | MySQL 8.4 开启 `general_log`                              | 运行账号脚本                                            | 日志只含 MySQL 重写的 `<secret>`，不含明文或可逆十六进制中间值                          |
| Compose 新部署      | 新 volume                                                 | `docker compose up`                                     | mysql → database-migrator-user → migrate → database-runtime-user → ares → web，最终健康 |
| Compose 旧 volume   | W04 前 fixture 且旧宽权限账号仍存在                       | `docker compose up`                                     | 写入前拒绝；DBA 审计撤权后重跑可完成升级，后续重启幂等                                  |
| 真实 CLI            | 隔离 MySQL 8.4 数据库                                     | 调用实际 `realMain` 入口                                | status/up/serve/用法/连接故障退出码与 stdout/stderr 脱敏契约一致                        |
| 备份恢复            | epoch 4 后模拟必须回退                                    | 将迁移前备份恢复到新库，启动创建该备份的精确 W04 前版本 | 恢复为旧两列 ledger 对应状态，旧应用健康检查及关键读写通过                              |

真实 MySQL 8.4 集成检查必须作为独立、稳定名称的 CI 必需检查。测试既要比较 ledger 和业务数据，也要导出并比较规范化 manifest；并发测试必须启动独立 OS 进程和独立连接，不能被进程内互斥锁掩盖；命令行契约必须通过真实 `realMain` 入口验证，不能只测试参数解析或内部函数。

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
