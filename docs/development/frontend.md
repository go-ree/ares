# 前端开发指南

## 1. 目录与职责

前端位于 `frontend/`，使用 Vue 3、TypeScript、Vite、Pinia 和 Element Plus。发布相关页面主要位于：

- `frontend/app/web/components/publish/`：发布、运行列表和任务详情组件；
- `frontend/app/web/composables/`：动态环境、发布和日志状态组合逻辑；
- `frontend/app/web/views/application/detail/`：AppConfig、域名及环境专属工作流编辑；
- `frontend/app/web/views/system/Settings.vue`：动态环境与可选集成配置。

环境代码是服务端目录数据，不得在 TypeScript 类型、选项或条件分支中新增固定环境枚举。Git 分支/ref 是每次发布的独立输入，不得根据环境名称自动改写。

## 2. 本地运行

```bash
cd frontend
npm ci
npm run dev
```

开发服务默认只监听 `127.0.0.1:8080`，不会自动暴露到局域网。完整 Compose 环境和反向代理方式见
[部署指南](../operations/deployment.md)。

## 3. 提交前验证

```bash
npm run eslint:check
npm run prettier:check
npm run type-check
npm test
npm run build
npm audit --audit-level=high
```

也可以在仓库根目录运行 `make frontend-check frontend-audit`。`frontend-check` 已包含 Vitest；身份
store、路由守卫、权限按钮、会话失效和日志连接生命周期都必须保留单元/组件测试。`npm run lint`
会执行带自动修复的格式化与 ESLint；准备提交时应检查 diff，避免格式化无关文件。涉及真实 Cookie、
SSE、Nginx 或重启持久性的交互仍需在隔离 Compose 环境补充端到端验收。

## 4. 身份与请求边界

- 浏览器身份只来自服务端 `/api/v1/auth/session`，不从 localStorage 恢复用户名、角色或权限。
- 统一 API 客户端发送同源 Cookie；非安全方法同时发送仅保存在内存中的 CSRF Token。
- 菜单、路由和按钮只消费服务端返回的最终权限集合，不在前端推导角色继承。
- 401 表示会话失效并触发全局身份收敛；403 只表示当前操作无权限，不能把仍有效用户强制登出。
- `X-Ares-Admin-Token` 是后端可选的短期兼容面，Web 不读取、存储或发送它。

## 5. 发布界面约束

- 发布环境来自 `GET /api/v1/environments`；选择器只展示启用项，历史详情可展示停用或未知环境。
- AppConfig 与工作流按环境独立维护，步骤可新增、删除和排序。
- 工作流读取使用 `workflows:read`，保存使用 `workflows:write`；没有写权限时服务端会脱敏执行器私有配置。
- 任务详情展示服务端返回的任意数量步骤和 capabilities，不按固定 CI/CD 顺序推导执行器能力。
- 前端不得伪造取消、重试或“仅重发”等后端尚未实现的动作。

## 6. 通用步骤日志

W03 的 v2 日志只调用
`GET /api/v1/tasks/:task_id/steps/:step_key/logs/stream?cursor=:cursor`。入口是否显示完全取决于当前
步骤的 `capabilities.logs`，不得回退到 Jenkins Job/Build 兼容字段。`engine_version=1` 的历史任务
仍由隔离的 task-scoped adapter 调用 deprecated 路由；浏览器只提交 task ID、CI/CD 类别与 cursor，
不提交 Job、Build ID 或 Jenkins 地址。

每个 `task_id + step_key` 独立维护文本 buffer、最后确认 cursor、完成标记和有限自动重连预算。
canonical 流使用同源 fetch SSE transport，以便读取建流前 HTTP 状态和 `Retry-After`；旧 v1
adapter 仍使用 EventSource。切换步骤前先中止旧 transport；切换任务、关闭详情、路由卸载或失去
日志权限时关闭所有连接并取消 timer。`end/completed` 是当前详情生命周期内的终态，健康检查不能再次打开；
`end/max_duration` 和 `end/upstream_idle` 才允许从最新 cursor 有界重建。

`auth-expired` 或会话探测 401 终止全部日志连接并收敛全局身份。`stream-error/forbidden` 或会话
仍有效时的 403 只终止日志能力、刷新权限，不登出用户。日志以纯文本分批追加并设置内存上限，
其中每个步骤最多保留 2 MiB、当前详情全部步骤合计最多保留 8 MiB，超限按 LRU 淘汰旧缓存；禁止通过
`v-html` 等方式解释上游内容。canonical fetch 使用 `no-referrer`，事件和错误处理的完整契约见
[通用任务步骤日志 API](task-step-logs-api.md)。
