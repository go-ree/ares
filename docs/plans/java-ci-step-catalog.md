# Java 预定义 CI 步骤计划

## 1. 状态与交付顺序

2026-09-14：本文件是 W11-F 的首批候选契约清单，**未实现真实执行器，也不是可导入的发布模板**。A2b 只存储定义；A3 提供管理 API；B/C 完成独立运行和产物交接；D/E 提供配置页面与模拟闭环；W07-C/W08 完成取消和凭据门禁后，F 接真实执行。进度统一见 [W11 计划](ci-artifact-cd-roadmap.md)。

Java 是可配置应用类型，可拥有 Maven JAR、Gradle JAR、Maven OCI、Gradle OCI 等组合；用户选择固定模板版本，不能在应用中复制维护私有步骤。首条真实验收优先 Maven JAR → OCI → CD 加 Agent → 部署；Gradle 后续复用相同产物契约，不能因写了清单就标为完成。

## 2. 步骤及参数边界

以下 uses 名称为拟议的版本化标识，尚未注册；精确执行参数须在 F 实现前连同适配器能力描述、隔离策略和契约测试一起冻结。

| 步骤 | 候选标识 | 参数与校验方向 | 输入 → 输出 |
| --- | --- | --- | --- |
| 获取源码 | `scm.checkout@v1` | 应用仓库引用、ref；解析并固定 commit；受管凭据引用，地址/协议允许列表 | 仓库 → source ZIP |
| Maven 校验/测试/打包 | `java.maven@v1` | 受管 JDK/工具链版本、项目相对目录、受限 profile 列表；首版固定 verify 生命周期，不接任意 Shell/自由命令串 | source ZIP → JAR、测试报告 ZIP |
| Gradle 校验/测试/打包 | `java.gradle@v1` | 受管 JDK/Gradle 版本、项目相对目录、受限任务组合；校验 wrapper 来源/摘要，首版固定测试及打包组合 | source ZIP → JAR、测试报告 ZIP |
| 发布文件产物 | `artifact.publish-file@v1` | 受管存储引用、输出槽位、保留策略；限制路径/大小，验证摘要和上传结果 | JAR → 已发布 JAR artifact_id |
| 构建 OCI 镜像 | `oci.build@v1` | 受管构建器、按 digest 固定基础镜像、允许的构建上下文及目标平台；不挂宿主 Docker socket | JAR → OCI 候选镜像 |
| 推送并登记镜像 | `artifact.publish-oci@v1` | 受管 registry/仓库引用、标签规则；推送后独立校验 manifest digest | OCI 候选 → 已发布 OCI artifact_id |

Maven/Gradle 构建会执行仓库中的代码，必须在隔离运行环境中进行；不能把允许列表误认为代码可信。网络、CPU/内存/时长、工作目录与缓存租户隔离、日志脱敏及取消确认都是 F 验收门禁。目录不得越界，产物选择不得依赖不确定的“第一个 JAR”；多模块需显式选择模块及产物槽位，缺失/多个匹配均失败。

模板参数区只存非敏感业务参数；凭据通过 W08 受管引用解析，禁止在 with、默认值、日志、产物元数据或构建参数中存明文密钥。是否允许触发时覆盖由模板版本声明；任意插件安装或 Shell 能力不在本清单授权范围。

## 3. 产物与 CD 交接

- source/test-report 使用 `file + application/zip`；JAR 使用 `file + application/java-archive`；镜像使用 `oci_image + application/vnd.oci.image.manifest.v1+json`，多架构另用 OCI index 类型。源码及报告是否发布为用户可选部署产物须由槽位用途和发布门禁区分，不能仅凭 file 类型判断可部署。
- 所有真实产物 `simulated=false`；模拟步骤必须 `simulated=true`，不能通过改字段转换成可信产物。稳定身份、受管位置、摘要、大小、来源 run/step/模板版本/commit 及父产物引用由 C 的产物模型登记；不把这些运行时字段塞进 v1 Spec 类型声明。
- CI 成功必须完成指定输出的发布与摘要核验；仅编译成功或上传了临时文件不算可部署。标签可变，CD 消费固定 artifact_id/digest，不消费 latest 或任意外链。
- 镜像加 Agent 属于独立 CD 加工步骤：输入已发布 OCI，Agent/base 固定版本和摘要，输出新 OCI、新 artifact_id 与父链，再部署这个新产物；不得覆盖 CI 原始产物。CD 按目标和输入类型组织，不按 Java 绑定。
- 文件/镜像存储后端、构建器及 Kubernetes/其他部署适配器的最终选择仍需 F 前设计评审；此处未承诺 Maven 私服、所有仓库平台或全部部署目标。

## 4. 验收清单

- [ ] A3：可创建/修改 Java 类型和多个模板，发布不可变版本，CAS 冲突可见。
- [ ] B～E：应用选 CI 版本；一次模拟 CI 产物供多个环境独立 CD，派生产物链和最终部署引用可追溯。
- [ ] F 首条：真实 Maven 测试失败不发布；成功 JAR/OCI 的摘要与受管存储一致；重试不重复登记，重启可恢复。
- [ ] F 首条：真实 CD 加 Agent 生成新摘要及父链，并以该产物部署；取消/超时和外部副作用确认经过测试。
- [ ] F 后续：Gradle 通过同样契约与隔离测试，未验收前保持未实现。

本文件不修改冻结的 v1 Spec/epoch 9。若需要新增类型、字段或能力验证语义，必须版本化设计与迁移，不覆盖历史校验器。
