package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-ree/ares/internal/entity"
)

type memoryLogSourceStore struct {
	source TaskStepLogSource
	err    error
	taskID int
	key    string
}

func (s *memoryLogSourceStore) GetTaskStepLogSource(_ context.Context, taskID int, key string) (TaskStepLogSource, error) {
	s.taskID, s.key = taskID, key
	return s.source, s.err
}

type logExecutor struct {
	*NoopExecutor
	read func(LogRequest) (LogChunk, error)
}

func newLogExecutor(read func(LogRequest) (LogChunk, error)) *logExecutor {
	return &logExecutor{NoopExecutor: NewNoopExecutor(), read: read}
}

func (e *logExecutor) Descriptor() Descriptor {
	descriptor := e.NoopExecutor.Descriptor()
	descriptor.Uses = "test.logs@v1"
	descriptor.Capabilities.Logs = true
	return descriptor
}

func (e *logExecutor) ReadLogs(_ context.Context, request LogRequest) (LogChunk, error) {
	return e.read(request)
}

func TestLogServiceDispatchesOnlyPersistedSnapshotIdentity(t *testing.T) {
	reference := json.RawMessage(`{"provider_id":"server-owned"}`)
	store := &memoryLogSourceStore{source: TaskStepLogSource{
		TaskID: 17, StepKey: "build", Uses: "test.logs@v1", Status: StepRunning,
		ExternalReference: reference,
	}}
	var got LogRequest
	registry := NewRegistry()
	if err := registry.Register(newLogExecutor(func(request LogRequest) (LogChunk, error) {
		got = request
		return LogChunk{Content: "hello\n", Cursor: "next"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	session, err := NewLogService(store, registry).Open(context.Background(), 17, "build")
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := session.Read(context.Background(), "before")
	if err != nil {
		t.Fatal(err)
	}
	if store.taskID != 17 || store.key != "build" || got.TaskID != 17 || got.StepKey != "build" ||
		got.Cursor != "before" || string(got.ExternalReference) != string(reference) {
		t.Fatalf("store=(%d,%q), request=%#v", store.taskID, store.key, got)
	}
	if chunk.Content != "hello\n" || chunk.Cursor != "next" {
		t.Fatalf("chunk = %#v", chunk)
	}
}

func TestLogServiceFailsClosedOnMismatchedStoreIdentity(t *testing.T) {
	called := false
	registry := NewRegistry()
	if err := registry.Register(newLogExecutor(func(LogRequest) (LogChunk, error) {
		called = true
		return LogChunk{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	store := &memoryLogSourceStore{source: TaskStepLogSource{
		TaskID: 18, StepKey: "build", Uses: "test.logs@v1",
		ExternalReference: []byte(`{"id":1}`),
	}}
	_, err := NewLogService(store, registry).Open(context.Background(), 17, "build")
	if !errors.Is(err, ErrNotFound) || called {
		t.Fatalf("Open() error=%v readerCalled=%v", err, called)
	}
}

func TestLogServiceReportsUnsupportedMissingAndNotReady(t *testing.T) {
	registry := DefaultRegistry()
	for _, test := range []struct {
		name   string
		source TaskStepLogSource
		want   error
	}{
		{name: "unsupported", source: TaskStepLogSource{TaskID: 1, StepKey: "noop", Uses: NoopUses, ExternalReference: []byte(`{}`)}, want: ErrLogsUnsupported},
		{name: "unregistered", source: TaskStepLogSource{TaskID: 1, StepKey: "gone", Uses: "test.gone@v1", ExternalReference: []byte(`{}`)}, want: ErrExecutorUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewLogService(&memoryLogSourceStore{source: test.source}, registry).Open(context.Background(), 1, test.source.StepKey)
			if !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
	logs := NewRegistry()
	if err := logs.Register(newLogExecutor(func(LogRequest) (LogChunk, error) { return LogChunk{}, nil })); err != nil {
		t.Fatal(err)
	}
	_, err := NewLogService(&memoryLogSourceStore{source: TaskStepLogSource{
		TaskID: 1, StepKey: "build", Uses: "test.logs@v1",
	}}, logs).Open(context.Background(), 1, "build")
	if !errors.Is(err, ErrLogsNotReady) {
		t.Fatalf("not-ready error=%v", err)
	}
}

func TestLogSessionRejectsUnsafeCursorAndChunks(t *testing.T) {
	for _, cursor := range []string{"bad\nvalue", "bad\rvalue", "bad\x00value", strings.Repeat("a", MaxLogCursorBytes+1)} {
		if err := ValidateLogCursor(cursor); !errors.Is(err, ErrInvalidLogCursor) {
			t.Fatalf("ValidateLogCursor(%q)=%v", cursor, err)
		}
	}
	for _, chunk := range []LogChunk{
		{Content: "content", Cursor: "same"},
		{Cursor: "next"},
		{Content: "content", Cursor: "bad\nnext"},
		{Content: strings.Repeat("x", MaxLogChunkBytes+1), Cursor: "next"},
	} {
		if err := validateLogChunk("same", chunk); !errors.Is(err, ErrInvalidLogChunk) {
			t.Fatalf("validateLogChunk(%#v)=%v", chunk, err)
		}
	}
}

func TestCoordinatorDerivesStepCapabilitiesWithoutSerializingPrivateFields(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(newLogExecutor(func(LogRequest) (LogChunk, error) { return LogChunk{}, nil })); err != nil {
		t.Fatal(err)
	}
	steps := []entity.TaskStepRecord{{
		Uses: "test.logs@v1", Config: json.RawMessage(`{"password":"hidden"}`),
		ExternalRef: json.RawMessage(`{"address":"hidden"}`), Output: json.RawMessage(`{"token":"hidden"}`),
	}}
	views := (&Coordinator{registry: registry}).TaskStepViews(steps)
	encoded, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	if !views[0].Capabilities.Logs || strings.Contains(string(encoded), "hidden") ||
		!strings.Contains(string(encoded), `"capabilities":{"retry":true,"logs":true,"cancel":false}`) {
		t.Fatalf("public step = %s", encoded)
	}
}
