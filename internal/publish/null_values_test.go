package publish

import (
	"encoding/json"
	"testing"

	"ares/internal/entity"
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
