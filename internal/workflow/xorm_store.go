package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	session.Context(ctx)
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
		if _, decodeErr := DecodeStoredWorkflowVersion(latest); decodeErr != nil {
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
	spec, err := DecodeStoredWorkflowVersion(version)
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

// DecodeStoredWorkflowVersion validates the immutable checksum before exposing
// a workflow snapshot to another domain transaction.
func DecodeStoredWorkflowVersion(version entity.ReleaseWorkflowVersion) (WorkflowSpec, error) {
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
	session := s.engine.NewSession()
	defer session.Close()
	session.Context(ctx)
	if err = session.Begin(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = session.Rollback()
		}
	}()

	if err = InsertTaskWithSnapshotInSession(ctx, session, task, workflow); err != nil {
		return err
	}
	return session.Commit()
}

// Kept private for package tests and older internal call sites.
func decodeStoredWorkflowSpec(version entity.ReleaseWorkflowVersion) (WorkflowSpec, error) {
	return DecodeStoredWorkflowVersion(version)
}

// InsertTaskWithSnapshotInSession inserts a v2 task and every step snapshot in
// the caller-owned transaction. It exists so release admission, idempotency and
// the immutable execution snapshot can share one commit boundary.
func InsertTaskWithSnapshotInSession(ctx context.Context, session *xorm.Session, task *entity.TaskRecord, workflow WorkflowView) error {
	if session == nil {
		return fmt.Errorf("数据库事务不能为空")
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
	task.NextPollAt = nil
	task.LeaseOwner = nil
	task.LeaseExpiresAt = nil
	task.LeaseFencingToken = 0
	task.PollFailureCount = 0

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
	if _, err := session.Context(ctx).Omit(omit...).Insert(task); err != nil {
		return err
	}
	if task.TaskId <= 0 {
		return fmt.Errorf("数据库未返回 task_id")
	}
	result, err := session.Context(ctx).Exec(`UPDATE task_record
		SET next_poll_at = UTC_TIMESTAMP(6)
		WHERE task_id = ? AND engine_version = ? AND deleted_at IS NULL
			AND status IN (?, ?)`, task.TaskId, 2, TaskQueued, TaskRunning)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("初始化任务调度时间失败，task_id=%d", task.TaskId)
	}
	return insertTaskStepSnapshots(ctx, session, task.TaskId, workflow)
}

func (s *XORMStore) CreateTaskSnapshot(ctx context.Context, taskID int, workflow WorkflowView) (err error) {
	if s.engine == nil {
		return fmt.Errorf("数据库未初始化")
	}
	session := s.engine.NewSession()
	defer session.Close()
	session.Context(ctx)
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
		SetExpr("next_poll_at", "UTC_TIMESTAMP(6)").
		Update(map[string]any{
			"engine_version":      2,
			"workflow_version_id": workflow.WorkflowVersionID,
			"status":              TaskQueued,
			"message":             nil,
			"lease_owner":         nil,
			"lease_expires_at":    nil,
			"poll_failure_count":  0,
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

type lockedTaskLeaseRow struct {
	TaskID            int        `xorm:"'task_id'"`
	EngineVersion     int        `xorm:"'engine_version'"`
	Status            string     `xorm:"'status'"`
	DeletedAt         *time.Time `xorm:"'deleted_at'"`
	LeaseOwner        []byte     `xorm:"'lease_owner'"`
	LeaseExpiresAt    *time.Time `xorm:"'lease_expires_at'"`
	LeaseFencingToken uint64     `xorm:"'lease_fencing_token'"`
	DatabaseNow       time.Time  `xorm:"'database_now'"`
}

func validateTaskLeaseHandle(lease TaskLease) error {
	if lease.TaskID <= 0 || lease.FencingToken == 0 {
		return ErrLeaseLost
	}
	if len(lease.Owner) == 0 || len(lease.Owner) > MaxLeaseOwnerBytes {
		return ErrLeaseLost
	}
	for index := 0; index < len(lease.Owner); index++ {
		if lease.Owner[index] < 0x21 || lease.Owner[index] > 0x7e {
			return ErrLeaseLost
		}
	}
	return nil
}

func (s *XORMStore) withValidTaskLease(
	ctx context.Context,
	lease TaskLease,
	operation func(*xorm.Session) error,
) (err error) {
	if s == nil || s.engine == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := validateTaskLeaseHandle(lease); err != nil {
		return err
	}
	session := s.engine.NewSession()
	defer session.Close()
	session.Context(ctx)
	if err = session.Begin(); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = session.Rollback()
		}
	}()

	var locked lockedTaskLeaseRow
	has, queryErr := session.Context(ctx).SQL(`SELECT
			task_id, engine_version, status, deleted_at, lease_owner,
			lease_expires_at, lease_fencing_token, UTC_TIMESTAMP(6) AS database_now
		FROM task_record WHERE task_id = ? FOR UPDATE`, lease.TaskID).Get(&locked)
	if queryErr != nil {
		return queryErr
	}
	if !has || locked.EngineVersion != 2 || locked.DeletedAt != nil ||
		(locked.Status != TaskQueued && locked.Status != TaskRunning) ||
		locked.LeaseExpiresAt == nil || !locked.LeaseExpiresAt.After(locked.DatabaseNow) ||
		locked.LeaseFencingToken != lease.FencingToken ||
		!bytes.Equal(locked.LeaseOwner, []byte(lease.Owner)) {
		return ErrLeaseLost
	}
	if err = operation(session); err != nil {
		return err
	}
	if err = session.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *XORMStore) GetTaskReleaseContext(ctx context.Context, lease TaskLease) (release ReleaseContext, err error) {
	err = s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		var task entity.TaskRecord
		has, queryErr := session.Context(ctx).ID(lease.TaskID).Get(&task)
		if queryErr != nil {
			return queryErr
		}
		if !has {
			return ErrLeaseLost
		}
		release = ReleaseContext{
			AppName:   task.AppName,
			Env:       task.Env,
			Ref:       task.Branch,
			Publisher: task.Publisher,
			Inputs:    append(json.RawMessage(nil), task.PipelineParam...),
		}
		return nil
	})
	return release, err
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

func (s *XORMStore) ListTaskStepsForLease(ctx context.Context, lease TaskLease) (rows []entity.TaskStepRecord, err error) {
	err = s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		return session.Context(ctx).Where("task_id = ?", lease.TaskID).Asc("position").Find(&rows)
	})
	return rows, err
}

func (s *XORMStore) StepTimeRemaining(
	ctx context.Context,
	lease TaskLease,
	stepRecordID int64,
	timeoutSeconds int,
) (remaining time.Duration, err error) {
	if stepRecordID <= 0 || timeoutSeconds <= 0 {
		return 0, fmt.Errorf("步骤超时参数无效")
	}
	err = s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		var row struct {
			RemainingMicroseconds int64 `xorm:"'remaining_microseconds'"`
		}
		has, queryErr := session.Context(ctx).SQL(`SELECT
			TIMESTAMPDIFF(MICROSECOND, CURRENT_TIMESTAMP(6),
				TIMESTAMPADD(SECOND, ?, COALESCE(started_at, updated_at, created_at)))
				AS remaining_microseconds
			FROM task_step_records
			WHERE step_record_id = ? AND task_id = ?`, timeoutSeconds, stepRecordID, lease.TaskID).Get(&row)
		if queryErr != nil {
			return queryErr
		}
		if !has {
			return fmt.Errorf("任务步骤不存在: %w", ErrNotFound)
		}
		if row.RemainingMicroseconds <= 0 {
			remaining = 0
			return nil
		}
		remaining = time.Duration(row.RemainingMicroseconds) * time.Microsecond
		maximum := time.Duration(timeoutSeconds) * time.Second
		if remaining > maximum {
			remaining = maximum
		}
		return nil
	})
	return remaining, err
}

func (s *XORMStore) GetTaskStepLogSource(ctx context.Context, taskID int, stepKey string) (TaskStepLogSource, error) {
	if s == nil || s.engine == nil {
		return TaskStepLogSource{}, fmt.Errorf("数据库未初始化")
	}
	var row entity.TaskStepRecord
	has, err := s.engine.Context(ctx).
		Table(entity.TableTaskStepRecords).Alias("step").
		Select("step.task_id, step.step_key, step.uses, step.status, step.external_ref").
		Join("INNER", []string{entity.TableTaskRecord, "task"}, "task.task_id = step.task_id").
		Where("task.task_id = ? AND step.step_key = ?", taskID, stepKey).
		And("task.deleted_at IS NULL AND task.engine_version >= ?", 2).
		And("task.workflow_version_id = step.workflow_version_id").
		Get(&row)
	if err != nil {
		return TaskStepLogSource{}, err
	}
	if !has {
		// An authorized caller may receive the stable compatibility signal for
		// an existing v1 task, while deleted, unknown, and malformed historical
		// rows remain indistinguishable from a missing resource.
		var task entity.TaskRecord
		hasTask, taskErr := s.engine.Context(ctx).
			Table(entity.TableTaskRecord).
			Select("engine_version").
			Where("task_id = ? AND deleted_at IS NULL", taskID).
			Get(&task)
		if taskErr != nil {
			return TaskStepLogSource{}, taskErr
		}
		if hasTask && task.EngineVersion == 1 {
			return TaskStepLogSource{}, ErrLegacyTask
		}
		return TaskStepLogSource{}, fmt.Errorf("任务步骤不存在，task_id=%d step_key=%s: %w", taskID, stepKey, ErrNotFound)
	}
	return TaskStepLogSource{
		TaskID: row.TaskID, StepKey: row.StepKey, Uses: row.Uses, Status: row.Status,
		ExternalReference: append([]byte(nil), row.ExternalRef...),
	}, nil
}

func (s *XORMStore) ClaimStep(ctx context.Context, lease TaskLease, stepRecordID int64) (claimed bool, err error) {
	if stepRecordID <= 0 {
		return false, fmt.Errorf("step_record_id 必须大于 0")
	}
	err = s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		// Every provider update takes an exclusive lock on its stable settings
		// row. Taking shared locks here makes the pending -> running transition
		// ordered with cross-replica configuration commits without coupling the
		// workflow core to a specific executor. The epoch-7 data contract ensures
		// both built-in fence rows always exist.
		fences, lockErr := session.Context(ctx).Query(`SELECT provider, revision
			FROM integration_settings
			WHERE provider IN ('jenkins', 'kubernetes')
			ORDER BY provider FOR SHARE`)
		if lockErr != nil {
			return lockErr
		}
		if len(fences) != 2 {
			return errors.New("集成配置事务围栏不可用")
		}
		result, updateErr := session.Context(ctx).Exec(`UPDATE task_step_records
			SET status = ?, started_at = CURRENT_TIMESTAMP(6), message = NULL
			WHERE step_record_id = ? AND task_id = ? AND status = ?`,
			StepRunning, stepRecordID, lease.TaskID, StepPending)
		if updateErr != nil {
			return updateErr
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		claimed = updated == 1
		if !claimed {
			return nil
		}
		_, updateErr = session.Context(ctx).Exec(`UPDATE task_record
			SET status = ?, message = NULL, updated_at = CURRENT_TIMESTAMP
			WHERE task_id = ?`, TaskRunning, lease.TaskID)
		return updateErr
	})
	return claimed, err
}

func (s *XORMStore) ReleaseStep(
	ctx context.Context,
	lease TaskLease,
	stepRecordID int64,
	message string,
) (released bool, err error) {
	if stepRecordID <= 0 {
		return false, fmt.Errorf("step_record_id 必须大于 0")
	}
	err = s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		result, updateErr := session.Context(ctx).Exec(`UPDATE task_step_records
			SET status = ?, started_at = NULL, message = ?
			WHERE step_record_id = ? AND task_id = ? AND status = ? AND external_ref IS NULL`,
			StepPending, nullableText(truncateRunes(message, 1000)), stepRecordID, lease.TaskID, StepRunning)
		if updateErr != nil {
			return updateErr
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		released = updated == 1
		return nil
	})
	return released, err
}

func (s *XORMStore) SaveStepResult(
	ctx context.Context,
	lease TaskLease,
	stepRecordID int64,
	stepResult Result,
) (saved bool, err error) {
	if stepRecordID <= 0 {
		return false, fmt.Errorf("step_record_id 必须大于 0")
	}
	status := stepResult.State
	if status == ResultUnknown {
		status = StepRunning
	}
	err = s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		finishedExpression := "NULL"
		if status != StepRunning {
			finishedExpression = "CURRENT_TIMESTAMP(6)"
		}
		query := fmt.Sprintf(`UPDATE task_step_records
			SET status = ?, external_ref = ?, output = ?, message = ?, finished_at = %s
			WHERE step_record_id = ? AND task_id = ? AND status = ?`, finishedExpression)
		result, updateErr := session.Context(ctx).Exec(query,
			status, nullableJSON(stepResult.ExternalReference), nullableJSON(stepResult.Output),
			nullableText(truncateRunes(stepResult.Message, 1000)), stepRecordID, lease.TaskID, StepRunning)
		if updateErr != nil {
			return updateErr
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		saved = updated == 1
		return nil
	})
	return saved, err
}

func (s *XORMStore) SkipPendingSteps(ctx context.Context, lease TaskLease, message string) error {
	return s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		_, err := session.Context(ctx).Exec(`UPDATE task_step_records
			SET status = ?, message = ?, finished_at = CURRENT_TIMESTAMP(6)
			WHERE task_id = ? AND status = ?`,
			StepSkipped, nullableText(truncateRunes(message, 1000)), lease.TaskID, StepPending)
		return err
	})
}

func (s *XORMStore) SetTaskStatus(ctx context.Context, lease TaskLease, status, message string) error {
	if !validTaskStatus(status) {
		return fmt.Errorf("无效任务状态 %q", status)
	}
	return s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		var storedMessage any
		if message != "" {
			storedMessage = truncateRunes(message, 255)
		}
		query := `UPDATE task_record
			SET status = ?, message = ?, updated_at = CURRENT_TIMESTAMP WHERE task_id = ?`
		if terminalTaskStatus(status) {
			query = `UPDATE task_record
				SET status = ?, message = ?, next_poll_at = NULL,
					lease_owner = NULL, lease_expires_at = NULL, poll_failure_count = 0,
					updated_at = CURRENT_TIMESTAMP
				WHERE task_id = ?`
		}
		_, err := session.Context(ctx).Exec(query, status, storedMessage, lease.TaskID)
		return err
	})
}

func validTaskStatus(status string) bool {
	switch status {
	case TaskQueued, TaskRunning, TaskSucceeded, TaskFailed, TaskCancelled, TaskSucceededWithWarnings:
		return true
	default:
		return false
	}
}

func terminalTaskStatus(status string) bool {
	switch status {
	case TaskSucceeded, TaskFailed, TaskCancelled, TaskSucceededWithWarnings:
		return true
	default:
		return false
	}
}

const (
	maxTaskLeaseBatch = 64
	maxWorkerDelay    = 24 * time.Hour
)

func (s *XORMStore) AcquireTaskLeases(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
) ([]TaskLease, error) {
	if s == nil || s.engine == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if !validLeaseOwner(owner) {
		return nil, fmt.Errorf("worker owner 无效")
	}
	if limit < 1 || limit > maxTaskLeaseBatch {
		return nil, fmt.Errorf("任务租约领取数量必须在 1 到 %d 之间", maxTaskLeaseBatch)
	}
	leaseMicroseconds, err := workerDurationMicroseconds("任务租期", leaseDuration, false)
	if err != nil {
		return nil, err
	}

	transaction, err := s.engine.DB().DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = transaction.Rollback() }()

	query := fmt.Sprintf(`SELECT task_id, lease_fencing_token, poll_failure_count
		FROM task_record
		WHERE engine_version = 2 AND deleted_at IS NULL
			AND status IN (?, ?) AND next_poll_at IS NOT NULL
			AND next_poll_at <= UTC_TIMESTAMP(6)
			AND (lease_owner IS NULL OR lease_expires_at <= UTC_TIMESTAMP(6))
			AND lease_fencing_token < 18446744073709551615
		ORDER BY next_poll_at ASC, task_id ASC
		LIMIT %d FOR UPDATE SKIP LOCKED`, limit)
	rows, err := transaction.QueryContext(ctx, query, TaskQueued, TaskRunning)
	if err != nil {
		return nil, err
	}
	type leaseCandidate struct {
		taskID       int
		fencingToken uint64
		failureCount uint32
	}
	candidates := make([]leaseCandidate, 0, limit)
	for rows.Next() {
		var candidate leaseCandidate
		if err := rows.Scan(&candidate.taskID, &candidate.fencingToken, &candidate.failureCount); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if candidate.failureCount > MaxPollFailureCount {
			_ = rows.Close()
			return nil, fmt.Errorf("任务调度失败计数超出允许范围，task_id=%d", candidate.taskID)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	leases := make([]TaskLease, 0, len(candidates))
	for _, candidate := range candidates {
		result, updateErr := transaction.ExecContext(ctx, `UPDATE task_record
			SET lease_owner = ?,
				lease_expires_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)),
				next_poll_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)),
				lease_fencing_token = lease_fencing_token + 1
			WHERE task_id = ? AND engine_version = 2 AND deleted_at IS NULL
				AND status IN (?, ?) AND next_poll_at IS NOT NULL
				AND next_poll_at <= UTC_TIMESTAMP(6)
				AND (lease_owner IS NULL OR lease_expires_at <= UTC_TIMESTAMP(6))
				AND lease_fencing_token = ?
				AND lease_fencing_token < 18446744073709551615`,
			[]byte(owner), leaseMicroseconds, leaseMicroseconds, candidate.taskID,
			TaskQueued, TaskRunning, candidate.fencingToken)
		if updateErr != nil {
			return nil, updateErr
		}
		updated, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return nil, rowsErr
		}
		if updated != 1 {
			return nil, ErrLeaseLost
		}
		leases = append(leases, TaskLease{
			TaskID:       candidate.taskID,
			Owner:        owner,
			FencingToken: candidate.fencingToken + 1,
			FailureCount: candidate.failureCount,
		})
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return leases, nil
}

func (s *XORMStore) RenewTaskLease(ctx context.Context, lease TaskLease, leaseDuration time.Duration) error {
	if s == nil || s.engine == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := validateTaskLeaseHandle(lease); err != nil {
		return err
	}
	leaseMicroseconds, err := workerDurationMicroseconds("任务租期", leaseDuration, false)
	if err != nil {
		return err
	}
	result, err := s.engine.DB().DB.ExecContext(ctx, `UPDATE task_record
		SET lease_expires_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)),
			next_poll_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6))
		WHERE task_id = ? AND engine_version = 2 AND deleted_at IS NULL
			AND status IN (?, ?) AND lease_owner = ?
			AND lease_fencing_token = ?
			AND lease_expires_at > UTC_TIMESTAMP(6)`,
		leaseMicroseconds, leaseMicroseconds, lease.TaskID, TaskQueued, TaskRunning,
		[]byte(lease.Owner), lease.FencingToken)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated == 1 {
		return nil
	}
	if updated > 1 {
		return fmt.Errorf("续租更新了意外数量的任务")
	}
	valid, err := s.taskLeaseStillValid(ctx, lease)
	if err != nil {
		return err
	}
	if valid {
		return nil
	}
	return ErrLeaseLost
}

func (s *XORMStore) ReleaseTaskLease(
	ctx context.Context,
	lease TaskLease,
	delay time.Duration,
	pollFailed bool,
) error {
	if s == nil || s.engine == nil {
		return fmt.Errorf("数据库未初始化")
	}
	if err := validateTaskLeaseHandle(lease); err != nil {
		return err
	}
	delayMicroseconds, err := workerDurationMicroseconds("下次调度延迟", delay, true)
	if err != nil {
		return err
	}
	failureExpression := "0"
	if pollFailed {
		failureExpression = fmt.Sprintf("LEAST(%d, poll_failure_count + 1)", MaxPollFailureCount)
	}
	query := fmt.Sprintf(`UPDATE task_record
		SET next_poll_at = TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)),
			lease_owner = NULL, lease_expires_at = NULL,
			poll_failure_count = %s
		WHERE task_id = ? AND engine_version = 2 AND deleted_at IS NULL
			AND status IN (?, ?) AND lease_owner = ?
			AND lease_fencing_token = ?
			AND lease_expires_at > UTC_TIMESTAMP(6)`, failureExpression)
	result, err := s.engine.DB().DB.ExecContext(ctx, query, delayMicroseconds, lease.TaskID,
		TaskQueued, TaskRunning, []byte(lease.Owner), lease.FencingToken)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *XORMStore) taskLeaseStillValid(ctx context.Context, lease TaskLease) (bool, error) {
	var valid bool
	err := s.engine.DB().DB.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM task_record
		WHERE task_id = ? AND engine_version = 2 AND deleted_at IS NULL
			AND status IN (?, ?) AND lease_owner = ?
			AND lease_fencing_token = ?
			AND lease_expires_at > UTC_TIMESTAMP(6)
	)`, lease.TaskID, TaskQueued, TaskRunning, []byte(lease.Owner), lease.FencingToken).Scan(&valid)
	return valid, err
}

func validLeaseOwner(owner string) bool {
	if len(owner) == 0 || len(owner) > MaxLeaseOwnerBytes {
		return false
	}
	for index := 0; index < len(owner); index++ {
		if owner[index] < 0x21 || owner[index] > 0x7e {
			return false
		}
	}
	return true
}

func workerDurationMicroseconds(name string, value time.Duration, allowZero bool) (int64, error) {
	if value < 0 || (!allowZero && value == 0) || value > maxWorkerDelay {
		return 0, fmt.Errorf("%s 超出允许范围", name)
	}
	if value == 0 {
		return 0, nil
	}
	microseconds := value / time.Microsecond
	if microseconds == 0 {
		return 0, fmt.Errorf("%s 不能小于 1 微秒", name)
	}
	return int64(microseconds), nil
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
