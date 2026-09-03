package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"ares/internal/security"
)

const NoopUses = "builtin.noop@v1"

var noopSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"message":{"type":"string","maxLength":255},"outcome":{"type":"string","enum":["succeeded","failed"]},"output":{"type":"object"}}}`)

type NoopExecutor struct{}

type noopConfig struct {
	Message string          `json:"message"`
	Outcome string          `json:"outcome"`
	Output  json.RawMessage `json:"output"`
}

func NewNoopExecutor() *NoopExecutor { return &NoopExecutor{} }

func (n *NoopExecutor) Descriptor() Descriptor {
	return Descriptor{
		Uses:         NoopUses,
		Name:         "内置 Noop",
		Description:  "无外部依赖的同步步骤，供 Demo、验证和占位使用",
		ConfigSchema: append(json.RawMessage(nil), noopSchema...),
		Capabilities: Capabilities{},
	}
}

func decodeNoopConfig(config json.RawMessage) (noopConfig, error) {
	if len(config) == 0 || string(config) == "null" {
		config = json.RawMessage(`{}`)
	}
	var value noopConfig
	decoder := json.NewDecoder(bytes.NewReader(config))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return noopConfig{}, fmt.Errorf("配置格式错误：%w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return noopConfig{}, fmt.Errorf("配置只能包含一个 JSON 对象")
	}
	if len([]rune(value.Message)) > 255 {
		return noopConfig{}, fmt.Errorf("message 不能超过 255 个字符")
	}
	if value.Outcome == "" {
		value.Outcome = ResultSucceeded
	}
	if value.Outcome != ResultSucceeded && value.Outcome != ResultFailed {
		return noopConfig{}, fmt.Errorf("outcome 只支持 succeeded 或 failed")
	}
	if len(value.Output) > 0 {
		var output map[string]any
		if string(value.Output) == "null" || json.Unmarshal(value.Output, &output) != nil || output == nil {
			return noopConfig{}, fmt.Errorf("output 必须是 JSON 对象")
		}
		if err := security.ValidateNoSensitiveKeys(output, "output"); err != nil {
			return noopConfig{}, fmt.Errorf("%w；Noop output 会持久化，请勿写入明文凭据", err)
		}
	}
	return value, nil
}

func (n *NoopExecutor) Validate(config json.RawMessage) error {
	_, err := decodeNoopConfig(config)
	return err
}

func (n *NoopExecutor) Start(_ context.Context, request StartRequest) (Result, error) {
	config, err := decodeNoopConfig(request.Config)
	if err != nil {
		return Result{}, err
	}
	output := config.Output
	if len(output) == 0 {
		output = json.RawMessage(`{}`)
	}
	return Result{State: config.Outcome, Output: output, Message: config.Message}, nil
}

func (n *NoopExecutor) Reconcile(_ context.Context, _ ReconcileRequest) (Result, error) {
	return Result{}, fmt.Errorf("%s 是同步执行器，不支持协调运行中任务", NoopUses)
}

func DefaultRegistry() *Registry {
	registry := NewRegistry()
	if err := registry.Register(NewNoopExecutor()); err != nil {
		panic(err)
	}
	return registry
}
