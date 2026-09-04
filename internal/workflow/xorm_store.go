package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-ree/ares/internal/canonicaljson"
	"github.com/go-ree/ares/internal/entity"

	"xorm.io/xorm"
)

type XORMStore struct {
	engine *xorm.Engine
}

func NewXORMStore(engine *xorm.Engine) *XORMStore {
	return &XORMStore{engine: engine}
}

func (s *XORMStore) SaveWorkflow(ctx context.Context, command SaveWorkflowCommand) (view WorkflowView, err error) {
	if s.engine == nil {
		return WorkflowView{}, fmt.Errorf("数据库未初始化")
	}
	specJSON, err := json.Marshal(command.Spec)
	if err != nil {
		return WorkflowView{}, fmt.Errorf("序列化工作流规范: %w", err)
	}
	specJSON, err = canonicaljson.Canonicalize(specJSON)
	if err != nil {
		return WorkflowView{}, fmt.Errorf("规范化工作流规范: %w", err)
	}
	digest := sha256.Sum256(specJSON)

	session := s.engine.NewSession()
	defer session.Close()
	if err := session.Begin(); err != nil {
		return WorkflowView{}, err
	}
	defer func() {
		if err != nil {
			_ = session.Rollback()
		}
	}()

	var appConfig entity.AppConfigs
	has, err := session.Context(ctx).ForUpdate().ID(command.ConfigID).Where("deleted_at IS NULL").Get(&appConfig)
	if err != nil {
		return WorkflowView{}, err
	}
	if !has {
		return WorkflowView{}, fmt.Errorf("应用配置不存在，config_id=%d: %w", command.ConfigID, ErrNotFound)
	}

	var binding entity.AppConfigWorkflow
	hasBinding, err := session.Context(ctx).ForUpdate().Where("app_config_id = ?", command.ConfigID).Get(&binding)
	if err != nil {
		return WorkflowView{}, err
	}
	actor := strings.TrimSpace(command.Actor)
	if actor == "" {
		actor = "system"
	}

	var workflowID int64
	var nextVersion, nextRevision int
	if !hasBinding {
		if command.ExpectedRevision != 0 {
			return WorkflowView{}, fmt.Errorf("期望 revision=%d，当前尚无绑定: %w", command.ExpectedRevision, ErrRevisionConflict)
		}
		workflowRow := entity.ReleaseWorkflow{Name: command.Spec.Name}
		if _, err = session.Context(ctx).Insert(&workflowRow); err != nil {
			return WorkflowView{}, err
		}
		workflowID = workflowRow.WorkflowID
		nextVersion = 1
		nextRevision = 1
	} else {
		if command.ExpectedRevision != binding.Revision {
			return WorkflowView{}, fmt.Errorf("期望 revision=%d，当前 revision=%d: %w", command.ExpectedRevision, binding.Revision, ErrRevisionConflict)
		}
		workflowID = binding.WorkflowID
		var latest entity.ReleaseWorkflowVersion
		hasLatest, queryErr := session.Context(ctx).Where("workflow_id = ?", workflowID).Desc("version").Get(&latest)
		if queryErr != nil {
			return WorkflowView{}, queryErr
		}
		if !hasLatest {
			return WorkflowView{}, fmt.Errorf("流程 %d 没有版本: %w", workflowID, ErrNotFound)
		}
		if _, decodeErr := decodeStoredWorkflowSpec(latest); decodeErr != nil {
			return WorkflowView{}, decodeErr
		}
		nextVersion = latest.Version + 1
		nextRevision = binding.Revision + 1
		if _, err = session.Context(ctx).ID(workflowID).Cols("name").Update(&entity.ReleaseWorkflow{Name: command.Spec.Name}); err != nil {
			return WorkflowView{}, err
		}
	}

	versionRow := entity.ReleaseWorkflowVersion{
		WorkflowID:      workflowID,
		Version:         nextVersion,
		Spec:            append(json.RawMessage(nil), specJSON...),
		Checksum:        hex.EncodeToString(digest[:]),
		CreatedBy:       actor,
		CreatedByUserID: command.ActorUserID,
	}
	if _, err = session.Context(ctx).Insert(&versionRow); err != nil {
		return WorkflowView{}, err
	}

	if !hasBinding {
		binding = entity.AppConfigWorkflow{
			AppConfigID: command.ConfigID,
			WorkflowID:  workflowID,
			VersionID:   versionRow.VersionID,
			Revision:    nextRevision,
		}
		if _, err = session.Context(ctx).Insert(&binding); err != nil {
			return WorkflowView{}, err
		}
	} else {
		updated, updateErr := session.Context(ctx).
			Where("binding_id = ? AND revision = ?", binding.BindingID, command.ExpectedRevision).
			Cols("version_id", "revision").
			Update(&entity.AppConfigWorkflow{VersionID: versionRow.VersionID, Revision: nextRevision})
		if updateErr != nil {
			return WorkflowView{}, updateErr
		}
		if updated != 1 {
			return WorkflowView{}, ErrRevisionConflict
		}
	}

	if err = session.Commit(); err != nil {
		return WorkflowView{}, err
	}
	return WorkflowView{
		ConfigID:          command.ConfigID,
		WorkflowID:        workflowID,
		WorkflowVersionID: versionRow.VersionID,
		Version:           nextVersion,
		Revision:          nextRevision,
		Spec:              command.Spec,
	}, nil
}

func (s *XORMStore) GetCurrentWorkflow(ctx context.Context, configID int) (WorkflowView, error) {
	if s.engine == nil {
		return WorkflowView{}, fmt.Errorf("数据库未初始化")
	}
	var binding entity.AppConfigWorkflow
	has, err := s.engine.Context(ctx).Where("app_config_id = ?", configID).Get(&binding)
	if err != nil {
		return WorkflowView{}, err
	}
	if !has {
		return WorkflowView{}, ErrNotFound
	}
	var version entity.ReleaseWorkflowVersion
	has, err = s.engine.Context(ctx).ID(binding.VersionID).Get(&version)
	if err != nil {
		return WorkflowView{}, err
	}
	if !has {
		return WorkflowView{}, ErrNotFound
	}
	spec, err := decodeStoredWorkflowSpec(version)
	if err != nil {
		return WorkflowView{}, err
	}
	return WorkflowView{
		ConfigID:          configID,
		WorkflowID:        binding.WorkflowID,
		WorkflowVersionID: binding.VersionID,
		Version:           version.Version,
		Revision:          binding.Revision,
		Spec:              spec,
	}, nil
}

func decodeStoredWorkflowSpec(version entity.ReleaseWorkflowVersion) (WorkflowSpec, error) {
	spec, err := DecodeSpecJSON(version.Spec)
	if err != nil {
		return WorkflowSpec{}, fmt.Errorf("读取工作流版本 %d: %w", version.VersionID, err)
	}
	canonical, err := canonicaljson.Canonicalize(version.Spec)
	if err != nil {
		return WorkflowSpec{}, fmt.Errorf("读取工作流版本 %d: %w", version.VersionID, err)
	}
	digest := sha256.Sum256(canonical)
	wantChecksum := hex.EncodeToString(digest[:])
	if len(version.Checksum) != len(wantChecksum) || version.Checksum != wantChecksum {
		return WorkflowSpec{}, fmt.Errorf("工作流版本 %d 完整性校验失败", version.VersionID)
	}
	return spec, nil
}

// CreateTaskWithSnapshot is the normal v2 publishing path. The task row and
// all step snapshots commit together, so a worker never observes a v2 task
// without the immutable inputs needed to resume it.
func (s *XORMStore) CreateTaskWithSnapshot(ctx context.Context, task *entity.TaskRecord, workflow WorkflowView) (err error) {
	if s.engine == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if task == nil {
		return fmt.Errorf("任务不能为空")
	}
	if task.TaskId != 0 {
		return fmt.Errorf("新任务不能预设 task_id")
	}
	task.EngineVersion = 2
	task.WorkflowVersionID = workflow.WorkflowVersionID
	task.Status = TaskQueued
	task.Message = ""

	session := s.engine.NewSession()
	defer session.Close()
	if err = session.Begin(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = session.Rollback()
		}
	}()

	// Omit empty nullable compatibility columns so SQL stores NULL instead of
	// semantically meaningless empty strings.
	omit := []string{"message"}
	if task.CiJobName == "" {
		omit = append(omit, "ci_job_name")
	}
	if task.CdJobName == "" {
		omit = append(omit, "cd_job_name")
	}
	if task.Products == "" {
		omit = append(omit, "products")
	}
	if _, err = session.Context(ctx).Omit(omit...).Insert(task); err != nil {
		return err
	}
	if task.TaskId <= 0 {
		return fmt.Errorf("数据库未返回 task_id")
	}
	if err = insertTaskStepSnapshots(ctx, session, task.TaskId, workflow); err != nil {
		return err
	}
	return session.Commit()
}

func (s *XORMStore) CreateTaskSnapshot(ctx context.Context, taskID int, workflow WorkflowView) (err error) {
	if s.engine == nil {
		return fmt.Errorf("数据库未初始化")
	}
	session := s.engine.NewSession()
	defer session.Close()
	if err = session.Begin(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = session.Rollback()
		}
	}()

	var task entity.TaskRecord
	has, err := session.Context(ctx).ForUpdate().ID(taskID).Get(&task)
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("任务不存在，task_id=%d: %w", taskID, ErrNotFound)
	}
	count, err := session.Context(ctx).Where("task_id = ?", taskID).Count(&entity.TaskStepRecord{})
	if err != nil {
		return err
	}
	if count > 0 || task.EngineVersion >= 2 {
		return ErrTaskHasSnapshot
	}

	if err = insertTaskStepSnapshots(ctx, session, taskID, workflow); err != nil {
		return err
	}
	updated, err := session.Context(ctx).Table(new(entity.TaskRecord)).ID(taskID).
		Update(map[string]any{
			"engine_version":      2,
			"workflow_version_id": workflow.WorkflowVersionID,
			"status":              TaskQueued,
			"message":             nil,
		})
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("更新任务工作流快照失败，task_id=%d", taskID)
	}
	return session.Commit()
}

func insertTaskStepSnapshots(ctx context.Context, session *xorm.Session, taskID int, workflow WorkflowView) error {
	for position, step := range workflow.Spec.Steps {
		record := entity.TaskStepRecord{
			TaskID:            taskID,
			WorkflowVersionID: workflow.WorkflowVersionID,
			StepKey:           step.Key,
			Name:              step.Name,
			Uses:              step.Uses,
			Category:          step.Category,
			Position:          position,
			Config:            append(json.RawMessage(nil), step.With...),
			TimeoutSeconds:    step.TimeoutSeconds,
			OnFailure:         step.OnFailure,
			Status:            StepPending,
			Attempt:           1,
		}
		if _, err := session.Context(ctx).
			Nullable("category", "external_ref", "output", "message", "started_at", "finished_at").
			Insert(&record); err != nil {
			return err
		}
	}
	return nil
}

func (s *XORMStore) GetTaskReleaseContext(ctx context.Context, taskID int) (ReleaseContext, error) {
	var task entity.TaskRecord
	has, err := s.engine.Context(ctx).ID(taskID).Get(&task)
	if err != nil {
		return ReleaseContext{}, err
	}
	if !has {
		return ReleaseContext{}, ErrNotFound
	}
	return ReleaseContext{
		AppName:   task.AppName,
		Env:       task.Env,
		Ref:       task.Branch,
		Publisher: task.Publisher,
		Inputs:    append(json.RawMessage(nil), task.PipelineParam...),
	}, nil
}

func (s *XORMStore) ListTaskSteps(ctx context.Context, taskID int) ([]entity.TaskStepRecord, error) {
	hasTask, err := s.engine.Context(ctx).ID(taskID).Exist(new(entity.TaskRecord))
	if err != nil {
		return nil, err
	}
	if !hasTask {
		return nil, fmt.Errorf("任务不存在，task_id=%d: %w", taskID, ErrNotFound)
	}
	var rows []entity.TaskStepRecord
	if err := s.engine.Context(ctx).Where("task_id = ?", taskID).Asc("position").Find(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *XORMStore) ClaimStep(ctx context.Context, stepRecordID int64) (bool, error) {
	now := time.Now()
	updated, err := s.engine.Context(ctx).
		Where("step_record_id = ? AND status = ?", stepRecordID, StepPending).
		Cols("status", "started_at").
		Update(&entity.TaskStepRecord{Status: StepRunning, StartedTime: &now})
	return updated == 1, err
}

func (s *XORMStore) ReleaseStep(ctx context.Context, stepRecordID int64, message string) (bool, error) {
	updated, err := s.engine.Context(ctx).Table(new(entity.TaskStepRecord)).
		Where("step_record_id = ? AND status = ? AND external_ref IS NULL", stepRecordID, StepRunning).
		Update(map[string]any{
			"status": StepPending, "started_at": nil, "message": nullableText(truncateRunes(message, 1000)),
		})
	return updated == 1, err
}

func (s *XORMStore) SaveStepResult(ctx context.Context, stepRecordID int64, result Result) (bool, error) {
	status := result.State
	if status == ResultUnknown {
		status = StepRunning
	}
	updates := map[string]any{
		"status":       status,
		"external_ref": nullableJSON(result.ExternalReference),
		"output":       nullableJSON(result.Output),
		"message":      nullableText(truncateRunes(result.Message, 1000)),
	}
	if status != StepRunning {
		now := time.Now()
		updates["finished_at"] = now
	}
	updated, err := s.engine.Context(ctx).
		Where("step_record_id = ? AND status = ?", stepRecordID, StepRunning).
		Table(new(entity.TaskStepRecord)).Update(updates)
	return updated == 1, err
}

func (s *XORMStore) SkipPendingSteps(ctx context.Context, taskID int, message string) error {
	now := time.Now()
	_, err := s.engine.Context(ctx).Table(new(entity.TaskStepRecord)).
		Where("task_id = ? AND status = ?", taskID, StepPending).
		Update(map[string]any{
			"status": StepSkipped, "message": nullableText(truncateRunes(message, 1000)), "finished_at": now,
		})
	return err
}

func (s *XORMStore) SetTaskStatus(ctx context.Context, taskID int, status, message string) error {
	var storedMessage any
	if message != "" {
		storedMessage = truncateRunes(message, 255)
	}
	updated, err := s.engine.Context(ctx).Table(new(entity.TaskRecord)).ID(taskID).
		Update(map[string]any{"status": status, "message": storedMessage})
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	// MySQL reports zero affected rows when the requested status/message are
	// already stored. That is a successful idempotent update, not a missing
	// task. This commonly happens when a synchronous workflow starts its next
	// step within the same coordinator pass and the task is already running.
	exists, err := s.engine.Context(ctx).ID(taskID).Where("deleted_at IS NULL").Exist(new(entity.TaskRecord))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("任务不存在，task_id=%d: %w", taskID, ErrNotFound)
	}
	return nil
}

func nullableJSON(raw json.RawMessage) any {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
