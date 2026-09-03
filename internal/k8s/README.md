# Kubernetes 运行时说明

`internal/k8s` 提供按环境隔离的 Kubernetes 客户端和应用、Pod、Service 业务管理器。环境不是 Go 枚举，也没有 `dev/test/moni` 固定槽位。

## 运行时模型

- 环境身份来自数据库环境目录 `env_configs.env`。
- 管理员先在 Web“系统配置 → 发布环境”中创建并启用环境。
- Kubernetes 是环境的可选能力。需要访问集群时，再在同一页面为该环境录入自包含 kubeconfig。
- 集成配置验证成功后，运行时注册表原子替换完整客户端快照；在途请求继续使用自己取得的旧快照。
- Jenkins、Kubernetes、Redis 和 RabbitMQ 都不是 Ares 启动依赖。

调用方始终传递环境代码：

```go
manager := k8s.GetApplicationManager()
result, err := manager.GetApplicationStatus(ctx, "demo-api", "default", "preview")
```

环境不存在或没有 Kubernetes 客户端时，管理器返回明确错误；不得把未知环境映射到某个默认集群。停用环境只禁止新建 AppConfig 和发起发布，已经保存的集群配置仍可保留和查询，便于运维存量资源。

## 安全边界

- kubeconfig 通过受管理员令牌保护的系统设置接口写入，查询接口不回显明文。
- 数据库只保存由 `ARES_SETTINGS_ENCRYPTION_KEY` 加密后的内容。
- 拒绝 `exec`、`auth-provider` 以及容器文件路径形式的证书、密钥和 token；Compose 场景必须使用内嵌凭据。
- 外部集群不参与 `/health/live` 和 `/health/ready`，连接异常只影响使用该集群的操作。

完整部署方式见 [部署指南](../../docs/operations/deployment.md)，环境与发布流程的关系见 [可插拔 CI/CD 架构](../../docs/architecture/pluggable-cicd.md)。
