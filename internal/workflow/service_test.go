package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"ares/internal/entity"
)

type memoryDefinitionStore struct {
	mu       sync.Mutex
	current  map[int]WorkflowView
	versions map[int][]WorkflowView
}

func newMemoryDefinitionStore() *memoryDefinitionStore {
	return &memoryDefinitionStore{current: make(map[int]WorkflowView), versions: make(map[int][]WorkflowView)}
}

func (m *memoryDefinitionStore) SaveWorkflow(_ context.Context, command SaveWorkflowCommand) (WorkflowView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.current[command.ConfigID]
	if (!exists && command.ExpectedRevision != 0) || (exists && command.ExpectedRevision != current.Revision) {
		return WorkflowView{}, ErrRevisionConflict
	}
	view := WorkflowView{ConfigID: command.ConfigID, WorkflowID: int64(command.ConfigID), Spec: command.Spec}
	if exists {
		view.Version = current.Version + 1
		view.Revision = current.Revision + 1
	} else {
		view.Version = 1
		view.Revision = 1
	}
	view.WorkflowVersionID = int64(command.ConfigID*100 + view.Version)
	m.current[command.ConfigID] = view
	m.versions[command.ConfigID] = append(m.versions[command.ConfigID], view)
	return view, nil
}

func (m *memoryDefinitionStore) GetCurrentWorkflow(_ context.Context, configID int) (WorkflowView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	view, exists := m.current[configID]
	if !exists {
		return WorkflowView{}, ErrNotFound
	}
	return view, nil
}

func TestServiceCreatesImmutableVersionsWithRevisionGuard(t *testing.T) {
	store := newMemoryDefinitionStore()
	service := NewService(store, DefaultRegistry())
	firstSpec := WorkflowSpec{SchemaVersion: 1, Name: "first", Steps: []StepSpec{{Key: "one", Name: "one", Uses: NoopUses}}}
	first, err := service.Save(context.Background(), 42, 0, "tester", firstSpec)
	if err != nil {
		t.Fatal(err)
	}
	secondSpec := WorkflowSpec{SchemaVersion: 1, Name: "second", Steps: []StepSpec{{Key: "two", Name: "two", Uses: NoopUses}}}
	second, err := service.Save(context.Background(), 42, first.Revision, "tester", secondSpec)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != 2 || second.Revision != 2 || first.Spec.Name != "first" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	_, err = service.Save(context.Background(), 42, first.Revision, "stale", secondSpec)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale save error = %v", err)
	}
}

type atomicMemoryExecutionStore struct {
	memoryExecutionStore
	createdWorkflow WorkflowView
	createCalled    bool
}

func (m *atomicMemoryExecutionStore) CreateTaskWithSnapshot(_ context.Context, task *entity.TaskRecord, workflow WorkflowView) error {
	m.createCalled = true
	task.TaskId = 321
	task.EngineVersion = 2
	task.WorkflowVersionID = workflow.WorkflowVersionID
	m.createdWorkflow = workflow
	return nil
}

func TestServiceAllowsSavingUnavailableExecutorButBlocksTaskCreation(t *testing.T) {
	definitions := newMemoryDefinitionStore()
	registry := NewRegistry()
	if err := registry.Register(unavailableExecutor{NewNoopExecutor()}); err != nil {
		t.Fatal(err)
	}
	service := NewService(definitions, registry)
	spec := WorkflowSpec{
		SchemaVersion: SchemaVersionV1,
		Name:          "Jenkins can be configured before its integration",
		Steps: []StepSpec{{
			Key: "build", Name: "build", Uses: "test.unavailable@v1",
		}},
	}
	if _, err := service.Save(context.Background(), 9, 0, "tester", spec); err != nil {
		t.Fatalf("Save() should validate configuration without requiring runtime availability: %v", err)
	}

	execution := &atomicMemoryExecutionStore{}
	task := &entity.TaskRecord{AppName: "demo", Env: "qa-cn", Branch: "main", Publisher: "tester"}
	_, err := service.CreateTask(context.Background(), execution, 9, task)
	if err == nil || !strings.Contains(err.Error(), "当前不可用") {
		t.Fatalf("CreateTask() error = %v, want unavailable executor error", err)
	}
	if execution.createCalled || task.TaskId != 0 {
		t.Fatalf("unavailable workflow must not create a task: called=%v task=%#v", execution.createCalled, task)
	}
}

func TestServiceCreateTaskUsesAtomicStoreAndFillsTaskID(t *testing.T) {
	definitions := newMemoryDefinitionStore()
	service := NewService(definitions, DefaultRegistry())
	spec := WorkflowSpec{SchemaVersion: 1, Name: "demo", Steps: []StepSpec{{Key: "one", Name: "one", Uses: NoopUses}}}
	view, err := service.Save(context.Background(), 7, 0, "tester", spec)
	if err != nil {
		t.Fatal(err)
	}
	execution := &atomicMemoryExecutionStore{}
	task := &entity.TaskRecord{AppName: "demo", Env: "qa-cn", Branch: "main", Publisher: "tester"}
	createdView, err := service.CreateTask(context.Background(), execution, 7, task)
	if err != nil {
		t.Fatal(err)
	}
	if task.TaskId != 321 || createdView.WorkflowVersionID != view.WorkflowVersionID {
		t.Fatalf("task=%#v view=%#v", task, createdView)
	}
	if execution.createdWorkflow.WorkflowVersionID != view.WorkflowVersionID {
		t.Fatalf("atomic store received %#v", execution.createdWorkflow)
	}
}
