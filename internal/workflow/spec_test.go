package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeAndValidateAppliesDefaults(t *testing.T) {
	spec := WorkflowSpec{
		SchemaVersion: 1,
		Name:          " Demo ",
		Steps: []StepSpec{{
			Key:  " smoke ",
			Name: " 冒烟检查 ",
			Uses: NoopUses,
		}},
	}
	normalized, err := NormalizeAndValidate(spec, DefaultRegistry())
	if err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	if normalized.Name != "Demo" || normalized.Steps[0].Key != "smoke" {
		t.Fatalf("values were not normalized: %#v", normalized)
	}
	if normalized.Steps[0].TimeoutSeconds != defaultTimeoutSeconds {
		t.Fatalf("timeout = %d", normalized.Steps[0].TimeoutSeconds)
	}
	if normalized.Steps[0].OnFailure != FailureStop {
		t.Fatalf("on_failure = %q", normalized.Steps[0].OnFailure)
	}
	if string(normalized.Steps[0].With) != `{}` {
		t.Fatalf("with = %s", normalized.Steps[0].With)
	}
}

func TestNormalizeAndValidateRejectsDuplicateAndUnknownExecutor(t *testing.T) {
	spec := WorkflowSpec{
		SchemaVersion: 1,
		Name:          "bad",
		Steps: []StepSpec{
			{Key: "same", Name: "first", Uses: NoopUses, With: json.RawMessage(`{}`)},
			{Key: "same", Name: "second", Uses: "other.task@v1", With: json.RawMessage(`{}`)},
		},
	}
	_, err := NormalizeAndValidate(spec, DefaultRegistry())
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %T %v", err, err)
	}
	joined := strings.Join(validation.Problems, "|")
	if !strings.Contains(joined, "重复") || !strings.Contains(joined, "未注册") {
		t.Fatalf("problems = %v", validation.Problems)
	}
}

func TestDecodeSpecRejectsUnknownFields(t *testing.T) {
	_, err := DecodeSpec(strings.NewReader(`{
        "schema_version":1,
        "name":"demo",
        "steps":[{"key":"one","name":"one","uses":"builtin.noop@v1","with":{},"unexpected":true}]
    }`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("error = %v", err)
	}
}

type unavailableExecutor struct{ *NoopExecutor }

func (u unavailableExecutor) Descriptor() Descriptor {
	d := u.NoopExecutor.Descriptor()
	d.Uses = "test.unavailable@v1"
	return d
}

func (u unavailableExecutor) Available(context.Context) error { return errors.New("not configured") }

func TestRegistryRejectsDuplicateAndReportsAvailability(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewNoopExecutor()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(NewNoopExecutor()); err == nil {
		t.Fatal("duplicate registration should fail")
	}
	if err := registry.Register(unavailableExecutor{NewNoopExecutor()}); err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors(context.Background())
	if len(descriptors) != 2 || descriptors[1].Uses != "test.unavailable@v1" {
		t.Fatalf("descriptors = %#v", descriptors)
	}
	if descriptors[1].Available || descriptors[1].UnavailableReason != "not configured" {
		t.Fatalf("availability = %#v", descriptors[1])
	}
}

func TestNoopStrictConfigurationAndOutcome(t *testing.T) {
	executor := NewNoopExecutor()
	if err := executor.Validate(json.RawMessage(`{"unknown":true}`)); err == nil {
		t.Fatal("unknown config field should fail")
	}
	result, err := executor.Start(context.Background(), StartRequest{
		Config: json.RawMessage(`{"message":"expected","outcome":"failed","output":{"code":7}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != ResultFailed || result.Message != "expected" || string(result.Output) != `{"code":7}` {
		t.Fatalf("result = %#v", result)
	}
	for _, config := range []string{
		`{"output":{"api_token":"plain"}}`,
		`{"output":{"nested":[{"clientSecret":"plain"}]}}`,
	} {
		if err := executor.Validate(json.RawMessage(config)); err == nil {
			t.Fatalf("sensitive output config %s should fail", config)
		}
	}
}
