package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/pipelinebinding"
)

type preflightBoundary struct {
	calls  int
	intent pipelinebinding.Intent
	err    error
}

func (s *preflightBoundary) Check(_ context.Context, in pipelinebinding.Intent) (pipelinebinding.Preflight, error) {
	s.calls++
	s.intent = in
	return pipelinebinding.Preflight{Valid: true, Persisted: true, Executable: true, VersionID: "9007199254740993"}, s.err
}

func TestBindingPreflightBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, audit, sessions := newAuthBoundary(t)
	cases := []struct{ path, body, kind string }{
		{"/api/v1/apps/1/ci-binding/preflight", `{"version_id":"9007199254740993","application_type":"java","parameters":{"profile":"PRIVATE-PARAMETER"}}`, "ci"},
		{"/api/v1/app-configs/1/cd-binding/preflight", `{"version_id":"9007199254740993","target_type":"kubernetes","parameters":{"profile":"PRIVATE-PARAMETER"}}`, "cd"},
	}
	for _, tc := range cases {
		for _, role := range []auth.Role{auth.RoleViewer, auth.RoleDeveloper, auth.RoleReleaser, auth.RoleAdmin} {
			audit.mu.Lock()
			audit.audits = nil
			audit.auditErr = nil
			audit.mu.Unlock()
			store := &preflightBoundary{}
			r := gin.New()
			RouterWithRuntime(r, Runtime{Auth: service, BindingPreflight: store})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, authenticatedRequest(t, service, sessions[role], "POST", tc.path, tc.body))
			want := 403
			if role == auth.RoleAdmin || role == auth.RoleDeveloper {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("%s %s %d %s", tc.path, role, w.Code, w.Body.String())
			}
			if want == 403 && store.calls != 0 {
				t.Fatal("denial reached service")
			}
			if want == 200 {
				if store.intent.Kind != tc.kind || store.intent.VersionID != 9007199254740993 {
					t.Fatal("intent lost precision/kind")
				}
				if !strings.Contains(w.Body.String(), `"executable":false`) || !strings.Contains(w.Body.String(), `"persisted":false`) {
					t.Fatal("false promise")
				}
				if len(audit.audits) != 2 || audit.audits[1].Result != "succeeded" || audit.audits[1].ResourceID != "1" {
					t.Fatal("missing audit")
				}
			}
			raw, _ := json.Marshal(audit.audits)
			if strings.Contains(w.Body.String(), "PRIVATE-PARAMETER") || strings.Contains(string(raw), "PRIVATE-PARAMETER") {
				t.Fatal("parameter leaked")
			}
		}
		for _, mode := range []string{"anonymous", "csrf", "origin", "audit", "legacy"} {
			audit.auditErr = nil
			store := &preflightBoundary{}
			r := gin.New()
			RouterWithRuntime(r, Runtime{Auth: service, BindingPreflight: store, LegacyAdminTokenEnabled: true, LegacyAdminToken: "legacy"})
			req := authenticatedRequest(t, service, sessions[auth.RoleAdmin], "POST", tc.path, tc.body)
			want := 403
			switch mode {
			case "anonymous":
				req.Header.Del("Cookie")
				want = 401
			case "csrf":
				req.Header.Del("X-CSRF-Token")
			case "origin":
				req.Header.Set("Origin", "http://invalid.example")
			case "audit":
				audit.auditErr = errors.New("private database failure")
				want = 503
			case "legacy":
				req.Header.Del("Cookie")
				req.Header.Set("X-Ares-Admin-Token", "legacy")
				want = 401
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != want || store.calls != 0 {
				t.Fatalf("%s %d calls=%d", mode, w.Code, store.calls)
			}
		}
	}
}
func TestBindingPreflightRequestsAndErrors(t *testing.T) {
	service, audit, sessions := newAuthBoundary(t)
	path := "/api/v1/apps/1/ci-binding/preflight"
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"version_id":1,"application_type":"java"}`, 400}, {`{"version_id":"01","application_type":"java"}`, 400}, {`{"version_id":"1","version_id":"2"}`, 400}, {`{"version_id":"1","application_type":"java","target_type":"k8s"}`, 400}, {`{"version_id":"1","application_type":"java","expected_revision":"0"}`, 400}, {strings.Repeat(" ", 20481), 413},
	} {
		store := &preflightBoundary{}
		r := gin.New()
		RouterWithRuntime(r, Runtime{Auth: service, BindingPreflight: store})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleAdmin], "POST", path, tc.body))
		if w.Code != tc.status || store.calls != 0 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{pipelinebinding.ErrTarget, 404}, {pipelinebinding.ErrDisabled, 409}, {pipelinebinding.ErrMismatch, 422}, {pipelinebinding.ErrParameters, 422}, {pipelinebinding.ErrDefinition, 503}, {errors.New("PRIVATE-DSN"), 503}} {
		r := gin.New()
		RouterWithRuntime(r, Runtime{Auth: service, BindingPreflight: &preflightBoundary{err: tc.err}})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleAdmin], "POST", path, `{"version_id":"1","application_type":"java"}`))
		if w.Code != tc.status || strings.Contains(w.Body.String(), "PRIVATE") {
			t.Fatal(w.Body.String())
		}
		if audit.audits[len(audit.audits)-1].Result != "failed" {
			t.Fatal("missing failure audit")
		}
	}
}
