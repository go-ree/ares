# Ares

Ares 是一个包含 Go 发布编排 API 与 ChaosCanvas Vue 管理端的 CI/CD 控制台。实际构建和发布由外部 Jenkins 执行，Kubernetes 集成用于查询集群资源。

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

空数据库会自动创建表，并初始化 3 个 Demo 应用、9 份环境配置、示例域名和终态发布记录。初始化是幂等的：只要 `apps` 表已有记录，重启就不会再次写入或覆盖数据。

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

完整配置、外部 Jenkins/Kubernetes 接入和生产注意事项见 [部署指南](docs/deployment.md)。

## 本地验证

```bash
go test ./...
npm --prefix frontend ci
npm --prefix frontend run type-check
npm --prefix frontend run build:prod
docker compose config --quiet
```
