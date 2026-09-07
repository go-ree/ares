package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/workflow"
)

func TestPublishEndpointsRejectClientOwnedActorFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		path string
		body string
		call func(*PublishController, *gin.Context)
	}{
		{
			name: "single publisher",
			path: "/publish",
			body: `{"app_name":"demo","branch":"main","env":"qa","publisher":"Mallory"}`,
			call: (*PublishController).CreateBuildTask,
		},
		{
			name: "batch publisher_cn",
			path: "/publish/batch",
			body: `{"batch_publish":[{"app_name":"demo","branch":"main","env":"qa","publisher_cn":"Mallory"}]}`,
			call: (*PublishController).CreateBatchBuildTask,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := NewPublishController()
			router := gin.New()
			router.POST(test.path, func(c *gin.Context) {
				SetPrincipal(c, auth.Principal{UserID: 88, Username: "alice", DisplayName: "Alice"})
				test.call(controller, c)
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCurrentPublishActorUsesAuthenticatedPrincipal(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetPrincipal(context, auth.Principal{UserID: 88, Username: "alice", DisplayName: " Alice Chen "})
	actor, ok := currentPublishActor(context)
	if !ok || actor.UserID != 88 || actor.DisplayName != "Alice Chen" {
		t.Fatalf("actor = %#v, ok = %v", actor, ok)
	}
}

type recordingWorkflowStore struct {
	command *workflow.SaveWorkflowCommand
}

func (s *recordingWorkflowStore) SaveWorkflow(_ context.Context, command workflow.SaveWorkflowCommand) (workflow.WorkflowView, error) {
	s.command = &command
	return workflow.WorkflowView{
		ConfigID: command.ConfigID, WorkflowID: 1, WorkflowVersionID: 1,
		Version: 1, Revision: 1, Spec: command.Spec,
	}, nil
}

func (*recordingWorkflowStore) GetCurrentWorkflow(context.Context, int) (workflow.WorkflowView, error) {
	return workflow.WorkflowView{}, workflow.ErrNotFound
}

func TestWorkflowEndpointUsesAuthenticatedPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingWorkflowStore{}
	service := workflow.NewService(store, workflow.DefaultRegistry())
	controller := NewWorkflowController(service, nil)
	router := gin.New()
	router.PUT("/app-configs/:config_id/workflow", func(c *gin.Context) {
		SetPrincipal(c, auth.Principal{UserID: 88, Username: "alice", DisplayName: " Alice Chen "})
		controller.PutAppConfigWorkflow(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/app-configs/7/workflow", strings.NewReader(
		`{"revision":0,"spec":{"schema_version":1,"name":"demo","steps":[{"key":"one","name":"one","uses":"builtin.noop@v1"}]}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if store.command == nil || store.command.Actor != "Alice Chen" || store.command.ActorUserID == nil || *store.command.ActorUserID != 88 {
		t.Fatalf("workflow actor was not derived from principal: %#v", store.command)
	}
}

func TestWorkflowEndpointRejectsClientOwnedActorFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &recordingWorkflowStore{}
	service := workflow.NewService(store, workflow.DefaultRegistry())
	controller := NewWorkflowController(service, nil)
	router := gin.New()
	router.PUT("/app-configs/:config_id/workflow", func(c *gin.Context) {
		SetPrincipal(c, auth.Principal{UserID: 88, Username: "alice", DisplayName: "Alice"})
		controller.PutAppConfigWorkflow(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/app-configs/7/workflow", strings.NewReader(
		`{"revision":0,"created_by":"Mallory","spec":{"schema_version":1,"name":"demo","steps":[{"key":"one","name":"one","uses":"builtin.noop@v1"}]}}`,
	))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if store.command != nil {
		t.Fatal("a request with a client-owned actor must not reach the workflow store")
	}
}
