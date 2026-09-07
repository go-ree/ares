# 质量门禁与依赖治理

本文说明 Ares 在本地和 GitHub Actions 中执行的合并前检查。开发进度与阶段验收仍以[开源化与生产能力开发计划](../plans/open-source-production-roadmap.md)为准。

## 1. 固定工具版本

当前质量基线固定为：

- Go `1.26.8`，模块最低版本为 Go `1.26.0`；
- Node.js `24.20.0`；
- npm `11.19.1`；
- Swag `v1.16.4`；
- govulncheck `v1.7.0`；
- actionlint `v1.7.12`；
- Syft `v1.51.1`；
- Trivy `v0.74.0`。

Dockerfile、`go.mod`、前端 `package.json`、GitHub Actions 和本文中的版本必须同步更新。自动化使用的第三方 Action 固定到完整提交 SHA，版本注释只用于说明来源。

## 2. 本地完整检查

首次检出或前端 lockfile 变化后先安装依赖：

```bash
make frontend-install
```

提交前在仓库根目录执行：

```bash
make verify
```

`make verify` 包含以下检查：

- GitHub Actions 语法与 Go 模块完整性、一致性检查；
- Go 格式、全量测试、Vet、关键包 Race Detector 和可达漏洞扫描；
- 前端 ESLint、Prettier、Type Check、Vitest 单元测试、生产构建和完整依赖审计；
- Swagger 固定版本重复生成检查；
- Docker Compose 配置校验。

仓库根目录的 `.gitattributes` 将 Go、Shell、SQL、YAML、Markdown 和 JSON 文本统一固定为 LF。迁移 checksum、实现/引擎源码指纹和 Shell 容器执行都依赖稳定字节边界；不得用平台默认换行静默重写这些文件。

W02 前端身份状态、路由守卫、权限按钮和会话失效处理使用 Vitest 验证；该测试已纳入 `frontend-check`，因此 GitHub Actions 和 `make verify` 都会自动执行。

数据库 migration 需要真实 MySQL 8.4 语义，因此不放进无数据库依赖的 `make verify`。在一次性测试实例上另行执行：

```bash
ARES_TEST_MYSQL_DSN='root:<密码>@tcp(127.0.0.1:3306)/mysql?parseTime=true' \
  make db-integration
```

该 DSN 必须是能够创建/删除隔离测试数据库和临时账号的 MySQL 8.4 管理连接，不得指向包含业务数据的实例。自动化矩阵会校验：

- 空库只读 status、epoch 1 schema bootstrap、顺序迁移至 epoch 5、重复 up 与 schema bootstrap 中断续跑；
- 固定 W04 前历史库和每个旧 ledger 连续前缀的精确契约，未知版本/断档/畸形 ledger、历史数据或任意 schema 漂移均在新 dirty 行前零写入拒绝；历史 NULL 批处理覆盖 `0`、负 INT 和最小 BIGINT 主键；
- dirty 的显式恢复、目标对象定义和语句顺序边界、首次 `started_at` 保留、初始 `last_error=NULL` marker、checksum/兼容区间/未知 epoch fail-closed，以及失败后的真实 dirty 状态；
- 全列定义、精确字符集/排序规则、CHECK、视图、主键/唯一索引及其他索引的类型/方向/可见性、出向及外部入向外键语义、活动环境代码，以及未删除 AppConfig 必须指向未删除环境目录项的数据不变量；活动环境代码末尾的 LF/CR/CRLF 必须被拒绝，同类历史任务值不得回填为目录；结构必须先于依赖它的数据查询被分类为 schema 漂移；
- 两个独立 OS 进程和独立连接经同一 MySQL 实例执行时的 migrator 并发收敛、精确锁超时零写入、MySQL 版本拒绝、错误脱敏，以及运行时业务 DML 正常、DDL 与 ledger 写入均被 MySQL 拒绝；
- 通过真实 `realMain` 入口验证 `status`、`up`、`serve`、用法错误和连接故障的退出码、stdout/stderr 与敏感值脱敏，而不只测试内部函数；
- epoch 5 六张身份/审计表、Bootstrap singleton、发布任务与工作流版本稳定主体字段的精确 manifest、数据契约和 dirty 恢复边界；历史显示名不得被猜测成用户 ID。

历史夹具来自 `main@e2cfd2a`，内容由 SHA-256 测试锁定；改变基线必须先做显式架构决策，不能直接覆盖夹具。每个 epoch 的 manifest/data-contract、bootstrap 和迁移实现都有独立 golden；共享引擎指纹额外覆盖 runner、ledger 收养、manifest 比较、迁移目录调度和 dirty 恢复路径，安全修复必须显式更新审计基线。MySQL 会对低权限账号隐藏部分 trigger/event/routine 和外部入向外键元数据，因此账号有效权限、特权对象及入向依赖缺失还必须执行管理员 E2E，不能以普通 manifest 查询替代。guarded 数据库身份还要在 `lower_case_table_names=1` 的 MySQL 8.4 实例上验证：DSN 大小写可由服务端归一化，但 migrator、管理员和清理连接的实际 `DATABASE()` 必须一致。配置单元测试同时固定严格 YAML 契约：未知顶层/嵌套字段、多文档均失败且不替换活动配置。

账号脚本另有真实 MySQL 8.4 动态检查，可在隔离容器上执行：

```bash
ARES_TEST_MYSQL_CONTAINER='<容器名>' \
ARES_TEST_MYSQL_ROOT_PASSWORD='<root 密码>' \
  make db-account-integration
```

该检查验证未知旧 schema grantee 在任何写入前被拒绝、migrator 初始化及重跑后均锁定且无会话、长期密码无法登录、有效权限精确且无 `DROP`，以及开启 `general_log` 时密码语句仅留下 MySQL 重写的 `<secret>`、不出现明文或可逆十六进制中间值。它还会拒绝缺任一直接全局 `PROCESS`、`CREATE USER`、`SELECT`、`TRIGGER`、`EVENT`、`SHOW VIEW`、`CONNECTION_ADMIN`/`SUPER` 或带 partial Restrictions 的管理员身份。GitHub Actions 的 `MySQL 8.4 最小权限账号检查` 会在专用临时容器中自动运行同一入口。

发起数据库相关 PR 前还应在一次性隔离 Compose 环境记录完整 E2E 证据：`auth-secrets` 与新 volume 的完整依赖链及重复启动、旧 volume 在旧授权未撤销时零写入拒绝及 DBA 撤权后的升级、当前 epoch 5 的 20 张受管表、3 个 Demo 应用/4 个环境/12 个 AppConfig、runtime 业务 DML 与 DDL/ledger 拒绝、六张身份/审计表的精确写权限、guarded migrator 成功/失败后均锁定且无会话，以及账号脚本对 mandatory roles、匿名/同名 Host、双密码、旧会话、出向 role/PROXY/DEFINER、管理员元数据权限/Restrictions、schema 可执行对象和外部入向外键的 fail-closed 行为。所有账号与迁移连接必须固定到同一 single-writer MySQL 8.4 实例；多写拓扑另需外部分布式互斥。备份恢复需证明当前 epoch 5 dump 可由 W02 `status`/`serve` 使用，并分别保留 epoch 4 历史兼容夹具和迁移前 dump 的精确旧二进制恢复证据。旧二进制启动恢复仍是发布前本地 E2E，不由当前 GitHub Actions 自动执行。

W02 身份与授权边界至少需要以下自动化证据：

- Bootstrap 并发只能成功一次，OIDC `viewer` 先登录不消费或阻止 Bootstrap；启动状态必须存在启用的 `admin` 或仍可用的显式 Bootstrap，本地密码登录不泄露用户是否存在，登录和登出都会轮换或撤销不透明会话；
- 本地密码修改必须重新验证当前密码，并在真实 MySQL 事务中原子更新哈希、撤销该用户全部会话；旧密码登录与轮换并发时，建会话事务必须复核已验证哈希并拒绝陈旧登录；错误旧哈希、OIDC/禁用用户不能修改密码或会话，成功后旧密码与修改前所有会话均失效；
- OIDC Authorization Code + PKCE S256 的 state、nonce、issuer、audience/authorized party、签名、时钟偏差、浏览器绑定和站内回跳校验；已禁用用户不能更新资料/登录时间或建立会话；
- 已消费/过期 OIDC 流的事务清理、4096 条活动流容量上限和匿名入口的单进程限流；生产部署另由入口提供按客户端、跨副本的分布式限流，不能以本项测试替代；
- 身份选项、鉴权前会话查询和 readiness 的速率/并发边界与 single-flight，伪造或轮换会话 Cookie 不能绕过客户端维度、占满全局容量或把并发请求放大为同量数据库查询；密码修改还必须在 Argon2 前按用户和可信客户端进入独立低速门禁，等待计算槽时响应 context 取消；
- 四角色路由矩阵、匿名 `401`、越权 `403`，以及发布人和工作流修改人只能来自服务端 Principal；
- Cookie 写请求的精确 Origin 与 CSRF 校验，严格 JSON 的类型、大小、未知字段、重复字段和尾随值拒绝；
- 分页参数只接受范围内 JSON 整数，字符串、浮点、负数、页长超过 200 及 offset 溢出均在查询前拒绝；API 未分类错误、批量子项、执行器不可用原因和结构化日志均不得回显测试凭据、DSN 或上游正文；
- 工作流规范 JSON 的等价普通/小数/科学计数值产生同一 checksum；数学整数经真实 MySQL JSON 往返后仍保持整数类型，4096 位边界精确，超限指数在展开前失败且不丢精度；运行期 Demo seed 写入的 12 份工作流也必须逐份通过完整性读取和 schema 兼容检查，避免迁移完成后的初始化数据绕过同一规范化边界；
- 审计事件追加、脱敏和最小数据库权限，用户禁用/改角色后的会话撤销，以及 `through_id` 固定快照上界在并发追加事件时仍能终止分页；
- OIDC、Jenkins 与 Kubernetes 拒绝不安全 URL 和全部重定向，`Content-Length` 与 chunked/压缩超限响应均在读取硬上限内关闭且不回显正文；Jenkins progressive log 仍可按 256 KiB 游标分段读取；
- HTTP Header/Read/Write/Idle 超时，以及 SSE 在不超过 60 秒且不长于 idle timeout 的间隔重新认证、会话失效后关闭并停止前端重连；
- 后端 `/metrics` 返回 404；公开 Web 同名路径即使命中 SPA fallback 也不包含 Prometheus/运行指标，健康与 readiness 响应不泄露进程、数据库或业务指标；
- Vite 开发服务只绑定 loopback，静态资源中间件拒绝编码/双重编码路径穿越、反斜杠、NUL、点目录、符号链接与非 GET/HEAD 请求。
- Git 仓库按钮只为严格 HTTPS 或可安全转换的 SSH clone 地址生成外链；HTTP、脚本/data scheme、凭据、query、fragment、路径歧义和控制字符均保持纯文本且不能触发导航。

另外要在隔离 Compose 环境手工验证：匿名访问 Swagger 与业务 API 返回 `401`，读取随机 Bootstrap Token 后可创建首位管理员，第二次 Bootstrap 被拒绝，四角色关键操作符合矩阵，Demo 数据在登录后可见，12 个 AppConfig 的当前工作流均能读取，Jenkins/Kubernetes 关闭时核心功能仍可用；精确重启 API 与 Web 后，会话、Bootstrap 状态、Demo 计数以及 12 份工作流的规范化响应摘要必须保持一致。再修改本地管理员密码，确认旧密码和修改前 Cookie 均失效、新密码登录成功且审计事件存在；注入测试用 `v1` 系统凭据时，应确认界面要求重新录入、启用失败关闭但仍可先禁用/删除。向 OIDC callback 发送仅供测试的标记 `code`/`state` 后，还要确认 Nginx 与后端日志均未出现 query、标记值或 Referer，并验证响应包含 `Cache-Control: no-store` 和 `Referrer-Policy: no-referrer`。只有实际执行并保存命令输出、HTTP 状态和必要的脱敏日志后，才能在 PR 中声称这组 E2E 已通过。

当前后端 Go 源码位于模块根目录和 `internal/`，因此门禁显式使用 `. ./internal/...`，避免误扫 `frontend/node_modules` 中第三方包附带的 Go 示例。后续新增 `cmd/`、`pkg/` 等 Go 源码目录时，必须在同一个 PR 中扩展 `GO_PACKAGES` 并更新本文。

镜像相关检查单独执行，避免日常代码检查隐式修改或启动本地容器：

```bash
docker compose build --pull ares web
```

本地安装上文固定版本的 Syft 和 Trivy 后，也可以对根 `Makefile` 构建的后端镜像执行：

```bash
make docker-build
make sbom
make image-scan
```

`make sbom` 与 `make image-scan` 会先校验本地扫描器版本；版本不一致时直接失败，避免本地证据与 CI 基线不可比较。

完整 Compose 的两份运行时镜像 SBOM、前端应用依赖 SBOM 与漏洞扫描以 GitHub Actions 结果为准。

## 3. GitHub Actions

仓库包含两条工作流：

- `质量门禁`：检查 Actions 语法、后端、MySQL 8.4 迁移与恢复、MySQL 8.4 最小权限账号、关键包竞态、Go 可达漏洞和前端；
- `镜像与供应链`：校验 Compose、构建前后端镜像、生成 SPDX JSON SBOM，并使用 Trivy 扫描 high/critical 漏洞。

工作流在指向 `main` 的 PR 和 `main` 推送上运行；镜像扫描还会每周执行一次。供应链工作流分别保存后端运行时镜像、前端 Nginx 运行时镜像和前端 lockfile 应用依赖三份 SBOM，避免压缩后的前端 bundle 丢失 npm 元数据。SBOM 与扫描报告作为 Actions Artifact 保留 14 天。工作流只有 `contents: read` 权限，不使用 PR 代码可访问的写权限或发布凭据。

以下检查应作为 `main` 的 Required Checks：

1. `工作流语法检查`
2. `后端测试与静态检查`
3. `MySQL 8.4 迁移与恢复检查`
4. `MySQL 8.4 最小权限账号检查`
5. `关键包竞态检查`
6. `Go 漏洞检查`
7. `前端质量检查`
8. `Compose、镜像与供应链检查`

Required Checks 只能在本 PR 合并、对应检查至少成功运行一次后由仓库维护者启用。在保护规则启用前，这份清单是目标配置，不代表 GitHub 已经强制执行。

## 4. `main` 保护规则

维护者应为 `main` 创建 Ruleset 或分支保护，并至少启用：

- 所有变更必须通过 PR 合并；
- 合并前必须通过上一节列出的 Required Checks；
- 新提交进入分支后撤销旧批准，并要求分支在合并前保持最新；
- 禁止 force push 和删除 `main`；
- 不允许维护者日常绕过规则，紧急绕过必须留下审计记录和补充 PR。

规则启用后，应在路线图 W01 的验收证据中记录配置日期和验证 PR。规则名称或 Job 名称发生变化时，必须在同一个 PR 中同步本文和仓库设置，避免保护规则静默失效。

## 5. 依赖更新策略

Dependabot 每周检查 Go Modules、前端 npm、GitHub Actions、两份 Dockerfile 和根目录 Docker Compose 镜像。minor/patch 更新按生态分组，major 更新保持独立 PR，便于评估兼容性。

依赖 PR 仍需执行全部 Required Checks。不得仅通过忽略扫描结果或降低严重级别来获得绿色检查。确实无法立即修复时，豁免必须在路线图风险看板和 PR 中同时记录：漏洞编号、不可达或不可利用的证据、影响范围、责任人、到期日和移除条件。

Go 以 govulncheck 的可达调用结果为合并门禁，同时保留模块级公告供人工判断；npm 对完整依赖树阻止未豁免的 high/critical。Trivy 覆盖两个运行时镜像中的操作系统包和能够从镜像元数据识别的应用依赖；SARIF 输出显式使用与退出码相同的 high/critical 过滤条件，避免报告格式默认放宽范围后误改门禁语义。压缩进静态 bundle 的前端依赖由完整 npm audit 与 lockfile 应用依赖 SBOM 覆盖。

## 6. 变更同步规则

以下任一项发生变化时，必须在同一个 PR 中更新本文和[开发进度看板](../plans/open-source-production-roadmap.md)：

- 工具链、依赖管理器或最低运行版本；
- Makefile 验证入口；
- `.gitattributes` 的文本与换行策略；
- 工作流、Job 名称或 Required Checks；
- 漏洞阈值、SBOM 格式、报告保留周期或豁免；
- 分支保护和合并策略。
