package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBusinessPaginationEndpointsRejectInvalidValuesBeforeDatabaseAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		target  string
		handler gin.HandlerFunc
	}{
		{name: "applications", target: "/apps/query", handler: NewAppsController().QueryApps},
		{name: "publishes", target: "/deploy/publish/query", handler: NewPublishController().QueryBuildTaskList},
	}
	payloads := []string{
		`{"page_num":null,"page_size":10}`,
		`{"page_num":1,"page_size":null}`,
		`{"page_num":"1","page_size":10}`,
		`{"page_num":1.5,"page_size":10}`,
		`{"page_num":-1,"page_size":10}`,
		`{"page_num":1,"page_size":0}`,
		`{"page_num":1,"page_size":201}`,
		`{"page_num":9223372036854775807,"page_size":200}`,
	}
	for _, test := range tests {
		for _, payload := range payloads {
			t.Run(test.name+"/"+payload, func(t *testing.T) {
				router := gin.New()
				router.POST(test.target, test.handler)
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, test.target, strings.NewReader(payload))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, request)
				if recorder.Code != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}
