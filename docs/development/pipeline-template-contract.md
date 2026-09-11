# CI/CD 模板结构契约（W11-A1）

## 范围与入口

`POST /api/v1/pipeline-templates/validate` 已实现纯结构校验，不保存模板、不查询类型目录、不登记产物、不调用执行器或外部网络。结构合法不代表可发布/可执行：响应始终包含 `executable: false` 和 `validation_scope: structure_only`。A2/A3 才实现类型目录、持久化版本及语义发布校验；旧 AppConfig 工作流 API 不接受此规范。

接口使用现有 `workflows:write`（目前只有 admin）与会话、Origin、CSRF 及审计中间件，viewer/developer/releaser 返回 403，匿名返回 401。A1 没有增加角色权限或开启 legacy token 旁路；类型/模板管理的完整权限矩阵在 A3 交付。

## 限制与字段

请求体最多 64 KiB，仅接收单一 JSON 对象；未知字段、重复键（含嵌套对象）和非法 JSON 返回 400，超限 413，错误 Content-Type 415。领域错误返回 422，error 为 `invalid_template`，result.problems 包含稳定 path/code；成功返回 200、result.valid=true。请求规范、参数值和执行器 with 不在响应中回显，也不进入审计正文。

| 字段 | 约束 |
| --- | --- |
| schema_version | 固定为 1，是新模板规范版本，不是数据库 epoch 或旧 WorkflowSpec 的版本 |
| kind | ci 或 cd |
| name | 非空，最多 120 个 Unicode 字符 |
| application_type | CI 必填稳定代码；CD 必须省略/为空；A1 只校验格式，不证明目录项存在 |
| target_type | CD 必填稳定代码；CI 必须省略/为空；不是集群地址或执行器能力证明 |
| steps | 1～32 个串行步骤，key 唯一；uses 为精确版本引用，最多 128 字节 |
| inputs / outputs | 每个运行或步骤最多 16 个命名槽位 |
| parameters | 最多 32 个参数，每步最多 32 个参数绑定 |

标识（类型、目标、步骤、槽位、参数）使用 `^[a-z][a-z0-9_-]{0,62}$`；不自动 trim 或更改大小写。`uses` 采用现有注册标识风格，如 `example.maven@v1`。A1 允许尚未注册的标识通过格式校验，绝不能据此调用它；后续发布必须校验注册、阶段适用性、真实槽位和当前能力。

参数声明支持 `type: string|boolean|integer`、`required`、`allow_override` 和可选 `default`。布尔标识默认 false。默认值为对应 JSON 原生类型，不接受 null、字符串形式的数字、浮点/指数整数；整数限制为有符号 int64，字符串最多 2048 UTF-8 字节，默认值原始 JSON 最多 4096 字节。A1 只验证声明，required 和 allow_override 将在应用绑定/运行参数合并阶段执行，不在此阶段解析参数或 Secret。

每步 `parameters` 为执行器参数槽位到模板参数名的映射；不存在的参数拒绝。`with` 仅接受 JSON 对象、最多 8192 字节，省略合法、显式 null 不合法；敏感键沿用现有递归检测。参数名和绑定槽位也拒绝敏感键。没有机制能仅凭字段名证明内容不是 Secret，用户仍禁止把凭据写入普通字符串；通用 Secret 引用/解析能力等待 W08。A1 不提供模板插值或任意表达式。

## 产物类型与引用

`ArtifactType` 包含 kind、media_type、simulated（默认 false）。输入引用的三个字段必须与来源声明完全一致，模拟产物不可以声明为真实产物消费。

- `oci_image`：media_type 为 `application/vnd.oci.image.manifest.v1+json` 或 `application/vnd.oci.image.index.v1+json`。
- `file`：media_type 为 `application/java-archive`、`application/zip`（例如 wheel）或 `application/octet-stream`。具体类型支持仍需后续适配器证明。

模板 inputs 和步骤 outputs 的值直接是 ArtifactType；步骤 inputs 和模板 outputs 的值为 `{from,type}`。from 只接受已经声明的 `inputs.<name>` 或 `steps.<key>.outputs.<name>`，通过精确查表解析，不执行表达式。自引用、未来步骤、缺失输出、循环均拒绝。CI 至少声明一个来自步骤的输出且无运行产物输入；CD 至少一个运行产物输入，模板输出可为空（加工后最终产物可由部署记录追踪）。A1 不证明 CD 已含真实部署步骤，后续语义发布校验负责该门禁。

## CI 示例（结构示例，不可执行）

```json
{
  "schema_version": 1,
  "kind": "ci",
  "name": "Java 构建示例",
  "application_type": "java",
  "parameters": {"module": {"type": "string", "default": "app", "allow_override": false}},
  "steps": [{
    "key": "build",
    "uses": "example.maven@v1",
    "parameters": {"module": "module"},
    "outputs": {"jar": {"kind": "file", "media_type": "application/java-archive", "simulated": true}}
  }],
  "outputs": {"package": {
    "from": "steps.build.outputs.jar",
    "type": {"kind": "file", "media_type": "application/java-archive", "simulated": true}
  }}
}
```

示例返回 result 为 `{"valid":true,"executable":false,"validation_scope":"structure_only","problems":[]}`。例如把 from 改为未来或不存在的步骤，会返回 `unknown_or_forward_reference`；模拟标识或 media_type 不同则返回 `artifact_type_mismatch`。

## 兼容与后续门禁

本次新增独立 `pipelinetemplate` 包，不改写旧 workflow 模型、不更改 epoch 8，也不将当前执行器 Output 自动公开。新规范暂不提供 retry/on_failure 字段；运行模型接入时按 ADR-0006 显式扩展并补版本兼容测试，不能默认继承旧接口的 continue 行为绕过质量门禁。

详见 [W11 计划](../plans/ci-artifact-cd-roadmap.md)。下一增量 A2 增加类型/模板存储和版本，不把本次无副作用校验的结果持久化为“已发布”。
