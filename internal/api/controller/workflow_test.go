package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/workflow"
)

func TestRedactWorkflowExecutorConfigDoesNotMutateStoredView(t *testing.T) {
	original := workflow.WorkflowView{Spec: workflow.WorkflowSpec{Steps: []workflow.StepSpec{
		{Key: "build", With: json.RawMessage(`{"parameters":{"githubPat":"must-not-leak"}}`)},
		{Key: "deploy", With: json.RawMessage(`{"target":"production"}`)},
	}}}

	redacted := redactWorkflowExecutorConfig(original)
	for _, step := range redacted.Spec.Steps {
		if string(step.With) != `{}` {
			t.Fatalf("redacted config for %s = %s", step.Key, step.With)
		}
	}
	if string(original.Spec.Steps[0].With) != `{"parameters":{"githubPat":"must-not-leak"}}` {
		t.Fatalf("redaction mutated the stored workflow view: %s", original.Spec.Steps[0].With)
	}
}

type unavailableWorkflowExecutor struct{}

func (unavailableWorkflowExecutor) Descriptor() workflow.Descriptor {
	return workflow.Descriptor{Uses: "test.unavailable@v1", Name: "Unavailable"}
}

func (unavailableWorkflowExecutor) Validate(json.RawMessage) error { return nil }
func (unavailableWorkflowExecutor) Start(context.Context, workflow.StartRequest) (workflow.Result, error) {
	return workflow.Result{}, nil
}
func (unavailableWorkflowExecutor) Reconcile(context.Context, workflow.ReconcileRequest) (workflow.Result, error) {
	return workflow.Result{}, nil
}
func (unavailableWorkflowExecutor) Available(context.Context) error {
	return errors.New("provider-super-secret at https://internal-jenkins.example")
}

func TestListPipelineStepTypesRedactsAvailabilityError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry := workflow.NewRegistry()
	if err := registry.Register(unavailableWorkflowExecutor{}); err != nil {
		t.Fatal(err)
	}
	controller := NewWorkflowController(workflow.NewService(nil, registry), nil)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/pipeline-step-types", nil)
	controller.ListPipelineStepTypes(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "provider-super-secret") || strings.Contains(body, "internal-jenkins.example") {
		t.Fatalf("availability error leaked: %s", body)
	}
	if !strings.Contains(body, "executor unavailable") {
		t.Fatalf("generic availability reason missing: %s", body)
	}
}
