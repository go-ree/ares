# 数据库迁移与恢复手册

本文面向部署和维护 Ares 的管理员，说明数据库版本检查、升级、失败恢复与回退边界。架构取舍和迁移状态机见 [ADR-0001：版本化数据库迁移与运行时兼容性检查](../architecture/decisions/0001-versioned-database-migrations.md)。

## 命令与职责边界

Ares 二进制提供三个运行入口：

| 命令                  | 数据库连接                                                               | 是否写数据库 | 用途                                                                                                   |
| --------------------- | ------------------------------------------------------------------------ | ------------ | ------------------------------------------------------------------------------------------------------ |
| `ares migrate status` | `db.conn_str` / `ARES_DB_CONN_STR`                                       | 否           | 只读检查 ledger、checksum、兼容区间和 schema manifest                                                  |
| `ares migrate up`     | `db.migration_conn_str`；生产/Compose 另配 `db.migration_admin_conn_str` | 是           | 由管理员连接守护唯一 migrator 会话，获取数据库级锁，bootstrap 空库、收养旧 ledger 并执行所有待处理迁移 |
| `ares serve`          | `db.conn_str` / `ARES_DB_CONN_STR`                                       | 仅业务 DML   | 先做只读兼容性检查，再初始化参考数据、可选 Demo 数据并启动服务                                         |

不带子命令时仍等价于 `ares serve`，用于兼容原有启动方式。所有命令默认读取 `config/default.yaml`，也可以用 `--config <文件>` 指定配置：

```bash
./ares --config /etc/ares/config.yaml migrate status
./ares --config /etc/ares/config.yaml migrate up
./ares --config /etc/ares/config.yaml serve
```

配置文件严格只接受一个 YAML 文档和已声明字段。未知顶层/嵌套 key 或额外 `---` 文档会在环境变量覆盖前直接返回配置错误（退出码 `5`），避免 `migration_admin_conn_str` 等关键字段拼写错误后静默降级；修正配置本身，不要依赖环境变量掩盖错误 key。

`migrate up` 必须显式配置迁移连接，生产/Compose 还使用 `db.migration_admin_conn_str` / `ARES_DB_MIGRATION_ADMIN_CONN_STR` 守护迁移账号的短生命周期；缺失时不会回退使用运行时连接。Compose 的 `migrate` 容器把管理员检查连接放在自己的 `ARES_DB_CONN_STR` 中，以便 runtime 账号尚未配置时执行严格只读的 `migrate status`；`ares` 容器的同名变量始终是运行时连接。命令不会初始化 Web、Worker、Jenkins 或 Kubernetes。

### 退出码

| 退出码 | 含义                          | 常见场景                                                                               |
| ------ | ----------------------------- | -------------------------------------------------------------------------------------- |
| `0`    | 操作成功                      | schema 已兼容，或迁移全部完成                                                          |
| `2`    | 命令行参数错误                | 未知子命令、缺少参数、错误使用 `--resume-dirty`                                        |
| `3`    | schema 状态需要迁移或人工处置 | 空库、存在待执行迁移、旧 ledger 待收养、dirty、checksum 不符、未知版本或 manifest 漂移 |
| `5`    | 运行故障                      | 配置读取失败、数据库不可达、权限不足、SQL 失败或等待迁移锁超时                         |

`migrate status` 在空库或待升级数据库上返回 `3` 是预期行为，不表示检查命令本身执行失败。自动化发布应区分 `3` 与连接类故障 `5`，但两者都不得继续启动不兼容的应用。

## 最小权限账号

生产部署应使用两个不同的 MySQL 账号：

- 运行时账号拥有 Ares 数据库的只读权限，并且只对 20 张受管表授予精确的写权限：原有业务表为 `INSERT`、`UPDATE`、`DELETE`，身份与审计表按实际用途进一步收紧；它可以读取 `schema_migrations` 做 `status`/`serve` 检查，但不能修改 ledger；
- 迁移账号额外授予 `CREATE`、`ALTER`、`INDEX`、`REFERENCES`，并保留迁移所需的 DML 权限；当前迁移不需要 `DROP`。该账号常态必须锁定且没有活动会话；
- 不要向应用容器注入迁移 DSN 或管理员 DSN，也不要让 `migrate up` 回退使用运行时 DSN；guarded 管理员必须直接持有全局 `PROCESS`、`CREATE USER`、`SELECT`、`TRIGGER`、`EVENT`、`SHOW VIEW`，以及 `CONNECTION_ADMIN` 或 `SUPER`，且 `mysql.user.User_attributes.$.Restrictions` 为空。缺任一项、通过角色间接获得或存在部分权限限制时，都无法权威证明会话与元数据全集；
- 密码只应进入部署系统的 Secret 或本地未提交的 `.env`，不得写入仓库、命令输出或日志。

默认 Compose 使用 `MYSQL_RUNTIME_USER` / `MYSQL_RUNTIME_PASSWORD` 和 `MYSQL_MIGRATION_USER` / `MYSQL_MIGRATION_PASSWORD`。同一份[账号初始化脚本](../../deploy/compose/mysql/01-create-users.sh) 按角色分两次运行：

- `database-migrator-user` 在 MySQL 健康后创建/更新迁移账号并重置其授权，最终保持账号锁定，成功后才允许 migrator 启动；
- `database-runtime-user` 在 schema 迁移完成后创建/更新运行时账号，先撤销既有授权，再授予全库 `SELECT` 和 20 张受管表的逐表写权限，成功后才允许 `ares` 启动。

六张身份与审计表的写权限分别为：`auth_users` 仅 `INSERT, UPDATE`，`auth_identities` 仅 `INSERT`，`auth_sessions` 和 `auth_oidc_flows` 为 `INSERT, UPDATE, DELETE`，`auth_bootstrap_state` 仅 `UPDATE`，`audit_events` 仅 `INSERT`。应用运行账号不能修改或删除审计事件；服务端对 Bootstrap singleton 只允许从未完成原子转换为已完成，不提供恢复入口。

该流程不依赖 MySQL 仅在空数据目录执行一次的 `/docker-entrypoint-initdb.d`。升级 PR #6 等旧 Compose volume 前，必须先由 DBA 撤销旧版/未知主体对目标 schema 的授权；否则账号任务会在任何写入前 fail-closed。`.env` 中的 `MYSQL_ROOT_PASSWORD` 必须继续使用数据库当前实际的 root 密码；只改环境变量不会轮换数据库内的 root 密码。账号任务限定 MySQL 8.4.x，并分别受 `ARES_DATABASE_ACCOUNT_CONNECT_TIMEOUT_SECONDS`（单次连接，默认 5 秒）、`ARES_DATABASE_ACCOUNT_LOCK_TIMEOUT_SECONDS`（等待账号级互斥锁，默认 30 秒）和 `ARES_DATABASE_ACCOUNT_INIT_TIMEOUT_SECONDS`（整个任务，默认 60 秒）约束。正常 `docker compose up` 会自动运行该任务，也可以在 MySQL 已健康时单独执行：

```bash
docker compose up -d mysql
docker compose run --rm --no-deps database-migrator-user
docker compose run --rm --no-deps migrate
docker compose run --rm --no-deps database-runtime-user
```

手工重放时必须保持上述顺序，因为运行时逐表 DML 授权在 schema 建立后执行。两个账号任务只管理配置的 `<user>@'%'` 身份，并要求每个身份由这一套 Ares schema 独占；目标身份存在全局动态/静态权限、其他 schema 授权或外部表/列/存储程序授权时，会在任何写入前拒绝收权，避免静默破坏共享账号。其他前置门禁包括：服务端必须为 MySQL 8.4.x、`@@GLOBAL.mandatory_roles` 为空、实例没有匿名账号和同名非 `%` Host 身份、目标账号没有作为 role/PROXY/DEFINER 向外扩散权限，Ares 专用 schema 不含 trigger、event、routine 或 view，并且 runtime/migrator 以外没有任何主体仍在 MySQL grant 表中持有目标 schema 权限。

账号任务以账号名 SHA-256 摘要派生稳定的 MySQL named lock。migrator 任务持有迁移账号锁；runtime 任务同时持有迁移账号锁和运行时账号锁，并按锁名全局字节序获取，因而既不会撞上 guarded migration，也不会与复用同一身份的另一任务交叉修改。所有特权预检、账号 DDL、授权、会话清理和终态检查都由持有这些锁的同一条 root 物理连接执行；客户端显式使用 `--skip-reconnect`。连接丢失时，故障收敛只能由新连接以 `GET_LOCK(name, 0)` 按相同顺序立即拿齐全部锁后执行；任一锁已有后续 owner 时，旧任务不得等待或修改账号。

这些 named lock 只在单个 MySQL 服务端实例内互斥。全部账号任务、管理员/迁移连接及并发迁移 Job 必须指向同一个稳定 single-writer MySQL 8.4 端点，不能经 Router、ProxySQL、DNS 轮询或读写分离把同一作业或不同作业分流到多个 writer。active-active 拓扑必须由外部编排提供跨节点分布式互斥，不能把 `GET_LOCK` 当作集群锁。HA 故障转移导致连接或 `server_uuid` 变化时让作业失败，先核对账号锁定/会话清理与 dirty 状态，再从账号任务开始重跑。

门禁通过后，任务会先锁定账号，轮换 primary password 并丢弃旧 secondary password，设置 `DEFAULT ROLE NONE`，撤销全部入向 role binding、直接授权、`GRANT OPTION` 和入向 PROXY；随后终止并复核该用户名的所有既有会话，再授予白名单权限。runtime 会解锁并以目标密码回连，要求 `CURRENT_USER()` 精确命中 `<user>@'%'`，且执行 `SET ROLE ALL` 后 `CURRENT_ROLE()` 仍为 `NONE`；回连或终态验证失败会在仍持锁时 fail-closed 地锁号、轮换凭据并清空会话。migrator 不在账号任务中解锁：guarded preflight 还拒绝 root/同名管理员、其他 Host 影子身份、入/出向角色、PROXY/被代理关系和 DEFINER 对象；`migrate up` 的管理员连接持有同一个迁移账号 named lock，为本次执行设置随机一次性密码、短暂解锁并建立唯一会话，核对 `CURRENT_USER()`、数据库和 MySQL `server_uuid` 后立即重新锁号、轮换掉一次性密码并清除其他会话。数据库身份使用管理员物理连接返回的 `DATABASE()` 规范值，再与 migrator 和清理回连的实际值比较；因此 DSN 大小写被 `lower_case_table_names` 归一化时不会误拒绝，实际跨 schema 仍会失败。迁移期间 watchdog 每两秒证明 named lock 仍由原管理员连接持有；账号已锁定但仍有会话时，新的迁移/账号任务会视为既有 guarded 执行并零写入拒绝接管。迁移结束时关闭唯一会话，再锁号、清理和复核会话数为零；最终清理使用独立 30 秒后台 context，原管理员连接失效时只允许重连后非阻塞重取同一 named lock，拿不到即拒绝越过新 owner 收敛，仍无法证明安全状态则按操作性故障失败。运行时账号任务还会先证明 migrator 已锁定且无会话。任何查询、撤销、断连或验证无法完成都会使一次性任务失败并阻止应用启动。

MySQL 在 `@@GLOBAL.partial_revokes=0` 时会把数据库级 `GRANT` 的 schema 名当作 LIKE pattern，即使名称使用反引号；账号任务会对其中的 `\\`、`%`、`_` 使用 MySQL grant-pattern 转义，并要求 `mysql.db` 只保留与当前模式相符的唯一字面授权。`partial_revokes=1` 时则使用字面 schema 名。guarded migrator 会独立执行同一检查，因此旧版未转义的 `ares_prod` 一类授权不会被误判为精确权限；须先由 DBA 撤销不安全授权，再重放账号任务。

账号脚本启用 `NO_BACKSLASH_ESCAPES`，对密码中的单引号做 SQL literal 转义后直接执行 `CREATE USER` / `ALTER USER`；它不使用会被日志留下可逆内容的密码用户变量、十六进制中间值或动态 `PREPARE`。自动化会在 MySQL 8.4 开启 `general_log` 后验证这些语句由服务端重写成 `<secret>`，日志中不能出现明文或可逆密码表示。托管平台若改变该日志语义，必须先完成等价验证。

托管或共享 MySQL 若启用了 mandatory roles、存在匿名/同名多 Host 身份、出向 role/PROXY/DEFINER 依赖、schema 可执行对象，或管理连接无权检查和清理上述状态，不得绕过错误继续启动。应由 DBA 解除外部关系或手工建号，并按相同项目审计最终身份和有效权限。任务不会擅自删除其他账号或外部依赖；不要把长期 root 凭据注入 Ares 应用容器。

### 旧版或未知 schema 授权主体处置

PR #6 及更早的 Compose 使用 `MYSQL_USER` 作为应用账号，MySQL 首次建卷时通常向它授予目标数据库的 `ALL PRIVILEGES`。W04 不再使用这个账号，但 MySQL 会继续保留它；新账号任务不会根据名称猜测并自动删除共享数据库用户，并会因发现该 schema grantee 而在任何写入前拒绝继续。

确认所有旧 Ares 实例已经停止、备份已经验证后，数据库管理员必须在运行当前账号任务和迁移前使用旧 `.env` 中的实际用户名和 host 执行以下操作：

1. 从 `mysql.user` 定位旧账号，并用 `SHOW GRANTS` 核对其授权和使用范围；
2. 如果账号仍被其他系统使用，先协调迁移，不能直接处理；
3. 对 Ares 专用旧账号执行 `REVOKE ALL PRIVILEGES, GRANT OPTION FROM '<旧用户名>'@'<host>'`，或在确认不再需要时执行 `DROP USER '<旧用户名>'@'<host>'`；
4. 再次 `SHOW GRANTS` 或确认账号不存在；确认目标 schema 只向计划使用的 runtime/migrator 身份授权后，才运行当前账号任务和迁移，并从部署 Secret/`.env` 删除废弃的 `MYSQL_USER`、`MYSQL_PASSWORD`。

不要把示例用户名或 host 直接用于生产；账号可能被定制，且同名用户在不同 host 下是不同 MySQL 身份。保留旧账号用于“快速回退”也不安全：当前 epoch 5 不允许 epoch 4 及更早二进制连接升级后的可写数据库，真正回退必须恢复迁移前备份。

## Schema ledger 与只读检查

`schema_migrations` 记录每条迁移的 epoch、版本、描述、checksum、开始/结束时间、dirty 状态、兼容区间和最近错误。`migrate status` 和 `serve` 会验证：

- ledger 是编译期迁移目录从 epoch 1 开始的连续前缀；
- 版本、epoch、checksum 和兼容区间与当前二进制一致；
- 不存在未完成的 dirty 迁移；
- 当前应用 epoch 位于数据库兼容区间内；
- Ares 专用 schema 的基础表/视图集合、每个受管列的完整定义、CHECK、索引语义、外键语义、字符集和排序规则符合对应 epoch 的完整 schema manifest；Ares 指向外部 schema 或外部 schema 子表反向引用 Ares 受管表/ledger 的外键均不允许；epoch 2 起还校验活动环境代码，并要求每条未删除 AppConfig 的环境都能精确对应一个未删除的环境目录项，后续 epoch 显式继承这些仍有效的数据约束。

当前应用 schema 为 epoch 5，兼容区间是 `[5,5]`，完整 manifest 管理 20 张表。迁移 `20260904_001_auth_rbac_audit` 新增 `auth_users`、`auth_identities`、`auth_sessions`、`auth_oidc_flows`、`auth_bootstrap_state`、`audit_events`，并为 `task_record` 与 `release_workflow_versions` 增加可空的稳定用户 ID 字段。迁移不会根据历史显示名猜测或回填用户身份。

未知版本、断档、checksum 被修改、数据约束或结构漂移都会 fail-closed。每个 epoch 保存独立完整快照，只校验最高已应用 epoch；历史快照不会累积套用，但仍有效的不变量必须由后续 verifier 显式继承。检查命令只读取数据库，不会补表、改列、清理 dirty 或修复差异，并在同一物理连接上完成全部多查询检查。MySQL 会按权限隐藏 trigger/event/routine 及外部入向外键元数据，因此低权限 `status` 的空结果不是权威不存在证明；Compose root 账号任务与 guarded 管理员检查承担完整元数据门禁，非 Compose 部署必须由 DBA 执行等价 preflight。

旧版只有 `version`、`applied_at` 两列的 ledger 由 `migrate up` 在锁内收养。只有版本 1～3 构成连续前缀、每行固定目录元数据可映射，且当前结构满足最高已应用 epoch 的完整后置契约时才会补齐元数据；不会把较早 epoch 快照套用到较新结构。`migrate status` 只报告“需要收养”，不会修改旧表。

## Docker Compose 新部署

默认依赖顺序为：

```text
auth-secrets（一次性生成并复用身份/配置密钥） -----------------------------┐
                                                                        │
mysql healthy                                                           │
    -> database-migrator-user（一次性创建/更新迁移账号并保持锁定，成功后退出 0）
        -> migrate（管理员连接守护唯一迁移会话，执行 migrate up，成功后退出 0）
            -> database-runtime-user（一次性创建/收紧运行时账号，成功后退出 0）
                --------------------------------------------------------> ares（serve，仅运行时账号）
                    -> web
```

新环境先修改所有示例密码，再启动：

```bash
cp .env.example .env
docker compose up -d --build --wait
docker compose ps -a
docker compose logs auth-secrets database-migrator-user database-runtime-user
docker compose logs migrate
```

预期 `auth-secrets`、`database-migrator-user`、`migrate`、`database-runtime-user` 均为 `Exited (0)`，`mysql`、`ares` 和 `web` 最终为 healthy。任一一次性任务失败时 `ares` 都不会启动；先查看对应任务日志，不要绕过依赖直接启动服务。

runtime 账号尚未创建或收紧前，可让一次性 `migrate` 容器使用其管理员检查 DSN 执行只读检查；该命令不解锁 migrator，也不写数据库：

```bash
docker compose run --rm --no-deps migrate migrate status
```

迁移和运行时账号任务都成功后，再使用运行时账号做最终只读复核：

```bash
docker compose run --rm --no-deps ares migrate status
```

空库的表结构只由 migrator 的显式 bootstrap 和版本化迁移创建。`serve` 不执行 Xorm Sync、`CREATE TABLE` 或 `ALTER TABLE`；兼容性检查通过后，它只以 DML 初始化缺失的语言规则和可选 Demo 业务数据。首次管理员使用的随机 Token 可由以下命令显式显示：

```bash
docker compose run --rm --no-deps \
  -e ARES_AUTH_SECRETS_PRINT_BOOTSTRAP=true auth-secrets
```

该命令只读取 `auth_secrets` volume，不修改 schema。管理员 Bootstrap 在数据库中只能成功一次；成功后即使再次读取相同 Token，也不能创建第二位管理员。

## 从 epoch 4 升级至 epoch 5

epoch 5 的兼容区间为 `[5,5]`。升级会新增六张身份/审计表，并扩展发布任务与工作流版本的稳定主体字段；不会根据旧记录中的显示名猜测用户，也不会改变历史快照。推荐顺序：

1. 排空发布任务，停止所有旧 `web` 和 `ares` 实例，创建并验证数据库备份。
2. 保存现有系统配置加密密钥；为新版本准备稳定、至少 32 字节的身份根密钥和一次性 Bootstrap Token。生产环境使用受控 Secret 文件，不把值写入仓库或日志。
3. 配置用户实际访问的精确 HTTPS `web.public_url`，并决定是否启用 OIDC。本地 HTTP 只允许 loopback 开发地址。
4. 运行 `database-migrator-user`、`migrate`、`database-runtime-user`，再用运行时连接执行 `migrate status`；必须看到 epoch 5 兼容且账号写权限与 20 表矩阵一致。
5. 启动新版本，通过一次性 Bootstrap 创建首位 `admin`。确认匿名业务 API 返回 `401`、已登录会话可读取数据、写请求需要 CSRF，再配置 OIDC、Jenkins 或 Kubernetes。
6. 完成后移除 Bootstrap Token 并关闭 Bootstrap；保持旧共享管理员 Token 兼容开关关闭。

不得在迁移完成后重新连接 epoch 4 二进制。需要回退时，停止写入并恢复 epoch 5 迁移前备份及匹配的旧应用版本；不能只降级镜像。

## 从 W04 前版本升级

epoch 4 是从“应用启动隐式 DDL”切换为“独立迁移任务”的历史停机边界，其兼容区间为 `[4,4]`；当前迁移链会继续执行到 epoch 5。不能在旧 Ares 实例仍连接数据库时执行升级。

推荐顺序：

1. 排空或明确处置仍在运行的发布任务，冻结外部写入。
2. 停止全部旧版 `web` 和 `ares` 实例。
3. 创建数据库备份，并在独立环境确认备份可读取或完成恢复演练。
4. 按“旧版或未知 schema 授权主体处置”枚举现有 grantee；确认不再被其他系统使用后，先撤销或删除旧 `MYSQL_USER` 等主体对目标 schema 的授权。未完成时当前账号任务会在任何写入前拒绝。
5. 配置独立的运行时/迁移账号和仅一次性迁移作业使用的管理员连接；已有 Compose volume 必须保留可用的 root Secret。
6. 构建或拉取目标版本镜像，确保 MySQL 已健康；可先通过 `migrate` 容器的管理员检查 DSN 运行只读 `migrate status`。
7. 运行迁移账号任务，让 migrator 在授予白名单权限后保持锁定；再执行一次 `migrate up`，由管理员连接短暂建立唯一迁移会话、立即重新锁号，并在结束后复核账号锁定且无会话。
8. 迁移成功后运行运行时账号任务，清理目标 runtime 身份自身的密码、直授权、角色、PROXY 和旧会话，再授予逐表 DML。
9. 使用运行时账号执行 `migrate status`，必须返回 `0`，然后才启动 `ares` 和 `web`。
10. 验证健康检查、应用读取和一次受控业务写入，并确认运行时账号不能修改 ledger 或执行 DDL。

目标版本包含 epoch 5，因此从 W04 前升级时还必须完成上一节的身份根密钥、精确公开源、首位管理员 Bootstrap 和 OIDC 规划；不能恢复匿名页面身份或把旧共享管理员 Token 当作长期权限边界。

epoch 1 的历史 NULL 字符串批处理支持带符号主键的完整范围，包括 `0`、负 INT 和最小 BIGINT；不要为了迁移手工改写这些主键。若数据契约依赖的表或列已缺失，检查会先报告结构漂移（退出码 `3`）而不是执行数据查询或修改 ledger。

Compose 示例：

```bash
docker compose stop web ares
docker compose build
docker compose run --rm --no-deps auth-secrets
docker compose up -d mysql
# 先由 DBA 审计并撤销旧版/未知主体对目标 schema 的授权
docker compose run --rm --no-deps migrate migrate status
docker compose run --rm --no-deps database-migrator-user
docker compose run --rm --no-deps migrate
docker compose run --rm --no-deps database-runtime-user
docker compose run --rm --no-deps ares migrate status
docker compose up -d --no-deps ares
docker compose up -d --no-deps web
docker compose ps -a
```

以上示例只展示迁移阶段，执行前必须先按下一节创建并验证备份，并由 DBA 完成旧授权主体审计/撤权。Compose 的 `migrate status` 使用只注入该一次性容器的管理员检查 DSN，但命令语义仍严格只读；`migrate up` 还会再次执行同等的 fail-closed schema 检查，并在迁移后用最终运行时账号复核。不要用 `|| true` 掩盖连接失败、未知授权主体或未知 schema 状态。

## 备份与恢复准备

Compose 环境可用以下命令创建逻辑备份：

```bash
docker compose exec -T mysql \
  sh -c 'export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysqldump --single-transaction --routines --triggers --user=root "$MYSQL_DATABASE"' \
  > ares-backup.sql
```

`--single-transaction` 为 InnoDB 表提供一致性快照；密码通过容器进程环境传递，不放在 `mysqldump` 命令行参数中。生产环境应优先使用平台 Secret 文件或专用备份身份，并限制进程环境和备份文件的读取权限。

备份文件至少应核对以下内容：

- 命令退出码为 `0`，文件非空且包含 `schema_migrations` 与关键业务表；
- 记录备份对应的应用版本、schema epoch、时间和目标数据库；
- 在隔离数据库中完成导入；当前 epoch 5 备份用 W02 版本的 `migrate status` 验证，较早版本使用与备份 epoch 对应的二进制；W04 前版本没有该子命令时应核对表、旧 ledger 和哨兵数据，并实际启动创建该备份的精确旧版本完成健康检查与关键读写；
- 备份按生产数据级别加密、限制访问并设置保留期。

逻辑备份导入不会自动删除目标库中已有的新结构，因此不能把旧备份直接覆盖导入已经完成 epoch 5 的数据库。回退恢复应停止所有 Ares 实例，把故障库保留为只读证据，在全新的空数据库或已清空且确认无须保留的目标中恢复，再启动与该备份 epoch 兼容的应用。生产恢复还应提供备份时匹配的系统配置加密密钥；会话根密钥无法恢复时，已有会话会失效，但不能因此更改数据库身份或审计记录。

## Dirty 迁移恢复

MySQL DDL 可能隐式提交。migrator 会在第一条迁移动作前写入 `dirty=1`；Up 或后置校验失败时保留 dirty，并尽力写入脱敏后的最近错误，普通 `migrate up` 与 `serve` 都会拒绝继续。连接或 ledger 写入自身失败时 `last_error` 可能为空，应同时保留 migrator 日志和故障现场。

恢复流程：

1. 保持应用停止，运行 `migrate status`，记录唯一的 dirty 版本、checksum 和 manifest 差异。
2. 保留升级前备份，并额外备份当前故障现场。
3. 根据日志排查权限、磁盘空间、锁等待或非法历史数据等外部原因。
4. 确认当前二进制、迁移版本与 dirty checksum 未变化。
5. 修复根因后，用输出中的精确版本显式恢复：

```bash
./ares migrate up --resume-dirty 20260904_001_auth_rbac_audit
```

Compose 环境使用：

```bash
docker compose run --rm --no-deps migrate \
  migrate up --resume-dirty 20260904_001_auth_rbac_audit
```

6. 恢复命令先在任何新写入前校验该迁移允许的精确中间状态：上一干净 epoch 的完整契约，加上按语句顺序枚举的目标迁移边界；已有目标对象会校验完整定义。通过后才从迁移开头执行可重入操作，且不会重写首次 `started_at`；最终后置校验成功后清除 dirty 并继续后续迁移。完成后再次运行 `migrate status`，只有返回 `0` 才能启动应用。

版本不精确、数据库没有 dirty、checksum 不一致、出现多个异常状态或无法证明后置条件时，resume 会返回 `3`。系统不提供 `--force`、手工标记完成、自动清 dirty 或 `down` 命令。不要直接 `UPDATE schema_migrations`；无法安全前滚时应恢复迁移前备份。

## 回滚原则

- 首选修复新版本并前滚，不对已经发布的旧迁移做原地修改。
- 只有数据库最新兼容区间包含目标旧应用 epoch 时，才允许只回退二进制。
- epoch 5 不兼容 epoch 4；完成当前迁移后，不能让只支持 epoch 4 的旧镜像重新连接这个可写数据库。epoch 4 与 epoch 3 的历史不兼容边界同样保留。
- 必须回退到 W04 前版本时，应冻结写入、停止所有实例、恢复迁移前数据库备份，再部署与备份匹配的旧应用。
- 不支持通用 down migration。未来需要删除列、表或数据的 contract 迁移必须单独设计、评审和安排维护窗口。

## 故障排查

### `migrate` 退出后 `ares` 没有启动

运行 `docker compose ps -a` 和 `docker compose logs migrate`。只有 migrator 退出 `0`，Compose 才会启动 `ares`。根据退出码区分 schema 状态 `3` 与连接/权限/锁故障 `5`。

### 提示缺少 `ARES_DB_MIGRATION_CONN_STR`

`migrate up` 只接受独立迁移 DSN。检查迁移 Job 的 Secret 或 `MYSQL_MIGRATION_*` 环境变量，不要把运行时 DSN 复制为兜底。

### 生产部署未配置 `ARES_DB_MIGRATION_ADMIN_CONN_STR`

未配置管理员连接时，二进制仍可用 `ARES_DB_MIGRATION_CONN_STR` 执行普通迁移，但无法自行强制 migrator 的锁定、一次性凭据和会话清理；这只适合已经由外部编排或 DBA 实现等价账号生命周期的部署。默认 Compose 已只向 `migrate` 注入管理员连接。生产部署不要把管理员 DSN 注入 `ares`，也不要为绕过守护流程而长期解锁迁移账号。

### 修改 `.env` 后数据库认证失败

两个账号任务可以同步运行时和迁移账号，但必须先用数据库当前实际的 root 密码连接。旧 volume 升级时检查 `MYSQL_ROOT_PASSWORD` 是否仍与建卷时一致；root 密码轮换应由数据库管理员先在 MySQL 内完成，再同步 Secret。不要通过删除 volume 解决生产凭据问题。

### 账号初始化因安全门禁失败

不要跳过 `database-migrator-user` / `database-runtime-user` 或直接启动后续容器。根据日志由 DBA 检查 mandatory roles、匿名账号、同名 Host 身份、出向 role/PROXY、DEFINER、schema 内 trigger/event/routine/view、现有目标账号会话，以及 runtime/migrator 之外的目标 schema grantee。旧版 `MYSQL_USER` 命中最后一项是预期的升级保护：先确认旧实例停止并撤权，再重跑任务。账号任务可能在中途失败后保持目标账号锁定，这是 fail-closed 行为；处理根因后按原顺序重跑即可。共享 MySQL 若不能授权 root 等价检查，应由 DBA 提供经过同一矩阵审计的账号和短生命周期迁移 Job。

### 提示迁移账号未锁定或仍有会话

不要直接解锁、复用或强制终止正在执行的迁移。先确认是否还有合法的 `migrate` 作业；若没有，停止发布并由 DBA 检查 `mysql.user.account_locked` 与 `information_schema.PROCESSLIST`，保留日志后终止残留会话、锁定账号，再从账号任务开始重跑。`migrate` 无论成功或失败都应最终锁号并清空会话；无法完成最终清理属于操作性故障，不得启动 runtime。

### 等待 migration lock 超时

确认是否已有 migrator 正在工作。锁超时返回 `5`，且在取得锁之前不会新增 dirty 行。不要并行启动更多迁移任务；等待持锁任务结束，或在确认原连接已经终止后重试。

如果部署经过 Router、ProxySQL、DNS 轮询、读写分离或多写集群，还必须确认全部账号与迁移作业实际固定到同一 single-writer 实例。不同 MySQL 实例可以同时取得同名 `GET_LOCK`；这种拓扑不能依赖 Ares 内置锁，必须先由外部编排提供跨节点互斥。

### `schema_migrations` 不存在但数据库已有表

这不是合法空库，migrator 不会猜测来源或覆盖现有结构。确认是否连错数据库；如果它确实来自受支持的 W04 前版本，应使用包含合法两列 ledger 的完整备份。其他来源必须先离线评估，不能强制 bootstrap。

### checksum、未知版本或断档

停止发布并核对二进制版本、数据库来源和变更记录。这类状态不会自动收养，即使数据库行声明兼容也会被拒绝。恢复正确备份或通过新增、经过评审的前向迁移处理，不要修改旧 checksum。

### schema manifest 漂移

`status` 会列出规范化差异，但不会修复。先确认是否有人绕过 migrator 手工改表，保留 `SHOW CREATE TABLE` 和变更审计证据；修复必须通过新的版本化迁移，或恢复到已验证备份。

若差异是外部 schema 子表反向引用 Ares 受管表或 `schema_migrations`，应由该外部 schema 的 owner 删除外键后再重跑。低权限 runtime/migrator 可能看不到这类元数据，不能以普通 `status` 的空结果代替 guarded 管理员或 DBA 权威检查。

### 数据库版本不受支持

Ares W02 仅支持 MySQL 8.4.x；账号任务、`migrate status`、`migrate up` 和 `serve` 都会拒绝 MySQL 8.0/创新版本及 MariaDB。不要通过修改版本字符串检查强行运行，应先把备份恢复到经过演练的 MySQL 8.4 环境，再执行迁移。

### 运行时账号执行 DDL 被拒绝

这是预期的最小权限边界。所有结构变更都应进入新 migration，由一次性迁移 Job 使用迁移账号执行；不要给 `ares serve` 增加 DDL 权限。
