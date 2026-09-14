package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/controller"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/pipelinetemplate"
	"github.com/go-ree/ares/internal/templatecatalog"
)

type catalogBoundaryStore struct {
	controller.TemplateCatalogStore
	calls int
	actor int64
	err   error
}

func (s *catalogBoundaryStore) CreateType(context.Context, string, string) error {
	s.calls++
	return s.err
}
func (s *catalogBoundaryStore) UpdateType(context.Context, string, string, bool, uint64) error {
	s.calls++
	return s.err
}
func (s *catalogBoundaryStore) GetType(context.Context, string) (templatecatalog.ApplicationType, error) {
	s.calls++
	return templatecatalog.ApplicationType{Key: "java", Name: "Java", Enabled: true, Revision: 9007199254740993}, s.err
}
func (s *catalogBoundaryStore) ListTypes(context.Context, string, int) ([]templatecatalog.ApplicationType, error) {
	s.calls++
	return []templatecatalog.ApplicationType{{Key: "java", Revision: 1}, {Key: "python", Revision: 1}}, s.err
}
func (s *catalogBoundaryStore) CreateTemplate(_ context.Context, _ string, _ pipelinetemplate.Spec, actor int64) (int64, error) {
	s.calls++
	s.actor = actor
	return 9007199254740993, s.err
}
func (s *catalogBoundaryStore) UpdateTemplate(context.Context, int64, uint64, pipelinetemplate.Spec, bool) error {
	s.calls++
	return s.err
}
func (s *catalogBoundaryStore) GetTemplate(context.Context, int64) (templatecatalog.Template, error) {
	s.calls++
	return templatecatalog.Template{ID: 1, Revision: 1, Draft: json.RawMessage(`{"private":"DO-NOT-LEAK"}`)}, s.err
}
func (s *catalogBoundaryStore) ListTemplates(context.Context, int64, int) ([]templatecatalog.Template, error) {
	s.calls++
	return []templatecatalog.Template{{ID: 1, Revision: 1, Draft: json.RawMessage(`{"private":"DO-NOT-LEAK"}`)}}, s.err
}
func (s *catalogBoundaryStore) Publish(_ context.Context, _ int64, _ uint64, actor int64) (templatecatalog.Version, error) {
	s.calls++
	s.actor = actor
	return templatecatalog.Version{ID: 1, TemplateID: 1, Number: 1, SourceRevision: 1, Spec: json.RawMessage(`{"private":"DO-NOT-LEAK"}`)}, s.err
}
func (s *catalogBoundaryStore) GetVersion(context.Context, int64, uint64) (templatecatalog.Version, error) {
	s.calls++
	return templatecatalog.Version{ID: 1, TemplateID: 1, Number: 1, Spec: json.RawMessage(`{"private":"DO-NOT-LEAK"}`)}, s.err
}
func (s *catalogBoundaryStore) ListVersions(context.Context, int64, uint64, int) ([]templatecatalog.Version, error) {
	s.calls++
	return []templatecatalog.Version{{ID: 1, TemplateID: 1, Number: 1, Spec: json.RawMessage(`{"private":"DO-NOT-LEAK"}`)}}, s.err
}

func TestTemplateCatalogPermissionsAndAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, audit, sessions := newAuthBoundary(t)
	resetAudit := func() { audit.mu.Lock(); audit.audits = nil; audit.auditErr = nil; audit.mu.Unlock() }
	cases := []struct {
		method, path, body string
		private            bool
		status             int
	}{
		{"GET", "/api/v1/application-types", "", false, 200},
		{"GET", "/api/v1/application-types/java", "", false, 200},
		{"POST", "/api/v1/application-types", `{"key":"java","name":"Java"}`, true, 201},
		{"PUT", "/api/v1/application-types/java", `{"name":"Java","enabled":false,"expected_revision":"1"}`, true, 200},
		{"GET", "/api/v1/pipeline-templates", "", false, 200},
		{"POST", "/api/v1/pipeline-templates", `{"key":"java-ci","spec":{}}`, true, 201},
		{"GET", "/api/v1/pipeline-templates/1", "", true, 200},
		{"PUT", "/api/v1/pipeline-templates/1", `{"spec":{},"enabled":true,"expected_revision":"1"}`, true, 200},
		{"GET", "/api/v1/pipeline-templates/1/versions", "", false, 200},
		{"POST", "/api/v1/pipeline-templates/1/versions", `{"expected_revision":"1"}`, true, 201},
		{"GET", "/api/v1/pipeline-templates/1/versions/1", "", true, 200},
	}
	for _, tc := range cases {
		for _, role := range []auth.Role{auth.RoleViewer, auth.RoleDeveloper, auth.RoleReleaser, auth.RoleAdmin} {
			t.Run(tc.method+tc.path+string(role), func(t *testing.T) {
				resetAudit()
				store := &catalogBoundaryStore{}
				r := gin.New()
				RouterWithRuntime(r, Runtime{Auth: service, TemplateCatalog: store})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, authenticatedRequest(t, service, sessions[role], tc.method, tc.path, tc.body))
				want := tc.status
				if tc.private && role != auth.RoleAdmin {
					want = 403
				}
				if w.Code != want {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
				if want == 403 && store.calls != 0 {
					t.Fatal("unauthorized storage access")
				}
				if !tc.private && strings.Contains(w.Body.String(), "DO-NOT-LEAK") {
					t.Fatal("raw spec leaked into metadata")
				}
				if tc.private && role == auth.RoleAdmin {
					if len(audit.audits) != 2 || audit.audits[0].Result != "authorized" || audit.audits[1].Result != "succeeded" {
						t.Fatalf("audit %+v", audit.audits)
					}
					raw, _ := json.Marshal(audit.audits)
					if strings.Contains(string(raw), "DO-NOT-LEAK") {
						t.Fatal("audit leaked specification")
					}
					if store.actor != 0 && (audit.audits[1].ActorUserID == nil || store.actor != *audit.audits[1].ActorUserID) {
						t.Fatal("actor not server owned")
					}
				}
			})
		}
	}
	for _, tc := range cases {
		resetAudit()
		store := &catalogBoundaryStore{}
		r := gin.New()
		RouterWithRuntime(r, Runtime{Auth: service, TemplateCatalog: store, LegacyAdminTokenEnabled: true, LegacyAdminToken: "legacy-admin-token"})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != 401 {
			t.Fatalf("anonymous %s %d", tc.path, w.Code)
		}
		if tc.method != "GET" {
			req := authenticatedRequest(t, service, sessions[auth.RoleAdmin], tc.method, tc.path, tc.body)
			req.Header.Del("X-CSRF-Token")
			w = httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 403 {
				t.Fatal("missing CSRF allowed")
			}
		}
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("X-Ares-Admin-Token", "legacy-admin-token")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code < 400 {
			t.Fatal("legacy identity allowed")
		}
		if store.calls != 0 {
			t.Fatal("rejected request reached store")
		}
		if tc.private {
			audit.auditErr = errors.New("private database error")
			w = httptest.NewRecorder()
			r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleAdmin], tc.method, tc.path, tc.body))
			if w.Code != 503 || store.calls != 0 {
				t.Fatal("audit unavailable not fail closed")
			}
		}
	}
}

func TestTemplateCatalogRequestBoundsAndErrors(t *testing.T) {
	service, audit, sessions := newAuthBoundary(t)
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"PUT", "/api/v1/application-types/java", `{"name":"Java","expected_revision":"1"}`, 400},
		{"PUT", "/api/v1/application-types/java", `{"name":"Java","enabled":true,"expected_revision":1}`, 400},
		{"PUT", "/api/v1/application-types/java", `{"name":"Java","enabled":true,"expected_revision":"01"}`, 400},
		{"POST", "/api/v1/pipeline-templates", `{"key":"a","spec":{},"created_by_user_id":42}`, 400},
		{"POST", "/api/v1/application-types", `{"key":"java","key":"python"}`, 400},
		{"POST", "/api/v1/application-types", strings.Repeat(" ", 4097), 413},
		{"POST", "/api/v1/pipeline-templates", strings.Repeat(" ", 69633), 413},
		{"GET", "/api/v1/pipeline-templates/01", "", 400},
		{"GET", "/api/v1/pipeline-templates?limit=0", "", 400},
		{"GET", "/api/v1/pipeline-templates?limit=101", "", 400},
		{"GET", "/api/v1/pipeline-templates?limit=1&limit=2", "", 400},
		{"GET", "/api/v1/pipeline-templates?unknown=1", "", 400},
		{"GET", "/api/v1/pipeline-templates?after=-1", "", 400},
		{"GET", "/api/v1/pipeline-templates/1/versions/0", "", 400},
	}
	for _, tc := range cases {
		store := &catalogBoundaryStore{}
		r := gin.New()
		RouterWithRuntime(r, Runtime{Auth: service, TemplateCatalog: store})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleAdmin], tc.method, tc.path, tc.body))
		if w.Code != tc.status || store.calls != 0 {
			t.Fatalf("%s status=%d calls=%d body=%s", tc.path, w.Code, store.calls, w.Body.String())
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{templatecatalog.ErrInvalid, 422, "invalid_catalog_request"}, {templatecatalog.ErrNotFound, 404, "catalog_not_found"}, {templatecatalog.ErrConflict, 409, "catalog_conflict"}, {templatecatalog.ErrDisabled, 409, "catalog_disabled"}, {errors.New("PRIVATE-DSN"), 503, "catalog_unavailable"}} {
		r := gin.New()
		RouterWithRuntime(r, Runtime{Auth: service, TemplateCatalog: &catalogBoundaryStore{err: tc.err}})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleAdmin], "POST", "/api/v1/pipeline-templates/1/versions", `{"expected_revision":"1"}`))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), "PRIVATE-DSN") {
			t.Fatal(w.Body.String())
		}
		if audit.audits[len(audit.audits)-1].Result != "failed" {
			t.Fatal("failure audit missing")
		}
	}
	r := gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, TemplateCatalog: &catalogBoundaryStore{}})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleViewer], "GET", "/api/v1/application-types?limit=1", ""))
	if !strings.Contains(w.Body.String(), `"next_cursor":"java"`) || !strings.Contains(w.Body.String(), `"has_more":true`) {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleViewer], "GET", "/api/v1/application-types/java", ""))
	if !strings.Contains(w.Body.String(), `"revision":"9007199254740993"`) {
		t.Fatal("revision lost precision")
	}
}
