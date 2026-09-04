# Ares 部署指南

## 运行拓扑

默认 `compose.yaml` 编排六个服务，其中两个账号任务和 `migrate` 是成功后退出的一次性任务：

```text
mysql healthy
    │
    ▼
database-migrator-user / 创建迁移账号并保持锁定（root 管理连接，退出 0）
    │
    ▼
migrate / 管理员连接守护唯一迁移会话并执行 ares migrate up（退出 0）
    │
    ▼
database-runtime-user / 创建并收紧运行时账号（root 管理连接，退出 0）
    │
    ▼
ares / ares serve（运行时账号） ◄── web / Nginx ◄── 浏览器 :8080
    ├─────────► Jenkins（可选外部集成）
    └─────────► Kubernetes（可选外部集成）
```

只有 Web 端口对外开放。后端调试端口默认只绑定 `127.0.0.1:8081`，MySQL 不暴露宿主端口。Nginx 为 Vue Router 提供 SPA fallback，并针对 Jenkins SSE 日志关闭代理缓冲、保留 `Last-Event-ID` 和长连接。

## 快速启动

```bash
cp .env.example .env
# 首次启动前先修改 .env 中的 MySQL 密码
docker compose up -d --build --wait
docker compose ps -a
```

如果当前 Compose 版本不支持 `--wait`，可运行 `docker compose up -d --build`，再用 `docker compose ps -a` 等待 `database-migrator-user`、`migrate`、`database-runtime-user` 均为 `Exited (0)`，并确认 `mysql`、`ares`、`web` 均为 healthy。账号初始化、迁移或运行时收权失败都会阻止后端启动，可用 `docker compose logs database-migrator-user migrate database-runtime-user` 查看原因。

验证：

```bash
curl --fail http://localhost:8080/inside/checkup
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
curl --fail \
  -H 'Content-Type: application/json' \
  -d '{"page_num":1,"page_size":10}' \
  http://localhost:8080/api/v1/apps/query
```

访问 `http://localhost:8080`，在登录页输入任意姓名，然后进入“应用管理”即可看到 Demo 数据。

## 环境变量

| 变量                                            | 默认值                            | 说明                                                          |
| ----------------------------------------------- | --------------------------------- | ------------------------------------------------------------- |
| `ARES_BIND_ADDRESS`                             | `127.0.0.1`                       | Web 监听地址；确认已有网关保护后才改为 `0.0.0.0`              |
| `ARES_HTTP_PORT`                                | `8080`                            | Web 对外端口                                                  |
| `ARES_API_PORT`                                 | `8081`                            | 仅绑定本机的后端调试端口                                      |
| `MYSQL_DATABASE`                                | `ares`                            | 数据库名                                                      |
| `MYSQL_RUNTIME_USER`                            | `ares_runtime`                    | 仅持有业务 DML 权限的运行时数据库用户                         |
| `MYSQL_RUNTIME_PASSWORD`                        | 本地示例值                        | 运行时数据库密码                                              |
| `MYSQL_MIGRATION_USER`                          | `ares_migrator`                   | 执行显式 schema migration 的数据库用户                        |
| `MYSQL_MIGRATION_PASSWORD`                      | 本地示例值                        | 迁移数据库密码                                                |
| `MYSQL_ROOT_PASSWORD`                           | `ares-root-password`              | MySQL root 密码                                               |
| `ARES_DB_SCHEMA_MIGRATION_TIMEOUT`              | `2m`                              | 单次版本化 schema 迁移操作的超时；大库可按 DDL / 扫描耗时调高 |
| `ARES_DB_MIGRATION_LOCK_TIMEOUT`                | `30s`                             | migrator 等待数据库级锁的最长时间                             |
| `ARES_DATABASE_ACCOUNT_CONNECT_TIMEOUT_SECONDS` | `5`                               | 数据库账号任务单次 MySQL 连接超时（1～30 秒）                 |
| `ARES_DATABASE_ACCOUNT_LOCK_TIMEOUT_SECONDS`    | `30`                              | 数据库账号任务等待账号级互斥锁的时限（1～300 秒）             |
| `ARES_DATABASE_ACCOUNT_INIT_TIMEOUT_SECONDS`    | `60`                              | 每个数据库账号任务总时限（1～300 秒）                         |
| `ARES_DEMO_DATA_ENABLED`                        | `true`                            | 空库是否写入 Demo 数据                                        |
| `ARES_SETTINGS_ADMIN_TOKEN`                     | `ares-local-admin-token`          | Web 系统配置接口的管理员令牌；共享环境必须修改                |
| `ARES_SETTINGS_ENCRYPTION_KEY`                  | 本地示例值                        | 加密 Jenkins Token 与 kubeconfig；共享环境必须替换且妥善备份  |
| `GOPROXY`                                       | `https://proxy.golang.org,direct` | 构建后端镜像时使用的 Go 模块代理                              |

Compose 会根据两组 MySQL 变量生成运行时 `ARES_DB_CONN_STR`、迁移 `ARES_DB_MIGRATION_CONN_STR`，并根据 root Secret 为一次性 `migrate` 生成 `ARES_DB_MIGRATION_ADMIN_CONN_STR`。`ares` 只收到运行时连接；`migrate` 同时收到迁移连接和管理员连接，且其 `ARES_DB_CONN_STR` 是仅用于严格只读 `migrate status` 的管理员检查连接，以便 runtime 尚未配置时也能审计 schema。脱离 Compose 时，`serve` 和常规只读 `migrate status` 使用 `ARES_DB_CONN_STR`，生产 `migrate up` 应同时使用迁移连接与管理员连接；迁移连接缺失时不会回退到运行时连接。guarded 管理员身份必须与 DSN 用户精确一致，并直接持有全局 `PROCESS`、`CREATE USER`、`SELECT`、`TRIGGER`、`EVENT`、`SHOW VIEW`，以及 `CONNECTION_ADMIN` 或 `SUPER`；其 `mysql.user.User_attributes.$.Restrictions` 必须为空，经角色间接获得或缺少这些能力都会在任何账号、schema 或 ledger 修改前失败。管理员连接缺失时二进制可以执行普通迁移，但无法自行强制账号锁定、单会话生命周期和权威元数据检查，只适用于外部编排或 DBA 已实现等价守护的场景。YAML 配置严格要求单文档和已知字段，拼错 `migration_admin_conn_str` 等 key 会直接失败，不会被环境变量覆盖掩盖。其他可用配置包括 `ARES_WEB_ADDRESS`、`ARES_LOG_LEVEL`、`ARES_LOG_ACCESS_FILE` 和 `ARES_LOG_RUNTIME_FILE`。

请勿把真实密码或 Token 提交到仓库。默认密码仅用于实现开箱体验；部署到共享环境前必须在未提交的 `.env` 中替换，并且应在首次 `docker compose up` 之前完成。

Compose 不依赖 MySQL 只在空数据目录运行一次的 initdb 机制。MySQL 健康后，`database-migrator-user` 先使用 root 管理连接安全收敛迁移账号并保持锁定；账号任务的 named lock、全部特权预检和账号修改始终复用同一条禁用自动重连的物理连接。`migrate` 的管理员连接持有相同迁移账号锁，再为本次执行设置随机一次性密码、短暂解锁并建立唯一迁移会话，随后立即重新锁号、轮换掉一次性密码并清理其他会话。watchdog 会持续验证锁 ownership；退出路径会关闭唯一连接并复核 migrator 仍锁定且没有残留会话。迁移完成后，`database-runtime-user` 同时按全局顺序持有迁移账号锁和运行时账号锁，再收敛运行时账号，授予全库只读和 14 张业务表的逐表 DML，确保它不能修改 `schema_migrations`。持锁连接失效时，旧任务只有非阻塞拿齐原锁才能执行 fail-closed 收敛，不会排队越过后续 owner。

MySQL named lock 只在当前服务端实例内有效。全部账号任务、root/admin/migrator 连接和并发迁移 Job 必须固定到同一个稳定的 MySQL 8.4 single-writer 端点；不能通过 Router、ProxySQL、DNS 轮询、读写分离或 active-active 把它们分流到多个 writer。需要多写拓扑时必须由外部编排提供跨节点分布式互斥。HA 切换导致连接或 `server_uuid` 改变会使本次作业失败，核对账号与 dirty 状态后从账号任务重跑。

账号任务要求 MySQL 8.4.x，并在任何写入前拒绝 mandatory roles、匿名账号、同名非 `%` Host、目标身份的出向 role/PROXY/DEFINER、目标身份在其他 schema 或全局的权限、Ares schema 中的 trigger/event/routine/view，以及 runtime/migrator 之外仍持有目标 schema 权限的主体；guarded 管理员还会权威拒绝外部 schema 子表反向引用 Ares 受管表或 ledger 的外键。随后才锁号、轮换并丢弃旧双密码、清除入向角色/PROXY 和直授权、终止旧会话并授予白名单权限。数据库级授权还会根据 `@@GLOBAL.partial_revokes` 转义 `\\`、`%`、`_` 等 grant-pattern 元字符，避免 `ares_prod` 的授权意外覆盖 `aresXprod`；旧的不安全 pattern 会先被拒绝并要求 DBA 撤权。runtime 最终回连验证实际身份与有效角色；运行时任务还会先证明 migrator 已锁定且无会话。任何检查或清理失败都会阻止应用启动，不能绕过。

这意味着 PR #6 等旧 volume 不能在旧 `MYSQL_USER` 仍持有数据库级 `ALL PRIVILEGES` 时直接升级。两个账号任务只管理 `MYSQL_MIGRATION_USER` 与 `MYSQL_RUNTIME_USER`，不会猜测、修改或删除旧主体；必须先停止全部旧实例并验证备份，再由 DBA 按[数据库迁移与恢复手册](database-migrations.md)审计、撤销或删除旧账号对 Ares schema 的授权，之后才能启动 W04 链路。`.env` 中的 `MYSQL_ROOT_PASSWORD` 仍须匹配该 volume 内的实际 root 密码；只修改环境变量不会改变 MySQL 内的 root 密码。共享 MySQL 若不能满足特权门禁或不允许一次性 root 任务，应由 DBA 按相同身份解析、继承关系、旧会话和权限矩阵建号，并在生产编排中以受控的等价 Job 替换两个账号任务。不要通过删除生产 volume 解决凭据问题。

账号脚本在 `NO_BACKSLASH_ESCAPES` 模式下把正确转义的密码直接交给 `CREATE USER` / `ALTER USER`，依赖并验证 MySQL 8.4 `general_log` 将密码重写为 `<secret>`；脚本不会通过密码变量、可逆十六进制值或动态 `PREPARE` 中转。若托管平台或审计代理改变日志行为，必须先证明日志仍不含明文或可逆密码表示。

## 数据库与 Demo 初始化

Ares migrator 是专用 schema owner，当前仅支持 MySQL 8.4.x。空库由显式 bootstrap 创建 epoch 1 的固定 10 表基线，再按 epoch 顺序扩展到 epoch 4 的 14 表；bootstrap 中断只在已有对象是无业务数据、完整定义匹配且按固定顺序形成连续前缀时恢复。已有表只由 migration 修改。`ares serve` 仅做只读兼容性检查，不执行 Xorm 结构同步或其他 DDL。epoch 2 起的数据契约还要求每条未删除 AppConfig 的环境都对应未删除的 `env_configs` 目录项；缺失或软删除引用会 fail-closed，不会自动猜测。当前受管业务表包括：

- `apps`
- `app_configs`
- `app_config_domains`
- `task_record`
- `task_record_images`
- `pipelines`
- `pipelines_job_combination`
- `env_configs`
- `dev_language_rules`
- `integration_settings`
- `release_workflows`
- `release_workflow_versions`
- `app_config_workflows`
- `task_step_records`

应用 ID 自增起点会设为 `10000`，与 API 校验范围一致。四种开发语言规则只补缺失项，不覆盖已有规则。

当 `ARES_DEMO_DATA_ENABLED=true` 且应用、应用配置、发布任务和工作流等业务表全部为空时，一个事务会写入：

- `demo-api`、`demo-web`、`demo-worker`
- 每个应用的 `dev/test/moni/preview` 配置，其中 `preview` 用于证明环境不是代码枚举
- 示例域名、动态环境目录和每个 AppConfig 独立绑定的两步 Noop 流程
- 成功、失败和带告警的终态发布记录及步骤快照

Demo 任务不会使用运行中状态，因此在 Jenkins 关闭时不会触发后台轮询。重启容器不会重复写入。仓库中的历史 SQL 或 ORM entity 都不是 schema 真相源；权威入口是版本化 migration 和对应的 schema manifest。

迁移命令、退出码、ledger、dirty 恢复和账号权限详见[数据库迁移与恢复手册](database-migrations.md)。

## 接入 Jenkins 与 Kubernetes

启动 Ares 后进入“系统设置 → 系统配置”，输入 `.env` 中的 `ARES_SETTINGS_ADMIN_TOKEN`：

- 发布环境：创建任意合法环境代码，维护名称、排序和启停状态。停用只阻止新配置和新发布，不删除历史。
- Jenkins：填写服务地址、用户名、API Token 与请求超时后启用。
- Kubernetes：从已启用的动态环境目录选择环境，添加集群并粘贴自包含的 kubeconfig 后启用；命令型认证插件和容器内文件引用会被拒绝。
- 发布流程：在应用的环境配置页输入同一管理员令牌，为每个 AppConfig 组合并发布独立的版本化步骤流程。

公开环境目录接口会同时返回启用与停用项，便于历史页面保留正确标签；发布页仅提供启用项，后端在创建应用配置和发布任务时会再次校验启用状态。流程读取与写入都要求 `X-Ares-Admin-Token`，任务步骤接口不会返回执行器私有配置或外部引用。

敏感字段不会通过查询接口回显，数据库中只保存使用 `ARES_SETTINGS_ENCRYPTION_KEY` 加密后的密文。修改 Jenkins 地址或用户名时必须重新输入 Token，避免把旧凭据发送到另一个服务端点。

保存启用状态前，Ares 会先验证外部服务连接；失败时返回错误并保留原有可用配置。容器重启时若外部服务暂时不可达，Ares 仍会继续启动，错误会显示在系统配置页。未启用 Jenkins 时，仅 Jenkins 节点/日志接口和包含 `jenkins.job@v1` 的流程不可运行，Noop 或其他不依赖 Jenkins 的流程仍可发布；未启用 Kubernetes 时，集群查询接口返回 HTTP 503，其他功能正常可用。

## 健康检查与日志

- `/health/live`：进程存活，不检查外部依赖。
- `/health/ready`：在 1 秒超时内检查 MySQL。
- `/inside/checkup`：Nginx/前端健康检查。

Jenkins 和 Kubernetes 不参与基础 Compose readiness，也不会成为 Ares 的启动前置条件。

后端访问日志输出到 stdout，运行日志输出到 stderr，可直接查看：

```bash
docker compose logs -f ares
```

## 升级、备份与重置

新版本之间升级并保留数据库时，应先停止旧应用写入、备份，再显式执行迁移：

```bash
git pull --ff-only
docker compose stop web ares
docker compose exec -T mysql \
  sh -c 'export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysqldump --single-transaction --routines --triggers --user=root "$MYSQL_DATABASE"' \
  > ares-backup.sql
docker compose build
docker compose up -d mysql
# 先由 DBA 审计并撤销旧版/未知主体对 Ares schema 的授权
docker compose run --rm --no-deps migrate migrate status
docker compose run --rm --no-deps database-migrator-user
docker compose run --rm --no-deps migrate
docker compose run --rm --no-deps database-runtime-user
docker compose run --rm --no-deps ares migrate status
docker compose up -d --no-deps ares web
```

备份：

```bash
docker compose exec -T mysql \
  sh -c 'export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; exec mysqldump --single-transaction --routines --triggers --user=root "$MYSQL_DATABASE"' \
  > ares-backup.sql
```

`--single-transaction` 为 InnoDB 表提供一致性快照；密码通过容器进程环境传递，不出现在 `mysqldump` 的命令行参数中。生产环境应优先使用平台 Secret 文件或专用备份身份，并限制进程环境和备份文件的读取权限。

恢复前请先在独立环境验证备份。逻辑备份不能直接覆盖导入已经迁移的新 schema；需要回退时应恢复到新的空数据库，并同时使用与备份 epoch 兼容的应用版本。

停止服务但保留数据：

```bash
docker compose down
```

彻底删除数据卷并重新生成 Demo 数据：

```bash
docker compose down -v
docker compose up -d --build --wait
```

`down -v` 会不可恢复地删除 Compose 管理的 MySQL 数据，请仅在确认无需保留时执行。

### 从旧镜像迁移

新镜像不再把仓库的环境配置或集群凭据打包进镜像。数据库运行连接通过 `ARES_DB_CONN_STR` 注入，一次性 migrator 使用独立的 `ARES_DB_MIGRATION_CONN_STR` 和只在该作业内可见的 `ARES_DB_MIGRATION_ADMIN_CONN_STR`；Jenkins 与 Kubernetes 配置改为启动后在 Web 中保存。升级前请备份数据库，并准备固定的 `ARES_SETTINGS_ENCRYPTION_KEY`；密钥遗失或变更后，已保存的敏感配置无法解密，需要重新录入。

迁移 `20260902_001_cleanup_legacy_null_strings` 会治理历史字符串 `"NULL"` 并调整相关列约束，批处理覆盖 `0`、负数及最小带符号主键；`20260903_001_pluggable_cicd` 会扩展动态环境与工作流表，并把旧的 CI/CD Job 组合幂等转换为 AppConfig 工作流；`20260903_002_cicd_runtime_hardening` 会增加 Worker 调度索引与 Jenkins 实例地址字段；`20260903_003_versioned_migrations` 会验证当前 schema manifest 并完成版本化迁移边界收口。升级前须确认活动环境代码合法（末尾 LF、CR、CRLF 等控制字符不会被 MySQL 的行尾锚点误接纳）、没有规范化后的 `(app_id, env)` 重复项，且每条未删除 AppConfig 的环境都有未删除目录项；同时必须停止所有旧版 Ares 写入实例，并在迁移前撤销旧版/未知主体对目标 schema 的授权及外部入向外键。迁移结果记录在 `schema_migrations`。NULL 迁移细节见 [NULL 字符串治理方案](../plans/null-string-cleanup.md)，流水线迁移与回退边界见 [可插拔 CI/CD 实施路线](../plans/pluggable-cicd-roadmap.md)。

版本化迁移在专用连接上持有当前 MySQL 实例内的数据库级 named lock，单次操作超时可通过 `ARES_DB_SCHEMA_MIGRATION_TIMEOUT` 调高，等待锁的时间由 `ARES_DB_MIGRATION_LOCK_TIMEOUT` 单独控制。运行时只读检查和业务请求不会获得迁移账号权限。升级超大旧表时应先在副本验证，并按维护窗口调整迁移 DSN 的 I/O 超时；正式执行仍必须使用稳定 single-writer 端点。

迁移完成后不要把旧 `main` 镜像接回可写数据库：旧版 Xorm 同步可能删除本版本增加的唯一索引和调度索引，而 `schema_migrations` 不会随之回退，随后既可能写入重复数据，也可能使新版无法重建唯一约束。推荐以前向修复处理应用问题；必须回退时，应冻结写入，将数据库恢复到升级前备份，再部署与该备份匹配的旧应用。单独降级二进制/镜像不是受支持的回滚方式。完整停机升级、dirty 恢复和故障排查步骤见[数据库迁移与恢复手册](database-migrations.md)。

Jenkins 外部引用绑定到接收任务时的服务地址。仍有已绑定的 v1 `packaging/deploying`、会自动部署的 `packaged` 任务，或 v2 `running` Jenkins 步骤时，系统设置会拒绝更换 Jenkins 地址或停用集成；应先让这些任务结束或由管理员明确处置。旧结构没有保存任务所属的 Jenkins 地址，因此迁移不会根据当前设置猜测并自动回填。升级前创建且尚未结束的 v1 任务无法证明外部实例归属，旧轮询器会在任何 Jenkins 网络请求前把它们确定性终止为失败；其历史日志查询也会明确拒绝，避免误读或误触发新实例上的同名 Job/Build。生产升级必须优先排空旧任务；若无法排空，应预期这些任务需要在升级后人工重新发布。仅轮换同一地址的凭据不会触发换址限制。

## 上线前检查

- 首次启动前修改全部默认密码，关闭 Demo 数据。
- 当前前后端没有真实认证/RBAC；在补齐鉴权前，仅部署到受信网络或在网关层实施访问控制。
- 使用 TLS，并限制 `ARES_API_PORT` 的本机绑定。
- 使用外部托管 MySQL 时建立备份、恢复演练和监控。
- 使用真实 Jenkins Job、镜像仓库和 kubeconfig 完成二级联调。
- W06 多副本 Worker 与租约完成前，整个 Ares Worker 必须保持单副本运行。旧 v1 Jenkins 兼容轮询器没有跨实例领取或 leader election；v2 步骤虽然已有 pending 步骤认领 CAS，但多个实例仍可能同时 Reconcile 同一个 running 步骤，不能据此认为已经支持多副本。
