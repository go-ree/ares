package jenkinsstep

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"ares/internal/jenkins"
	"ares/internal/workflow"
)

type fakeJenkinsClient struct {
	address string
	start   func(context.Context, string, map[string]string) (int64, string, error)
	queue   func(context.Context, int64) (jenkins.QueueBuildState, error)
	status  func(context.Context, string, int64) (string, error)
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

func TestValidateConfig(t *testing.T) {
	executor := New()
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
		{name: "plain secret parameter", config: `{"job":"build","parameters":{"api_token":"secret"}}`, wantErr: true},
		{name: "camel access token parameter", config: `{"job":"build","parameters":{"accessToken":"secret"}}`, wantErr: true},
		{name: "camel client secret parameter", config: `{"job":"build","parameters":{"clientSecret":"secret"}}`, wantErr: true},
		{name: "camel API key parameter", config: `{"job":"build","parameters":{"apiKey":"secret"}}`, wantErr: true},
		{name: "camel password parameter", config: `{"job":"build","parameters":{"dbPassword":"secret"}}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := executor.Validate(json.RawMessage(test.config)); (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
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
		{"ABNORMAL", workflow.ResultFailed},
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
	if result.State != workflow.ResultFailed || !reflect.DeepEqual(result.ExternalReference, reference) {
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
			if result.State != workflow.ResultFailed || !reflect.DeepEqual(result.ExternalReference, reference) {
				t.Fatalf("result = %#v, want terminal failure retaining reference", result)
			}
			if acquireCalls != 0 {
				t.Fatalf("acquired %d runtime snapshots, want none", acquireCalls)
			}
		})
	}
}
