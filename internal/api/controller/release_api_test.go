package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/publish"
)

func TestCanonicalReleaseCreateRejectsInvalidKeyBeforeStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		keys       []string
		wantStatus int
	}{
		{name: "missing", wantStatus: http.StatusBadRequest},
		{name: "duplicate", keys: []string{"1234567890abcdef", "fedcba0987654321"}, wantStatus: http.StatusBadRequest},
		{name: "quoted", keys: []string{`"1234567890abcdef"`}, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := NewPublishController()
			router := gin.New()
			router.POST("/app-configs/:config_id/releases", func(c *gin.Context) {
				SetPrincipal(c, auth.Principal{UserID: 88, Username: "alice", DisplayName: "Alice"})
				controller.CreateRelease(c)
			})
			request := httptest.NewRequest(http.MethodPost, "/app-configs/7/releases", strings.NewReader(`{"ref":"main"}`))
			request.Header.Set("Content-Type", "application/json")
			for _, key := range test.keys {
				request.Header.Add("Idempotency-Key", key)
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), `"error":"idempotency_key_invalid"`) {
				t.Fatalf("status/body = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestCanonicalReleaseJSONErrorsUseStableCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    string
	}{
		{name: "unknown", contentType: "application/json", body: `{"ref":"main","unknown":true}`, wantStatus: 400, wantCode: "invalid_request"},
		{name: "duplicate", contentType: "application/json", body: `{"ref":"main","ref":"other"}`, wantStatus: 400, wantCode: "invalid_request"},
		{name: "invalid utf8", contentType: "application/json", body: "{\"ref\":\"main\xff\"}", wantStatus: 400, wantCode: "invalid_request"},
		{name: "content type", contentType: "text/plain", body: `{"ref":"main"}`, wantStatus: 415, wantCode: "invalid_request"},
		{name: "too large", contentType: "application/json", body: `{"ref":"main","inputs":{"x":"` + strings.Repeat("a", int(defaultJSONRequestBytes)) + `"}}`, wantStatus: 413, wantCode: "request_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller := NewPublishController()
			router := gin.New()
			router.POST("/app-configs/:config_id/releases", func(c *gin.Context) {
				SetPrincipal(c, auth.Principal{UserID: 88, Username: "alice", DisplayName: "Alice"})
				controller.CreateRelease(c)
			})
			request := httptest.NewRequest(http.MethodPost, "/app-configs/7/releases", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("Idempotency-Key", "1234567890abcdef")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), `"error":"`+test.wantCode+`"`) {
				t.Fatalf("status/body = %d %.500s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestBindJSONPreservesNumbersInUntypedFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var decoded struct {
		Inputs map[string]any `json:"inputs"`
	}
	router := gin.New()
	router.POST("/", func(c *gin.Context) {
		if BindJSON(c, &decoded, defaultJSONRequestBytes) {
			c.Status(http.StatusNoContent)
		}
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"inputs":{"large":9007199254740993}}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status/body = %d %s", recorder.Code, recorder.Body.String())
	}
	value, ok := decoded.Inputs["large"].(json.Number)
	if !ok || value.String() != "9007199254740993" {
		t.Fatalf("decoded number = %#v", decoded.Inputs["large"])
	}
}

func TestCanonicalReleaseUsesDecimalStringsForBigIntIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("request rejects JSON number", func(t *testing.T) {
		router := gin.New()
		router.POST("/", func(c *gin.Context) {
			var request publish.CreateReleaseRequest
			if BindCanonicalJSON(c, &request, defaultJSONRequestBytes) {
				c.Status(http.StatusNoContent)
			}
		})
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
			`{"ref":"main","expected_workflow_version_id":9007199254740993}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"error":"invalid_request"`) {
			t.Fatalf("status/body = %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("request accepts exact decimal string", func(t *testing.T) {
		var decoded publish.CreateReleaseRequest
		router := gin.New()
		router.POST("/", func(c *gin.Context) {
			if BindCanonicalJSON(c, &decoded, defaultJSONRequestBytes) {
				c.Status(http.StatusNoContent)
			}
		})
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
			`{"ref":"main","expected_workflow_version_id":"9007199254740993"}`))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent || decoded.ExpectedWorkflowVersionID == nil ||
			*decoded.ExpectedWorkflowVersionID != 9007199254740993 {
			t.Fatalf("status/body/value = %d %s %v", recorder.Code, recorder.Body.String(), decoded.ExpectedWorkflowVersionID)
		}
	})

	t.Run("response serializes record and workflow IDs as strings", func(t *testing.T) {
		workflowVersionID := int64(9007199254740993)
		body, err := json.Marshal(publish.ReleaseReceipt{
			RecordID: workflowVersionID,
			Items:    []publish.ReleaseReceiptItem{{WorkflowVersionID: &workflowVersionID}},
		})
		if err != nil {
			t.Fatal(err)
		}
		got := string(body)
		if !strings.Contains(got, `"record_id":"9007199254740993"`) ||
			!strings.Contains(got, `"workflow_version_id":"9007199254740993"`) {
			t.Fatalf("response = %s", got)
		}
	})
}

func TestIdempotentReleaseHeadersExposeOnlyStableReferences(t *testing.T) {
	gin.SetMode(gin.TestMode)
	taskID := 301
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	writeIdempotentReleaseHeaders(context, true, publish.ReleaseReceipt{
		RecordID: 901, TotalCount: 1, SuccessCount: 1,
		Items: []publish.ReleaseReceiptItem{{ConfigID: 201, TaskID: &taskID, Success: true}},
	})
	if got := recorder.Header().Get("Idempotency-Replayed"); got != "true" {
		t.Fatalf("Idempotency-Replayed = %q", got)
	}
	if got := recorder.Header().Get("Location"); got != "/api/v1/deploy/publish/query/301" {
		t.Fatalf("Location = %q", got)
	}
	if got := RequestAuditResourceID(context); got != "receipt:901;outcome:replayed;accepted:1;rejected:0;config:201;task:301" {
		t.Fatalf("audit resource = %q", got)
	}
}

func TestLegacyReleaseIdempotencyHeaderIsOptionalButStrictWhenPresent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name     string
		keys     []string
		wantCode string
	}{
		{name: "missing remains compatible"},
		{name: "valid", keys: []string{"1234567890abcdef"}},
		{name: "duplicate", keys: []string{"1234567890abcdef", "fedcba0987654321"}, wantCode: publish.ReleaseCodeIdempotencyKeyInvalid},
		{name: "invalid", keys: []string{"short"}, wantCode: publish.ReleaseCodeIdempotencyKeyInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/deploy/publish", nil)
			for _, key := range test.keys {
				context.Request.Header.Add("Idempotency-Key", key)
			}
			_, err := legacyIdempotencyToken(context)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("legacyIdempotencyToken() error = %v", err)
				}
				return
			}
			var commandError *publish.ReleaseCommandError
			if !errors.As(err, &commandError) || commandError.Code != test.wantCode ||
				commandError.HTTPStatus != http.StatusBadRequest {
				t.Fatalf("legacyIdempotencyToken() error = %#v", err)
			}
		})
	}
}

func TestLegacyPartialReleaseAuditResourceCoversPartialAndAllRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name                        string
		recordCount                 int
		firstRecordID, lastRecordID int64
		accepted, rejected          int
		want                        string
	}{
		{
			name: "partial", recordCount: 2, firstRecordID: 901, lastRecordID: 904,
			accepted: 2, rejected: 1,
			want: "outcome:created;records:2;accepted:2;rejected:1;receipt_sample_first:901;receipt_sample_last:904",
		},
		{
			name: "all rejected", rejected: 3,
			want: "outcome:created;records:0;accepted:0;rejected:3",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			setLegacyPartialReleaseAuditResource(context, test.recordCount, test.firstRecordID, test.lastRecordID,
				test.accepted, test.rejected)
			if got := RequestAuditResourceID(context); got != test.want {
				t.Fatalf("audit resource = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLegacyResolutionErrorsKeepCanonicalStatusAndSanitizeInfrastructure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		forbidden  string
	}{
		{
			name: "canonical size error",
			err: &publish.ReleaseCommandError{
				Code: publish.ReleaseCodeRequestTooLarge, HTTPStatus: http.StatusRequestEntityTooLarge,
				Message: "too large",
			},
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: publish.ReleaseCodeRequestTooLarge,
		},
		{
			name:       "legacy input error",
			err:        &publish.InputError{Message: "target is ambiguous"},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "storage error",
			err:        errors.New("database password=must-not-leak"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   publicInternalServiceError,
			forbidden:  "must-not-leak",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/deploy/publish", nil)
			writeLegacyResolutionError(context, "发布任务创建失败", test.err)
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus {
				t.Fatalf("status/body = %d %s", recorder.Code, body)
			}
			if test.wantCode != "" && !strings.Contains(body, `"error":"`+test.wantCode+`"`) {
				t.Fatalf("body = %s", body)
			}
			if test.forbidden != "" && strings.Contains(body, test.forbidden) {
				t.Fatalf("body leaked implementation detail: %s", body)
			}
		})
	}
}

func TestLegacyMissingTaskDetailsExposeOnlyStableTaskID(t *testing.T) {
	taskID := 301
	workflowVersionID := int64(9007199254740993)
	result := &publish.CreateBatchPublishResponse{
		TotalCount:  1,
		TaskRecords: make([]publish.CreatePublishResult, 1),
	}
	err := fillLegacyBatchResultFromDetails(
		[]publish.CreatePublishRequest{{AppName: "demo-api", Branch: "main", Env: "qa"}},
		publish.ReleaseReceipt{
			TotalCount: 1, SuccessCount: 1,
			Items: []publish.ReleaseReceiptItem{{
				ConfigID: 20001, Success: true, TaskID: &taskID, WorkflowVersionID: &workflowVersionID,
			}},
		},
		result,
		map[int]*publish.TaskRecordView{},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, `"task_id":301`) || result.SuccessCount != 1 {
		t.Fatalf("legacy result = %s", got)
	}
	for _, forbidden := range []string{`"task_record"`, `"status"`, `"created_at"`, `"updated_at"`} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("legacy result fabricated %s: %s", forbidden, got)
		}
	}
}

func TestReleaseTargetKeywordUsesUTF8CharacterLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	controller := NewPublishController()
	router := gin.New()
	router.GET("/releases/targets", controller.ListReleaseTargets)

	for _, test := range []struct {
		name        string
		query       string
		wantInvalid bool
	}{
		{name: "invalid utf8", query: "%FF", wantInvalid: true},
		{name: "one hundred unicode characters", query: url.QueryEscape(strings.Repeat("发", 100))},
		{name: "one hundred and one unicode characters", query: url.QueryEscape(strings.Repeat("发", 101)), wantInvalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/releases/targets?env=dev&q="+test.query, nil)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			isInvalid := recorder.Code == http.StatusBadRequest &&
				strings.Contains(recorder.Body.String(), `"error":"invalid_request"`)
			if isInvalid != test.wantInvalid {
				t.Fatalf("status/body = %d %s, invalid=%t", recorder.Code, recorder.Body.String(), isInvalid)
			}
		})
	}
}

func TestReleaseFailureAuditResourceDoesNotContainRequestData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Params = gin.Params{{Key: "config_id", Value: "201"}}
	setReleaseFailureAuditResource(context, publish.ReleaseCodeIdempotencyKeyConflict)
	if got := RequestAuditResourceID(context); got != "config:201;outcome:idempotency_key_conflict" {
		t.Fatalf("audit resource = %q", got)
	}
}
