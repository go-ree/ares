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

- `质量门禁`：检查 Actions 语法、后端、关键包竞态、Go 可达漏洞和前端；
- `镜像与供应链`：校验 Compose、构建前后端镜像、生成 SPDX JSON SBOM，并使用 Trivy 扫描 high/critical 漏洞。

工作流在指向 `main` 的 PR 和 `main` 推送上运行；镜像扫描还会每周执行一次。供应链工作流分别保存后端运行时镜像、前端 Nginx 运行时镜像和前端 lockfile 应用依赖三份 SBOM，避免压缩后的前端 bundle 丢失 npm 元数据。SBOM 与扫描报告作为 Actions Artifact 保留 14 天。工作流只有 `contents: read` 权限，不使用 PR 代码可访问的写权限或发布凭据。

以下检查应作为 `main` 的 Required Checks：

1. `工作流语法检查`
2. `后端测试与静态检查`
3. `关键包竞态检查`
4. `Go 漏洞检查`
5. `前端质量检查`
6. `Compose、镜像与供应链检查`

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

Go 以 govulncheck 的可达调用结果为合并门禁，同时保留模块级公告供人工判断；npm 对完整依赖树阻止未豁免的 high/critical。Trivy 覆盖两个运行时镜像中的操作系统包和能够从镜像元数据识别的应用依赖；压缩进静态 bundle 的前端依赖由完整 npm audit 与 lockfile 应用依赖 SBOM 覆盖。

## 6. 变更同步规则

以下任一项发生变化时，必须在同一个 PR 中更新本文和[开发进度看板](../plans/open-source-production-roadmap.md)：

- 工具链、依赖管理器或最低运行版本；
- Makefile 验证入口；
- 工作流、Job 名称或 Required Checks；
- 漏洞阈值、SBOM 格式、报告保留周期或豁免；
- 分支保护和合并策略。
