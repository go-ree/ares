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
  已经发出的 canonical create 是例外：该请求跳过全局 401 跳转，避免销毁只存在当前页面内存中的
  frozen pair，具体恢复边界见 5.1 节。
- `X-Ares-Admin-Token` 是后端可选的短期兼容面，Web 不读取、存储或发送它。

## 5. 发布界面约束

- 发布环境来自 `GET /api/v1/environments`；选择器只展示启用项，历史详情可展示停用或未知环境。
- AppConfig 与工作流按环境独立维护，步骤可新增、删除和排序。
- 工作流读取使用 `workflows:read`，保存使用 `workflows:write`；没有写权限时服务端会脱敏执行器私有配置。
- 创建目标以 `config_id` 为唯一身份，不再把 `app_name + env` 拼成写请求。单发和批量共用一个
  Release Composer、同一份 TypeScript model/service/composable；不同入口只提供初始目标，不能维护
  两套状态、校验或状态映射。
- 预检分别调用 `POST /api/v1/app-configs/:config_id/releases/preflight` 或
  `POST /api/v1/releases/batch/preflight`，展示服务端返回的目标、工作流版本、有序步骤和稳定
  `error_code`。
  前端严格校验 `request_index/config_id`，把每个通过项的精确工作流版本回填到待提交意图；
  未通过项只展示原因而不进入创建 body，零通过时不生成 key。前端不得从环境名推导步骤，也不提供
  `is_rundeck` 等执行器开关。
- 任务详情展示服务端返回的任意数量步骤和 capabilities，不按固定 CI/CD 顺序推导执行器能力。
- 发布列表和卡片只使用服务端任务/步骤状态与步骤统计计算进度；服务端没有统计时显示不确定进度，
  不得用 queued=10%、running=50% 等固定百分比伪造进展。
- 前端不得伪造取消、重试或“仅重发”等后端尚未实现的动作。

### 5.1 幂等提交状态机

创建请求遵守 [AppConfig 发布 API](release-api.md)，状态按以下顺序收敛：

```text
editing -> preflighting -> ready -> submitting -> succeeded
   ^            |           |           |
   |            +-> blocked +-----------+-> recoverable（仅原 key/body 重试）
   +----------------------- edit <------+-> failed（重新预检后创建新 key）
```

用户确认预检结果后，浏览器用 `crypto.randomUUID()` 生成符合项目协议的 key，并同时冻结规范 request
body 和当前稳定的 `actor_user_id`。网络无响应、401、403、408、429、所有 5xx、
`409 idempotency_request_in_progress`、`503 outcome_unknown`，以及缺少 Ares envelope、未知错误码或
状态码/错误码不匹配的畸形响应，都保留完全相同的 key/body，停止自动重试并只允许用户用原 pair
有界手动重试；`Retry-After` 存在时遵守服务端等待时间。不能因为 Axios timeout、网关 502/504、
页面组件重绘或点击两次而生成第二个 key。HTTP 2xx 也必须先校验 receipt 计数、顺序、目标、版本和
成功/失败不变量；空、截断或错配响应同样是结果不明确，不得清除 frozen pair。只有同时命中发布
API 稳定错误表中的 HTTP 状态与 `error` 组合、且不属于上述结果不明确集合的确定性 4xx，才能结束
本次提交并清除 frozen pair。

canonical create 收到 401 时不得在当前页面跳转登录。界面提示用户在另一标签页使用原账号恢复
会话；原页面重试前先刷新会话与 CSRF，并核对当前 user ID 与 frozen pair 的 `actor_user_id` 完全
一致。主体为空、会话无法确认或 ID 不同都必须在发出创建请求前本地拒绝并继续保留 pair。403 必须
等待同一主体恢复权限；换账号重放会进入新的服务端幂等作用域，可能创建重复任务，因此禁止发送。

目标、ref、inputs 或已确认工作流版本发生任何编辑时，立即废弃冻结 pair 和旧预检，重新预检后再
生成新 key。key/body 只保存在当前页面内存，不进入 URL、localStorage、sessionStorage、埋点或错误
日志。提交中或结果不明确时阻止 SPA 路由离开，并用 `beforeunload` 提示刷新/关闭风险；若用户仍强制离开，
返回后必须先通过任务或审计记录核对，不能盲目用新 key 重提。canonical 失败不能 fallback 到旧
`/deploy/publish` 路由。

批量结果按 `request_index` 完整展示，不截断稳定错误；每个成功项都提供任务详情链接，失败项可被
用户显式选入一个新的命令并获得新 key。组件不能自动把旧批次的失败项追加到原 receipt。

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
