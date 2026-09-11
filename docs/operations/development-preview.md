# 持续开发预览

## 约定与边界

使用独立工作目录和固定 Compose 项目 `ares-preview`，入口为 `http://localhost:8080`，仅绑定本机 loopback。这不是公网生产环境；电脑休眠、关机或 Docker Desktop 未运行时不可用。

后续每次开发交付，在完成验证后更新此预览，并记录实际部署 commit、对应 PR 和验证结果。可以预览尚未合并的 PR，但须明确版本；预览不代表合并授权。代码和文档继续通过中文 PR 交付，不直接合并 main。不定时自动部署任意远端变动。

## 固定资源与启动

私有 `.env` 设置 `COMPOSE_PROJECT_NAME=ares-preview`、`ARES_HTTP_PORT=8080`、`ARES_BIND_ADDRESS=127.0.0.1`、`ARES_DEMO_DATA_ENABLED=true` 和独立随机 MySQL 密码。文件权限为 `600`，不得提交。

```bash
docker compose up -d --build --wait --wait-timeout 300
docker compose ps -a
```

MySQL、API/Worker 和 Web 已配置 `restart: unless-stopped`，Docker 恢复后自动启动；一次性任务成功退出是正常状态。手动 `stop` 或 `down` 后需再次 `up`。Docker Desktop 是否随系统登录启动由使用者的系统设置决定。

保留 `ares-preview_mysql_data` 和 `ares-preview_auth_secrets`。空库首次填充 Demo；后续更新保留账号、配置、工作流和发布历史，不执行 `down -v` 或 volume prune。首次管理员由使用者在登录页设置，在预览目录读取一次性 Token：

```bash
docker compose run --rm --no-deps -e ARES_AUTH_SECRETS_PRINT_BOOTSTRAP=true auth-secrets
```

Token 不进入文档或 PR。后续更新不重置管理员或身份密钥。

## 每次更新流程

1. 确认目标 commit 已通过相关验证，预览目录无未保存改动；保留私有 `.env` 和固定项目名，不强制 reset 用户改动。
2. 停止旧 `web`、`ares`，按[迁移手册](database-migrations.md)备份并验证数据库，保留身份与配置密钥。
3. 切换到明确的已验证 commit，构建镜像；只操作本预览项目。
4. 执行 `docker compose up -d --build --force-recreate --wait --wait-timeout 300`，显式重建一次性任务，重新执行迁移及授权收敛。短暂中断服务但保留 named volumes，不允许新旧 Worker 混跑。
5. 确认四个一次性任务退出 `0`、三个常驻服务 healthy；验证运行时 schema 状态、首页、健康端点及匿名接口鉴权。涉及 UI/发布行为时补充对应流程验证。
6. 失败则停止新 Worker 并保留故障证据，按迁移手册处理；不得把旧镜像直接接入不兼容的新库。
7. 更新[进度看板](../plans/open-source-production-roadmap.md)，说明实际部署版本和验证范围。交付后不清理或停止此预览栈。
