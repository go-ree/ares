package controller

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/app"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/publish"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemovedRundeckFieldsRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name, body string
		target     any
	}{
		{"app patch", `{"app_name_cn":"新名称","rundeck_app_name":"legacy"}`, &app.PatchAppRequest{}},
		{"publish", `{"app_name":"demo","is_rundeck":false}`, &publish.CreatePublishRequest{}},
		{"batch", `{"batch_publish":[{"app_name":"demo","is_rundeck":true}]}`, &publish.CreateBatchPublishRequest{}},
		{"history", `{"app_name":"demo","is_rundeck":true}`, &publish.PublishQuery{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/", func(c *gin.Context) {
				if BindJSON(c, test.target, defaultJSONRequestBytes) {
					c.Status(204)
				}
			})
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != 400 {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestApplicationAndReleaseResponsesOmitRundeck(t *testing.T) {
	for _, value := range []any{
		entity.Apps{AppName: "demo"}, entity.TaskRecord{AppName: "demo"},
		publish.TaskRecordView{TaskRecord: entity.TaskRecord{AppName: "demo"}},
		publish.PublishRequest{AppName: "demo"},
	} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "rundeck") || !strings.Contains(string(body), `"app_name":"demo"`) {
			t.Fatalf("unexpected response: %s", body)
		}
	}
}
