# Ares Web Console

该目录包含 Ares 的 Vue 3 管理端，前身为 ChaosCanvas。前端已经与 Go API 合并到同一仓库，并通过根目录的 Docker Compose 一起交付。

## 本地开发

```bash
npm ci
npm run dev
```

开发服务默认监听 `0.0.0.0:8080`，API 地址和代理行为由 `config/vite.config.ts` 管理。

提交前至少运行：

```bash
npm run eslint:check
npm run prettier:check
npm run type-check
npm run build
npm audit --audit-level=high
```

也可以在仓库根目录运行 `make frontend-check frontend-audit`。

## 文档

- [Ares 文档入口](../docs/README.md)
- [前端开发指南](../docs/development/frontend.md)
- [环境与工作流 API](../docs/development/environment-workflow-api.md)
- [流水线步骤执行器扩展指南](../docs/development/pipeline-executors.md)

环境代码来自后端动态目录，前端不得重新添加 `dev/test/moni` 等固定环境枚举。发布和工作流界面也不得伪造后端尚未提供的重试、取消或日志能力。
