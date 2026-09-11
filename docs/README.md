# Ares 文档

文档按实际用途分类维护：

W07 当前设计：[ADR-0006：任务超时、失败恢复与取消](architecture/decisions/0006-task-lifecycle-recovery.md)，开发契约见[任务状态与恢复边界](development/task-lifecycle.md)。

- 架构文档：[可插拔 CI/CD 与动态环境架构](architecture/pluggable-cicd.md)、[ADR-0001：版本化数据库迁移与运行时兼容性检查](architecture/decisions/0001-versioned-database-migrations.md)、[ADR-0002：OIDC、服务端会话、RBAC 与只增审计](architecture/decisions/0002-authentication-rbac-audit.md)、[ADR-0003：执行器通用步骤日志与游标续传](architecture/decisions/0003-generic-step-logs.md)、[ADR-0004：以 AppConfig 为目标的原子幂等发布](architecture/decisions/0004-appconfig-idempotent-releases.md)、[ADR-0005：多副本 Worker 的任务租约与 fencing](architecture/decisions/0005-multi-replica-worker-leases.md)
- 开发文档：[前端开发](development/frontend.md)、[质量门禁与依赖治理](development/quality-gates.md)、[AppConfig 接口对接](development/app-config-api.md)、[环境与工作流 API](development/environment-workflow-api.md)、[AppConfig 发布 API](development/release-api.md)、[通用任务步骤日志 API](development/task-step-logs-api.md)、[流水线步骤执行器扩展指南](development/pipeline-executors.md)
- 运维文档：[部署指南](operations/deployment.md)、[数据库迁移与恢复手册](operations/database-migrations.md)、[持续开发预览](operations/development-preview.md)
- 计划文档：[开源化与生产能力开发计划（当前进度看板）](plans/open-source-production-roadmap.md)、[可插拔 CI/CD 实施路线](plans/pluggable-cicd-roadmap.md)、[“NULL 字符串”治理方案](plans/null-string-cleanup.md)
