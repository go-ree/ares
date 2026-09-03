package publish

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/entity"
)

func TestNormalizeLegacyPipelineParameters(t *testing.T) {
	parameters := map[string]string{
		"domain":              " NULL ",
		"code_package_path":   "null",
		"code_package_name":   "  ",
		"base_image":          " alpine:3 ",
		"pre_stop_command":    "NULLABLE",
		"unrelated_parameter": "NULL",
	}

	normalizeLegacyPipelineParameters(parameters)
	if parameters["domain"] != "" || parameters["code_package_path"] != "" || parameters["code_package_name"] != "" {
		t.Fatalf("legacy nullable parameters were not cleared: %#v", parameters)
	}
	if parameters["base_image"] != "alpine:3" || parameters["pre_stop_command"] != "NULLABLE" {
		t.Fatalf("normal parameter values were changed incorrectly: %#v", parameters)
	}
	if parameters["unrelated_parameter"] != "NULL" {
		t.Fatal("normalization must be limited to the field allowlist")
	}
}

func TestValidateReleaseExtraDataRejectsSensitiveKeysRecursively(t *testing.T) {
	for _, value := range []any{
		map[string]any{"deploy_token": "secret"},
		map[string]any{"accessToken": "secret"},
		map[string]any{"clientSecret": "secret"},
		map[string]any{"apiKey": "secret"},
		map[string]any{"dbPassword": "secret"},
		map[string]any{"config": map[string]any{"private-key": "secret"}},
		map[string]any{"items": []any{map[string]any{"api.password": "secret"}}},
		map[string]string{"credential": "secret"},
	} {
		if err := validateReleaseExtraData(value, "extra_data"); err == nil || !strings.Contains(err.Error(), "敏感字段") {
			t.Fatalf("validateReleaseExtraData(%#v) error = %v", value, err)
		}
	}
	if err := validateReleaseExtraData(map[string]any{
		"mini_type": "mp-weixin",
		"metadata":  map[string]any{"region": "cn"},
	}, "extra_data"); err != nil {
		t.Fatalf("normal release inputs were rejected: %v", err)
	}
}

func TestCreatePublishRequestRequiresObjectExtraData(t *testing.T) {
	var request CreatePublishRequest
	if err := json.Unmarshal([]byte(`{"extra_data":"plain-text-secret"}`), &request); err == nil {
		t.Fatal("string extra_data must be rejected; only a key-inspectable JSON object is allowed")
	}
	if err := json.Unmarshal([]byte(`{"extra_data":{"region":"cn"}}`), &request); err != nil {
		t.Fatalf("object extra_data was rejected: %v", err)
	}
}

func TestTaskRecordDoesNotSerializeInternalPipelineInputs(t *testing.T) {
	record := entity.TaskRecord{
		TaskId:         1,
		AppName:        "demo",
		PipelineParam:  json.RawMessage(`{"deploy_token":"must-not-leak"}`),
		JenkinsAddress: "https://internal-jenkins.example",
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "pipeline_param") || strings.Contains(string(encoded), "must-not-leak") ||
		strings.Contains(string(encoded), "jenkins_address") || strings.Contains(string(encoded), "internal-jenkins") {
		t.Fatalf("internal pipeline inputs leaked through JSON: %s", encoded)
	}
}

func TestNormalizeTaskRecordNullableText(t *testing.T) {
	rundeckAppName := " nUlL "
	record := entity.TaskRecord{
		RundeckAppName: &rundeckAppName,
		Message:        " Null ",
		CiJobName:      "NULL",
		CdJobName:      "  ",
		Products:       " image:v1 ",
		PipelineParam:  json.RawMessage(`{"domain":"NULL","branch":"NULL"}`),
	}

	normalizeTaskRecordNullableText(&record)
	if record.Message != "" || record.CiJobName != "" || record.CdJobName != "" {
		t.Fatalf("legacy task values were not cleared: %#v", record)
	}
	if record.RundeckAppName != nil {
		t.Fatalf("rundeck_app_name = %#v, want nil", record.RundeckAppName)
	}
	if record.Products != "image:v1" {
		t.Fatalf("products = %q, want trimmed normal value", record.Products)
	}
	var parameters map[string]string
	if err := json.Unmarshal(record.PipelineParam, &parameters); err != nil {
		t.Fatalf("decode normalized pipeline parameters: %v", err)
	}
	if parameters["domain"] != "" || parameters["branch"] != "NULL" {
		t.Fatalf("unexpected pipeline parameter normalization: %#v", parameters)
	}
}
