# 参与 Ares 开发

感谢你愿意参与 Ares。Ares 以应用及其环境配置为核心，提供可扩展的 CI/CD 流程编排能力。提交代码前，请先阅读本文以及 [开发文档](docs/README.md)。

## 开始之前

- 对较大的功能、数据模型变更或兼容性调整，请先创建 Issue，说明目标、边界和候选方案。
- 开发计划以 [开源化与生产能力开发计划](docs/plans/open-source-production-roadmap.md) 为进度事实源。涉及 W01～W10 的 PR 必须同步对应工作包的状态、验收证据和风险。
- 不要在 Issue、PR、日志或测试数据中提交密码、Token、kubeconfig、私钥及其他敏感信息。安全问题先查看 [安全策略](SECURITY.md) 中私密渠道的实际状态；渠道启用前不得公开漏洞细节。
- 在仓库正式加入 `LICENSE` 前，外部代码贡献是否接收及其授权方式由维护者逐项确认；Issue 和设计讨论不受此限制。

## 开发环境

建议使用与自动化检查一致的工具版本：

- Go `1.26.8`
- Node.js `24.20.0`
- npm `11.19.1`
- Docker Engine 与 Docker Compose v2

安装依赖：

```bash
go mod download
npm --prefix frontend ci
```

本地运行与 Compose 部署方式见 [README](README.md) 和 [部署指南](docs/operations/deployment.md)。

## 分支、提交与 PR

1. 从最新的 `main` 创建短生命周期分支，不要直接向 `main` 推送开发提交。
2. 每个 PR 聚焦一个可独立验证、可独立回退的主题；数据库、API 与前端变更应在描述中明确关联。
3. 提交信息建议采用 `type: 中文摘要`，例如 `feat: 增加通用步骤日志`、`fix: 修复任务认领竞态`。
4. PR 标题和说明使用中文，并完整填写仓库 PR 模板。
5. 合并前处理所有必需检查和评审意见。禁止通过修改测试、降低扫描级别或跳过检查来掩盖失败。

行为和协作边界见 [行为准则](CODE_OF_CONDUCT.md)。

## 提交前检查

后端：

```bash
make fmt-check mod-check test vet race vuln swagger-check
```

前端：

```bash
make frontend-install frontend-check frontend-audit
```

部署与工作流：

```bash
make workflow-check compose-config
docker compose build
```

完整镜像漏洞扫描、运行时镜像 SBOM 和前端 lockfile 应用依赖 SBOM 由 GitHub Actions 执行。若本地平台与 CI 的 `linux/amd64` 结果不同，以 CI 结果为准，并在 PR 中记录差异。

## 测试要求

- 修复缺陷时应先增加能复现问题的测试，再实现修复。
- 新增后端领域逻辑、迁移、鉴权或并发行为时必须包含自动化测试；并发路径应纳入 Race 检查。
- 前端目前缺少完整单元测试体系。涉及交互的变更除静态检查与构建外，还需在 Compose 环境中进行人工验收并记录结果。
- 涉及数据库结构时，需要验证空库升级、历史库升级、重复执行与失败恢复。
- 涉及外部系统时，测试不得依赖真实生产凭据或不可控的公网服务。

## 文档与兼容性

代码行为、API、配置项、迁移方式或用户界面发生变化时，应在同一个 PR 中更新相应文档。破坏性变化必须说明：

- 影响范围与识别方式；
- 升级前置条件；
- 数据备份及恢复方式；
- 可行的前向修复或回退边界；
- 仍未解决的风险。

生成文件必须由固定版本工具生成，并确认重复生成不会产生无意义差异。

## 评审与合并

维护者会重点检查设计边界、安全性、数据兼容、测试证据和文档同步情况。PR 获得批准且所有 Required Checks 通过后方可合并；是否采用 squash、rebase 或 merge commit 由维护者根据提交历史决定。
