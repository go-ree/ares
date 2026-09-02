# AppConfig 接口对接文档

本文档仅包含 **AppConfig（应用环境配置）** 相关接口，用于前端接入与适配。

## 约定

- **BasePath**：`/api/v1`
- **响应结构**：统一为 `util.ResponseTemplate`
  - `code=1` 表示成功；`code=0` 表示失败
  - `result` 为成功结果；`error` 为失败原因
- **PATCH 语义**：仅更新请求体中出现的字段（指针语义），未传的字段不会被改动。

### 响应示例（通用）

**成功**

```json
{
  "code": 1,
  "message": "查询成功",
  "result": {},
  "error": null,
  "help": "暂不提供帮助信息"
}
```

**失败**

```json
{
  "code": 0,
  "message": "请求参数格式错误",
  "result": null,
  "error": "xxx",
  "help": "暂不提供帮助信息"
}
```

---

## 1) 以 `app_id + env` 为主的配置接口（推荐给大多数前端页面）

### 1.1 获取应用所有环境配置

- **GET** `/apps/{app_id}/configs`
- **说明**：返回该应用在各环境（如 dev/test/moni）下的 `app_configs` 列表；每条包含 `config_id`，用于后续多域名操作。

**示例**

```bash
curl -X GET '/api/v1/apps/10001/configs'
```

**成功响应示例（result 为数组，每个元素为 AppConfigs）**

```json
{
  "code": 1,
  "message": "查询成功",
  "result": [
    {
      "config_id": 20001,
      "app_id": 10001,
      "env": "dev",
      "code_package_type": "miniapp",
      "code_package_path": "NULL",
      "code_package_name": "NULL",
      "base_image": "NULL",
      "pod_count": 1,
      "limits_memory": 2,
      "gpu_count": 0,
      "probe_type": "HTTP",
      "probe_check_path": "/inside/checkup",
      "pre_stop_type": "HTTP",
      "pre_stop_check_path": "/inside/prestop",
      "pre_stop_command": "NULL",
      "created_at": "2026-01-14T15:00:00+08:00",
      "updated_at": "2026-01-14T15:00:00+08:00",
      "deleted_at": null
    }
  ],
  "error": null,
  "help": "暂不提供帮助信息"
}
```

### 1.2 获取应用指定环境配置

- **GET** `/apps/{app_id}/configs/{env}`

**示例**

```bash
curl -X GET '/api/v1/apps/10001/configs/dev'
```

**成功响应示例（result 为单个 AppConfigs）**

```json
{
  "code": 1,
  "message": "查询成功",
  "result": {
    "config_id": 20001,
    "app_id": 10001,
    "env": "dev"
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

### 1.3 更新应用指定环境配置（部分更新）

- **PATCH** `/apps/{app_id}/configs/{env}`
- **请求体**：`app.UpdateAppConfigRequest`（只更新传入字段）

**示例：更新探针配置**

```bash
curl -X PATCH '/api/v1/apps/10001/configs/dev' \
  -H 'Content-Type: application/json' \
  -d '{
    "probe_type": "HTTP",
    "probe_check_path": "/healthz"
  }'
```

**成功响应示例**

```json
{
  "code": 1,
  "message": "更新成功",
  "result": null,
  "error": null,
  "help": "暂不提供帮助信息"
}
```

**说明**：多域名请使用后文 `domains` 接口（基于 `config_id`）。

---

## 2) 以 `config_id` 为主的配置接口（便于直连/系统对接）

### 2.1 按 config_id 获取配置

- **GET** `/app-configs/{config_id}`

**示例**

```bash
curl -X GET '/api/v1/app-configs/20001'
```

**成功响应示例（result 为单个 AppConfigs）**

```json
{
  "code": 1,
  "message": "查询成功",
  "result": {
    "config_id": 20001,
    "app_id": 10001,
    "env": "dev"
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

### 2.2 按 config_id 更新配置（部分更新）

- **PATCH** `/app-configs/{config_id}`
- **请求体**：`app.UpdateAppConfigRequest`

**示例：仅更新探针配置**

```bash
curl -X PATCH '/api/v1/app-configs/20001' \
  -H 'Content-Type: application/json' \
  -d '{
    "probe_type": "HTTP",
    "probe_check_path": "/healthz"
  }'
```

**成功响应示例**

```json
{
  "code": 1,
  "message": "更新成功",
  "result": null,
  "error": null,
  "help": "暂不提供帮助信息"
}
```

---

## 3) 多域名（Ingress host/path）配置：`app_config_domains`

> 多域名以 `config_id` 绑定；发布时会额外下发 Jenkins 参数 `domains`（JSON 字符串）与 `domains_list`（按 host 聚合 paths 的 JSON 字符串）。

### 3.1 查询多域名列表

- **GET** `/app-configs/{config_id}/domains`
- **返回**：`[]entity.AppConfigDomain`（包含 `id/config_id/host/path/...`）

**示例**

```bash
curl -X GET '/api/v1/app-configs/20001/domains'
```

**成功响应示例（result 为域名列表，包含 domain_id）**

```json
{
  "code": 1,
  "message": "查询成功",
  "result": [
    {
      "id": 123,
      "config_id": 20001,
      "host": "a.example.com",
      "path": "/",
      "created_at": "2026-01-14T15:00:00+08:00",
      "updated_at": "2026-01-14T15:00:00+08:00",
      "deleted_at": null
    }
  ],
  "error": null,
  "help": "暂不提供帮助信息"
}
```

### 3.2 全量覆盖写入多域名（幂等）

- **PUT** `/app-configs/{config_id}/domains`
- **请求体**：`app.UpsertDomainsRequest`
- **语义**：覆盖写入（先删除旧记录，再插入新列表）

**示例**

```bash
curl -X PUT '/api/v1/app-configs/20001/domains' \
  -H 'Content-Type: application/json' \
  -d '{
    "domains": [
      {"host":"a.example.com","path":"/"},
      {"host":"b.example.com","path":"/foo"}
    ]
  }'
```

**成功响应示例**

```json
{
  "code": 1,
  "message": "写入成功",
  "result": null,
  "error": null,
  "help": "暂不提供帮助信息"
}
```

### 3.3 新增单条域名

- **POST** `/app-configs/{config_id}/domains`
- **请求体**：`app.DomainItem`

**示例**

```bash
curl -X POST '/api/v1/app-configs/20001/domains' \
  -H 'Content-Type: application/json' \
  -d '{"host":"c.example.com","path":"/"}'
```

**成功响应示例（返回新增后的 domain 记录，包含 id）**

```json
{
  "code": 1,
  "message": "新增成功",
  "result": {
    "id": 124,
    "config_id": 20001,
    "host": "c.example.com",
    "path": "/"
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

### 3.4 删除单条域名（按 domain_id）

- **DELETE** `/app-configs/{config_id}/domains/{domain_id}`

**示例**

```bash
curl -X DELETE '/api/v1/app-configs/20001/domains/123'
```

**成功响应示例**

```json
{
  "code": 1,
  "message": "删除成功",
  "result": null,
  "error": null,
  "help": "暂不提供帮助信息"
}
```

### 3.5 修改单条域名（按 domain_id，部分更新）

- **PATCH** `/app-configs/{config_id}/domains/{domain_id}`
- **请求体**：`app.PatchDomainRequest`（可只传 `host` 或只传 `path`）

**示例：只改 path**

```bash
curl -X PATCH '/api/v1/app-configs/20001/domains/123' \
  -H 'Content-Type: application/json' \
  -d '{"path":"/new"}'
```

**成功响应示例（返回修改后的 domain）**

```json
{
  "code": 1,
  "message": "修改成功",
  "result": {
    "id": 123,
    "config_id": 20001,
    "host": "a.example.com",
    "path": "/new"
  },
  "error": null,
  "help": "暂不提供帮助信息"
}
```

---

## 4) 规范化与冲突规则（重要）

为避免 Ingress 出现 **同 host + 同 location(path)** 冲突，服务端会对输入做规范化与冲突校验：

- **host 规范化**：`trim` + `lowercase`
- **path 规范化**：
  - 空值 → `/`
  - 必须以 `/` 开头
  - `//` 会压缩为 `/`
  - 非根路径末尾 `/` 会去掉（`/foo/` → `/foo`）
- **冲突判定**：同一个 `config_id` 下，若出现重复的 `host + path`（规范化后），会返回错误（不会静默去重）。

常见错误示例：
- `多域名配置重复：host=... path=...`
- `多域名配置冲突：host=... path=...`

---

## 5) 前端接入建议

1. 页面以 **`app_id + env`** 为入口（更符合开发者心智）
2. 需要多域名时：
   - 先 `GET /apps/{app_id}/configs` 拿到对应环境的 `config_id`
   - 再用 `GET/PUT/POST/PATCH/DELETE /app-configs/{config_id}/domains...` 进行管理
