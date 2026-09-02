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
| `ARES_DEMO_DATA_ENABLED` | `true` | 空库是否写入 Demo 数据 |
| `ARES_JENKINS_ENABLED` | `false` | 是否在启动时连接 Jenkins |
| `ARES_JENKINS_ADDRESS` | 空 | Jenkins 地址 |
| `ARES_JENKINS_USERNAME` | 空 | Jenkins 用户名 |
| `ARES_JENKINS_TOKEN` | 空 | Jenkins API Token |
| `ARES_JENKINS_TIMEOUT_SECONDS` | `15` | Jenkins 单次启动/API 请求超时 |
| `ARES_K8S_ENABLED` | `false` | 是否初始化 Kubernetes 客户端 |
| `ARES_K8S_TIMEOUT_SECONDS` | `15` | Kubernetes API 请求超时 |
| `GOPROXY` | `https://proxy.golang.org,direct` | 构建后端镜像时使用的 Go 模块代理 |

Compose 会根据 MySQL 变量生成 `ARES_DB_CONN_STR`。如需脱离 Compose 运行后端，也可直接设置 `ARES_DB_CONN_STR`、`ARES_WEB_ADDRESS`、`ARES_LOG_LEVEL`、`ARES_LOG_ACCESS_FILE` 和 `ARES_LOG_RUNTIME_FILE`。

请勿把真实密码或 Token 提交到仓库。默认密码仅用于实现开箱体验；部署到共享环境前必须在未提交的 `.env` 中替换，并且应在首次 `docker compose up` 之前完成。

MySQL 官方镜像只会在空数据目录上应用 `MYSQL_DATABASE` / `MYSQL_USER` / 密码变量。命名卷已创建后，仅修改 `.env` 会让 Ares 使用新凭据连接仍保留旧凭据的 MySQL，导致启动失败。已有数据的环境请先备份，再在 MySQL 内执行 `ALTER USER` / `CREATE DATABASE` / `GRANT` 等变更并同步 `.env`；如果数据可丢弃，也可按本文的重置流程删卷重建。

## 数据库与 Demo 初始化

Ares 是 Compose 场景中的 schema owner，启动时通过 Xorm 自动创建或补齐这些表：

- `apps`
- `app_configs`
- `app_config_domains`
- `task_record`
- `task_record_images`
- `pipelines`
- `pipelines_job_combination`
- `env_configs`
- `dev_language_rules`

应用 ID 自增起点会设为 `10000`，与 API 校验范围一致。四种开发语言规则只补缺失项，不覆盖已有规则。

当 `ARES_DEMO_DATA_ENABLED=true` 且 `apps` 表完全为空时，一个事务会写入：

- `demo-api`、`demo-web`、`demo-worker`
- 每个应用的 `dev/test/moni` 配置
- 示例域名、环境和流水线映射
- 成功与失败的终态发布记录

Demo 任务不会使用运行中状态，因此在 Jenkins 关闭时不会触发后台轮询。重启容器不会重复写入。`init.sql` 只作为手工初始化兼容文件，Compose 不会挂载执行它。

## 接入 Jenkins

在 `.env` 中配置：

```dotenv
ARES_JENKINS_ENABLED=true
ARES_JENKINS_ADDRESS=https://jenkins.example.com
ARES_JENKINS_USERNAME=ares
ARES_JENKINS_TOKEN=replace-with-api-token
```

然后重建后端：

```bash
docker compose up -d --build --wait ares web
```

Jenkins 开启后采用 fail-fast：启动阶段无法连接会让 Ares 容器退出。还必须在 Jenkins 中创建与 `pipelines_job_combination` 对应的 CI/CD Job；Demo 流水线名称仅用于展示，并不代表真实 Job 已存在。

Jenkins 关闭时，发布、节点状态和日志流接口会返回 HTTP 503，并且不会先写入悬空的 `init` 任务。

## 接入 Kubernetes

Kubernetes 需要集群名称和 kubeconfig 路径，建议使用本地覆盖文件，不要把 kubeconfig 提交到仓库：

1. 复制 `config/docker.yaml` 为 `config/docker.local.yaml`。
2. 在其中配置 `k8s.clusters`，并确保 kubeconfig 对容器内的 `ares` 用户可读。
3. 在 `.env` 中设置 `ARES_K8S_ENABLED=true`。
4. 创建被 `.gitignore` 忽略的 `compose.override.yaml`，挂载配置与 kubeconfig。

示例：

```yaml
services:
  ares:
    command: ["/app/ares", "-config", "/app/config/docker.local.yaml"]
    volumes:
      - ./config/docker.local.yaml:/app/config/docker.local.yaml:ro
      - /absolute/path/to/kubeconfigs:/run/secrets/kubeconfigs:ro
```

`config/docker.local.yaml` 中的集群片段：

```yaml
k8s:
  enabled: true
  clusters:
    dev:
      name: dev-cluster
      config_path: /run/secrets/kubeconfigs/dev.yaml
      description: 开发集群
```

环境变量 `ARES_K8S_ENABLED` 的优先级高于 YAML。启用后若 kubeconfig 无效或集群不可达，Ares 会 fail-fast；关闭时 Pod/Deployment API 返回 HTTP 503，其他功能正常可用。

## 健康检查与日志

- `/health/live`：进程存活，不检查外部依赖。
- `/health/ready`：在 1 秒超时内检查 MySQL。
- `/inside/checkup`：Nginx/前端健康检查。

Jenkins 和 Kubernetes 不参与基础 Compose readiness；启用它们时，初始化本身采用 fail-fast。

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

新镜像不再把仓库的 `config/default.yaml`、`dev.yaml`、`moni.yaml` 或集群配置打包进镜像，以避免凭据随镜像分发。镜像内的 `/app/config/default.yaml` 是 Compose 专用的无密配置：数据库必须通过 `ARES_DB_CONN_STR` 注入，Apollo/Jenkins/Kubernetes 默认关闭。

旧部署如果依赖镜像内置的 Apollo 环境文件，升级前需改为以下任一方式：

- 通过环境变量注入数据库和集成配置。
- 只读挂载自己的 YAML，并使用 `command: ["/app/ares", "-config", "/run/secrets/ares.yaml"]` 显式选择。

旧 Compose 中的 `command: ["./ares", "-config", "..."]` 仍可覆盖镜像默认命令。

## 上线前检查

- 首次启动前修改全部默认密码，关闭 Demo 数据。
- 当前前后端没有真实认证/RBAC；在补齐鉴权前，仅部署到受信网络或在网关层实施访问控制。
- 使用 TLS，并限制 `ARES_API_PORT` 的本机绑定。
- 使用外部托管 MySQL 时建立备份、恢复演练和监控。
- 使用真实 Jenkins Job、镜像仓库和 kubeconfig 完成二级联调。
- Ares 的任务轮询器暂时没有 leader election；启用 Jenkins 时只运行一个 Ares 副本。
