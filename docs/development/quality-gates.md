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
- 前端 ESLint、Prettier、Type Check、生产构建和完整依赖审计；
- Swagger 固定版本重复生成检查；
- Docker Compose 配置校验。

仓库根目录的 `.gitattributes` 将 Go、Shell、SQL、YAML、Markdown 和 JSON 文本统一固定为 LF。迁移 checksum、实现/引擎源码指纹和 Shell 容器执行都依赖稳定字节边界；不得用平台默认换行静默重写这些文件。

数据库 migration 需要真实 MySQL 8.4 语义，因此不放进无数据库依赖的 `make verify`。在一次性测试实例上另行执行：

```bash
ARES_TEST_MYSQL_DSN='root:<密码>@tcp(127.0.0.1:3306)/mysql?parseTime=true' \
  make db-integration
```

该 DSN 必须是能够创建/删除隔离测试数据库和临时账号的 MySQL 8.4 管理连接，不得指向包含业务数据的实例。自动化矩阵会校验：

- 空库只读 status、epoch 1 bootstrap、顺序迁移、重复 up 与 bootstrap 中断续跑；
- 固定 W04 前历史库和每个旧 ledger 连续前缀的精确契约，未知版本/断档/畸形 ledger、历史数据或任意 schema 漂移均在新 dirty 行前零写入拒绝；历史 NULL 批处理覆盖 `0`、负 INT 和最小 BIGINT 主键；
- dirty 的显式恢复、目标对象定义和语句顺序边界、首次 `started_at` 保留、初始 `last_error=NULL` marker、checksum/兼容区间/未知 epoch fail-closed，以及失败后的真实 dirty 状态；
- 全列定义、精确字符集/排序规则、CHECK、视图、主键/唯一索引及其他索引的类型/方向/可见性、出向及外部入向外键语义、活动环境代码，以及未删除 AppConfig 必须指向未删除环境目录项的数据不变量；活动环境代码末尾的 LF/CR/CRLF 必须被拒绝，同类历史任务值不得回填为目录；结构必须先于依赖它的数据查询被分类为 schema 漂移；
- 两个独立 OS 进程和独立连接经同一 MySQL 实例执行时的 migrator 并发收敛、精确锁超时零写入、MySQL 版本拒绝、错误脱敏，以及运行时业务 DML 正常、DDL 与 ledger 写入均被 MySQL 拒绝；
- 通过真实 `realMain` 入口验证 `status`、`up`、`serve`、用法错误和连接故障的退出码、stdout/stderr 与敏感值脱敏，而不只测试内部函数。

历史夹具来自 `main@e2cfd2a`，内容由 SHA-256 测试锁定；改变基线必须先做显式架构决策，不能直接覆盖夹具。每个 epoch 的 manifest/data-contract、bootstrap 和迁移实现都有独立 golden；共享引擎指纹额外覆盖 runner、ledger 收养、manifest 比较、迁移目录调度和 dirty 恢复路径，安全修复必须显式更新审计基线。MySQL 会对低权限账号隐藏部分 trigger/event/routine 和外部入向外键元数据，因此账号有效权限、特权对象及入向依赖缺失还必须执行管理员 E2E，不能以普通 manifest 查询替代。guarded 数据库身份还要在 `lower_case_table_names=1` 的 MySQL 8.4 实例上验证：DSN 大小写可由服务端归一化，但 migrator、管理员和清理连接的实际 `DATABASE()` 必须一致。配置单元测试同时固定严格 YAML 契约：未知顶层/嵌套字段、多文档均失败且不替换活动配置。

账号脚本另有真实 MySQL 8.4 动态检查，可在隔离容器上执行：

```bash
ARES_TEST_MYSQL_CONTAINER='<容器名>' \
ARES_TEST_MYSQL_ROOT_PASSWORD='<root 密码>' \
  make db-account-integration
```

该检查验证未知旧 schema grantee 在任何写入前被拒绝、migrator 初始化及重跑后均锁定且无会话、长期密码无法登录、有效权限精确且无 `DROP`，以及开启 `general_log` 时密码语句仅留下 MySQL 重写的 `<secret>`、不出现明文或可逆十六进制中间值。它还会拒绝缺任一直接全局 `PROCESS`、`CREATE USER`、`SELECT`、`TRIGGER`、`EVENT`、`SHOW VIEW`、`CONNECTION_ADMIN`/`SUPER` 或带 partial Restrictions 的管理员身份。GitHub Actions 的 `MySQL 8.4 最小权限账号检查` 会在专用临时容器中自动运行同一入口。

发起数据库相关 PR 前还应在一次性隔离 Compose 环境记录完整 E2E 证据：新 volume 的完整依赖链与重复启动、旧 volume 在旧授权未撤销时零写入拒绝及 DBA 撤权后的升级、3 个 Demo 应用/4 个环境/12 个 AppConfig、runtime 业务 DML 与 DDL/ledger 拒绝、guarded migrator 成功/失败后均锁定且无会话，以及账号脚本对 mandatory roles、匿名/同名 Host、双密码、旧会话、出向 role/PROXY/DEFINER、管理员元数据权限/Restrictions、schema 可执行对象和外部入向外键的 fail-closed 行为。所有账号与迁移连接必须固定到同一 single-writer MySQL 8.4 实例；多写拓扑另需外部分布式互斥。备份恢复需分别证明 epoch 4 dump 可由 W04 `status`/`serve` 使用，以及迁移前 dump 可由创建备份的精确旧二进制健康启动并完成关键读写。旧二进制启动恢复仍是发布前本地 E2E，不由当前 GitHub Actions 自动执行。

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
