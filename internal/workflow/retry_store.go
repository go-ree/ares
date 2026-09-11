package workflow

import (
	"context"
	"fmt"

	"github.com/go-ree/ares/internal/entity"
	"xorm.io/xorm"
)

// Called only within the task row lock. Existing completed attempts are never
// overwritten; this also adopts an epoch-7 running snapshot after migration.
func ensureAttempt(session *xorm.Session, taskID int, stepID int64) error {
	_, err := session.Exec(`INSERT IGNORE INTO task_step_attempts
 (step_record_id, attempt, status, idempotency_key, retry_class, external_ref, output, message, started_at, finished_at)
 SELECT step_record_id, attempt, status, CONCAT(task_id, '/', step_key, '/', attempt), retry_class, external_ref, output, message, started_at, finished_at
 FROM task_step_records WHERE task_id = ? AND step_record_id = ? AND status = 'running'`, taskID, stepID)
	return err
}

func syncAttempt(session *xorm.Session, stepID int64) error {
	_, err := session.Exec(`UPDATE task_step_attempts a JOIN task_step_records s
 ON s.step_record_id = a.step_record_id AND s.attempt = a.attempt
 SET a.status = s.status, a.external_ref = s.external_ref, a.output = s.output,
 a.retry_class = s.retry_class, a.message = s.message, a.finished_at = s.finished_at
 WHERE s.step_record_id = ? AND a.status = 'running'`, stepID)
	return err
}

func (s *XORMStore) AdvanceRetry(ctx context.Context, lease TaskLease, stepID int64) (status string, err error) {
	err = s.withValidTaskLease(ctx, lease, func(session *xorm.Session) error {
		var step entity.TaskStepRecord
		has, err := session.Where("step_record_id = ? AND task_id = ?", stepID, lease.TaskID).Get(&step)
		if err != nil {
			return err
		}
		if !has {
			return ErrNotFound
		}
		status = step.Status
		if retryEligible(step) && step.RetryMode == "automatic" {
			if err := scheduleRetry(session, step); err != nil {
				return err
			}
			status = StepRetryWait
			return nil
		}
		if step.Status != StepRetryWait {
			return nil
		}
		if step.Attempt < 1 || step.Attempt >= step.MaxAttempts || !safeRetryClass(step.RetryClass) {
			return ErrRetryConflict
		}
		result, err := session.Exec(`UPDATE task_step_records SET status = 'pending', attempt = attempt + 1,
   external_ref = NULL, output = NULL, message = NULL, started_at = NULL, finished_at = NULL,
   retry_class = '', retry_at = NULL WHERE step_record_id = ? AND retry_at <= UTC_TIMESTAMP(6)`, stepID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 1 {
			status = StepPending
		}
		return nil
	})
	return status, err
}

func scheduleRetry(session *xorm.Session, step entity.TaskStepRecord) error {
	if step.RetryDelaySeconds < 1 || step.RetryMaxDelaySeconds > 3600 || step.RetryMaxDelaySeconds < step.RetryDelaySeconds {
		return ErrRetryConflict
	}
	_, err := session.Exec(`UPDATE task_step_records SET status = 'retry_wait',
  retry_at = TIMESTAMPADD(SECOND, ?, UTC_TIMESTAMP(6)) WHERE step_record_id = ? AND status = 'failed'`, retryDelay(step), step.StepRecordID)
	return err
}

func (s *XORMStore) RequestRetry(ctx context.Context, taskID int, key string, expected int, uses string) error {
	if taskID <= 0 || !ValidStepKey(key) || expected < 1 || expected >= 5 {
		return ErrRetryConflict
	}
	session := s.engine.NewSession()
	defer session.Close()
	session.Context(ctx)
	if err := session.Begin(); err != nil {
		return err
	}
	defer session.Rollback()
	var task struct{ Status string }
	has, err := session.SQL(`SELECT status FROM task_record WHERE task_id = ? AND engine_version = 2 AND deleted_at IS NULL AND lease_owner IS NULL FOR UPDATE`, taskID).Get(&task)
	if err != nil {
		return err
	}
	if !has || task.Status != TaskFailed {
		return ErrRetryConflict
	}
	var step entity.TaskStepRecord
	has, err = session.Where("task_id = ? AND step_key = ?", taskID, key).Get(&step)
	if err != nil {
		return err
	}
	if !has || step.Uses != uses || step.Attempt != expected || step.OnFailure != FailureStop || !retryEligible(step) {
		return ErrRetryConflict
	}
	// Never replay a successor that has already executed, including a previous
	// successful manual recovery. Only the stop boundary can be reopened.
	count, err := session.Where("task_id = ? AND position > ? AND (status <> ? OR started_at IS NOT NULL)", taskID, step.Position, StepSkipped).Count(new(entity.TaskStepRecord))
	if err != nil {
		return err
	}
	if count != 0 {
		return ErrRetryConflict
	}
	if err := scheduleRetry(session, step); err != nil {
		return err
	}
	if _, err := session.Exec(`UPDATE task_step_records SET status = 'pending', message = NULL, finished_at = NULL
 WHERE task_id = ? AND position > ? AND status = 'skipped' AND started_at IS NULL`, taskID, step.Position); err != nil {
		return err
	}
	if _, err := session.Exec(`UPDATE task_record SET status = 'running', message = NULL, next_poll_at = UTC_TIMESTAMP(6), poll_failure_count = 0, updated_at = CURRENT_TIMESTAMP WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	return session.Commit()
}

func (s *XORMStore) ListAttempts(ctx context.Context, taskID int, key string) ([]AttemptView, error) {
	if taskID <= 0 || !ValidStepKey(key) {
		return nil, ErrNotFound
	}
	_, err := s.GetTaskStepLogSource(ctx, taskID, key)
	if err != nil {
		return nil, err
	}
	rows := make([]AttemptView, 0)
	err = s.engine.Context(ctx).SQL(`SELECT a.attempt, a.status, COALESCE(a.message, '') AS message, a.started_at, a.finished_at
 FROM task_step_attempts a JOIN task_step_records s ON s.step_record_id = a.step_record_id
 JOIN task_record t ON t.task_id = s.task_id
 WHERE t.task_id = ? AND s.step_key = ? AND t.engine_version = 2 AND t.deleted_at IS NULL
 ORDER BY a.attempt DESC LIMIT 5`, taskID, key).Find(&rows)
	return rows, err
}

func (s *XORMStore) GetAttemptLogSource(ctx context.Context, taskID int, key string, attempt int) (TaskStepLogSource, error) {
	if attempt < 1 {
		return TaskStepLogSource{}, ErrNotFound
	}
	source, err := s.GetTaskStepLogSource(ctx, taskID, key)
	if err != nil {
		return source, err
	}
	var row struct {
		Status      string
		ExternalRef []byte `xorm:"'external_ref'"`
	}
	has, err := s.engine.Context(ctx).SQL(`SELECT a.status, a.external_ref FROM task_step_attempts a
 JOIN task_step_records s ON s.step_record_id = a.step_record_id JOIN task_record t ON t.task_id = s.task_id
 WHERE s.task_id = ? AND s.step_key = ? AND a.attempt = ? AND t.engine_version = 2 AND t.deleted_at IS NULL`, taskID, key, attempt).Get(&row)
	if err != nil {
		return source, err
	}
	if !has {
		return source, fmt.Errorf("尝试记录不存在: %w", ErrNotFound)
	}
	source.Status = row.Status
	source.ExternalReference = copyAttemptReference(row.ExternalRef)
	return source, nil
}
