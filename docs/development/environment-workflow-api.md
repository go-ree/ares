# 环境与工作流 API

## 1. 动态环境目录

`GET /api/v1/environments` 返回完整的非敏感目录，包括停用项。前端应使用 `enabled` 决定能否提供新建配置或发布动作，并保留停用项用于历史展示。

```json
{
  "code": 1,
  "result": [
    {"id": 1, "code": "qa-cn", "name": "华北 QA", "enabled": true, "sort_order": 10},
    {"id": 2, "code": "legacy", "name": "历史环境", "enabled": false, "sort_order": 20}
  ]
}
```

以下系统管理接口都要求请求头 `X-Ares-Admin-Token`：

- `GET /api/v1/system/environments`：读取完整目录；
- `POST /api/v1/system/environments`：创建环境，body 为 `code/name/enabled/sort_order`；
- `PATCH /api/v1/system/environments/:code`：修改 `name/enabled/sort_order`。

环境代码创建后不可修改，服务端会 trim、转小写并校验 `^[a-z][a-z0-9._-]{0,62}$`。停用不删除 AppConfig、Kubernetes 配置或历史任务，只阻止创建新 AppConfig 和发起新发布。

## 2. 应用环境配置

`POST /api/v1/apps/:app_id/configs` 按需创建 AppConfig：

```json
{"env":"qa-cn"}
```

环境必须存在且启用，同一应用不能有两个活动的同环境配置。查询和修改接口详见 [AppConfig 接口对接](app-config-api.md)。

## 3. 步骤类型与工作流

`GET /api/v1/pipeline-step-types` 返回注册表中的步骤描述符、JSON Schema、可选能力和当前可用性。外部集成暂时不可用并不影响保存一份结构合法的流程，但发起发布时会被明确拒绝。

工作流读取和写入都要求 `X-Ares-Admin-Token`：

- `GET /api/v1/app-configs/:config_id/workflow`：读取当前不可变版本；首次未配置返回 404；
- `PUT /api/v1/app-configs/:config_id/workflow`：以当前 `revision` 发布新版本并切换绑定；并发冲突返回 409，规范错误返回 422。

```json
{
  "revision": 0,
  "spec": {
    "schema_version": 1,
    "name": "QA 发布流程",
    "steps": [
      {
        "key": "prepare",
        "name": "准备",
        "uses": "builtin.noop@v1",
        "with": {"message": "ready"},
        "timeout_seconds": 60,
        "on_failure": "stop"
      }
    ]
  }
}
```

每次 PUT 创建新版本，旧任务继续使用创建时保存的步骤快照。步骤顺序由数组位置决定，`key` 在单个流程内唯一。

## 4. 发布与步骤详情

现有单个和批量发布 API 保持兼容。服务端按以下交集校验发布资格：环境启用、应用存在对应 AppConfig、AppConfig 已绑定流程、所有步骤执行器当前可用。

发布接口只持久化并返回 `queued` 任务，后台有界 Worker 负责推进步骤，HTTP 请求不会等待 Jenkins 等外部系统。批量请求必须包含 `1..100` 个条目。`extra_data` 只接受 JSON object，并会成为内部运行快照，因此常见 password/token/secret/credential/authorization/cookie/key 等敏感键（包括 camelCase 与复数写法）会被递归拒绝；任务 API 不回显完整发布输入。键名检测是纵深防护而非 Secret 管理方案，调用方仍不得在值中夹带凭据。

任务详情 `GET /api/v1/deploy/publish/query/:task_id` 会为 v2 任务附带 `steps`；也可调用 `GET /api/v1/deploy/publish/query/:task_id/steps`。响应包含步骤名称、执行器、位置、状态、失败策略、时间和消息，不返回执行器私有配置、外部引用或内部步骤输出。执行器输出只用于后续步骤的数据传递；未来如需展示，应通过单独的鉴权与字段级公开输出契约提供。
