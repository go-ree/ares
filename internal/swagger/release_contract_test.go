package swagger

import (
	"encoding/json"
	"testing"
)

type releaseSwaggerDocument struct {
	Paths map[string]struct {
		Post struct {
			Deprecated bool `json:"deprecated"`
			Parameters []struct {
				Name     string `json:"name"`
				In       string `json:"in"`
				Required bool   `json:"required"`
			} `json:"parameters"`
			Responses map[string]struct {
				Headers map[string]any `json:"headers"`
			} `json:"responses"`
		} `json:"post"`
	} `json:"paths"`
	Definitions map[string]struct {
		Properties map[string]struct {
			Type   string `json:"type"`
			Format string `json:"format"`
		} `json:"properties"`
	} `json:"definitions"`
}

func TestReleaseOpenAPIKeepsIdempotencyDeprecationAndBigIntContracts(t *testing.T) {
	var document releaseSwaggerDocument
	if err := json.Unmarshal([]byte(SwaggerInfo.ReadDoc()), &document); err != nil {
		t.Fatalf("decode generated Swagger document: %v", err)
	}

	for _, path := range []string{"/api/v1/deploy/publish", "/api/v1/deploy/publish/batch"} {
		operation, exists := document.Paths[path]
		if !exists || !operation.Post.Deprecated {
			t.Fatalf("legacy operation %s is not marked deprecated", path)
		}
		assertSwaggerHeaderParameter(t, operation.Post.Parameters, "Idempotency-Key", false)
		for _, status := range []string{"401", "403"} {
			headers := operation.Post.Responses[status].Headers
			if headers["Deprecation"] == nil || headers["Warning"] == nil {
				t.Fatalf("legacy operation %s response %s lacks deprecation headers", path, status)
			}
		}
	}

	for _, path := range []string{"/api/v1/app-configs/{config_id}/releases", "/api/v1/releases/batch"} {
		operation, exists := document.Paths[path]
		if !exists {
			t.Fatalf("canonical release operation %s missing", path)
		}
		assertSwaggerHeaderParameter(t, operation.Post.Parameters, "Idempotency-Key", true)
	}

	for _, field := range []struct {
		definition string
		property   string
	}{
		{definition: "publish.CreateReleaseRequest", property: "expected_workflow_version_id"},
		{definition: "publish.ReleaseCommand", property: "expected_workflow_version_id"},
		{definition: "publish.ReleaseReceipt", property: "record_id"},
		{definition: "publish.ReleaseReceiptItem", property: "workflow_version_id"},
		{definition: "publish.ReleasePreflight", property: "workflow_id"},
		{definition: "publish.ReleasePreflight", property: "workflow_version_id"},
		{definition: "publish.ReleaseTarget", property: "workflow_version_id"},
	} {
		definition, exists := document.Definitions[field.definition]
		if !exists {
			t.Fatalf("Swagger definition %s missing", field.definition)
		}
		property, exists := definition.Properties[field.property]
		if !exists || property.Type != "string" || property.Format != "int64" {
			t.Fatalf("Swagger property %s.%s = %#v, want string/int64",
				field.definition, field.property, property)
		}
	}
}

func assertSwaggerHeaderParameter(t *testing.T, parameters []struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
}, name string, required bool) {
	t.Helper()
	for _, parameter := range parameters {
		if parameter.Name == name && parameter.In == "header" {
			if parameter.Required != required {
				t.Fatalf("Swagger header %s required = %t, want %t", name, parameter.Required, required)
			}
			return
		}
	}
	t.Fatalf("Swagger header parameter %s missing", name)
}
