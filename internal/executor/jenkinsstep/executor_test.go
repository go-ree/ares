package jenkinsstep

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/workflow"
)

type fakeJenkinsClient struct {
	address string
	start   func(context.Context, string, map[string]string) (int64, string, error)
	queue   func(context.Context, int64) (jenkins.QueueBuildState, error)
	status  func(context.Context, string, int64) (string, error)
	logs    func(context.Context, string, int64, int64) (jenkins.BuildLogPage, error)
}

func (f *fakeJenkinsClient) Address() string { return f.address }
func (f *fakeJenkinsClient) QueueBuildTaskContext(ctx context.Context, job string, params map[string]string) (int64, string, error) {
	return f.start(ctx, job, params)
}
func (f *fakeJenkinsClient) GetQueueBuildStateContext(ctx context.Context, id int64) (jenkins.QueueBuildState, error) {
	return f.queue(ctx, id)
}
func (f *fakeJenkinsClient) GetBuildStatusContext(ctx context.Context, job string, id int64) (string, error) {
	return f.status(ctx, job, id)
}
func (f *fakeJenkinsClient) ReadBuildLogChunkContext(ctx context.Context, job string, id, cursor int64) (jenkins.BuildLogPage, error) {
	if f.logs == nil {
		return jenkins.BuildLogPage{}, errors.New("unexpected log read")
	}
	return f.logs(ctx, job, id, cursor)
}

func TestValidateConfig(t *testing.T) {
	executor := New()
	if !executor.Descriptor().Capabilities.Logs {
		t.Fatal("Jenkins executor must advertise its LogReader capability")
	}
	for _, test := range []struct {
		name    string
		config  string
		wantErr bool
	}{
		{name: "minimal", config: `{"job":"build-app"}`},
		{name: "parameters", config: `{"integration":"jenkins/default","job":"folder/build","parameters":{"target":"image"}}`},
		{name: "missing job", config: `{}`, wantErr: true},
		{name: "unknown integration", config: `{"integration":"other","job":"build"}`, wantErr: true},
		{name: "unknown field", config: `{"job":"build","token":"secret"}`, wantErr: true},
		{name: "dot segment", config: `{"job":"folder/../build"}`, wantErr: true},
		{name: "empty segment", config: `{"job":"folder//build"}`, wantErr: true},
		{name: "control character", config: `{"job":"folder\n/build"}`, wantErr: true},
		{name: "plain secret parameter", config: `{"job":"build","parameters":{"api_token":"secret"}}`, wantErr: true},
		{name: "camel access token parameter", config: `{"job":"build","parameters":{"accessToken":"secret"}}`, wantErr: true},
		{name: "camel client secret parameter", config: `{"job":"build","parameters":{"clientSecret":"secret"}}`, wantErr: true},
		{name: "camel API key parameter", config: `{"job":"build","parameters":{"apiKey":"secret"}}`, wantErr: true},
		{name: "camel password parameter", config: `{"job":"build","parameters":{"dbPassword":"secret"}}`, wantErr: true},
		{name: "pwd parameter", config: `{"job":"build","parameters":{"pwd":"secret"}}`, wantErr: true},
		{name: "pass parameter", config: `{"job":"build","parameters":{"pass":"secret"}}`, wantErr: true},
		{name: "passphrase parameter", config: `{"job":"build","parameters":{"keyPassphrase":"secret"}}`, wantErr: true},
		{name: "kubeconfig parameter", config: `{"job":"build","parameters":{"kubeconfig":"secret"}}`, wantErr: true},
		{name: "basic auth parameter", config: `{"job":"build","parameters":{"basicAuth":"secret"}}`, wantErr: true},
		{name: "docker auth parameter", config: `{"job":"build","parameters":{"dockerConfigJson":"secret"}}`, wantErr: true},
		{name: "GitHub PAT parameter", config: `{"job":"build","parameters":{"githubPat":"secret"}}`, wantErr: true},
		{name: "Sonar login parameter", config: `{"job":"build","parameters":{"sonar.login":"secret"}}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := executor.Validate(json.RawMessage(test.config)); (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestExecutorRefreshesSettingsBeforeExternalOperations(t *testing.T) {
	const privateError = "database-password-must-not-leak"
	acquires := 0
	executor := &Executor{
		acquire: func() jenkinsClient {
			acquires++
			return nil
		},
		ensureCurrent: func(context.Context) error { return errors.New(privateError) },
	}
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "availability", run: func() error { return executor.Available(context.Background()) }},
		{name: "start", run: func() error {
			_, err := executor.Start(context.Background(), workflow.StartRequest{Config: json.RawMessage(`{"job":"demo"}`)})
			return err
		}},
		{name: "reconcile", run: func() error {
			_, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{
				ExternalReference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo","build_id":1}`),
			})
			return err
		}},
		{name: "logs", run: func() error {
			_, err := executor.ReadLogs(context.Background(), workflow.LogRequest{
				ExternalReference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo","build_id":1}`),
			})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if !errors.Is(err, workflow.ErrExecutorUnavailable) {
				t.Fatalf("error = %v, want ErrExecutorUnavailable", err)
			}
			if strings.Contains(err.Error(), privateError) {
				t.Fatalf("private refresh error leaked: %v", err)
			}
		})
	}
	if acquires != 0 {
		t.Fatalf("runtime acquired %d times before settings refresh succeeded", acquires)
	}
}

func TestReadLogsUsesOnlyOpaqueReferenceAndCursor(t *testing.T) {
	reads := 0
	client := &fakeJenkinsClient{
		address: "https://jenkins.example",
		logs: func(_ context.Context, job string, buildID, cursor int64) (jenkins.BuildLogPage, error) {
			reads++
			if job != "folder/demo" || buildID != 42 || cursor != 17 {
				t.Fatalf("log request=(%q,%d,%d)", job, buildID, cursor)
			}
			return jenkins.BuildLogPage{Content: "next\n", NextStart: 22, EOF: true}, nil
		},
	}
	executor := &Executor{acquire: func() jenkinsClient { return client }}
	chunk, err := executor.ReadLogs(context.Background(), workflow.LogRequest{
		TaskID: 7, StepKey: "build", Cursor: "17",
		ExternalReference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"folder/demo","build_id":42}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || chunk.Content != "next\n" || chunk.Cursor != "22" || !chunk.EOF {
		t.Fatalf("reads=%d chunk=%#v", reads, chunk)
	}
}

func TestReadLogsRejectsUnsafeReferencesBeforeNetwork(t *testing.T) {
	for _, test := range []struct {
		name      string
		reference string
		cursor    string
		want      error
	}{
		{name: "queue only", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo","queue_id":9}`, want: workflow.ErrLogsNotReady},
		{name: "invalid cursor", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo","build_id":9}`, cursor: "-1", want: workflow.ErrInvalidLogCursor},
		{name: "unknown field", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo","build_id":9,"token":"hidden"}`, want: workflow.ErrLogSourceMismatch},
		{name: "wrong integration", reference: `{"integration":"jenkins/other","address":"https://jenkins.example","job":"demo","build_id":9}`, want: workflow.ErrLogSourceMismatch},
		{name: "invalid address", reference: `{"integration":"jenkins/default","address":"file:///tmp/jenkins","job":"demo","build_id":9}`, want: workflow.ErrLogSourceMismatch},
		{name: "oversized job", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","build_id":9}`, want: workflow.ErrLogSourceMismatch},
		{name: "control in job", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"folder\u000ademo","build_id":9}`, want: workflow.ErrLogSourceMismatch},
		{name: "empty path segment", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"folder//demo","build_id":9}`, want: workflow.ErrLogSourceMismatch},
		{name: "dot path segment", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"folder/../demo","build_id":9}`, want: workflow.ErrLogSourceMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			acquires := 0
			executor := &Executor{acquire: func() jenkinsClient {
				acquires++
				return &fakeJenkinsClient{address: "https://jenkins.example"}
			}}
			_, err := executor.ReadLogs(context.Background(), workflow.LogRequest{
				Cursor: test.cursor, ExternalReference: json.RawMessage(test.reference),
			})
			if !errors.Is(err, test.want) || acquires != 0 {
				t.Fatalf("error=%v want=%v acquires=%d", err, test.want, acquires)
			}
		})
	}

	reads := 0
	executor := &Executor{acquire: func() jenkinsClient {
		return &fakeJenkinsClient{address: "https://jenkins-new.example", logs: func(context.Context, string, int64, int64) (jenkins.BuildLogPage, error) {
			reads++
			return jenkins.BuildLogPage{}, nil
		}}
	}}
	_, err := executor.ReadLogs(context.Background(), workflow.LogRequest{ExternalReference: json.RawMessage(
		`{"integration":"jenkins/default","address":"https://jenkins-old.example","job":"demo","build_id":9}`,
	)})
	if !errors.Is(err, workflow.ErrLogSourceMismatch) || reads != 0 {
		t.Fatalf("address mismatch error=%v reads=%d", err, reads)
	}
}

func TestStartPassesStableMetadataAndReturnsOpaqueReference(t *testing.T) {
	var gotJob string
	var gotParameters map[string]string
	acquireCalls := 0
	client := &fakeJenkinsClient{
		address: "https://jenkins.example",
		start: func(_ context.Context, job string, parameters map[string]string) (int64, string, error) {
			gotJob = job
			gotParameters = parameters
			return 42, job, nil
		},
	}
	executor := &Executor{acquire: func() jenkinsClient {
		acquireCalls++
		return client
	}}
	result, err := executor.Start(context.Background(), workflow.StartRequest{
		TaskID: 7, StepKey: "build", Attempt: 1, IdempotencyKey: "7/build/1",
		Config: json.RawMessage(`{"job":"demo-ci","parameters":{"custom":"yes"}}`),
		Release: workflow.ReleaseContext{
			AppName: "demo-api", Env: "qa-cn", Ref: "main", Publisher: "tester",
			Inputs: json.RawMessage(`{"pod_count":"2"}`),
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if gotJob != "demo-ci" {
		t.Fatalf("job = %q", gotJob)
	}
	wantSubset := map[string]string{
		"app_name": "demo-api", "env": "qa-cn", "branch": "main", "publisher": "tester",
		"task_id": "7", "ares_step_key": "build", "ares_attempt": "1",
		"ares_idempotency_key": "7/build/1", "pod_count": "2", "custom": "yes",
	}
	for key, want := range wantSubset {
		if gotParameters[key] != want {
			t.Errorf("parameter %s = %q, want %q", key, gotParameters[key], want)
		}
	}
	var reference externalReference
	if err := json.Unmarshal(result.ExternalReference, &reference); err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	if !reflect.DeepEqual(reference, externalReference{
		Integration: "jenkins/default", Address: "https://jenkins.example", Job: "demo-ci", QueueID: 42,
	}) {
		t.Fatalf("reference = %#v", reference)
	}
	if result.State != workflow.ResultRunning {
		t.Fatalf("state = %q", result.State)
	}
	if acquireCalls != 1 {
		t.Fatalf("Start acquired %d runtime snapshots, want exactly one", acquireCalls)
	}
}

func TestReconcilePromotesQueueReferenceToBuildReference(t *testing.T) {
	acquireCalls := 0
	client := &fakeJenkinsClient{
		address: "https://jenkins.example",
		start: func(context.Context, string, map[string]string) (int64, string, error) {
			return 0, "", nil
		},
		queue: func(context.Context, int64) (jenkins.QueueBuildState, error) {
			return jenkins.QueueBuildState{BuildID: 73, Why: "upstream-secret-must-not-appear"}, nil
		},
		status: func(_ context.Context, _ string, buildID int64) (string, error) {
			if buildID != 73 {
				t.Fatalf("buildID = %d, want 73", buildID)
			}
			return "RUNNING", nil
		},
	}
	executor := &Executor{acquire: func() jenkinsClient {
		acquireCalls++
		return client
	}}
	result, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{
		ExternalReference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","queue_id":42}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	var reference externalReference
	if err := json.Unmarshal(result.ExternalReference, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.QueueID != 42 || reference.BuildID != 73 || result.State != workflow.ResultRunning {
		t.Fatalf("reference=%#v result=%#v", reference, result)
	}
	if result.Message == "upstream-secret-must-not-appear" {
		t.Fatal("untrusted Jenkins queue reason was exposed")
	}
	if acquireCalls != 1 {
		t.Fatalf("Reconcile acquired %d runtime snapshots, want exactly one", acquireCalls)
	}
}

func TestReconcileDoesNotExposeJenkinsQueueReason(t *testing.T) {
	client := &fakeJenkinsClient{
		address: "https://jenkins.example",
		queue: func(context.Context, int64) (jenkins.QueueBuildState, error) {
			return jenkins.QueueBuildState{Why: "upstream-secret-must-not-appear"}, nil
		},
	}
	executor := &Executor{acquire: func() jenkinsClient { return client }}
	result, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{
		ExternalReference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","queue_id":42}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != workflow.ResultRunning || result.Message != "Jenkins 任务仍在队列中" {
		t.Fatalf("result = %#v", result)
	}
}

func TestReconcileClassifiesProviderReadFailuresAsRetryableWithoutLeakingDetails(t *testing.T) {
	const privateError = "https://private-jenkins.example Authorization=must-not-leak"
	for _, test := range []struct {
		name      string
		reference json.RawMessage
		client    *fakeJenkinsClient
	}{
		{
			name:      "queue",
			reference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","queue_id":42}`),
			client: &fakeJenkinsClient{
				address: "https://jenkins.example",
				queue: func(context.Context, int64) (jenkins.QueueBuildState, error) {
					return jenkins.QueueBuildState{}, errors.New(privateError)
				},
			},
		},
		{
			name:      "build",
			reference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","build_id":42}`),
			client: &fakeJenkinsClient{
				address: "https://jenkins.example",
				status:  func(context.Context, string, int64) (string, error) { return "", errors.New(privateError) },
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &Executor{acquire: func() jenkinsClient { return test.client }}
			_, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{
				ExternalReference: test.reference,
			})
			if !errors.Is(err, workflow.ErrExecutorUnavailable) {
				t.Fatalf("Reconcile() error = %v, want ErrExecutorUnavailable", err)
			}
			if strings.Contains(err.Error(), privateError) {
				t.Fatalf("provider error leaked: %v", err)
			}
		})
	}
}

func TestReconcileMapsJenkinsStatus(t *testing.T) {
	reference := json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","build_id":42}`)
	for _, test := range []struct {
		jenkins string
		want    string
	}{
		{"RUNNING", workflow.ResultRunning},
		{"SUCCESS", workflow.ResultSucceeded},
		{"FAILURE", workflow.ResultFailed},
		{"ABORTED", workflow.ResultCancelled},
		{"UNSTABLE", workflow.ResultFailed},
		{"NOT_BUILT", workflow.ResultFailed},
		{"ABNORMAL", workflow.ResultUnknown},
	} {
		client := &fakeJenkinsClient{
			address: "https://jenkins.example",
			start: func(context.Context, string, map[string]string) (int64, string, error) {
				return 0, "", nil
			},
			queue: func(context.Context, int64) (jenkins.QueueBuildState, error) {
				return jenkins.QueueBuildState{}, nil
			},
			status: func(context.Context, string, int64) (string, error) { return test.jenkins, nil },
		}
		executor := &Executor{acquire: func() jenkinsClient { return client }}
		result, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{ExternalReference: reference})
		if err != nil {
			t.Fatalf("Reconcile(%s) error = %v", test.jenkins, err)
		}
		if result.State != test.want {
			t.Errorf("Reconcile(%s) state = %s, want %s", test.jenkins, result.State, test.want)
		}
	}
}

func TestReconcilePreservesResolvedBuildReferenceOnQueryFailure(t *testing.T) {
	client := &fakeJenkinsClient{
		address: "https://jenkins.example",
		queue: func(context.Context, int64) (jenkins.QueueBuildState, error) {
			return jenkins.QueueBuildState{BuildID: 42}, nil
		},
		status: func(context.Context, string, int64) (string, error) {
			return "", errors.New("temporary connection failure")
		},
	}
	executor := &Executor{acquire: func() jenkinsClient { return client }}
	result, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{
		ExternalReference: json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","queue_id":7}`),
	})
	if !errors.Is(err, workflow.ErrExecutorUnavailable) {
		t.Fatalf("err=%v", err)
	}
	reference, err := decodeExternalReference(result.ExternalReference)
	if err != nil || reference.BuildID != 42 || reference.QueueID != 7 {
		t.Fatalf("reference=%+v err=%v", reference, err)
	}
}

func TestReconcileFailsDeterministicallyWhenJenkinsAddressChanged(t *testing.T) {
	client := &fakeJenkinsClient{
		address: "https://jenkins-new.example",
		start: func(context.Context, string, map[string]string) (int64, string, error) {
			return 0, "", nil
		},
		queue: func(context.Context, int64) (jenkins.QueueBuildState, error) {
			t.Fatal("changed-address reconciliation must not query Jenkins")
			return jenkins.QueueBuildState{}, nil
		},
		status: func(context.Context, string, int64) (string, error) {
			t.Fatal("changed-address reconciliation must not query Jenkins")
			return "", nil
		},
	}
	executor := &Executor{acquire: func() jenkinsClient { return client }}
	reference := json.RawMessage(`{"integration":"jenkins/default","address":"https://jenkins-old.example","job":"demo-ci","build_id":42}`)
	result, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{ExternalReference: reference})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != workflow.ResultOutcomeUnknown || !reflect.DeepEqual(result.ExternalReference, reference) {
		t.Fatalf("result = %#v, want terminal failure retaining reference", result)
	}
}

func TestReconcileRejectsUnboundOrInvalidReferenceWithoutNetwork(t *testing.T) {
	for _, test := range []struct {
		name      string
		reference string
	}{
		{name: "missing integration", reference: `{"address":"https://jenkins.example","job":"demo-ci","build_id":42}`},
		{name: "wrong integration", reference: `{"integration":"jenkins/other","address":"https://jenkins.example","job":"demo-ci","build_id":42}`},
		{name: "missing address", reference: `{"integration":"jenkins/default","job":"demo-ci","build_id":42}`},
		{name: "invalid address", reference: `{"integration":"jenkins/default","address":"file:///tmp/jenkins","job":"demo-ci","build_id":42}`},
		{name: "trailing object", reference: `{"integration":"jenkins/default","address":"https://jenkins.example","job":"demo-ci","build_id":42}{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			acquireCalls := 0
			executor := &Executor{acquire: func() jenkinsClient {
				acquireCalls++
				return &fakeJenkinsClient{address: "https://jenkins.example"}
			}}
			reference := json.RawMessage(test.reference)
			result, err := executor.Reconcile(context.Background(), workflow.ReconcileRequest{ExternalReference: reference})
			if err != nil {
				t.Fatal(err)
			}
			if result.State != workflow.ResultOutcomeUnknown || !reflect.DeepEqual(result.ExternalReference, reference) {
				t.Fatalf("result = %#v, want terminal failure retaining reference", result)
			}
			if acquireCalls != 0 {
				t.Fatalf("acquired %d runtime snapshots, want none", acquireCalls)
			}
		})
	}
}
