# W11-B1：固定版本绑定意图预检

## 1. 本轮范围

B1 提供只读的 CI/CD 绑定意图预检，以及后续绑定/运行可复用的参数解析器。**不保存绑定，不创建运行或回执，不登记产物，也不调用旧工作流 fallback。** B1 交付时为 epoch 9；B2 已在 epoch 10 实现绑定 schema/CAS/读写（见[绑定存储契约](binding-storage.md)），本文件的预检边界不变。B3 接独立运行上下文和回执；完整 B 尚未完成。

预检成功只描述读取时的快照；不占用或锁定版本可用性。保存绑定和创建运行必须在各自事务中重新校验目标、归属和启停状态，不能信任客户端提交的旧预检结果或摘要。

## 2. API 与权限

| 接口 | 权限 | 意图字段 |
| --- | --- | --- |
| POST `/api/v1/apps/{app_id}/ci-binding/preflight` | `applications:write`，developer/admin | `version_id`、`application_type`、可选 `parameters` |
| POST `/api/v1/app-configs/{config_id}/cd-binding/preflight` | `app-configs:write`，developer/admin | `version_id`、`target_type`、可选 `parameters` |

viewer/releaser 返回 403；releaser 的发起运行权限不等于修改绑定权限。要求正式会话、Origin/CSRF；拒绝 legacy token。复用既有请求限流、10 秒上下文期限及授权前/结果审计，前置审计不可用时不查询业务存储。这里的 POST 记录审计事件，但业务预检数据库仅 SELECT。

请求示例（ID 必须替换为实际存在的已发布版本）：

```json
{
  "version_id": "42",
  "application_type": "java",
  "parameters": {"profile": "release", "tests": true}
}
```

version_id 是版本全局 ID，不是模板 ID、版本序号或 latest。ID 使用无前导零的正十进制字符串；路径 app/config ID 对齐既有正 INT，版本 ID 对齐正 BIGINT。CI 不接受 target_type，CD 不接受 application_type。请求没有 expected_revision，因为本轮不保存绑定；也不接运行时覆盖、artifact_id 或 URL 产物引用。

成功 HTTP 200、code=1，仅返回：

```json
{
  "valid": true,
  "executable": false,
  "persisted": false,
  "validation_scope": "binding_intent_only",
  "template_id": "12",
  "version_id": "42",
  "version_number": "3",
  "checksum": "<固定版本语义摘要>"
}
```

不返回规范、私有仓库、参数默认值或合并后的参数；日志/审计不记录请求体和这些值。业务错误只用稳定分类，不列出敏感值或数据库诊断：

- 400 `invalid_request`：ID/JSON/未知或重复字段等格式问题；413 `request_too_large`；415 `invalid_request`：Content-Type 不支持。
- 404 `binding_resource_not_found`：目标或版本不存在、应用/配置/环境已软删除。
- 409 `binding_resource_disabled`：类型、模板或 CD 环境停用。
- 422 `binding_ownership_mismatch`：阶段/类型/目标归属不匹配或标识不合法；`invalid_binding_parameters`：参数不满足契约。
- 503 `binding_preflight_unavailable`：存储/超时、固定版本校验和或规范损坏；认证及审计错误沿用现有响应。

## 3. 目标与固定版本校验

CI 校验应用存在且未软删除，**不要求任何环境配置**；显式 application_type 必须与选定 CI 版本归属一致且启用。当前旧 dev_language 只是历史字段，不据此推断/写入应用类型。B1 的 application_type 是本次意图，不表示已变更应用类型。

CD 校验配置及所属应用存在、环境可见且启用，选定版本必须为 CD 且 target_type 匹配。目标字符串不是集群身份，也不证明 Kubernetes/SSH 等集成已配置或可执行。本轮不校验实际产物槽位内容、来源归属或可部署性，这些依赖 B3/C。

查询采用 REPEATABLE READ 只读事务，按同一快照读取对象及版本；无锁定读取、无外部调用。读取指定不可变版本而不是当前草稿，核对 v1 规范、归属及 canonical SHA-256。模板/类型停用拒绝新意图；只读预检不会改变历史版本。

## 4. 参数合并规则

`pipelinebinding.ResolveParameters` 是内部纯函数，不是公开“查看合并参数”接口。

1. 模板默认值作为初始值。
2. 应用/环境绑定值覆盖默认值；allow_override 不限制绑定层，它控制单次运行覆盖。
3. 单次运行值只可覆盖声明了 allow_override=true 的参数。B1 HTTP 不接收该层，仅由纯函数测试冻结后续语义。
4. 合并后 required=true 的参数必须存在；required 是存在性约束，字符串业务上的非空/枚举/路径合法性留给相应执行器能力验证。未知参数、禁止覆盖项即使值相同也拒绝。

仅接受声明的原生 string/boolean/integer，不做字符串数字、浮点或指数整数的隐式转换；integer 保持 int64 精度。拒绝 null、数组、对象。字符串最多 2048 UTF-8 字节，单值 JSON 最多 4096 字节；拒绝未配对的 UTF-16 surrogate 转义。输入层与合并结果各最多 32 个参数/16 KiB JSON，HTTP 信封最多 20 KiB；parameters 省略或 null 表示未提供覆盖，参数值自身不能为 null。

结果拷贝原值，不共享调用者的 map/字节切片；不解析表达式、不插值、不访问网络。Secret 不能放入普通参数；字段名检测不能证明字符串无凭据，通用 Secret 引用仍待 W08。v1 Spec 和已发布迁移保持不变，新绑定校验不会重写历史默认值。

## 5. 验证与后续

- 单测：优先级、必填/可选、覆盖限制、未知项、类型/大小、UTF-8/转义、int64 边界及输入不可变。
- API：四角色、匿名/legacy/Origin/CSRF、前置审计失败、脱敏与不可执行/未保存标识。
- MySQL 8.4：在现有目录集成测试中增加 SELECT-only 主体的真实预检，覆盖无环境 CI、版本/归属、停用/软删除、校验和损坏拒绝、CD 环境和未创建旧绑定/任务；结束后检查 epoch 9 完整性。测试使用内存认证/审计夹具，不冒充全栈真实会话验收。
- B2 已完成专用绑定存储/CAS/最小权限及迁移验收，不复用旧 `app_config_workflows` 表，见[绑定存储契约](binding-storage.md)。本文件的预检响应仍**不能**当作"已绑定"或"可运行"：绑定写入必须在自己的事务里重新校验，预检不预留版本可用性。B3 才接独立运行语义；C 产物能力就绪前不得接纳不可执行任务。
