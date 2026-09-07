# Ares

Ares 是一个包含 Go 发布编排 API 与 ChaosCanvas Vue 管理端、正在推进开源化的 CI/CD 控制台。Ares 以应用及其环境配置为核心，通过可插拔步骤组合发布流程；Jenkins 和 Kubernetes 都是可选集成。

## Docker Compose 快速启动

需要 Docker Engine 与 Docker Compose v2。当前数据库基线固定为 MySQL 8.4.x。默认配置会自动生成本地高熵身份密钥，依次完成 MySQL 账号初始化、epoch 5 数据库迁移和运行时收权，再启动 Ares API 与 Nginx 前端。Jenkins/Kubernetes 默认关闭，因此无需准备外部基础设施即可浏览和测试应用管理功能。

```bash
cp .env.example .env
docker compose up -d --build --wait
```

首次启动后，只在需要创建管理员时显式读取随机 Bootstrap Token：

```bash
docker compose run --rm --no-deps \
  -e ARES_AUTH_SECRETS_PRINT_BOOTSTRAP=true auth-secrets
```

打开管理端，在“首次部署管理员”区域粘贴该 Token 并设置用户名、显示名和密码。数据库只允许 Bootstrap 成功一次；完成后应停止传播该 Token。之后使用本地管理员登录，生产部署也可改为 OIDC。

启动后访问：

- 管理端：<http://localhost:8080>
- Swagger（登录后）：<http://localhost:8080/swagger/index.html>
- 后端就绪检查：<http://localhost:8080/health/ready>
- 本机后端调试端口：<http://127.0.0.1:8081>

浏览器使用服务端不透明 Cookie 会话，角色与权限由后端裁决，写请求同时校验同源和 CSRF Token；前端不保存会话令牌或共享管理员令牌。Jenkins 与 Kubernetes 不再是启动依赖，管理员登录后可在“系统设置 → 系统配置”中保存集成配置；连接失败只影响对应功能。

`database-migrator-user` 会先经 root 特权门禁收敛迁移账号，并让它保持锁定；`migrate` 再通过仅迁移容器持有的管理员连接短暂建立唯一迁移会话，立即重新锁号后在该会话中完成版本化迁移，退出前关闭会话并复核迁移账号仍锁定且没有残留连接。随后 `database-runtime-user` 锁号、轮换密码、清理角色/代理/旧会话，并按用途收紧 20 张受管表的写权限；其中审计表只允许追加，身份表只获得所需操作。成功后 `ares` 才会启动。运行时账号可以只读检查 ledger，但不能修改 ledger 或执行 DDL。账号身份存在匿名遮蔽、mandatory role、出向授权、DEFINER、schema 可执行对象或非 Ares schema 授权主体等不确定状态时会在任何写入前 fail-closed，处置方式见[数据库迁移与恢复手册](docs/operations/database-migrations.md)。因此升级旧 volume 前必须停止旧实例，审计并撤销旧版 `MYSQL_USER` 等账号对 Ares schema 的授权，不能期待启动任务自动删除或越过旧授权。服务会初始化 3 个 Demo 应用、4 个动态环境、12 份应用环境配置、独立的 Noop 发布流程、示例域名和终态步骤记录。Demo 初始化是幂等的；任一相关业务表已经有数据时都会跳过整组写入，避免污染已有或部分恢复的数据。

常用命令：

```bash
docker compose ps -a
docker compose logs auth-secrets
docker compose logs database-migrator-user database-runtime-user
docker compose logs migrate
docker compose logs -f ares web
docker compose down
```

`docker compose down` 会保留 MySQL 与身份密钥数据卷。仅在确认要清空全部数据、身份密钥并重新生成 Demo 数据时运行：

```bash
docker compose down -v
```

架构、扩展开发、实施路线和部署说明统一从 [文档入口](docs/README.md) 查阅。升级已有数据库或处理迁移失败前，请先阅读[数据库迁移与恢复手册](docs/operations/database-migrations.md)。

## 本地验证

```bash
make frontend-install
make verify
npm --prefix frontend test
```

质量门禁、镜像扫描和固定工具版本见[质量门禁与依赖治理](docs/development/quality-gates.md)。参与开发前请阅读[贡献指南](CONTRIBUTING.md)、[行为准则](CODE_OF_CONDUCT.md)和[安全策略](SECURITY.md)。开源许可证尚待维护者确认；在 `LICENSE` 合并前，外部代码贡献的授权方式需要逐项确认。
