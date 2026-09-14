# 持续开发预览

## 1. 约定与边界

使用独立工作目录和固定 Compose 项目 `ares-preview`，入口为 `http://localhost:8080`，仅绑定本机 loopback。这不是公网生产环境；电脑休眠、关机或 Docker Desktop 未运行时不可用。

每次业务代码开发交付，在相关验证通过后更新预览，记录实际部署 commit、对应 PR 和验证范围。可以预览未合并的 PR，但预览不代表合并授权；默认通过中文 PR 交付，由维护者合并，只有维护者明确授权的单次操作才代为合并。不定时拉取或自动部署任意远端变动。纯文档、仅 GitHub CI 配置变更无需重启业务服务，记录保持运行的版本即可。

## 2. 固定资源与首次启动

私有 `.env` 设置 `COMPOSE_PROJECT_NAME=ares-preview`、`ARES_HTTP_PORT=8080`、`ARES_BIND_ADDRESS=127.0.0.1`、`ARES_DEMO_DATA_ENABLED=true`，并为数据库账号设置独立随机密码。文件权限为 `600`，不得提交或输出其中凭据。

从预览目录执行首次启动：

```bash
docker compose up -d --build --wait --wait-timeout 300
docker compose ps -a
```

MySQL、API/Worker 和 Web 使用 `restart: unless-stopped`；Docker 恢复后可自动启动未被手动停止的容器。四个一次性任务 `auth-secrets`、`database-migrator-user`、`migrate`、`database-runtime-user` 成功退出是正常状态。手动停止后需要重新启动；Docker Desktop 随登录启动由使用者的系统设置决定。

保留 `ares-preview_mysql_data` 和 `ares-preview_auth_secrets`。空库首次填充 Demo；更新保留账号、配置、工作流和发布历史，禁止 `down -v` 或 volume prune。Demo 不提供固定管理员密码，首次管理员通过登录页初始化；仅在尚未初始化的空环境中，由维护者私下读取一次性 Token：

```bash
docker compose run --rm --no-deps -e ARES_AUTH_SECRETS_PRINT_BOOTSTRAP=true auth-secrets
```

Token 不进入文档、截图或 PR。已有管理员的环境不要重新初始化；更新不重置账号或身份密钥。当前预览已有账号，密码按既有设置保留，不记录在此文档。

当前 Compose 的公开来源为 `http://localhost:8080`。浏览器访问也使用该地址；`127.0.0.1:8080` 虽能到达服务，但不是相同 Origin，登录可能被来源校验拒绝。不得通过关闭来源校验解决地址不匹配。

## 3. 每次更新流程

1. 确认目标 commit 已通过相关验证、预览目录无未保存改动；保留私有 `.env` 和固定项目名，不强制 reset 用户改动。构建前核对镜像标签，避免其他项目同时覆盖同名本地镜像。
2. 停止旧 `web`、`ares`，按[迁移手册](database-migrations.md)备份数据库、配置及身份密钥。备份只在私有受限目录存放；gzip 完整性检查不等于恢复演练。涉及 schema 升级时先完成独立库恢复验证并保留匹配旧镜像。
3. 切换到明确的已验证 commit，构建匹配镜像；仅操作本预览项目，不允许新旧 Worker 混跑。
4. **schema、账号、密钥初始化流程或 Compose 有变化时**，执行下面的完整更新，显式重建一次性任务，重新执行迁移及授权收敛：

   ```bash
   docker compose up -d --build --force-recreate --wait --wait-timeout 300
   docker compose ps -a
   ```

   **仅业务代码变化，且 schema、账号权限、密钥初始化和 Compose 均未变化时**，在构建匹配镜像后，可仅重建服务，保持数据库和已完成的一次性任务不变：

   ```bash
   docker compose up -d --no-deps --no-build --force-recreate --wait --wait-timeout 120 ares web
   ```

5. 完整更新确认四个一次性任务退出 `0`；两种更新都确认三个常驻服务 healthy。验证 schema、首页和健康端点；涉及鉴权/UI/发布行为时补对应流程验收，并比较关键业务行数，不把健康检查等同端到端验证：

   ```bash
   docker compose exec -T ares /app/ares migrate status
   curl --fail --silent --show-error http://localhost:8080/health/ready
   ```

6. 失败则停止新 Worker、保留故障证据，按迁移手册处理；不得把旧镜像直接连接不兼容的新库。数据库恢复也不等于撤销已经发生的外部部署。
7. 更新[进度看板](../plans/open-source-production-roadmap.md)，说明实际部署版本、PR、数据库影响、备份/恢复及实际验证范围。交付后不清理或停止预览栈；只有明确要求停止时才停机。

## 4. 本次文档补齐的核对结果

2026-09-14：PR #39 中 W07-B 已合并的状态已被后续主线更新覆盖，但本专门文档、文档索引入口和强制交付规则尚未合入。本次从 `main@3125f71` 补齐缺失部分，不重新引入旧的 epoch 8、待初始化账号或“下一步 W07-C”状态。

本次只读核对时，预览业务版本仍为 `c40ace3`，API/Web/MySQL healthy。epoch 9 和上次数据保留验收见进度看板中的 A2b 交付记录；不把历史验收冒充本次重测。纯文档补齐不重启或修改预览数据。
