# ADR-0002：OIDC、服务端会话、RBAC 与只增审计

- 状态：已接受
- 日期：2026-09-04
- 适用版本：W02 起
- 决策范围：浏览器身份、浏览器会话、API 授权、流式接口与安全审计

## 背景

Ares 当前把浏览器 `localStorage` 中任意填写的姓名当作登录身份，并接受客户端提交的
`publisher`。除环境、集成和工作流配置外，应用、发布、任务、日志和 Kubernetes API
均没有服务端授权；少数配置接口则依赖共享的 `X-Ares-Admin-Token`。该模型只适用于受信
网络中的原型，不能作为公开部署的身份或权限边界。

同时，现有 Web 客户端分散创建多个 Axios 实例，无法统一处理 Cookie、CSRF、401/403；
日志 EventSource 在身份失效后仍可能周期性重连。HTTP Server 也没有 Header、Read、Idle
或普通响应写入期限。W02 必须把这些入口一次性收敛到同一服务端安全边界。

## 决策摘要

1. 正式身份源采用 OIDC Authorization Code Flow，并强制 `state`、`nonce` 和 PKCE S256；
   首次部署提供只能成功一次的本地 bootstrap 管理员。
2. 浏览器只持有随机、不透明的 HttpOnly Cookie；会话摘要、到期和撤销状态保存在 MySQL，
   不把 ID Token、Access Token、Session ID 或最终权限写入 Web Storage。
3. 固定四个内置角色 `viewer`、`developer`、`releaser`、`admin`，服务端返回展开后的最终
   权限集合；前端只消费该集合，不自行推导角色继承。
4. 所有业务路由先认证、再按细粒度权限授权。按钮和菜单隐藏只改善体验，服务端 403
   始终是最终裁决。
5. 发布人、工作流修改人和审计主体只从服务端 `Principal` 生成；请求中同名身份字段属于
   未知字段并返回 400，不能覆盖真实主体。
6. 登录、登出、bootstrap、所有拒绝、所有变更和敏感读取写入只增审计表；只记录动作、
   资源、结果、状态码、request ID、主体快照和时间，不记录请求正文、密码、令牌或 OIDC code。
7. Cookie 鉴权的所有非安全方法均校验 `X-CSRF-Token`。Token 由服务端密钥和当前随机会话
   令牌派生，使用常量时间比较，不放入 Cookie 或持久化到浏览器。
8. SSE 使用同源 Cookie，不在 URL 中传令牌；服务端保留心跳和 cursor，前端确认会话失效
   后立即关闭连接并停止重连。
9. `X-Ares-Admin-Token` 仅在原有系统设置和工作流修改接口保留一版过渡兼容，响应携带弃用
   标记；Web 客户端不再读取、保存或发送该令牌。
10. 身份、会话、OIDC 流和审计表通过 W04 之后的 epoch 5 显式迁移创建。`serve` 不执行 DDL；
    epoch 4 的 manifest、checksum 和源码指纹保持不变。

## 威胁模型与信任边界

W02 直接处理以下威胁：伪造浏览器身份和角色、发布人冒充、越权调用、Cookie 窃取后的长期
有效、会话固定、跨站写请求、OIDC 回调伪造或 code 注入、开放跳转、超大/歧义 JSON、慢速
HTTP 连接、日志 URL 泄露令牌，以及审计正文泄密。

以下能力不在本 ADR 中冒充完成：应用级自定义角色、外部策略引擎、通用步骤日志的执行器
分派、多副本全局登录限流、IdP Back-Channel Logout、长期 refresh token 保存和 Secret
Resolver。W03、W06、W08 可在本边界上继续扩展。

## 身份与会话模型

### OIDC

OIDC 配置由部署者在服务启动前提供，至少包含精确 issuer、client ID、client secret、公开
基址或精确 callback URL。issuer discovery、令牌交换和 JWKS 请求都使用有限超时；未配置
OIDC 时核心服务仍可由本地管理员使用。

登录开始时服务端生成独立的高熵 `state`、`nonce`、PKCE verifier 和浏览器绑定令牌。浏览器
绑定令牌只进入短期的 `Secure; HttpOnly; SameSite=Lax` pre-login Cookie；数据库只保存
`state`、`nonce` 与绑定令牌的 SHA-256 摘要以及加密后的 verifier。回调必须在一次原子消费中
同时满足：

- state 存在、未过期、未消费，且 pre-login Cookie 摘要匹配；攻击者不能把自己发起的
  callback 链接转交给另一浏览器制造 login-CSRF；
- 使用同一 verifier 完成 PKCE S256 code exchange；
- ID Token 签名、issuer、client ID audience、有效期和允许的时钟偏差全部通过；
- nonce 摘要常量时间匹配；
- 回调地址与配置的精确地址一致，回跳路径是站内绝对路径，拒绝 `//`、协议 URL 和编码绕过。

OIDC 用户以 `(issuer, subject)` 为稳定身份，不按 email 自动合并账号。新身份默认获得
`viewer`；bootstrap 管理员可在用户管理界面显式调整角色。email 只作为展示属性，配置要求
已验证 email 时，未验证或缺失的 claim 会被拒绝。

### 一次性 bootstrap 管理员

部署者显式配置高熵 `ARES_AUTH_BOOTSTRAP_TOKEN`。epoch 5 创建固定的 singleton bootstrap
状态行；服务端在事务中 `SELECT ... FOR UPDATE`，只有该行仍未完成且身份表为空时才插入首个
管理员并设置完成时间，从而在多进程竞争下也只有一个请求成功。请求还需设置本地用户名和
密码；密码使用自适应密码哈希保存，bootstrap token 本身永不落库。

状态行完成后，bootstrap 接口永久按“已完成”拒绝后续调用，即使环境变量仍存在、用户被
禁用或数据库管理员误删用户。服务端启动日志应提示部署者删除该环境变量。本地管理员继续
使用用户名和密码登录，用于 OIDC 不可用时的恢复，但不会重新开放创建第二个 bootstrap
管理员。

登录失败使用统一错误文本并执行固定成本的密码校验，避免泄露用户名是否存在。成功登录
总是撤销当前 Cookie 对应的旧会话并生成新会话，防止会话固定。

### Cookie 与撤销

Cookie 默认属性为 `Secure; HttpOnly; SameSite=Lax; Path=/`，不设置 Domain。仅本地 HTTP
Compose 示例允许通过明确配置关闭 `Secure`，生产文档不允许该配置。会话令牌由 CSPRNG
生成，数据库只保存 SHA-256 摘要；会话有绝对到期时间、最后活动时间和可空撤销时间。

登出和管理员撤销立即写入 `revoked_at` 并清除 Cookie。每次请求都从数据库确认用户仍启用、
会话未撤销且未到期；不能仅相信进程内缓存。CSRF token 使用部署密钥对原始会话令牌做
HMAC 派生，由 `/auth/session` 返回到当前页面内存。前端刷新后重新获取，永不写入
`localStorage` 或 `sessionStorage`。

## RBAC 权限矩阵

权限是 API 契约，角色只是内置权限集合。服务端会话响应同时返回角色和展开后的权限；后续
可以增加角色而不要求前端复制继承规则。

| 权限                    | viewer | developer | releaser | admin |
| ----------------------- | :----: | :-------: | :------: | :---: |
| `applications:read`     |   ✓    |     ✓     |    ✓     |   ✓   |
| `applications:write`    |        |     ✓     |          |   ✓   |
| `app-configs:read`      |   ✓    |     ✓     |    ✓     |   ✓   |
| `app-configs:write`     |        |     ✓     |          |   ✓   |
| `domains:read`          |   ✓    |     ✓     |    ✓     |   ✓   |
| `domains:write`         |        |     ✓     |          |   ✓   |
| `workflows:read`        |   ✓    |     ✓     |    ✓     |   ✓   |
| `workflows:write`       |        |           |          |   ✓   |
| `releases:read`         |   ✓    |     ✓     |    ✓     |   ✓   |
| `releases:create`       |        |           |    ✓     |   ✓   |
| `tasks:read`            |   ✓    |     ✓     |    ✓     |   ✓   |
| `tasks:write`           |        |           |    ✓     |   ✓   |
| `logs:read`             |   ✓    |     ✓     |    ✓     |   ✓   |
| `kubernetes:read`       |   ✓    |     ✓     |    ✓     |   ✓   |
| `system-settings:read`  |        |           |          |   ✓   |
| `system-settings:write` |        |           |          |   ✓   |
| `users:read`            |        |           |          |   ✓   |
| `users:write`           |        |           |          |   ✓   |
| `audit:read`            |        |           |          |   ✓   |

角色互不隐式包含写权限：`developer` 不能发布，`releaser` 不能改应用配置，只有 `admin` 可以
修改环境目录、外部集成、工作流、用户角色或状态。所有角色都可读取完成发布所需的非敏感
业务元数据；Kubernetes debug 输出仍只允许 admin。

## 路由与响应契约

公共路由仅保留健康检查、登录选项、bootstrap、登录开始/回调和静态前端资源。Swagger、
兼容 API、环境目录和步骤目录都纳入认证。身份 API 使用以下稳定路径：

- `GET /api/v1/auth/options`：返回 OIDC 是否启用及 bootstrap 是否仍可用；
- `POST /api/v1/auth/bootstrap`：一次性创建本地 admin 并建立会话；
- `POST /api/v1/auth/login`：本地恢复管理员登录；
- `GET /api/v1/auth/oidc/start` 与 `GET /api/v1/auth/oidc/callback`：完整 OIDC 跳转；
- `GET /api/v1/auth/session`：返回当前用户、角色、最终权限、到期时间和 CSRF token；
- `POST /api/v1/auth/logout`：撤销当前会话；
- `GET/PATCH /api/v1/system/users...`：admin 查询用户、调整角色或启停；
- `GET /api/v1/system/audit-events`：admin 分页读取审计事件。

认证失败统一返回 401，已认证但权限不足返回 403；不能用 404 或 200 空结果掩盖权限错误。
401 会清理无效会话 Cookie。每个响应携带服务端生成的 `X-Request-ID`，不直接信任任意长度
或含控制字符的客户端 request ID。

所有 JSON API 使用同一严格解码器：校验 JSON Content-Type、设置路由声明的最大字节数、
`DisallowUnknownFields`、只接受一个 JSON 值，并把超限映射为 413，语法、尾随内容或未知字段
映射为 400。POST 查询同样适用。敏感值不进入错误响应。

## 只增审计

审计覆盖所有认证结果、401/403、非安全 HTTP 方法，以及用户/设置/日志/Kubernetes debug
等敏感读取；普通列表和轮询不逐次落库，避免攻击者通过廉价读请求灌爆审计表。写请求在业务
处理前先追加 `authorized` 事件，写入失败则返回 503 且不执行变更；处理后再追加最终
`succeeded` 或 `failed` 结果。后续需要“业务数据与最终审计严格同事务”时应引入统一
transaction/outbox 边界，不能通过更新旧审计行伪造原子性。

审计事件包含：自增 ID、主体用户 ID/用户名/显示名快照、认证来源、动作、资源类型和标识、
结果、HTTP 状态、request ID、创建时间。匿名拒绝和 OIDC/bootstrap 登录结果使用受限的匿名
主体快照。资源标识使用服务端路由参数或内部对象 ID，不拼接原始 query，也不保存 URL 中的
OIDC code/state。

应用运行账号对审计表只有 `SELECT, INSERT`，不拥有 `UPDATE` 或 `DELETE`；代码中只暴露
Append 和分页读取接口。数据库管理员仍可执行保留/归档等运维操作，因此这里的“只增”是
应用边界，不宣称外部不可篡改存证。最终结果写入失败会记录脱敏的结构化错误；前置
`authorized` 事件保证业务写不会在审计完全不可用时继续。

## SSE 与 HTTP 期限

HTTP Server 设置有限的 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout` 和 `IdleTimeout`。
SSE 在进入流处理前通过 `http.ResponseController` 清除普通全局写入期限，随后为每次心跳或
数据写入设置有限的滚动 deadline；普通 handler 不需要自行记住设置期限。

SSE 在鉴权和资源授权完成后才发送 200 响应，使用同源 HttpOnly Cookie。连接定期写入心跳、
每次写入设置有限 deadline，并受总空闲期限约束；事件 ID/cursor 用于重连续传。会话到期或
撤销时发送 `auth-expired`（若响应仍可写）并关闭。前端收到该事件，或 EventSource 出错后探测
到 session 401/403，必须停止所有定时器和重连；普通网络故障才允许有界退避。

W02 先把现有旧 Jenkins 日志流纳入该边界；W03 再将其替换为 `task_id + step_key + cursor`
的执行器通用日志能力。

## 数据库迁移与权限

epoch 5 新增以下受管表，并扩展任务/工作流版本的稳定主体引用；不修改 epoch 1～4 的历史
快照：

- `auth_users`：本地/OIDC 用户、密码哈希、内置角色、启停和登录时间；
- `auth_identities`：唯一的 `(issuer, subject)` 与用户关系；
- `auth_sessions`：会话摘要、到期、活动和撤销状态；
- `auth_oidc_flows`：一次性 state/nonce/浏览器绑定摘要、加密 PKCE verifier 和短期回跳；
- `auth_bootstrap_state`：不可回退为未完成的一次性 bootstrap 状态；
- `audit_events`：只增审计主体与结果快照。

`task_record.publisher_user_id` 和 `release_workflow_versions.created_by_user_id` 保存稳定用户 ID；
现有 `publisher`/`created_by` 继续保存不可变显示快照并兼容历史行，客户端不能写这四个字段。

epoch 5 使用独立完整 manifest、数据契约、迁移实现 ID 和 golden checksum。升级必须先运行
`ares migrate up`，再发布 epoch 5 应用；旧 epoch 4 应用不能连接已升级数据库。runtime
账号按表精确收敛 DML：审计表仅 INSERT，其他身份表只授予实际需要的写操作，ledger 仍只读。

## 前端边界

前端以单一 Axios 客户端统一 Cookie、CSRF 和 401/403；身份 store 有
`unknown/loading/authenticated/anonymous` 状态，并对并发 session 查询做 single-flight。
路由 guard 在身份未知时等待服务端，不从 Web Storage 恢复身份。

菜单、路由、详情标签和写按钮都使用服务端返回的权限集合。系统设置和工作流页面删除管理员
令牌输入；发布请求删除 `publisher`/`publisher_cn`。登出必须等待服务端撤销结果，不能只清
本地状态后伪装成功。

## 验收与回退

自动化至少覆盖四角色矩阵、匿名 401/越权 403、publisher 冒充、严格 JSON、CSRF、会话固定、
过期/撤销/登出、bootstrap 并发一次性、OIDC state/nonce/issuer/audience/PKCE、开放跳转、
审计脱敏与不可更新、HTTP 期限以及 SSE 失效停止重连。前端增加真实单元/组件测试，不再使用
永远成功的占位 `npm test`。

数据库迁移不可 down。回退应用前必须恢复 epoch 5 迁移前的数据库备份；不得让 epoch 4
二进制连接 epoch 5 数据库。功能层面可先禁用 OIDC、保留本地管理员，但不能关闭受保护路由
或恢复浏览器伪身份。
