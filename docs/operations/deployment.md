# Ares 部署指南

## 运行拓扑

默认 `compose.yaml` 编排七个服务；`auth-secrets`、两个数据库账号任务和 `migrate` 是成功后退出的一次性任务：

```text
auth-secrets / 在私有 volume 中生成身份与配置加密密钥（退出 0） ───────┐
                                                                    │
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
    └───────────────────────────────────────────────────────────────┤
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

如果当前 Compose 版本不支持 `--wait`，可运行 `docker compose up -d --build`，再用 `docker compose ps -a` 等待 `auth-secrets`、`database-migrator-user`、`migrate`、`database-runtime-user` 均为 `Exited (0)`，并确认 `mysql`、`ares`、`web` 均为 healthy。密钥初始化、账号初始化、迁移或运行时收权失败都会阻止后端启动，可用 `docker compose logs auth-secrets database-migrator-user migrate database-runtime-user` 查看原因。

验证：

```bash
curl --fail http://localhost:8080/inside/checkup
curl --fail http://localhost:8080/health/live
curl --fail http://localhost:8080/health/ready
test "$(curl --silent --output /dev/null --write-out '%{http_code}' \
  http://localhost:8080/swagger/index.html)" = 401
```

健康检查保持公开；Swagger 和业务 API 在匿名状态下返回 `401` 是预期结果。首次部署时，显式读取 Compose 自动生成的 Bootstrap Token：

```bash
docker compose run --rm --no-deps \
  -e ARES_AUTH_SECRETS_PRINT_BOOTSTRAP=true auth-secrets
```

访问 `http://localhost:8080`，在“首次部署管理员”区域粘贴该 Token，设置用户名、显示名和至少 12 字节的密码。只有第一个成功请求能完成 Bootstrap；之后应停止传播该 Token，并使用本地管理员账号登录。登录后可访问 Swagger，并在“应用管理”查看 Demo 数据；首次评估还应确认 12 份应用环境配置都能读取各自的两步 Noop 工作流，重启 `ares` 与 `web` 后仍保持一致。OIDC 用户即使先登录并自动创建为 `viewer`，也不会消费或阻止这个独立的一次性 Bootstrap。

## 环境变量

| 变量                                            | 默认值                            | 说明                                                          |
| ----------------------------------------------- | --------------------------------- | ------------------------------------------------------------- |
| `ARES_BIND_ADDRESS`                             | `127.0.0.1`                       | Web 监听地址；确认网络边界后才改为 `0.0.0.0`                  |
| `ARES_HTTP_PORT`                                | `8080`                            | Web 对外端口；Compose 据此形成精确公开源                      |
| `ARES_API_PORT`                                 | `8081`                            | 仅绑定本机的后端调试端口                                      |
| `MYSQL_DATABASE`                                | `ares`                            | 数据库名                                                      |
| `MYSQL_RUNTIME_USER`                            | `ares_runtime`                    | 按表授予最小业务 DML 权限的运行时数据库用户                   |
| `MYSQL_RUNTIME_PASSWORD`                        | 本地示例值                        | 运行时数据库密码                                              |
| `MYSQL_MIGRATION_USER`                          | `ares_migrator`                   | 执行显式 schema migration 的数据库用户                        |
| `MYSQL_MIGRATION_PASSWORD`                      | 本地示例值                        | 迁移数据库密码                                                |
| `MYSQL_ROOT_PASSWORD`                           | `ares-root-password`              | MySQL root 密码                                               |
| `ARES_DB_SCHEMA_MIGRATION_TIMEOUT`              | `2m`                              | 单次版本化 schema 迁移操作的超时；大库可按 DDL / 扫描耗时调高 |
| `ARES_DB_MIGRATION_LOCK_TIMEOUT`                | `30s`                             | migrator 等待数据库级锁的最长时间                             |
| `ARES_DATABASE_ACCOUNT_CONNECT_TIMEOUT_SECONDS` | `5`                               | 数据库账号任务单次 MySQL 连接超时（1～30 秒）                 |
| `ARES_DATABASE_ACCOUNT_LOCK_TIMEOUT_SECONDS`    | `30`                              | 数据库账号任务等待账号级互斥锁的时限（1～300 秒）             |
| `ARES_DATABASE_ACCOUNT_INIT_TIMEOUT_SECONDS`    | `60`                              | 每个数据库账号任务总时限（1～300 秒）                         |
| `ARES_DEMO_DATA_ENABLED`                        | `true`                            | 空库是否写入 Demo 业务数据                                    |
| `ARES_WEB_PUBLIC_URL`                           | Compose 自动生成                  | 浏览器实际访问的精确源；生产必须为 HTTPS                      |
| `ARES_WEB_MAX_JSON_BODY_BYTES`                  | `1048576`                         | 普通 JSON 请求体硬上限；单个敏感接口可以使用更小上限          |
| `ARES_WEB_SSE_REAUTH_INTERVAL`                  | `30s`                             | SSE 会话复验间隔；最多 60 秒且不能长于会话空闲期限            |
| `ARES_AUTH_ROOT_KEY_FILE`                       | 私有 volume 内文件                | 会话、CSRF 与 OIDC 流加密所需的稳定根密钥                     |
| `ARES_SETTINGS_ENCRYPTION_KEY_FILE`             | 私有 volume 内文件                | Jenkins Token、kubeconfig 等系统配置的稳定加密密钥            |
| `ARES_AUTH_LOCAL_LOGIN_ENABLED`                 | Compose 为 `true`                 | 是否允许 bootstrap 本地管理员登录                             |
| `ARES_AUTH_BOOTSTRAP_ENABLED`                   | Compose 为 `true`                 | 是否开放尚未完成的一次性 Bootstrap                            |
| `ARES_AUTH_BOOTSTRAP_TOKEN_FILE`                | 私有 volume 内文件                | 至少 32 字节的一次性 Bootstrap Token                          |
| `ARES_AUTH_OIDC_ENABLED`                        | Compose 为 `false`                | 是否启用 OIDC Authorization Code + PKCE                       |
| `ARES_AUTH_OIDC_CLIENT_SECRET_FILE`             | 无                                | OIDC Client Secret 文件；生产不使用明文环境变量               |
| `ARES_AUTH_LEGACY_ADMIN_TOKEN_ENABLED`          | `false`                           | 旧共享管理员 Token 兼容开关；新部署保持关闭                   |
| `GOPROXY`                                       | `https://proxy.golang.org,direct` | 构建后端镜像时使用的 Go 模块代理                              |

Compose 会根据两组 MySQL 变量生成运行时 `ARES_DB_CONN_STR`、迁移 `ARES_DB_MIGRATION_CONN_STR`，并根据 root Secret 为一次性 `migrate` 生成 `ARES_DB_MIGRATION_ADMIN_CONN_STR`。`ares` 只收到运行时连接；`migrate` 同时收到迁移连接和管理员连接，且其 `ARES_DB_CONN_STR` 是仅用于严格只读 `migrate status` 的管理员检查连接，以便 runtime 尚未配置时也能审计 schema。脱离 Compose 时，`serve` 和常规只读 `migrate status` 使用 `ARES_DB_CONN_STR`，生产 `migrate up` 应同时使用迁移连接与管理员连接；迁移连接缺失时不会回退到运行时连接。guarded 管理员身份必须与 DSN 用户精确一致，并直接持有全局 `PROCESS`、`CREATE USER`、`SELECT`、`TRIGGER`、`EVENT`、`SHOW VIEW`，以及 `CONNECTION_ADMIN` 或 `SUPER`；其 `mysql.user.User_attributes.$.Restrictions` 必须为空，经角色间接获得或缺少这些能力都会在任何账号、schema 或 ledger 修改前失败。管理员连接缺失时二进制可以执行普通迁移，但无法自行强制账号锁定、单会话生命周期和权威元数据检查，只适用于外部编排或 DBA 已实现等价守护的场景。YAML 配置严格要求单文档和已知字段，拼错 `migration_admin_conn_str` 等 key 会直接失败，不会被环境变量覆盖掩盖。其他可用配置包括 `ARES_WEB_ADDRESS`、`ARES_LOG_LEVEL`、`ARES_LOG_ACCESS_FILE` 和 `ARES_LOG_RUNTIME_FILE`。

Compose 把公开源固定为 `http://localhost:${ARES_HTTP_PORT}`，开启本地登录和一次性 Bootstrap，关闭 OIDC 与旧共享管理员 Token。`auth-secrets` 会在私有 `auth_secrets` volume 中一次生成并复用会话根密钥、Bootstrap Token 和系统配置加密密钥；`docker compose down` 会保留它们，`docker compose down -v` 会连同 MySQL 数据一起删除并重新生成。请勿把真实密码或 Token 提交到仓库。数据库示例密码仅用于本机体验；部署到共享环境前必须在未提交的 `.env` 中替换，并且应在首次启动之前完成。

Compose 不依赖 MySQL 只在空数据目录运行一次的 initdb 机制。MySQL 健康后，`database-migrator-user` 先使用 root 管理连接安全收敛迁移账号并保持锁定；账号任务的 named lock、全部特权预检和账号修改始终复用同一条禁用自动重连的物理连接。`migrate` 的管理员连接持有相同迁移账号锁，再为本次执行设置随机一次性密码、短暂解锁并建立唯一迁移会话，随后立即重新锁号、轮换掉一次性密码并清理其他会话。watchdog 会持续验证锁 ownership；退出路径会关闭唯一连接并复核 migrator 仍锁定且没有残留会话。迁移完成后，`database-runtime-user` 同时按全局顺序持有迁移账号锁和运行时账号锁，再收敛运行时账号，授予全库只读和 20 张受管表的精确写权限，确保审计事件只能追加且运行时不能修改 `schema_migrations`。持锁连接失效时，旧任务只有非阻塞拿齐原锁才能执行 fail-closed 收敛，不会排队越过后续 owner。

MySQL named lock 只在当前服务端实例内有效。全部账号任务、root/admin/migrator 连接和并发迁移 Job 必须固定到同一个稳定的 MySQL 8.4 single-writer 端点；不能通过 Router、ProxySQL、DNS 轮询、读写分离或 active-active 把它们分流到多个 writer。需要多写拓扑时必须由外部编排提供跨节点分布式互斥。HA 切换导致连接或 `server_uuid` 改变会使本次作业失败，核对账号与 dirty 状态后从账号任务重跑。

账号任务要求 MySQL 8.4.x，并在任何写入前拒绝 mandatory roles、匿名账号、同名非 `%` Host、目标身份的出向 role/PROXY/DEFINER、目标身份在其他 schema 或全局的权限、Ares schema 中的 trigger/event/routine/view，以及 runtime/migrator 之外仍持有目标 schema 权限的主体；guarded 管理员还会权威拒绝外部 schema 子表反向引用 Ares 受管表或 ledger 的外键。随后才锁号、轮换并丢弃旧双密码、清除入向角色/PROXY 和直授权、终止旧会话并授予白名单权限。数据库级授权还会根据 `@@GLOBAL.partial_revokes` 转义 `\\`、`%`、`_` 等 grant-pattern 元字符，避免 `ares_prod` 的授权意外覆盖 `aresXprod`；旧的不安全 pattern 会先被拒绝并要求 DBA 撤权。runtime 最终回连验证实际身份与有效角色；运行时任务还会先证明 migrator 已锁定且无会话。任何检查或清理失败都会阻止应用启动，不能绕过。

这意味着 PR #6 等旧 volume 不能在旧 `MYSQL_USER` 仍持有数据库级 `ALL PRIVILEGES` 时直接升级。两个账号任务只管理 `MYSQL_MIGRATION_USER` 与 `MYSQL_RUNTIME_USER`，不会猜测、修改或删除旧主体；必须先停止全部旧实例并验证备份，再由 DBA 按[数据库迁移与恢复手册](database-migrations.md)审计、撤销或删除旧账号对 Ares schema 的授权，之后才能启动 W04 链路。`.env` 中的 `MYSQL_ROOT_PASSWORD` 仍须匹配该 volume 内的实际 root 密码；只修改环境变量不会改变 MySQL 内的 root 密码。共享 MySQL 若不能满足特权门禁或不允许一次性 root 任务，应由 DBA 按相同身份解析、继承关系、旧会话和权限矩阵建号，并在生产编排中以受控的等价 Job 替换两个账号任务。不要通过删除生产 volume 解决凭据问题。

账号脚本在 `NO_BACKSLASH_ESCAPES` 模式下把正确转义的密码直接交给 `CREATE USER` / `ALTER USER`，依赖并验证 MySQL 8.4 `general_log` 将密码重写为 `<secret>`；脚本不会通过密码变量、可逆十六进制值或动态 `PREPARE` 中转。若托管平台或审计代理改变日志行为，必须先证明日志仍不含明文或可逆密码表示。

## 身份、权限与生产配置

浏览器只保存随机、不透明的 HttpOnly Cookie；服务端每次请求都检查数据库中的会话和用户状态。页面刷新后通过会话接口取得 CSRF Token，并仅保存在内存中；所有写请求还必须来自配置的精确公开源。发布人、工作流修改人和审计主体由服务端会话确定，客户端不能提交姓名冒充。登录、拒绝、变更和敏感读取会写入只增审计事件，审计内容不包含密码、Token、OIDC code 或请求正文。

内置角色职责如下；最终权限始终由后端判断，菜单和按钮隐藏不是安全边界：

- `viewer`：读取应用、配置、流程、发布、任务、日志和非调试 Kubernetes 信息；
- `developer`：在只读能力之外维护应用、应用环境配置和域名，不能发起发布；
- `releaser`：读取业务配置并创建发布、操作发布任务，不能修改应用配置；
- `admin`：拥有全部业务能力，并可管理环境目录、集成、工作流、用户、角色、审计和 Kubernetes debug。

### 首次初始化与管理员恢复

默认 Compose 是本机评估配置：公开源为 `http://localhost:${ARES_HTTP_PORT}`，本地登录和 Bootstrap 开启，OIDC 关闭。它不会把宿主环境中额外出现的 OIDC 变量自动透传给容器；生产部署应使用经过评审的 Compose override、编排平台 Secret/环境映射或 YAML 配置完成覆盖，不能只把变量追加到 `.env`。生产部署至少完成以下调整：

1. 在反向代理或入口网关终止 TLS，并把 `ARES_WEB_PUBLIC_URL` 设置为用户实际访问的精确 HTTPS 源，例如 `https://ares.example.com`；不要附加路径、查询或片段。OIDC 回调地址为该源下的 `/api/v1/auth/oidc/callback`。只有 loopback 开发环境允许 HTTP。
2. 通过权限受限的 Secret 文件提供至少 32 字节的 `ARES_AUTH_ROOT_KEY_FILE`，并通过 `ARES_SETTINGS_ENCRYPTION_KEY_FILE` 提供稳定的系统配置加密密钥。后者遗失或变化后，已保存的 Jenkins Token 和 kubeconfig 无法解密；应随数据库备份并按同等敏感级别管理。不要把 Secret 放入镜像、仓库、命令参数或普通日志。
3. 若启用 OIDC，设置 `ARES_AUTH_OIDC_ENABLED=true`、`ARES_AUTH_OIDC_AUTO_PROVISION=true`、精确的 `ARES_AUTH_OIDC_ISSUER_URL`、`ARES_AUTH_OIDC_CLIENT_ID` 和 `ARES_AUTH_OIDC_CLIENT_SECRET_FILE`。当前版本尚不支持预创建外部身份，因此启用 OIDC 时必须打开自动建号；默认 scope 为 `openid,profile,email`，默认签名算法为 `RS256`，生产 issuer 必须使用 HTTPS。新 OIDC 身份默认是 `viewer`，由管理员显式调整角色；IdP 能稳定提供已验证邮箱时，建议同时启用 `ARES_AUTH_OIDC_REQUIRE_VERIFIED_EMAIL=true`。
4. 首位管理员创建完成后，移除 Bootstrap Token Secret 并设置 `ARES_AUTH_BOOTSTRAP_ENABLED=false`；数据库的一次性状态仍会永久阻止第二次 Bootstrap。本地管理员可作为 OIDC 故障时的恢复身份，但应限制使用并纳入凭据轮换。
5. 保持 `ARES_AUTH_LEGACY_ADMIN_TOKEN_ENABLED=false`。旧共享 Token 只用于短期升级兼容，Web 不读取或发送它，不应作为新部署的访问控制。
6. 反向代理只转发受信来源的协议信息；只有确需解析代理地址时才配置精确的 `ARES_WEB_TRUSTED_PROXY_CIDRS`。不要信任公网客户端自行提供的转发头。

启动时 Ares 会先确认数据库中至少有一个启用的 `admin`，或者存在“明确启用、Token 合规且 singleton 尚未完成”的 Bootstrap；两者都不满足时拒绝启动。仅启用 OIDC 并不能替代首位管理员初始化，新自动创建的 OIDC 身份固定为 `viewer`，但它也不会让 Bootstrap 失效。完成 Bootstrap 后应在下一次部署配置中移除 Token 并关闭入口；是否完成以数据库 singleton 为准，不以 `auth_users` 是否为空为准。

正常用户管理会拒绝禁用或降级最后一个管理员。若有人绕过应用直接删除/禁用全部管理员，或恢复数据库时遗漏了身份数据，服务会 fail-closed。恢复时先停止 Web/API 写入并保存现场与备份，由 DBA 在隔离环境验证后恢复数据库，或重新启用一个已经核验的既有管理员，再重启服务；不要修改 `auth_bootstrap_state.completed_at`、伪造第二个 bootstrap 管理员或删除整个生产 volume。OIDC 故障时使用保留的本地管理员恢复身份；根密钥丢失会使现有会话与未完成的 OIDC 流失效，系统配置加密密钥丢失则必须重新录入已保存的外部凭据，因此两者都应与数据库备份配套保存和演练恢复。

本地 `bootstrap` 用户可从右上角账号菜单修改自己的密码。服务端会重新验证当前密码，并在同一数据库事务中保存新 Argon2id 哈希、撤销该用户全部会话；成功后当前浏览器也会退出，必须使用新密码重新登录。旧密码登录在建会话事务中还会重新核对刚才验证的哈希，因此即使它与改密并发，也不能在轮换提交后创建新会话。错误当前密码、并发修改、禁用用户或 OIDC 用户不会产生部分更新。建议定期演练该入口，不要直接修改数据库中的密码哈希；紧急恢复仍按上一段的停机、备份与既有管理员核验流程处理。

### 会话、OIDC 流量与代理日志

会话默认空闲 30 分钟、绝对有效期 8 小时；可通过 `ARES_AUTH_SESSION_IDLE_TIMEOUT`、`ARES_AUTH_SESSION_ABSOLUTE_TIMEOUT` 和 `ARES_AUTH_SESSION_TOUCH_INTERVAL` 调整。缩短或延长前应同时评估管理操作风险和用户体验。Swagger 与全部业务 API 都需要登录；只有健康检查、身份选项、Bootstrap、本地登录、OIDC 起始/回调和前端静态资源保持公开。

Cookie 的 `Secure` 属性由 `ARES_WEB_PUBLIC_URL` 的 scheme 决定：生产必须使用精确 HTTPS 源，不能通过 HTTP、错误 Host 或混用多个入口访问。Cookie 同时固定为 HttpOnly、SameSite=Lax、`Path=/` 且不设置 Domain；反向代理必须保留 Cookie，并让浏览器与 Ares 看到一致的公开源和回调 URL。

每次创建 OIDC 流会在同一事务中清理已消费或过期记录；未消费且未过期的流最多保留 4096 条，达到上限返回 503 与 `Retry-After`。bootstrap、本地登录和 OIDC 起始接口还有单进程令牌桶；已登录密码修改会在权限和 CSRF 通过后、Argon2 计算及授权审计前，按用户与可信客户端进入独立低速令牌桶和并发门禁。等待 Argon2 计算槽的请求会响应取消，但已开始的 Argon2 计算仍会完成。这些都不是多副本或按客户端的公网防护。生产入口必须增加按真实客户端身份/IP 的分布式限流、连接与请求体限制，并监控 429、OIDC 503 和认证失败率；扩容 Ares 不能用于绕过限制。

登录选项、鉴权前会话查询和 readiness 也使用有界并发及短期 single-flight/cache，避免同一瞬间把请求直接放大到 MySQL；失败结果只短暂缓存或不缓存。会话查询的准入同时绑定可信客户端 IP 与会话摘要，伪造或轮换 Cookie 不能占满其他客户端的容量。这些进程内保护只负责单实例资源边界，生产入口仍必须实施跨副本、按真实客户端的限流与连接上限。

当前后端不注册 `/metrics`；公开 Web 的同名路径可能按 SPA fallback 返回前端入口，但不会返回 Prometheus 或其他运行指标。健康与 readiness 只提供最小状态。W10 会在独立监听地址、访问控制、采集网络和低基数标签契约明确后再增加指标，生产入口不要自行把内部调试或运行时指标匿名暴露到公网。

仓库自带的 Nginx 使用脱敏访问日志，只记录不含 query 的 `$uri`，并把 OIDC 起始/回调响应设置为 `no-store`、`Referrer-Policy: no-referrer`；`code`、`state`、请求参数和 Referer 不应出现在代理日志中。接入 CDN、Ingress、WAF、APM 或集中日志平台时必须逐层检查并关闭原始 request target、query、Referer 和响应 Cookie 采集，不能假设内置 Nginx 的策略会自动传递到上游。

管理员读取审计事件时，首屏响应会返回固定的 `through_id`；后续请求须原样传回该值，并用 `next_after_id` 继续分页直到 `has_more=false`。分页期间新产生的事件不会混入当前快照，避免审计查询本身不断追加事件而无法结束；需要最新事件时重新发起不带 `through_id` 的首屏请求。

运行时账号对 `audit_events` 只有 `SELECT, INSERT`，保留策略不能通过给应用增加 `DELETE` 实现。生产应监控事件行数、表/索引字节和日增长率；达到约定保留期后，由独立 DBA 身份先导出并校验归档，再在维护窗口按递增主键小批量删除。任一归档校验或删除批次异常时应停止，不执行无界整表删除。

## 数据库与 Demo 初始化

Ares migrator 是专用 schema owner，当前仅支持 MySQL 8.4.x。空库由显式 bootstrap 创建 epoch 1 的固定 10 表基线，再按 epoch 顺序扩展到当前 epoch 5 的 20 张受管表；bootstrap 中断只在已有对象是无业务数据、完整定义匹配且按固定顺序形成连续前缀时恢复。已有表只由 migration 修改。`ares serve` 仅做只读兼容性检查，不执行 Xorm 结构同步或其他 DDL。epoch 2 起的数据契约还要求每条未删除 AppConfig 的环境都对应未删除的 `env_configs` 目录项；缺失或软删除引用会 fail-closed，不会自动猜测。当前受管表包括：

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
- `auth_users`
- `auth_identities`
- `auth_sessions`
- `auth_oidc_flows`
- `auth_bootstrap_state`
- `audit_events`

epoch 5 还为发布任务和工作流版本增加稳定的用户 ID 引用；历史显示名快照继续保留，但新请求不能自行指定发布人或修改人。应用 ID 自增起点会设为 `10000`，与 API 校验范围一致。四种开发语言规则只补缺失项，不覆盖已有规则。

当 `ARES_DEMO_DATA_ENABLED=true` 且应用、应用配置、发布任务和工作流等业务表全部为空时，一个事务会写入：

- `demo-api`、`demo-web`、`demo-worker`
- 每个应用的 `dev/test/moni/preview` 配置，其中 `preview` 用于证明环境不是代码枚举
- 示例域名、动态环境目录和每个 AppConfig 独立绑定的两步 Noop 流程
- 成功、失败和带告警的终态发布记录及步骤快照

Demo 任务不会使用运行中状态，因此在 Jenkins 关闭时不会触发后台轮询。重启容器不会重复写入。仓库中的历史 SQL 或 ORM entity 都不是 schema 真相源；权威入口是版本化 migration 和对应的 schema manifest。

迁移命令、退出码、ledger、dirty 恢复和账号权限详见[数据库迁移与恢复手册](database-migrations.md)。

## 接入 Jenkins 与 Kubernetes

使用具备 `admin` 角色的会话登录 Ares，再进入“系统设置 → 系统配置”：

- 发布环境：创建任意合法环境代码，维护名称、排序和启停状态。停用只阻止新配置和新发布，不删除历史。
- Jenkins：填写服务地址、用户名、API Token 与请求超时后启用。
- Kubernetes：从已启用的动态环境目录选择环境，添加集群并粘贴自包含的 kubeconfig 后启用；命令型认证插件和容器内文件引用会被拒绝。
- 发布流程：在应用的环境配置页为每个 AppConfig 组合并发布独立的版本化步骤流程。

已认证用户可读取启用与停用的环境目录，便于历史页面保留正确标签；发布页仅提供启用项，后端在创建应用配置和发布任务时会再次校验启用状态。流程读取对已认证角色开放，环境、集成和流程修改只允许 `admin`；任务步骤接口不会返回执行器私有配置或外部引用。

敏感字段不会通过查询接口回显，数据库中只保存使用系统配置加密密钥加密后的密文。默认 Compose 从 `auth_secrets` volume 的 `settings_encryption_key` 文件读取该密钥；生产部署使用 `ARES_SETTINGS_ENCRYPTION_KEY_FILE`。修改 Jenkins 地址或用户名时必须重新输入 Jenkins API Token，避免把旧凭据发送到另一个服务端点。

当前版本使用带字段用途和实例标识上下文的 `v2` AES-GCM 密文。升级前保存的 `v1` 密文不会被静默解密或改写；系统配置页会标记“需要重新录入”。管理员可以先以禁用状态保存或删除旧配置，再录入 Jenkins Token/kubeconfig 后启用，避免旧凭据阻塞处置。重新录入前相关集成保持不可用，不能通过恢复旧代码绕过该边界。

Jenkins 地址与 kubeconfig server 不得包含 URL 用户名密码、query 或 fragment；远端端点必须使用 HTTPS，只有 loopback 开发端点允许 HTTP。Kubernetes 还拒绝跳过 TLS 校验、代理 URL、命令型认证和文件型凭据。OIDC、Jenkins 与 Kubernetes HTTP 客户端均不跟随重定向，避免 Authorization Code、Client Secret、API Token 或集群凭据被转发到另一地址。探测和通用 JSON 响应有 1 MiB 硬上限，Kubernetes 运行时响应上限为 16 MiB，Jenkins progressive log 按 256 KiB 游标分段；超限只返回脱敏错误。超大集群列表应使用更窄的查询范围，不应通过调高进程内存绕过限制。

保存启用状态前，Ares 会先验证外部服务连接；失败时返回错误并保留原有可用配置。容器重启时若外部服务暂时不可达，Ares 仍会继续启动，错误会显示在系统配置页。未启用 Jenkins 时，仅 Jenkins 节点/日志接口和包含 `jenkins.job@v1` 的流程不可运行，Noop 或其他不依赖 Jenkins 的流程仍可发布；未启用 Kubernetes 时，集群查询接口返回 HTTP 503，其他功能正常可用。

## 健康检查与日志

- `/health/live`：进程存活，不检查外部依赖。
- `/health/ready`：在独立 1 秒超时内检查 MySQL；并发请求合并，成功短缓存 1 秒、失败短缓存 250 毫秒。
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
docker compose run --rm --no-deps auth-secrets
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

`down -v` 会不可恢复地删除 Compose 管理的 MySQL 数据和 `auth_secrets`；下次启动将生成新的会话根密钥、Bootstrap Token 和系统配置加密密钥。请仅在确认数据库与密钥都无需保留时执行。

### 从旧镜像迁移

新镜像不再把仓库的环境配置或集群凭据打包进镜像。数据库运行连接通过 `ARES_DB_CONN_STR` 注入，一次性 migrator 使用独立的 `ARES_DB_MIGRATION_CONN_STR` 和只在该作业内可见的 `ARES_DB_MIGRATION_ADMIN_CONN_STR`；Jenkins 与 Kubernetes 配置改为启动后由 `admin` 会话在 Web 中保存。升级前请备份数据库和现有系统配置加密密钥，并为身份服务准备稳定的会话根密钥。系统配置加密密钥遗失或变更后，已保存的敏感配置无法解密，需要重新录入；W02 之前保存的 `v1` 凭据也会在界面明确要求重新录入，不做无上下文的静默迁移。会话根密钥变化会使现有会话和未完成的 OIDC 登录流失效。

迁移 `20260902_001_cleanup_legacy_null_strings` 会治理历史字符串 `"NULL"` 并调整相关列约束，批处理覆盖 `0`、负数及最小带符号主键；`20260903_001_pluggable_cicd` 会扩展动态环境与工作流表，并把旧的 CI/CD Job 组合幂等转换为 AppConfig 工作流；`20260903_002_cicd_runtime_hardening` 会增加 Worker 调度索引与 Jenkins 实例地址字段；`20260903_003_versioned_migrations` 会验证 schema manifest 并完成版本化迁移边界收口；`20260904_001_auth_rbac_audit` 会建立六张身份/审计表，并为发布任务和工作流版本增加稳定的用户 ID 引用。升级前须确认活动环境代码合法（末尾 LF、CR、CRLF 等控制字符不会被 MySQL 的行尾锚点误接纳）、没有规范化后的 `(app_id, env)` 重复项，且每条未删除 AppConfig 的环境都有未删除目录项；同时必须停止所有旧版 Ares 写入实例，并在迁移前撤销旧版/未知主体对目标 schema 的授权及外部入向外键。迁移结果记录在 `schema_migrations`。NULL 迁移细节见 [NULL 字符串治理方案](../plans/null-string-cleanup.md)，流水线迁移与回退边界见 [可插拔 CI/CD 实施路线](../plans/pluggable-cicd-roadmap.md)。

版本化迁移在专用连接上持有当前 MySQL 实例内的数据库级 named lock，单次操作超时可通过 `ARES_DB_SCHEMA_MIGRATION_TIMEOUT` 调高，等待锁的时间由 `ARES_DB_MIGRATION_LOCK_TIMEOUT` 单独控制。运行时只读检查和业务请求不会获得迁移账号权限。升级超大旧表时应先在副本验证，并按维护窗口调整迁移 DSN 的 I/O 超时；正式执行仍必须使用稳定 single-writer 端点。

迁移完成后不要把旧镜像接回可写数据库：epoch 5 与 epoch 4 及更早应用不兼容，旧版也缺少当前身份和稳定主体边界。推荐以前向修复处理应用问题；必须回退时，应冻结写入，将数据库恢复到升级前备份，再部署与该备份匹配的旧应用。单独降级二进制/镜像不是受支持的回滚方式。完整停机升级、dirty 恢复和故障排查步骤见[数据库迁移与恢复手册](database-migrations.md)。

Jenkins 外部引用绑定到接收任务时的服务地址。仍有已绑定的 v1 `packaging/deploying`、会自动部署的 `packaged` 任务，或 v2 `running` Jenkins 步骤时，系统设置会拒绝更换 Jenkins 地址或停用集成；应先让这些任务结束或由管理员明确处置。旧结构没有保存任务所属的 Jenkins 地址，因此迁移不会根据当前设置猜测并自动回填。升级前创建且尚未结束的 v1 任务无法证明外部实例归属，旧轮询器会在任何 Jenkins 网络请求前把它们确定性终止为失败；其历史日志查询也会明确拒绝，避免误读或误触发新实例上的同名 Job/Build。生产升级必须优先排空旧任务；若无法排空，应预期这些任务需要在升级后人工重新发布。仅轮换同一地址的凭据不会触发换址限制。

## 上线前检查

- 首次启动前修改全部数据库示例密码，关闭 Demo 数据，并把身份与配置加密密钥迁移到受控 Secret 存储。
- 使用 HTTPS 精确配置公开源和 OIDC 回调，完成首位管理员 Bootstrap 后关闭 Bootstrap，并确认旧共享管理员 Token 保持关闭。
- 验证 `viewer`、`developer`、`releaser`、`admin` 的实际权限边界，以及匿名 `401`、越权 `403`、写请求 CSRF 和会话撤销行为。
- 使用本地管理员演练密码修改，确认修改前全部浏览器会话和旧密码均立即失效，新密码可以重新登录。
- 限制 `ARES_API_PORT` 的本机绑定；生产流量只通过实施 TLS 和请求大小/超时限制的入口进入。
- 确认 OIDC/Jenkins/Kubernetes 只使用受信 HTTPS 精确端点，不依赖重定向、代理 URL 或跳过 TLS 校验；旧版系统凭据已重新录入。
- 使用外部托管 MySQL 时建立备份、恢复演练和监控。
- 为 `audit_events` 设置行数/字节/增长率告警和经校验的 DBA 归档、分批保留流程。
- 使用真实 Jenkins Job、镜像仓库和 kubeconfig 完成二级联调。
- W06 多副本 Worker 与租约完成前，整个 Ares Worker 必须保持单副本运行。旧 v1 Jenkins 兼容轮询器没有跨实例领取或 leader election；v2 步骤虽然已有 pending 步骤认领 CAS，但多个实例仍可能同时 Reconcile 同一个 running 步骤，不能据此认为已经支持多副本。
