package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/pipelinebinding"
)

type bindingBoundaryStore struct {
	saveCalls, getCalls, parameterCalls int
	intent                              pipelinebinding.SaveIntent
	kind, targetLookup                  string
	saveErr, getErr, parameterErr       error
}

func (s *bindingBoundaryStore) Save(_ context.Context, in pipelinebinding.SaveIntent) (pipelinebinding.Binding, error) {
	s.saveCalls++
	s.intent = in
	if s.saveErr != nil {
		return pipelinebinding.Binding{}, s.saveErr
	}
	return pipelinebinding.Binding{
		ID: 7, Kind: in.Kind, TargetID: in.TargetID, VersionID: in.VersionID, TemplateID: 12,
		VersionNumber: 3, Checksum: strings.Repeat("a", 64), Enabled: in.Enabled, Revision: 1,
		CreatedBy: in.Actor, UpdatedBy: in.Actor, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
	}, nil
}

func (s *bindingBoundaryStore) Get(_ context.Context, kind string, targetID int64) (pipelinebinding.Binding, error) {
	s.getCalls++
	s.kind = kind
	s.targetLookup = "target"
	if s.getErr != nil {
		return pipelinebinding.Binding{}, s.getErr
	}
	return pipelinebinding.Binding{
		ID: 7, Kind: kind, TargetID: targetID, VersionID: 9, TemplateID: 12, VersionNumber: 3,
		Checksum: strings.Repeat("a", 64), Enabled: true, Revision: 4, CreatedBy: 1, UpdatedBy: 1,
		CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC(),
	}, nil
}

func (s *bindingBoundaryStore) ReadParameters(_ context.Context, kind string, targetID int64) (pipelinebinding.StoredParameters, error) {
	s.parameterCalls++
	s.kind = kind
	if s.parameterErr != nil {
		return pipelinebinding.StoredParameters{}, s.parameterErr
	}
	return pipelinebinding.StoredParameters{
		BindingID: 7,
		Revision:  4,
		Document: map[string]json.RawMessage{
			"repo":    json.RawMessage(`"https://example.invalid/repo.git"`),
			"profile": json.RawMessage(`"PRIVATE-PARAMETER"`),
		},
	}, nil
}

const (
	ciBindingPath = "/api/v1/apps/1/ci-binding"
	cdBindingPath = "/api/v1/app-configs/1/cd-binding"
	validCIBody   = `{"version_id":"9007199254740993","application_type":"java","parameters":{"repo":"https://example.invalid/repo.git"},"enabled":true}`
	validCDBody   = `{"version_id":"9007199254740993","target_type":"kubernetes","parameters":{"repo":"https://example.invalid/repo.git"},"enabled":true}`
	rebindCIBody  = `{"version_id":"9007199254740993","application_type":"java","parameters":{},"enabled":false,"expected_revision":"4"}`
)

func TestBindingManagementPermissionsAndAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, audit, sessions := newAuthBoundary(t)
	resetAudit := func() {
		audit.mu.Lock()
		audit.audits = nil
		audit.auditErr = nil
		audit.mu.Unlock()
	}
	cases := []struct {
		method, path, body string
		kind               string
		editorsOnly        bool
		sensitive          bool
		status             int
	}{
		{"PUT", ciBindingPath, validCIBody, "ci", true, false, 201},
		{"PUT", cdBindingPath, validCDBody, "cd", true, false, 201},
		{"GET", ciBindingPath, "", "ci", false, false, 200},
		{"GET", cdBindingPath, "", "cd", false, false, 200},
		{"GET", ciBindingPath + "/parameters", "", "ci", true, true, 200},
		{"GET", cdBindingPath + "/parameters", "", "cd", true, true, 200},
	}
	for _, tc := range cases {
		for _, role := range []auth.Role{auth.RoleViewer, auth.RoleDeveloper, auth.RoleReleaser, auth.RoleAdmin} {
			t.Run(tc.method+tc.path+string(role), func(t *testing.T) {
				resetAudit()
				store := &bindingBoundaryStore{}
				r := gin.New()
				RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, authenticatedRequest(t, service, sessions[role], tc.method, tc.path, tc.body))
				editor := role == auth.RoleAdmin || role == auth.RoleDeveloper
				want := tc.status
				if tc.editorsOnly && !editor {
					want = 403
				}
				if w.Code != want {
					t.Fatalf("%s %s %d %s", tc.path, role, w.Code, w.Body.String())
				}
				// Denial must never reach storage.
				if want == 403 && (store.saveCalls+store.getCalls+store.parameterCalls) != 0 {
					t.Fatal("denial reached binding storage")
				}
				if w.Code == 200 || w.Code == 201 {
					if !strings.Contains(w.Body.String(), `"executable":false`) {
						t.Fatal("response promised execution")
					}
					if tc.method == "PUT" {
						if store.intent.Kind != tc.kind || store.intent.VersionID != 9007199254740993 || store.intent.TargetID != 1 {
							t.Fatalf("intent lost: %+v", store.intent)
						}
						if store.intent.Actor <= 0 {
							t.Fatal("actor not server owned")
						}
						if tc.kind == "ci" && store.intent.Ownership != "java" {
							t.Fatal("CI ownership lost")
						}
						if tc.kind == "cd" && store.intent.Ownership != "kubernetes" {
							t.Fatal("CD ownership lost")
						}
					}
					if chain := strings.Contains(w.Body.String(), "PRIVATE-PARAMETER"); chain && !strings.Contains(tc.path, "parameters") {
						t.Fatal("binding metadata leaked parameters")
					}
				}
				// Only unsafe methods and sensitive reads are audited. A denial
				// on an audited route still records exactly one denied event.
				audited := tc.method == "PUT" || tc.sensitive
				wantAudits := 0
				if audited {
					wantAudits = 2
					if editor == false {
						wantAudits = 1
						if audit.audits[0].Result != "denied" || audit.audits[0].HTTPStatus != 403 {
							t.Fatalf("denial audit %+v", audit.audits)
						}
					}
				}
				if len(audit.audits) != wantAudits {
					t.Fatalf("audit %+v", audit.audits)
				}
				if audited && editor {
					if audit.audits[0].Result != "authorized" || audit.audits[1].Result != "succeeded" {
						t.Fatalf("sensitive read audit %+v", audit.audits)
					}
					raw, _ := json.Marshal(audit.audits)
					if strings.Contains(string(raw), "PRIVATE-PARAMETER") {
						t.Fatal("audit leaked binding parameters")
					}
				}
			})
		}
	}
}

func TestBindingManagementRejectsUnsafeRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, sessions := newAuthBoundary(t)
	role := sessions[auth.RoleAdmin]
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{ciBindingPath, `{"version_id":"1","application_type":"java"}`, 400},
		{ciBindingPath, `{"version_id":"0","application_type":"java","enabled":true}`, 400},
		{ciBindingPath, `{"version_id":"01","application_type":"java","enabled":true}`, 400},
		{ciBindingPath, `{"version_id":"1","application_type":"java","enabled":true,"target_type":"k8s"}`, 400},
		{ciBindingPath, `{"version_id":"1","version_id":"2","application_type":"java","enabled":true}`, 400},
		{ciBindingPath, `{"version_id":"1","application_type":"java","enabled":true,"expected_revision":"0"}`, 400},
		{ciBindingPath, `{"version_id":"1","application_type":"java","enabled":true} ` + strings.Repeat(" ", 20481), 413},
		{cdBindingPath, `{"version_id":"1","target_type":"kubernetes"}`, 400},
		{cdBindingPath, `{"version_id":"1","target_type":"kubernetes","enabled":true,"application_type":"java"}`, 400},
		{"/api/v1/apps/01/ci-binding/parameters", "", 400},
		// CI and CD bindings belong to different resources; there is deliberately
		// no CD binding route under an application.
		{"/api/v1/apps/1/cd-binding", validCDBody, 404},
	} {
		store := &bindingBoundaryStore{}
		r := gin.New()
		RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store})
		w := httptest.NewRecorder()
		method := "PUT"
		if strings.Contains(tc.path, "/parameters") {
			method = "GET"
		}
		r.ServeHTTP(w, authenticatedRequest(t, service, role, method, tc.path, tc.body))
		if w.Code != tc.status {
			t.Fatalf("%s %s => %d %s", method, tc.path, w.Code, w.Body.String())
		}
		if store.saveCalls+store.getCalls+store.parameterCalls != 0 {
			t.Fatal("malformed request reached storage")
		}
		if tc.status == 400 && !strings.Contains(w.Body.String(), "invalid_request") {
			t.Fatalf("wrong error code: %s", w.Body.String())
		}
	}
	// A request body must be JSON with a supported media type.
	store := &bindingBoundaryStore{}
	r := gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store})
	request := authenticatedRequest(t, service, role, "PUT", ciBindingPath, validCIBody)
	request.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	if w.Code != http.StatusUnsupportedMediaType || store.saveCalls != 0 {
		t.Fatalf("media type %d", w.Code)
	}
}

func TestBindingManagementErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, audit, sessions := newAuthBoundary(t)
	role := sessions[auth.RoleAdmin]
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{pipelinebinding.ErrTarget, 404, "binding_resource_not_found"},
		{pipelinebinding.ErrDisabled, 409, "binding_resource_disabled"},
		{pipelinebinding.ErrConflict, 409, "binding_conflict"},
		{pipelinebinding.ErrMismatch, 422, "binding_ownership_mismatch"},
		{pipelinebinding.ErrParameters, 422, "invalid_binding_parameters"},
		{pipelinebinding.ErrDefinition, 503, "binding_store_unavailable"},
		{errors.New("PRIVATE-DSN"), 503, "binding_store_unavailable"},
	} {
		for _, call := range []string{"save", "read", "parameters"} {
			store := &bindingBoundaryStore{saveErr: tc.err, getErr: tc.err, parameterErr: tc.err}
			if call == "save" {
				store = &bindingBoundaryStore{saveErr: tc.err}
			}
			r := gin.New()
			RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store})
			w := httptest.NewRecorder()
			method, path, body := "PUT", ciBindingPath, validCIBody
			if call == "read" {
				method, body = "GET", ""
			}
			if call == "parameters" {
				method, path, body = "GET", ciBindingPath+"/parameters", ""
			}
			r.ServeHTTP(w, authenticatedRequest(t, service, role, method, path, body))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
				t.Fatalf("%s %d %s", call, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "PRIVATE") {
				t.Fatal("storage error leaked diagnostics")
			}
			if audit.audits[len(audit.audits)-1].Result != "failed" {
				t.Fatal("missing failure audit")
			}
		}
	}
}

func TestBindingManagementDeniesUnauthenticatedAndLegacy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, audit, sessions := newAuthBoundary(t)
	// Safe methods skip CSRF/Origin and are audited only when the route is a
	// sensitive read, so each mode's expectation depends on the request.
	for _, tc := range []struct {
		method, path, body string
		unsafe, audited    bool
	}{
		{"PUT", ciBindingPath, validCIBody, true, true},
		{"PUT", cdBindingPath, validCDBody, true, true},
		{"GET", ciBindingPath, "", false, false},
		{"GET", cdBindingPath, "", false, false},
		{"GET", ciBindingPath + "/parameters", "", false, true},
		{"GET", cdBindingPath + "/parameters", "", false, true},
	} {
		for _, mode := range []string{"anonymous", "csrf", "origin", "audit", "legacy"} {
			audit.mu.Lock()
			audit.audits = nil
			audit.auditErr = nil
			audit.mu.Unlock()
			store := &bindingBoundaryStore{}
			r := gin.New()
			RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store, LegacyAdminTokenEnabled: true, LegacyAdminToken: "legacy"})
			request := authenticatedRequest(t, service, sessions[auth.RoleAdmin], tc.method, tc.path, tc.body)
			want := 200
			switch mode {
			case "anonymous":
				request.Header.Del("Cookie")
				want = 401
			case "legacy":
				request.Header.Del("Cookie")
				request.Header.Set("X-Ares-Admin-Token", "legacy")
				want = 401
			case "csrf":
				if !tc.unsafe {
					continue
				}
				request.Header.Del("X-CSRF-Token")
				want = 403
			case "origin":
				if !tc.unsafe {
					continue
				}
				request.Header.Set("Origin", "http://invalid.example")
				want = 403
			case "audit":
				if !tc.audited {
					continue
				}
				audit.auditErr = errors.New("private database failure")
				want = 503
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, request)
			if w.Code != want {
				t.Fatalf("%s %s %s => %d %s", mode, tc.method, tc.path, w.Code, w.Body.String())
			}
			if store.saveCalls+store.getCalls+store.parameterCalls != 0 {
				t.Fatalf("%s %s reached binding storage", mode, tc.path)
			}
		}
	}
}

func TestBindingManagementStatusCodesAndParameterIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _, sessions := newAuthBoundary(t)
	role := sessions[auth.RoleAdmin]
	// A present expected_revision is an update, so it answers 200 and forwards
	// the parsed revision instead of creating a second binding.
	store := &bindingBoundaryStore{}
	r := gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authenticatedRequest(t, service, role, "PUT", ciBindingPath, rebindCIBody))
	if w.Code != 200 || store.intent.Expected != 4 || store.intent.Enabled {
		t.Fatalf("rebind %d %+v", w.Code, store.intent)
	}
	if !strings.Contains(w.Body.String(), `"persisted":true`) {
		t.Fatal("rebind did not report persistence")
	}
	// The parameter read returns the stored binding layer verbatim.
	store = &bindingBoundaryStore{}
	r = gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authenticatedRequest(t, service, role, "GET", ciBindingPath+"/parameters", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "repo") || !strings.Contains(w.Body.String(), `"binding_id":"7"`) {
		t.Fatalf("parameters %d %s", w.Code, w.Body.String())
	}
	// A missing binding is a normal not-found, and a viewer can still read the
	// metadata route that the store answers.
	store = &bindingBoundaryStore{getErr: pipelinebinding.ErrTarget}
	r = gin.New()
	RouterWithRuntime(r, Runtime{Auth: service, BindingManagement: store})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authenticatedRequest(t, service, sessions[auth.RoleViewer], "GET", cdBindingPath, ""))
	if w.Code != 404 {
		t.Fatalf("viewer read of a missing binding: %d", w.Code)
	}
	// The metadata route must not fabricate a binding when no store is wired.
	r = gin.New()
	RouterWithRuntime(r, Runtime{Auth: service})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authenticatedRequest(t, service, role, "GET", ciBindingPath, ""))
	if w.Code != 503 {
		t.Fatalf("unavailable store answered %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "binding_store_unavailable") {
		t.Fatalf("unavailable store body %s", body)
	}
}
