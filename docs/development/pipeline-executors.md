# 流水线步骤执行器扩展指南

## 1. 适用范围

本文说明如何为 Ares 增加新的流水线步骤。执行器负责“如何执行一个步骤”，工作流引擎负责顺序、状态持久化和失败策略。新增执行器不应修改工作流状态机，也不应在 `publish` 核心包中增加平台判断分支。

## 2. 命名和版本

执行器使用 `namespace.name@version`：

- `builtin.noop@v1`
- `jenkins.job@v1`
- 未来示例：`webhook.call@v1`、`kubernetes.apply@v1`

任何破坏配置或输出契约的修改必须发布新版本。旧版本只要仍有非终态运行就不能从注册表移除。

## 3. 接口职责

执行器至少实现：

```go
type Executor interface {
    Descriptor() Descriptor
    Validate(config json.RawMessage) error
    Start(ctx context.Context, request StartRequest) (Result, error)
    Reconcile(ctx context.Context, request ReconcileRequest) (Result, error)
}
```

- `Descriptor` 返回稳定 `uses`、展示名、说明、配置 Schema 和能力。
- `Validate` 只做确定性的配置校验，不产生外部副作用。
- `Start` 发起一次执行；同步步骤可以直接返回终态。
- `Reconcile` 查询异步执行；必须能根据持久化的 opaque external reference 恢复。
- 所有网络与等待必须尊重 `context.Context`。

可选实现日志和取消接口。没有相应能力时，描述符必须明确返回 `false`，UI 不展示不可用动作。
Registry 会核对静态能力和接口实现：`capabilities.logs=true` 必须实现 `LogReader`，实现了
`LogReader` 也必须声明该能力；不一致时启动注册失败。执行器暂时不可用只影响
`Descriptor.Available`，不能动态改写已经声明的静态能力。

## 4. 请求与返回约束

`StartRequest` 会提供：

- 任务、步骤、attempt 和稳定幂等键；
- 应用与 AppConfig 的只读快照；
- 环境代码；
- 发布 ref/branch、发布者和显式 inputs；
- 已完成步骤的内部输出（只供后续步骤使用，不通过公开任务 API 返回）。

执行结果必须映射到通用状态。外部引用使用 JSON，例如 Jenkins 可以保存：

```json
{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","queue_id":123,"build_id":456}
```

不要让工作流引擎解析该结构。只有拥有它的执行器可以解释 external reference。

Start 与 Reconcile 的错误语义、总时限以及 `timed_out` / `outcome_unknown` 处理见[任务状态与恢复边界](task-lifecycle.md)。轮询失败不能映射为外部构建失败；返回 error 时也应携带刚解析到的新外部引用。

`Result.Message` 会进入公开任务接口，只能返回稳定、可公开的状态说明，不能透传上游响应正文、Header、URL 或原始网络错误。执行器返回的 `error` 默认会被引擎转换成通用公开文案。`Result.Output` 只用于步骤间内部传递，虽不通过 API 返回，仍会持久化；引擎会在落库前递归拒绝常见敏感键，执行器自身也必须先校验并避免把凭据放入普通字段。

## 5. 配置和 Secret

- 为配置提供严格 JSON Schema；保存流程时同时调用 `Validate`。
- 在 Secret Resolver 上线前，执行器配置不得保存明文凭据。`jenkins.job@v1` 会拒绝常见 token/password/secret/credential 参数名；凭据应放在 Jenkins Credentials 中并由 Job 自行引用。
- 未知字段默认拒绝，防止拼写错误静默生效。
- Secret 只允许引用，如 `secret://integrations/jenkins/default/token`。
- 不要将解析后的 Secret 写入 Result、日志、错误或输出。
- 模板值只从引擎提供的白名单上下文解析，禁止解释用户提供的脚本或表达式。

## 6. 通用日志能力

W03 的通用日志入口、cursor 与 SSE 事件见[通用任务步骤日志 API](task-step-logs-api.md)和
[ADR-0003](../architecture/decisions/0003-generic-step-logs.md)。支持日志的执行器实现：

```go
type LogReader interface {
    ReadLogs(context.Context, LogRequest) (LogChunk, error)
}
```

服务端只会向 `LogRequest` 放入经过鉴权和归属校验的 task ID、step key、数据库步骤快照中的
opaque external reference，以及经过通用边界校验的 cursor。执行器不得接受客户端补充或覆盖 Job、
Build ID、地址、运行 ID 或其他日志来源标识。

`ReadLogs` 是一次有界读取，不负责持有浏览器 SSE 连接。通用 API 层负责轮询、心跳、会话复验、
写入 deadline、连接准入和终止；执行器必须：

1. 在任何外连前严格解码并验证自己的 external reference，拒绝未知字段和不完整引用；
2. 把引用中的集成实例标识与同一次读取持有的不可变 client snapshot 比较，不能因热更新把历史
   任务导向另一实例；
3. 尊重 context 取消与 deadline，不创建脱离请求生命周期的 goroutine；
4. 限制上游响应和单个 chunk 的字节数，不等待无界正文或把整份日志读入内存；
5. 返回有效 UTF-8、无 CR/LF/NUL、最多 256 bytes 且符合本执行器语义的下一 cursor；
   对有可比顺序的 cursor，执行器必须拒绝回退；
6. 只有能够确认上游日志结束时才返回 `EOF=true`，暂时没有增量不等于 EOF；
7. 将上游失败映射为稳定分类，不把原始错误、URL、Header、响应正文或 Secret 写入返回值或日志。

cursor 由执行器定义且是其版本化契约。`jenkins.job@v1` 使用 progressiveText 的无符号十进制
int64 byte offset；其他执行器不能假设 cursor 是数字。改变已发布执行器的 cursor 语义必须发布
新的 `uses` 版本或提供显式兼容。

不支持日志的执行器保持 `capabilities.logs=false`，并且不实现空壳 LogReader。步骤尚未取得足以
定位日志的 external reference 属于“日志未就绪”，也不能伪装为成功空日志。

## 7. 幂等与故障处理

Ares 能保证数据库状态转移只被一个 Worker 认领，但无法跨数据库和外部平台提供 exactly-once。执行器应：

1. 将 `task_id/step_key/attempt` 传给外部系统作为幂等键。
2. 如果外部系统支持按幂等键查询，`Start` 超时后先查询再决定是否重试。
3. 网络、限流和 5xx 返回可重试错误；配置或业务失败返回不可重试错误。
4. 已取得外部引用时，对未知结果保持 `unknown/running` 并交给 `Reconcile`，不要盲目再次执行部署；触发请求在取得引用前结果不明确仍是首版限制，执行器应依赖外部平台幂等能力降低重复风险。
5. 外部引用必须包含配置实例或版本标识，避免集成设置热更新后查询到另一套系统。
6. 只有在能够证明尚未产生任何外部副作用时，`Start` 才能包装返回 `workflow.ErrExecutorUnavailable`。引擎收到该错误会释放步骤 claim 并在后续轮询重新调用 `Start`；请求已经发出、结果不明或已经取得外部引用后绝不能使用它，否则会造成重复执行。

## 8. 注册步骤

执行器在应用启动时注册：

```go
registry := workflow.NewRegistry()
if err := registry.Register(noop.New()); err != nil { return err }
if err := registry.Register(jenkinsstep.New(clientProvider)); err != nil { return err }
```

相同 `uses` 重复注册必须使启动失败。执行器不可通过包级隐式 `init()` 注册，以便测试能够显式构造依赖。

## 9. 测试清单

每个执行器至少覆盖：

- 描述符和版本稳定；
- 有效、缺失、未知和类型错误配置；
- 同步成功或异步启动；
- 外部成功、失败、取消、未知状态映射；
- context 取消和超时；
- external reference 序列化后可恢复；
- 幂等键透传；
- descriptor 日志能力与 LogReader 实现一致；
- 日志 cursor 首次读取、增量、空增量、EOF、续传、非法值以及执行器可判定的回退值；
- external reference 或实例不匹配时在外连前失败；
- 上游及 chunk 字节上限、context 取消后无残留请求或 goroutine；
- 日志/错误不泄漏 Secret、external reference、URL 或上游正文；
- 未配置外部集成时 `Available` 和错误信息明确。

可以使用执行器契约测试套件复用上述断言。集成测试不得要求开发者本机一定安装 Jenkins、Kubernetes、Redis 或 RabbitMQ。

## 10. 评审检查

- 核心工作流包没有新增平台专用 import 或状态。
- 步骤可以插入任意位置且不依赖固定前后步骤名称。
- 配置变更有明确的执行器版本策略。
- 同一请求重放不会无提示地产生重复破坏性操作。
- 所有资源有 timeout，所有 goroutine 可退出。
- 新能力已写入描述符并在前端按能力呈现。
- 日志只能从服务端步骤快照定位来源，cursor 重放不产生执行副作用。
