package jenkinsstep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-ree/ares/internal/integration"
	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/security"
	"github.com/go-ree/ares/internal/workflow"
)

const Uses = "jenkins.job@v1"

var configSchema = json.RawMessage(`{
  "type":"object",
  "additionalProperties":false,
  "required":["job"],
  "properties":{
    "integration":{"type":"string","enum":["jenkins/default"]},
    "job":{"type":"string","minLength":1,"maxLength":100},
    "parameters":{"type":"object","additionalProperties":{"type":"string"}}
  }
}`)

type Config struct {
	Integration string            `json:"integration,omitempty"`
	Job         string            `json:"job"`
	Parameters  map[string]string `json:"parameters,omitempty"`
}

type externalReference struct {
	Integration string `json:"integration"`
	Address     string `json:"address,omitempty"`
	Job         string `json:"job"`
	QueueID     int64  `json:"queue_id,omitempty"`
	BuildID     int64  `json:"build_id,omitempty"`
}

type jenkinsClient interface {
	Address() string
	QueueBuildTaskContext(context.Context, string, map[string]string) (int64, string, error)
	GetQueueBuildStateContext(context.Context, int64) (jenkins.QueueBuildState, error)
	GetBuildStatusContext(context.Context, string, int64) (string, error)
}

type jenkinsLogClient interface {
	Address() string
	ReadBuildLogChunkContext(context.Context, string, int64, int64) (jenkins.BuildLogPage, error)
}

type Executor struct {
	acquire          func() jenkinsClient
	acquireOperation func() (jenkinsClient, func())
	ensureCurrent    func(context.Context) error
}

func New() *Executor {
	return &Executor{
		acquire: func() jenkinsClient {
			snapshot := jenkins.Acquire()
			if snapshot == nil {
				return nil
			}
			return snapshot
		},
		acquireOperation: func() (jenkinsClient, func()) {
			snapshot, release := jenkins.AcquireForOperation()
			if snapshot == nil {
				return nil, release
			}
			return snapshot, release
		},
		ensureCurrent: integration.EnsureJenkinsCurrent,
	}
}

func (e *Executor) operationClient() (jenkinsClient, func()) {
	if e != nil && e.acquireOperation != nil {
		return e.acquireOperation()
	}
	if e == nil || e.acquire == nil {
		return nil, func() {}
	}
	return e.acquire(), func() {}
}

func (e *Executor) Descriptor() workflow.Descriptor {
	return workflow.Descriptor{
		Uses:         Uses,
		Name:         "Jenkins Job",
		Description:  "触发并跟踪一个可参数化 Jenkins Job",
		ConfigSchema: append(json.RawMessage(nil), configSchema...),
		Capabilities: workflow.Capabilities{Logs: true},
	}
}

func (e *Executor) Available(ctx context.Context) error {
	if err := e.ensureRuntimeCurrent(ctx); err != nil {
		return err
	}
	if e == nil || e.acquire == nil || e.acquire() == nil {
		return errors.New("Jenkins 集成未配置或未连接")
	}
	return nil
}

func (e *Executor) ensureRuntimeCurrent(ctx context.Context) error {
	if e == nil || e.ensureCurrent == nil {
		return nil
	}
	if err := e.ensureCurrent(ctx); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w：Jenkins 集成设置暂不可用", workflow.ErrExecutorUnavailable)
	}
	return nil
}

func (e *Executor) Validate(raw json.RawMessage) error {
	_, err := decodeConfig(raw)
	return err
}

func decodeConfig(raw json.RawMessage) (Config, error) {
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("配置格式错误：%w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("配置只能包含一个 JSON 对象")
	}
	config.Integration = strings.TrimSpace(config.Integration)
	if config.Integration == "" {
		config.Integration = "jenkins/default"
	}
	if config.Integration != "jenkins/default" {
		return Config{}, fmt.Errorf("首版只支持 integration=jenkins/default")
	}
	config.Job = strings.TrimSpace(config.Job)
	if !validExternalJobName(config.Job) {
		return Config{}, errors.New("job 必须是 1-100 字节的合法 Jenkins 任务路径")
	}
	for key := range config.Parameters {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return Config{}, errors.New("parameters 的键不能为空")
		}
		if security.IsSensitiveKey(trimmedKey) {
			return Config{}, fmt.Errorf("parameters.%s 疑似敏感字段；首版不保存明文 Secret，请在 Jenkins 凭据库和 Job 内解析", trimmedKey)
		}
	}
	return config, nil
}

func (e *Executor) Start(ctx context.Context, request workflow.StartRequest) (workflow.Result, error) {
	config, err := decodeConfig(request.Config)
	if err != nil {
		return workflow.Result{}, err
	}
	if err := e.ensureRuntimeCurrent(ctx); err != nil {
		return workflow.Result{}, err
	}
	// Pin one immutable runtime snapshot for the whole operation. Calling
	// Available and acquiring again would allow a settings hot-switch between
	// the check and the external request.
	client, release := e.operationClient()
	defer release()
	if client == nil {
		return workflow.Result{}, fmt.Errorf("%w：Jenkins 集成未配置或未连接", workflow.ErrExecutorUnavailable)
	}

	parameters, err := releaseParameters(request.Release.Inputs)
	if err != nil {
		return workflow.Result{}, err
	}
	for key, value := range config.Parameters {
		parameters[key] = value
	}
	parameters["app_name"] = request.Release.AppName
	parameters["env"] = request.Release.Env
	parameters["branch"] = request.Release.Ref
	parameters["publisher"] = request.Release.Publisher
	parameters["task_id"] = strconv.Itoa(request.TaskID)
	parameters["ares_task_id"] = strconv.Itoa(request.TaskID)
	parameters["ares_step_key"] = request.StepKey
	parameters["ares_attempt"] = strconv.Itoa(request.Attempt)
	parameters["ares_idempotency_key"] = request.IdempotencyKey

	queueID, _, err := client.QueueBuildTaskContext(ctx, config.Job, parameters)
	if err != nil {
		return workflow.Result{}, fmt.Errorf("触发 Jenkins Job %s: %w", config.Job, err)
	}
	reference, err := json.Marshal(externalReference{
		Integration: config.Integration,
		Address:     client.Address(),
		Job:         config.Job,
		QueueID:     queueID,
	})
	if err != nil {
		return workflow.Result{}, fmt.Errorf("保存 Jenkins 外部引用: %w", err)
	}
	return workflow.Result{State: workflow.ResultRunning, ExternalReference: reference}, nil
}

func (e *Executor) Reconcile(ctx context.Context, request workflow.ReconcileRequest) (workflow.Result, error) {
	reference, referenceErr := decodeExternalReference(request.ExternalReference)
	if referenceErr != nil {
		return invalidReferenceResult(request.ExternalReference, referenceErr.Error()), nil
	}
	if err := e.ensureRuntimeCurrent(ctx); err != nil {
		return workflow.Result{}, err
	}
	// Reconciliation must query through the same immutable snapshot that was
	// checked against the persisted external reference below.
	client, release := e.operationClient()
	defer release()
	if client == nil {
		return workflow.Result{}, fmt.Errorf("%w：Jenkins 集成未配置或未连接", workflow.ErrExecutorUnavailable)
	}
	if client.Address() != reference.Address {
		return workflow.Result{
			State:             workflow.ResultOutcomeUnknown,
			ExternalReference: append(json.RawMessage(nil), request.ExternalReference...),
			Message:           "Jenkins 连接已变更，无法继续查询原实例任务",
		}, nil
	}
	if reference.BuildID <= 0 {
		queueState, err := client.GetQueueBuildStateContext(ctx, reference.QueueID)
		if err != nil {
			return workflow.Result{}, fmt.Errorf("%w：Jenkins 队列状态暂不可用", workflow.ErrExecutorUnavailable)
		}
		if queueState.Cancelled {
			return workflow.Result{
				State: workflow.ResultCancelled, ExternalReference: append(json.RawMessage(nil), request.ExternalReference...),
				Message: "Jenkins 队列任务已取消",
			}, nil
		}
		if queueState.BuildID <= 0 {
			return workflow.Result{
				State: workflow.ResultRunning, ExternalReference: append(json.RawMessage(nil), request.ExternalReference...),
				Message: "Jenkins 任务仍在队列中",
			}, nil
		}
		reference.BuildID = queueState.BuildID
		updatedReference, err := json.Marshal(reference)
		if err != nil {
			return workflow.Result{}, fmt.Errorf("更新 Jenkins 外部引用: %w", err)
		}
		request.ExternalReference = updatedReference
	}
	status, err := client.GetBuildStatusContext(ctx, reference.Job, reference.BuildID)
	if err != nil {
		return workflow.Result{ExternalReference: append(json.RawMessage(nil), request.ExternalReference...)}, fmt.Errorf("%w：Jenkins 构建状态暂不可用", workflow.ErrExecutorUnavailable)
	}
	result := workflow.Result{ExternalReference: append(json.RawMessage(nil), request.ExternalReference...)}
	switch status {
	case "RUNNING":
		result.State = workflow.ResultRunning
	case "SUCCESS":
		result.State = workflow.ResultSucceeded
	case "FAILURE":
		result.State = workflow.ResultFailed
		result.Message = "Jenkins 构建失败"
	case "ABORTED":
		result.State = workflow.ResultCancelled
		result.Message = "Jenkins 构建已取消"
	case "UNSTABLE", "NOT_BUILT":
		result.State = workflow.ResultFailed
		result.Message = "Jenkins 构建终态：" + status
	default:
		result.State = workflow.ResultUnknown
		result.Message = "Jenkins 构建状态尚无法识别，将继续查询"
	}
	return result, nil
}

func (e *Executor) ReadLogs(ctx context.Context, request workflow.LogRequest) (workflow.LogChunk, error) {
	reference, err := decodeExternalReference(request.ExternalReference)
	if err != nil {
		return workflow.LogChunk{}, fmt.Errorf("%w: invalid Jenkins external reference", workflow.ErrLogSourceMismatch)
	}
	if reference.BuildID <= 0 {
		return workflow.LogChunk{}, workflow.ErrLogsNotReady
	}
	start, err := parseLogCursor(request.Cursor)
	if err != nil {
		return workflow.LogChunk{}, err
	}
	if err := e.ensureRuntimeCurrent(ctx); err != nil {
		return workflow.LogChunk{}, err
	}
	client, release := e.operationClient()
	defer release()
	if client == nil {
		return workflow.LogChunk{}, workflow.ErrExecutorUnavailable
	}
	logClient, ok := client.(jenkinsLogClient)
	if !ok {
		return workflow.LogChunk{}, errors.New("Jenkins client does not support progressive logs")
	}
	// The operation lock makes this comparison and the following request
	// atomic with respect to a settings hot-switch. No network request may
	// happen before the persisted instance identity has matched.
	if logClient.Address() != reference.Address {
		return workflow.LogChunk{}, workflow.ErrLogSourceMismatch
	}
	page, err := logClient.ReadBuildLogChunkContext(ctx, reference.Job, reference.BuildID, start)
	if err != nil {
		return workflow.LogChunk{}, err
	}
	return workflow.LogChunk{
		Content: page.Content, Cursor: strconv.FormatInt(page.NextStart, 10), EOF: page.EOF,
	}, nil
}

func parseLogCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > 19 {
		return 0, workflow.ErrInvalidLogCursor
	}
	for _, character := range []byte(cursor) {
		if character < '0' || character > '9' {
			return 0, workflow.ErrInvalidLogCursor
		}
	}
	value, err := strconv.ParseUint(cursor, 10, 63)
	if err != nil {
		return 0, workflow.ErrInvalidLogCursor
	}
	return int64(value), nil
}

func decodeExternalReference(raw json.RawMessage) (externalReference, error) {
	var reference externalReference
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil {
		return externalReference{}, errors.New("Jenkins 外部引用无效")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return externalReference{}, errors.New("Jenkins 外部引用只能包含一个 JSON 对象")
	}
	if reference.Integration != "jenkins/default" {
		return externalReference{}, errors.New("Jenkins 外部引用缺少或不支持 integration")
	}
	normalizedAddress, err := jenkins.NormalizeAddress(reference.Address)
	if err != nil || normalizedAddress == "" {
		return externalReference{}, errors.New("Jenkins 外部引用缺少或包含无效的实例地址")
	}
	if !validExternalJobName(reference.Job) ||
		reference.QueueID < 0 || reference.BuildID < 0 || (reference.QueueID == 0 && reference.BuildID == 0) {
		return externalReference{}, errors.New("Jenkins 外部引用缺少合法的 job 及 queue_id/build_id")
	}
	reference.Address = normalizedAddress
	return reference, nil
}

func validExternalJobName(job string) bool {
	if job == "" || len(job) > 100 || job != strings.TrimSpace(job) {
		return false
	}
	for _, character := range job {
		if unicode.IsControl(character) {
			return false
		}
	}
	for _, segment := range strings.Split(job, "/") {
		if segment == "" || segment == "." || segment == ".." || segment != strings.TrimSpace(segment) {
			return false
		}
	}
	return true
}

func invalidReferenceResult(reference json.RawMessage, message string) workflow.Result {
	return workflow.Result{
		State:             workflow.ResultOutcomeUnknown,
		ExternalReference: append(json.RawMessage(nil), reference...),
		Message:           message,
	}
}

func releaseParameters(raw json.RawMessage) (map[string]string, error) {
	parameters := make(map[string]string)
	if len(raw) == 0 || string(raw) == "null" {
		return parameters, nil
	}
	var input map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("发布 inputs 不是 JSON 对象：%w", err)
	}
	for key, value := range input {
		switch typed := value.(type) {
		case nil:
			continue
		case string:
			parameters[key] = typed
		case json.Number:
			parameters[key] = typed.String()
		case bool:
			parameters[key] = strconv.FormatBool(typed)
		default:
			encoded, err := json.Marshal(typed)
			if err != nil {
				return nil, fmt.Errorf("序列化发布参数 %s: %w", key, err)
			}
			parameters[key] = string(encoded)
		}
	}
	return parameters, nil
}
