# Ares 部署指南

## 运行拓扑

默认 `compose.yaml` 启动三个服务：

```text
浏览器 :8080
    │
    ▼
web / Nginx ── /api/* ──► ares:8080 ──► mysql:3306
    │                         ├─────────► Jenkins（可选外部集成）
    └─ Vue SPA               └─────────► Kubernetes（可选外部集成）
```

只有 Web 端口对外开放。后端调试端口默认只绑定 `127.0.0.1:8081`，MySQL 不暴露宿主端口。Nginx 为 Vue Router 提供 SPA fallback，并针对 Jenkins SSE 日志关闭代理缓冲、保留 `Last-Event-ID` 和长连接。

## 快速启动

```bash
cp .env.example .env
# 首次启动前先修改 .env 中的 MySQL 密码
docker compose up -d --build --wait
docker compose ps
```

如果当前 Compose 版本不支持 `--wait`，可运行 `docker compose up -d --build`，再用 `docker compose ps` 等待三个服务均为 healthy。

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

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ARES_BIND_ADDRESS` | `127.0.0.1` | Web 监听地址；确认已有网关保护后才改为 `0.0.0.0` |
| `ARES_HTTP_PORT` | `8080` | Web 对外端口 |
| `ARES_API_PORT` | `8081` | 仅绑定本机的后端调试端口 |
| `MYSQL_DATABASE` | `ares` | 数据库名 |
| `MYSQL_USER` | `ares` | 业务数据库用户 |
| `MYSQL_PASSWORD` | `ares-demo-password` | 业务数据库密码 |
| `MYSQL_ROOT_PASSWORD` | `ares-root-password` | MySQL root 密码 |
| `ARES_DB_SCHEMA_MIGRATION_TIMEOUT` | `2m` | 单次版本化 schema 迁移操作的超时；大库可按 DDL / 扫描耗时调高 |
| `ARES_DEMO_DATA_ENABLED` | `true` | 空库是否写入 Demo 数据 |
| `ARES_SETTINGS_ADMIN_TOKEN` | `ares-local-admin-token` | Web 系统配置接口的管理员令牌；共享环境必须修改 |
| `ARES_SETTINGS_ENCRYPTION_KEY` | 本地示例值 | 加密 Jenkins Token 与 kubeconfig；共享环境必须替换且妥善备份 |
| `GOPROXY` | `https://proxy.golang.org,direct` | 构建后端镜像时使用的 Go 模块代理 |

Compose 会根据 MySQL 变量生成 `ARES_DB_CONN_STR`。如需脱离 Compose 运行后端，也可直接设置 `ARES_DB_CONN_STR`、`ARES_WEB_ADDRESS`、`ARES_LOG_LEVEL`、`ARES_LOG_ACCESS_FILE` 和 `ARES_LOG_RUNTIME_FILE`。

请勿把真实密码或 Token 提交到仓库。默认密码仅用于实现开箱体验；部署到共享环境前必须在未提交的 `.env` 中替换，并且应在首次 `docker compose up` 之前完成。

MySQL 官方镜像只会在空数据目录上应用 `MYSQL_DATABASE` / `MYSQL_USER` / 密码变量。命名卷已创建后，仅修改 `.env` 会让 Ares 使用新凭据连接仍保留旧凭据的 MySQL，导致启动失败。已有数据的环境请先备份，再在 MySQL 内执行 `ALTER USER` / `CREATE DATABASE` / `GRANT` 等变更并同步 `.env`；如果数据可丢弃，也可按本文的重置流程删卷重建。

## 数据库与 Demo 初始化

Ares 是 Compose 场景中的 schema owner，启动时通过 Xorm 同步与版本化迁移创建或补齐这些表：

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

Demo 任务不会使用运行中状态，因此在 Jenkins 关闭时不会触发后台轮询。重启容器不会重复写入。`init.sql` 只作为手工初始化兼容文件，Compose 不会挂载执行它。

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

升级代码并保留数据库：

```bash
git pull --ff-only
docker compose up -d --build --wait
```

备份：

```bash
docker compose exec -T mysql \
  sh -c 'exec mysqldump -u root -p"$MYSQL_ROOT_PASSWORD" "$MYSQL_DATABASE"' \
  > ares-backup.sql
```

恢复前请先在独立环境验证备份。

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

新镜像不再把仓库的环境配置或集群凭据打包进镜像。数据库连接仍通过 `ARES_DB_CONN_STR` 注入；Jenkins 与 Kubernetes 配置改为启动后在 Web 中保存。升级前请备份数据库，并准备固定的 `ARES_SETTINGS_ENCRYPTION_KEY`；密钥遗失或变更后，已保存的敏感配置无法解密，需要重新录入。

包含迁移 `20260902_001_cleanup_legacy_null_strings` 的版本会在启动时治理历史字符串 `"NULL"` 并调整相关列约束；`20260903_001_pluggable_cicd` 会扩展动态环境与工作流表，并把旧的 CI/CD Job 组合幂等转换为 AppConfig 工作流；`20260903_002_cicd_runtime_hardening` 会增加 Worker 调度索引与 Jenkins 实例地址字段。升级前须确认活动环境代码合法、没有规范化后的 `(app_id, env)` 重复项，并停止所有旧版 Ares 写入实例；迁移结果记录在 `schema_migrations`。NULL 迁移细节见 [NULL 字符串治理方案](../plans/null-string-cleanup.md)，流水线迁移与回退边界见 [可插拔 CI/CD 实施路线](../plans/pluggable-cicd-roadmap.md)。

版本化 SQL 迁移使用独立连接，单次操作超时可通过 `ARES_DB_SCHEMA_MIGRATION_TIMEOUT` 调高，不会放宽正常 API 查询的 I/O 上限。当前启动流程仍保留一段历史 Xorm 结构同步，它使用 `ARES_DB_CONN_STR` 中的连接超时；升级超大旧表时应先在副本验证，并按维护窗口调整 DSN 超时。后续会把结构变更全部收敛到版本化迁移，避免两套超时语义。

迁移完成后不要把旧 `main` 镜像接回可写数据库：旧版 Xorm 同步可能删除本版本增加的唯一索引和调度索引，而 `schema_migrations` 不会随之回退，随后既可能写入重复数据，也可能使新版无法重建唯一约束。推荐以前向修复处理应用问题；必须回退时，应冻结写入，使用同时保留新索引与 v2 Worker 的兼容补丁镜像，或将应用和数据库整体恢复到升级前备份。单独降级二进制/镜像不是受支持的回滚方式。

Jenkins 外部引用绑定到接收任务时的服务地址。仍有已绑定的 v1 `packaging/deploying`、会自动部署的 `packaged` 任务，或 v2 `running` Jenkins 步骤时，系统设置会拒绝更换 Jenkins 地址或停用集成；应先让这些任务结束或由管理员明确处置。旧结构没有保存任务所属的 Jenkins 地址，因此迁移不会根据当前设置猜测并自动回填。升级前创建且尚未结束的 v1 任务无法证明外部实例归属，旧轮询器会在任何 Jenkins 网络请求前把它们确定性终止为失败；其历史日志查询也会明确拒绝，避免误读或误触发新实例上的同名 Job/Build。生产升级必须优先排空旧任务；若无法排空，应预期这些任务需要在升级后人工重新发布。仅轮换同一地址的凭据不会触发换址限制。

## 上线前检查

- 首次启动前修改全部默认密码，关闭 Demo 数据。
- 当前前后端没有真实认证/RBAC；在补齐鉴权前，仅部署到受信网络或在网关层实施访问控制。
- 使用 TLS，并限制 `ARES_API_PORT` 的本机绑定。
- 使用外部托管 MySQL 时建立备份、恢复演练和监控。
- 使用真实 Jenkins Job、镜像仓库和 kubeconfig 完成二级联调。
- 旧 v1 Jenkins 兼容轮询器暂时没有跨实例领取或 leader election；仍有旧在途任务时只运行一个 Ares 副本。v2 步骤已有 CAS 认领，但完整多副本租约仍在后续路线中。
