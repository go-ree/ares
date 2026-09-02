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

## 4. 请求与返回约束

`StartRequest` 会提供：

- 任务、步骤、attempt 和稳定幂等键；
- 应用与 AppConfig 的只读快照；
- 环境代码；
- 发布 ref/branch、发布者和显式 inputs；
- 已完成步骤的非敏感输出。

执行结果必须映射到通用状态。外部引用使用 JSON，例如 Jenkins 可以保存：

```json
{"job":"demo-ci","queue_id":123,"build_number":456,"integration":"jenkins/default"}
```

不要让工作流引擎解析该结构。只有拥有它的执行器可以解释 external reference。

## 5. 配置和 Secret

- 为配置提供严格 JSON Schema；保存流程时同时调用 `Validate`。
- 未知字段默认拒绝，防止拼写错误静默生效。
- Secret 只允许引用，如 `secret://integrations/jenkins/default/token`。
- 不要将解析后的 Secret 写入 Result、日志、错误或输出。
- 模板值只从引擎提供的白名单上下文解析，禁止解释用户提供的脚本或表达式。

## 6. 幂等与故障处理

Ares 能保证数据库状态转移只被一个 Worker 认领，但无法跨数据库和外部平台提供 exactly-once。执行器应：

1. 将 `task_id/step_key/attempt` 传给外部系统作为幂等键。
2. 如果外部系统支持按幂等键查询，`Start` 超时后先查询再决定是否重试。
3. 网络、限流和 5xx 返回可重试错误；配置或业务失败返回不可重试错误。
4. 对未知结果保持 `unknown/running` 并交给 `Reconcile`，不要盲目再次执行部署。
5. 外部引用必须包含配置实例或版本标识，避免集成设置热更新后查询到另一套系统。

## 7. 注册步骤

执行器在应用启动时注册：

```go
registry := workflow.NewRegistry()
if err := registry.Register(noop.New()); err != nil { return err }
if err := registry.Register(jenkinsstep.New(clientProvider)); err != nil { return err }
```

相同 `uses` 重复注册必须使启动失败。执行器不可通过包级隐式 `init()` 注册，以便测试能够显式构造依赖。

## 8. 测试清单

每个执行器至少覆盖：

- 描述符和版本稳定；
- 有效、缺失、未知和类型错误配置；
- 同步成功或异步启动；
- 外部成功、失败、取消、未知状态映射；
- context 取消和超时；
- external reference 序列化后可恢复；
- 幂等键透传；
- 日志/错误不泄漏 Secret；
- 未配置外部集成时 `Available` 和错误信息明确。

可以使用执行器契约测试套件复用上述断言。集成测试不得要求开发者本机一定安装 Jenkins、Kubernetes、Redis 或 RabbitMQ。

## 9. 评审检查

- 核心工作流包没有新增平台专用 import 或状态。
- 步骤可以插入任意位置且不依赖固定前后步骤名称。
- 配置变更有明确的执行器版本策略。
- 同一请求重放不会无提示地产生重复破坏性操作。
- 所有资源有 timeout，所有 goroutine 可退出。
- 新能力已写入描述符并在前端按能力呈现。
