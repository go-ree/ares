package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	defaultTimeoutSeconds = 3600
	maxWorkflowSteps      = 100
)

var (
	stepKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,62}$`)
	usesPattern    = regexp.MustCompile(`^[a-z][a-z0-9.-]*\.[a-z][a-z0-9.-]*@v[1-9][0-9]*$`)
)

type ValidationError struct {
	Problems []string `json:"problems"`
}

func (e *ValidationError) Error() string {
	return "工作流校验失败：" + strings.Join(e.Problems, "；")
}

func DecodeSpec(reader io.Reader) (WorkflowSpec, error) {
	var spec WorkflowSpec
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return WorkflowSpec{}, fmt.Errorf("解析工作流规范: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return WorkflowSpec{}, errors.New("工作流规范只能包含一个 JSON 对象")
		}
		return WorkflowSpec{}, fmt.Errorf("解析工作流规范尾部内容: %w", err)
	}
	return spec, nil
}

func DecodeSpecJSON(data []byte) (WorkflowSpec, error) {
	return DecodeSpec(bytes.NewReader(data))
}

// NormalizeAndValidate applies documented defaults and validates both the
// workflow envelope and each executor-owned configuration.
func NormalizeAndValidate(spec WorkflowSpec, registry *Registry) (WorkflowSpec, error) {
	problems := make([]string, 0)
	if spec.SchemaVersion != SchemaVersionV1 {
		problems = append(problems, fmt.Sprintf("schema_version 必须为 %d", SchemaVersionV1))
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" || len([]rune(spec.Name)) > 120 {
		problems = append(problems, "name 长度必须为 1-120")
	}
	if len(spec.Steps) == 0 {
		problems = append(problems, "steps 至少包含一个步骤")
	}
	if len(spec.Steps) > maxWorkflowSteps {
		problems = append(problems, fmt.Sprintf("steps 不能超过 %d 个", maxWorkflowSteps))
	}

	seen := make(map[string]struct{}, len(spec.Steps))
	for index := range spec.Steps {
		step := &spec.Steps[index]
		prefix := fmt.Sprintf("steps[%d]", index)
		step.Key = strings.TrimSpace(step.Key)
		step.Name = strings.TrimSpace(step.Name)
		step.Uses = strings.TrimSpace(step.Uses)
		step.Category = strings.TrimSpace(step.Category)
		step.OnFailure = strings.TrimSpace(step.OnFailure)

		if !stepKeyPattern.MatchString(step.Key) {
			problems = append(problems, prefix+".key 格式无效")
		} else if _, exists := seen[step.Key]; exists {
			problems = append(problems, prefix+".key 在流程内重复")
		} else {
			seen[step.Key] = struct{}{}
		}
		if step.Name == "" || len([]rune(step.Name)) > 120 {
			problems = append(problems, prefix+".name 长度必须为 1-120")
		}
		if !usesPattern.MatchString(step.Uses) {
			problems = append(problems, prefix+".uses 格式无效")
		}
		if len(step.Category) > 32 {
			problems = append(problems, prefix+".category 不能超过 32 个字符")
		}
		if step.TimeoutSeconds == 0 {
			step.TimeoutSeconds = defaultTimeoutSeconds
		}
		if step.TimeoutSeconds < 1 || step.TimeoutSeconds > 86400 {
			problems = append(problems, prefix+".timeout_seconds 必须在 1-86400 之间")
		}
		if step.OnFailure == "" {
			step.OnFailure = FailureStop
		}
		if step.OnFailure != FailureStop && step.OnFailure != FailureContinue {
			problems = append(problems, prefix+".on_failure 只支持 stop 或 continue")
		}
		if len(step.With) == 0 || string(step.With) == "null" {
			step.With = json.RawMessage(`{}`)
		}
		if !json.Valid(step.With) {
			problems = append(problems, prefix+".with 不是有效 JSON")
			continue
		}
		executor, found := registry.Get(step.Uses)
		if !found {
			problems = append(problems, prefix+".uses 未注册："+step.Uses)
			continue
		}
		if err := executor.Validate(step.With); err != nil {
			problems = append(problems, prefix+".with："+err.Error())
		}
	}
	if len(problems) > 0 {
		return WorkflowSpec{}, &ValidationError{Problems: problems}
	}
	return spec, nil
}
