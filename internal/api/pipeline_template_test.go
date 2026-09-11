package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
)

func TestTemplateValidationPermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, sessions := newAuthBoundary(t)
	r := gin.New()
	RouterWithRuntime(r, Runtime{Auth: service})
	const path = "/api/v1/pipeline-templates/validate"
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleDeveloper, auth.RoleReleaser, auth.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			req := authenticatedRequest(t, service, sessions[role], http.MethodPost, path, `{}`)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			want := 403
			if role == auth.RoleAdmin {
				want = 422
			}
			if w.Code != want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
	if w.Code != 401 {
		t.Fatalf("anonymous=%d", w.Code)
	}
	req := authenticatedRequest(t, service, sessions[auth.RoleAdmin], http.MethodPost, path, `{}`)
	req.Header.Del("X-CSRF-Token")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("missing csrf=%d %s", w.Code, w.Body.String())
	}
}
