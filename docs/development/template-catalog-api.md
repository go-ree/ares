# W11-A3：应用类型与模板管理 API

## 1. 范围与权限

API 前缀 `/api/v1`。A3 接入 A2b 事务存储，不修改 epoch 9，不绑定应用/环境、不创建任务或产物。所有模板/版本响应均标记 `executable: false`，发布仅表示保存不可变定义，不证明执行器能力。Web 管理页面在 W11-D，真实 Java CI 在 F。

复用现有 `workflows:read/write` 权限，不修改已冻结的角色/迁移定义。以下路径均要求正式会话，不接受 legacy admin token；写请求要求 Origin、CSRF，受既有认证和请求限流约束。

| 接口 | viewer/developer/releaser | admin | 返回内容 |
| --- | --- | --- | --- |
| GET `/application-types`、`/application-types/{key}` | 允许 | 允许 | 类型元数据，含停用项 |
| POST `/application-types`、PUT `/application-types/{key}` | 403 | 允许，写审计 | 创建/修改回执 |
| GET `/pipeline-templates` | 允许 | 允许 | 模板元数据，不含 draft |
| GET `/pipeline-templates/{id}` | 403 | 允许，敏感读审计 | 完整草稿 |
| POST `/pipeline-templates`、PUT `/pipeline-templates/{id}` | 403 | 允许，写审计 | 创建/修改回执 |
| GET `/pipeline-templates/{id}/versions` | 允许 | 允许 | 固定版本元数据，不含 spec |
| GET `/pipeline-templates/{id}/versions/{number}` | 403 | 允许，敏感读审计 | 完整已发布规范 |
| POST `/pipeline-templates/{id}/versions` | 403 | 允许，写审计 | 已发布版本元数据 |

完整规范可能包含私有仓库引用及非密钥参数，不能因结构校验通过就对普通角色公开。列表在 SQL 层不读取 draft/spec，HTTP 层再次移除这些字段。后续 B/D 若需参数填写契约，须增加经过筛选的专用 DTO，不放开原始 JSON。任何角色都不能通过这些接口删除对象或修改历史版本。

## 2. DTO 与资源限制

### 2.1 数值与分页

ID、revision、version number、source_revision 一律是规范十进制**字符串**，避免浏览器超过 2^53 后失真。拒绝数值 JSON、前导零、正负号、空白和指数写法；revision 为 1～18446744073709551614，模板 ID 为正 int64。更新返回本次成功写入的 revision，不追加一次可能与并发修改交错的读取。

三个列表支持 `limit=1..100`（默认 20）和 `after`，拒绝未知/重复参数。返回 `items`（空列表为 `[]`）、`has_more`、`next_cursor`；只有存在下一页时返回非空游标。类型按稳定 key、模板按 ID、版本按模板内 number 正序分页；游标应原样使用上页返回值。SQL 只取 limit+1 行，不做无界 COUNT/offset。跨页不是快照，期间新增或启停可影响后续页；类型 key 位于已过游标之前的新行需重新从首页读取。

类型/发布请求体最多 4096 字节；模板请求信封最多 69632 字节，内部 Spec 仍受 65536 字节及 MySQL 排版后字段限制。使用严格 JSON 解析：拒绝重复键、未知字段、尾随值、非 UTF-8 和不支持的 Content-Type。模板存储路由设 10 秒上下文期限，约束 SQL/锁等待；最终审计使用独立短期限。

### 2.2 创建与修改

创建类型：`{"key":"java-custom","name":"自定义 Java"}`，初始启用、revision `"1"`。成功 201 返回 `{key,revision,executable:false}`。

修改类型采用 PUT，必须提供全部可变字段：

```json
{"name":"Java","enabled":false,"expected_revision":"1"}
```

创建模板：`{ "key": "java-maven", "spec": <v1 Spec> }`；Spec 见[结构契约](pipeline-template-contract.md)。创建者只取服务端已认证用户 ID；请求不得指定 actor/created_by。成功 201 返回 `{id,revision,executable:false}`。

修改模板采用 PUT：`{ "expected_revision": "1", "enabled": true, "spec": <完整 v1 Spec> }`。enabled 缺失/null 返回 400，不能将遗漏误判为停用。key/kind/归属不可变；Spec 归属与原模板不一致返回 422。成功 200 返回 `{id,revision,executable:false}`。

类型停用后仍允许维护已有草稿，但禁止创建该类型新模板或发布；模板自身停用同样禁止发布。历史元数据和已发布版本可按权限读取；CI 归属应用类型，CD 归属 target_type，不绑定语言。

### 2.3 发布与回执

POST `/pipeline-templates/{id}/versions`：`{"expected_revision":"2"}`。成功 201 返回 `{id,template_id,number,source_revision,checksum,executable:false}`，同事务递增模板 revision；新 revision 等于 source_revision+1。checksum 是规范语义摘要，不依赖 JSON 排版。

重复/陈旧 revision 返回 409，不自动重放；网络超时后先读取模板与版本列表核对 source_revision，不要自动以最新 revision 再次发布，否则会创建另一个版本。修改草稿或发布新版本不影响旧规范。原子性、固定锁序及数据库权限见[存储契约](template-storage.md)。

## 3. 错误与审计

业务结果仍使用 `util.ResponseTemplate`，成功 code=1，失败 code=0；以 HTTP 状态和稳定 error 字段判断，不依赖 message 文案。

| HTTP | error | 含义 |
| --- | --- | --- |
| 400 | `invalid_request` | JSON/字段/数值/分页格式错误 |
| 401 / 403 | 既有认证错误 | 未登录、权限不足、来源/CSRF 不合法 |
| 404 | `catalog_not_found` | 读取不存在的对象/版本 |
| 409 | `catalog_conflict` | 稳定 key 冲突、陈旧 revision、数据库竞争；类型 CAS 不存在也返回此类 |
| 409 | `catalog_disabled` | 类型或模板停用，不允许创建/发布 |
| 413 | `request_too_large` | 请求信封超过上限 |
| 415 | `invalid_request` | 不支持的 Content-Type |
| 422 | `invalid_catalog_request` | key/name/Spec/归属或存储后限额不合法 |
| 503 | `catalog_unavailable` / `audit unavailable` | 存储超时/故障或前置审计不可用 |

审计复用现有 authorized → succeeded/failed 与 denied 机制，包含服务端身份、动作、资源、HTTP 状态和 request_id；不记录请求正文、Spec、参数或数据库错误。创建成功补充新资源 ID，版本读取使用模板/版本复合标识。前置审计失败则拒绝访问存储；最终审计失败只记录脱敏运维信号，不把已提交成功写入改成失败响应。审计与业务数据库不是同一事务，不宣称具备事务 outbox 级最终事件保证。

## 4. 验收与后续

- API 边界覆盖四角色、匿名、legacy token、CSRF、前置审计失败拒绝、成功/失败审计、服务端 actor、大整数精度、分页和输入上限、私有字段与错误脱敏。
- MySQL 8.4 最小目录权限账号通过真实 HTTP handler 创建类型/模板、草稿 CAS、固定版本发布/读取、停用拦截和分页；写入后检查 epoch 9 兼容，证明没有生成任务。该测试复用内存认证/审计夹具，不冒充真实会话库端到端测试。
- 下一增量 W11-B：应用 CI / 环境 CD 的固定版本绑定、独立运行上下文与创建回执。A3 不增加 UI、不自动迁移旧工作流、不将示例 Spec 注册成可运行 Java 模板。
