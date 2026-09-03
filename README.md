# Ares

Ares 是一个包含 Go 发布编排 API 与 ChaosCanvas Vue 管理端的开源 CI/CD 控制台。Ares 以应用及其环境配置为核心，通过可插拔步骤组合发布流程；Jenkins 和 Kubernetes 都是可选集成。

## Docker Compose 快速启动

需要 Docker Engine 与 Docker Compose v2。默认配置会启动 MySQL、Ares API 和 Nginx 前端；Jenkins/Kubernetes 默认关闭，因此无需准备外部基础设施即可浏览和测试应用管理功能。

```bash
cp .env.example .env
docker compose up -d --build --wait
```

启动后访问：

- 管理端：<http://localhost:8080>
- Swagger：<http://localhost:8080/swagger/index.html>
- 后端就绪检查：<http://localhost:8080/health/ready>
- 本机后端调试端口：<http://127.0.0.1:8081>

当前登录页尚未接入服务端鉴权，输入任意姓名即可进入。默认 Compose 仅适合本地体验或受信网络，不能未经鉴权直接暴露到公网。

Jenkins 与 Kubernetes 不再是启动依赖。进入“系统设置 → 系统配置”，使用 `.env` 中的 `ARES_SETTINGS_ADMIN_TOKEN` 加载并保存集成配置；连接失败只影响对应功能，不影响 Ares 与应用管理功能运行。

空数据库会自动创建表，并初始化 3 个 Demo 应用、4 个动态环境、12 份应用环境配置、独立的 Noop 发布流程、示例域名和终态步骤记录。初始化是幂等的；任一相关业务表已经有数据时都会跳过整组 Demo 写入，避免污染已有或部分恢复的数据。

常用命令：

```bash
docker compose ps
docker compose logs -f ares web
docker compose down
```

`docker compose down` 会保留 MySQL 数据卷。仅在确认要清空全部数据并重新生成 Demo 数据时运行：

```bash
docker compose down -v
```

架构、扩展开发、实施路线和部署说明统一从 [文档入口](docs/README.md) 查阅。

## 本地验证

```bash
go test ./...
npm --prefix frontend ci
npm --prefix frontend run type-check
npm --prefix frontend run build:prod
docker compose config --quiet
```
