# ADR-0002：OIDC、服务端会话、RBAC 与只增审计

- 状态：已接受
- 日期：2026-09-05
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
11. 本地管理员可以在已认证会话中修改自己的密码；新密码哈希与该用户全部会话撤销必须在
    同一数据库事务提交。旧密码登录在创建会话前必须于事务内重新核对已验证的密码哈希，
    避免与密码轮换并发时仍创建新会话；成功改密后当前浏览器也必须重新登录。
12. 公共认证入口、鉴权前会话查询和 readiness 采用有界速率、并发及短缓存/single-flight；
    会话 Cookie 不能成为绕过客户端维度或挤占全局容量的工具。密码修改另按用户和可信
    客户端隔离限速及并发，所有 Argon2 排队都响应请求取消。
13. OIDC、Jenkins 与 Kubernetes 外连只接受经过规范化的安全端点，禁止服务端跟随重定向，
    并对已知长度和 chunked/压缩响应统一设置读取硬上限。
14. 系统设置凭据使用带用途上下文的 `v2` AES-GCM 信封；旧 `v1` 密文不尝试静默迁移，保持
    失败关闭并要求管理员重新录入，避免旧密文被跨字段、跨集群重放。
15. 所有公开错误响应均来自显式安全分类，未知内部或上游错误只返回稳定通用文本；分页参数
    只接受有界整数并执行偏移溢出检查，避免响应、数据库与审计容量被输入放大。
16. W02 后端不注册匿名 `/metrics` 路由；Web 的 SPA fallback 即使返回前端入口也不得包含指标。
    内部指标的认证、隔离监听地址、低基数标签和生产采集契约统一留到 W10，不能为提前暴露
    进程指标而绕过当前认证边界。
17. 应用记录中的 Git clone 地址属于不可信业务数据；浏览器导航只接受无凭据、query、fragment
    的 HTTPS 仓库地址，常见 SSH clone 地址经严格校验后转换为 HTTPS，其余内容只显示文本。
18. 工作流 JSON 的规范化保持精确十进制语义；数学整数一律写成无小数十进制，避免 MySQL 把
    科学计数整数持久化为浮点 JSON。整数展开最多 4096 位，超限失败而非舍入或无限分配。

## 威胁模型与信任边界

W02 直接处理以下威胁：伪造浏览器身份和角色、发布人冒充、越权调用、Cookie 窃取后的长期
有效、会话固定、跨站写请求、OIDC 回调伪造或 code 注入、开放跳转、超大/歧义 JSON、慢速
HTTP 连接、日志 URL 泄露令牌、不可信 Git 地址触发降级或脚本导航，以及审计正文泄密。

以下能力不在本 ADR 中冒充完成：应用级自定义角色、外部策略引擎、通用步骤日志的执行器
分派、多副本全局登录限流、IdP Back-Channel Logout、长期 refresh token 保存和 Secret
Resolver。W03、W06、W08 可在本边界上继续扩展。

## 身份与会话模型

### OIDC

OIDC 配置由部署者在服务启动前提供，至少包含精确 issuer、client ID、client secret、公开
基址或精确 callback URL。issuer discovery、令牌交换和 JWKS 请求都使用有限超时与响应体
硬上限，HTTP 客户端不跟随任何重定向。discovery 返回的 authorization、token、JWKS 和可选
userinfo 端点必须满足同一安全协议边界；HTTPS issuer 不能降级到 loopback HTTP。只有 issuer
本身就是 loopback HTTP 时才允许本地开发端点使用 HTTP。未配置 OIDC 时核心服务仍可由本地
管理员使用。

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

创建新 OIDC 流时在数据库事务中清理已经消费或过期的记录，并把未消费且未过期的记录限制为
4096 条；达到上限返回可重试的 503，不继续扩张表。bootstrap、本地登录和 OIDC 起始接口还
分别使用进程级令牌桶限制匿名请求，以约束密码哈希计算与短期流创建。已登录用户的密码修改
另在权限与 CSRF 校验后、Argon2 和 `authorized` 审计写入前，按稳定用户 ID 与可信客户端地址
执行专用低速令牌桶和并发门禁；Argon2 全局计算槽的排队响应请求 context 取消。该限制只保护
单个 Ares 进程，不提供多副本、按客户端或全局公平性；生产入口必须按真实客户端身份/IP 增加
分布式限流，并对异常 429/503 和 OIDC 流容量告警，不能把进程级限制当作公网抗滥用边界。

OIDC 用户以 `(issuer, subject)` 为稳定身份，不按 email 自动合并账号。新身份默认获得
`viewer`；bootstrap 管理员可在用户管理界面显式调整角色。email 只作为展示属性，配置要求
已验证 email 时，未验证或缺失的 claim 会被拒绝。

### 一次性 bootstrap 管理员

部署者显式配置高熵 `ARES_AUTH_BOOTSTRAP_TOKEN`。epoch 5 创建固定的 singleton bootstrap
状态行；服务端在事务中 `SELECT ... FOR UPDATE`，只以该行尚未完成作为一次性状态转换条件，
插入本地管理员并设置完成时间，从而在多进程竞争下也只有一个请求成功。请求还需设置本地
用户名和密码；密码使用自适应密码哈希保存，bootstrap token 本身永不落库。OIDC 自动建号的
`viewer` 可以先于该仪式登录，但不会消费或阻止独立的 bootstrap；身份表是否为空不再是条件。

状态行完成后，bootstrap 接口永久按“已完成”拒绝后续调用，即使环境变量仍存在、用户被
禁用或数据库管理员误删用户。服务端启动日志应提示部署者删除该环境变量。本地管理员继续
使用用户名和密码登录，用于 OIDC 不可用时的恢复，但不会重新开放创建第二个 bootstrap
管理员。

服务启动时必须满足以下二者之一：数据库中至少有一个启用的 `admin`；或者 bootstrap 明确
启用、Token 长度合规且 singleton 状态仍可用。仅配置 OIDC、仅存在自动创建的 `viewer`、
bootstrap 已完成但所有管理员都被禁用/删除等状态都会 fail-closed，避免启动一个无人能够
管理的实例。正常用户管理同时阻止禁用或降级最后一个管理员；若数据库被外部误改，应停机后
由 DBA 恢复备份或重新启用已核验的既有管理员，不能把 singleton 改回未完成来绕过一次性边界。

登录失败使用统一错误文本并执行固定成本的密码校验，避免泄露用户名是否存在。密码校验成功
后，数据库事务按与账号变更一致的顺序锁定 bootstrap singleton 和用户行，再常量时间复核
刚才验证的密码哈希、账号来源与启用状态；只有全部仍一致时才原子记录登录时间、撤销当前
Cookie 对应的旧会话并生成新会话。这样既防止会话固定，也保证与密码修改并发的旧密码登录
不会在轮换提交后建立新会话。

本地管理员可在已认证页面提交当前密码和新密码。服务端先重新读取并验证同一启用的
`bootstrap` 用户，再生成新的 Argon2id 哈希；数据库事务锁定用户状态，只有保存的旧哈希仍与
服务层验证值一致时才更新密码，并在同一事务撤销该用户全部会话。错误当前密码、并发修改、
禁用用户或 OIDC 用户都失败关闭且不产生部分状态。成功响应清除当前 Cookie，旧密码与修改前
所有浏览器会话均立即失效。

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
| `kubernetes:debug`      |        |           |          |   ✓   |
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
- `POST /api/v1/auth/password`：本地用户修改自己的密码并撤销全部会话；
- `GET/PATCH /api/v1/system/users...`：admin 查询用户、调整角色或启停；
- `GET /api/v1/system/audit-events`：admin 分页读取审计事件。

认证失败统一返回 401，已认证但权限不足返回 403；不能用 404 或 200 空结果掩盖权限错误。
401 会清理无效会话 Cookie。每个响应携带服务端生成的 `X-Request-ID`，不直接信任任意长度
或含控制字符的客户端 request ID。

所有 JSON API 使用同一严格解码器：校验 JSON Content-Type、设置路由声明的最大字节数、
`DisallowUnknownFields`、只接受一个 JSON 值，并把超限映射为 413，语法、尾随内容或未知字段
映射为 400。POST 查询同样适用；分页字段只接受 JSON 整数，页码和页长必须落在声明范围内，
页长上限为 200，并在计算数据库 offset 前检查整数溢出。敏感值不进入错误响应。

## 外连、凭据与错误边界

Jenkins 地址和 kubeconfig server 必须是无用户名密码、query、fragment 的规范 URL；远端只
允许 HTTPS，loopback 开发端点可以显式使用 HTTP。Kubernetes 还拒绝
`insecure-skip-tls-verify`、代理 URL、exec/auth-provider 以及容器文件凭据引用。OIDC、Jenkins
和 Kubernetes 客户端都不自动跟随 3xx，避免把 authorization code、client secret、API Token
或集群身份转发到攻击者端点。

外连响应在 `RoundTripper` 层同时检查 `Content-Length` 并以 `max+1` 限制未知长度读取：OIDC
和 Jenkins 通用 JSON、Jenkins/Kubernetes 探测为 1 MiB，Kubernetes 运行时 API 为 16 MiB；
Jenkins progressive log 继续以 256 KiB 分段读取，不受通用 JSON 上限误伤。超限、解析失败和
网络错误只映射为稳定的内部/上游不可用响应，结构化日志仅保留 request ID、组件、操作和错误
分类，不记录原始错误或上游正文。

系统设置敏感值按字段用途、实例标识和版本组成 AAD，以 `v2` AES-GCM 密文保存且永不回显。
旧 `v1` 或未知格式无法满足这一上下文绑定，升级后不会解密或自动改写；Web 标记
`credential_reentry_required` 并要求重新录入。管理员仍可在未启用旧配置的情况下先禁用或
删除它，避免不可解密凭据反过来阻塞安全处置。

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

审计保留不是运行时删除职责。部署者应监控 `audit_events` 行数、表/索引字节与增长率，在
约定保留期后由独立 DBA 身份先导出可校验归档，再于维护窗口按主键小批量删除；运行时账号
不得为实现保留策略增加 `DELETE`。归档失败、校验失败或删除影响异常时停止后续批次。

审计查询使用只增 ID 游标，但首屏读取时先固定当前最大 ID 并返回 `through_id`。后续分页必须
原样提交该上界，只读取 `after_id < id <= through_id`，同时使用 `has_more` 和
`next_after_id` 推进。这样查询本身或并发业务写入产生的新审计事件不会不断延长当前遍历；
完成该快照后重新发起首屏请求，才能看到更新的上界和新增事件。

## SSE 与 HTTP 期限

HTTP Server 设置有限的 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout` 和 `IdleTimeout`。
SSE 在进入流处理前通过 `http.ResponseController` 清除普通全局写入期限，随后为每次心跳或
数据写入设置有限的滚动 deadline；普通 handler 不需要自行记住设置期限。

SSE 在鉴权和资源授权完成后才发送 200 响应，使用同源 HttpOnly Cookie。连接定期写入心跳、
每次写入设置有限 deadline，并受总空闲期限约束；事件 ID/cursor 用于重连续传。会话复验间隔
最多 60 秒且不能长于会话 idle timeout。会话到期或撤销时发送 `auth-expired`（若响应仍可写）
并关闭。前端收到该事件，或 EventSource 出错后探测到 session 401/403，必须停止所有定时器和
重连；普通网络故障才允许有界退避。

W02 先把现有旧 Jenkins 日志流纳入该边界；W03 再将其替换为 `task_id + step_key + cursor`
的执行器通用日志能力。

W02 后端不注册 `/metrics`，公开 Web 入口的同名路径至多命中 SPA fallback，不能返回 Prometheus
或其他运行指标。健康与 readiness 只返回最小状态，不包含进程、数据库或业务指标；W10 在
明确独立监听地址、访问控制、标签基数和采集网络边界后再引入指标端点。

OIDC 起始和回调响应使用 `Cache-Control: no-store` 与 `Referrer-Policy: no-referrer`。默认
Nginx 访问日志只记录 `$uri`，不记录包含 query 的原始 request target、`$args` 或 Referer；
callback 的 `code`、`state` 和 issuer 参数因此不会进入代理访问日志。生产入口、CDN 和外部
日志平台必须维持同一脱敏策略，不能被上游默认日志格式重新记录。

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

epoch 5 同时把工作流版本迁移到规范 JSON 与稳定 checksum。规范化不使用 `float64`：普通、
小数或科学计数的等价数值产生相同字节，数学整数展开为十进制整数，使 MySQL JSON 往返后仍可
解码到 Go 整数字段；超过 4096 位的整数在写入/迁移时失败关闭。epoch 1～4 的 checksum、实现
指纹和历史夹具不因这一尚未发布的 epoch 5 实现调整而改变。

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

本地 Vite 开发服务默认只监听 `127.0.0.1`。开发期 `/assets` 中间件在解析、规范化和真实路径
检查后只读取构建目录内的普通文件，拒绝编码/双重编码穿越、反斜杠、NUL、点目录、符号链接及
非 GET/HEAD 方法；该中间件不能成为任意本地文件读取入口。

应用 Git 地址不会直接进入 `href` 或 `window.open`。前端只为规范 HTTPS URL，或能够严格解析
为无端口 `git@host:path` / `ssh://git@host/path` 的 clone 地址生成 HTTPS 导航；拒绝 HTTP、
脚本/data scheme、凭据、query、fragment、控制字符、反斜杠、路径穿越和含歧义 SSH 端口的输入。

## 验收与回退

自动化至少覆盖四角色矩阵、匿名 401/越权 403、publisher 冒充、严格 JSON、CSRF、会话固定、
过期/撤销/登出、旧密码登录与密码轮换并发互斥、密码入口专用准入、密码轮换全会话撤销、
bootstrap 并发一次性、OIDC state/nonce/issuer/audience/PKCE、开放跳转、仓库外链协议/输入校验、
MySQL JSON 数字往返、迁移后 Demo seed 的工作流完整性与重启读取、外连重定向与响应上限、分页边界、
错误与审计脱敏、HTTP 期限以及 SSE 失效停止重连。前端增加真实单元/组件测试，不再使用永远成功的
占位 `npm test`。

数据库迁移不可 down。回退应用前必须恢复 epoch 5 迁移前的数据库备份；不得让 epoch 4
二进制连接 epoch 5 数据库。功能层面可先禁用 OIDC、保留本地管理员，但不能关闭受保护路由
或恢复浏览器伪身份。
