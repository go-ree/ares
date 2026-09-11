package workflow_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/workflow"
)

func TestMySQLTaskRetries(t *testing.T) {
	h := newWorkerLeaseMySQLHarness(t)
	ctx := context.Background()
	seed := func(t *testing.T, mode, config string, max int) (int, int64) {
		h.retireActiveTasks(t)
		taskID := h.insertDueTask(t, -time.Second, 0)
		stepID := h.insertStep(t, taskID)
		if _, err := h.database.Exec(`UPDATE task_step_records SET uses = ?, config = ?, max_attempts = ?, retry_mode = ?, retry_delay_seconds = 1, retry_max_delay_seconds = 10 WHERE step_record_id = ?`, workflow.NoopUses, config, max, mode, stepID); err != nil {
			t.Fatal(err)
		}
		if _, err := h.database.Exec(`INSERT INTO task_step_records (task_id, workflow_version_id, step_key, name, uses, position, config, status, timeout_seconds, on_failure) VALUES (?, 0, 'next', 'Next', ?, 1, '{}', 'pending', 60, 'stop')`, taskID, workflow.NoopUses); err != nil {
			t.Fatal(err)
		}
		return taskID, stepID
	}
	due := func(t *testing.T, stepID int64) {
		if _, err := h.database.Exec(`UPDATE task_step_records SET retry_at = UTC_TIMESTAMP(6) - INTERVAL 1 SECOND WHERE step_record_id = ?`, stepID); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("automatic retry preserves history and wait across takeover", func(t *testing.T) {
		taskID, stepID := seed(t, "automatic", `{"fail_attempts":1}`, 3)
		lease := h.acquireExactly(t, 0, "attempt-first", 1, 30*time.Second)[0]
		c := workflow.NewCoordinator(h.stores[0], workflow.DefaultRegistry())
		result, err := c.RunUntilBlocked(ctx, lease, 10)
		if err != nil || result.StepStatus != workflow.StepRetryWait || !result.Blocked {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		rows, err := c.ListAttempts(ctx, taskID, "verify")
		if err != nil || len(rows) != 1 || rows[0].Status != workflow.StepFailed || rows[0].FinishedAt == nil {
			t.Fatalf("history=%+v err=%v", rows, err)
		}
		// A future deadline survives releasing/reacquiring the task and creating a new coordinator.
		if _, err := h.database.Exec(`UPDATE task_step_records SET retry_at = UTC_TIMESTAMP(6) + INTERVAL 1 HOUR WHERE step_record_id = ?`, stepID); err != nil {
			t.Fatal(err)
		}
		var original time.Time
		if err := h.database.QueryRow(`SELECT retry_at FROM task_step_records WHERE step_record_id = ?`, stepID).Scan(&original); err != nil {
			t.Fatal(err)
		}
		if err := h.stores[0].ReleaseTaskLease(ctx, lease, 0, false); err != nil {
			t.Fatal(err)
		}
		fresh := h.acquireExactly(t, 1, "attempt-takeover", 1, 30*time.Second)[0]
		if _, err := h.stores[0].AdvanceRetry(ctx, lease, stepID); !errors.Is(err, workflow.ErrLeaseLost) {
			t.Fatalf("stale=%v", err)
		}
		c = workflow.NewCoordinator(h.stores[1], workflow.DefaultRegistry())
		result, err = c.RunUntilBlocked(ctx, fresh, 10)
		if err != nil || result.StepStatus != workflow.StepRetryWait {
			t.Fatalf("wait=%+v err=%v", result, err)
		}
		var unchanged time.Time
		var attempt int
		if err := h.database.QueryRow(`SELECT retry_at, attempt FROM task_step_records WHERE step_record_id = ?`, stepID).Scan(&unchanged, &attempt); err != nil {
			t.Fatal(err)
		}
		if !unchanged.Equal(original) || attempt != 1 {
			t.Fatal("takeover reset wait or attempt")
		}
		due(t, stepID)
		result, err = c.RunUntilBlocked(ctx, fresh, 10)
		if err != nil || result.TaskStatus != workflow.TaskSucceeded {
			t.Fatalf("completion=%+v err=%v", result, err)
		}
		rows, err = c.ListAttempts(ctx, taskID, "verify")
		if err != nil || len(rows) != 2 || rows[0].Attempt != 2 || rows[0].Status != workflow.StepSucceeded || rows[1].Status != workflow.StepFailed {
			t.Fatalf("history=%+v err=%v", rows, err)
		}
		var firstKey, secondKey string
		if err := h.database.QueryRow(`SELECT MIN(idempotency_key), MAX(idempotency_key) FROM task_step_attempts WHERE step_record_id = ?`, stepID).Scan(&firstKey, &secondKey); err != nil {
			t.Fatal(err)
		}
		if firstKey == secondKey {
			t.Fatal("attempt keys reused")
		}
		// Historical logs resolve their own reference, never the latest one.
		if _, err := h.database.Exec(`UPDATE task_step_attempts SET external_ref = JSON_OBJECT('attempt', attempt) WHERE step_record_id = ?`, stepID); err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{1, 2} {
			source, err := h.stores[1].GetAttemptLogSource(ctx, taskID, "verify", n)
			if err != nil || string(source.ExternalReference) != fmt.Sprintf(`{"attempt": %d}`, n) {
				t.Fatalf("attempt %d source=%+v err=%v", n, source, err)
			}
		}
		if _, err := h.stores[1].GetAttemptLogSource(ctx, taskID, "verify", 3); !errors.Is(err, workflow.ErrNotFound) {
			t.Fatalf("missing attempt: %v", err)
		}
	})

	t.Run("manual retry has one concurrent winner and does not replay successors", func(t *testing.T) {
		taskID, stepID := seed(t, "manual", `{"fail_attempts":1}`, 2)
		lease := h.acquireExactly(t, 0, "manual-first", 1, 30*time.Second)[0]
		c := workflow.NewCoordinator(h.stores[0], workflow.DefaultRegistry())
		result, err := c.RunUntilBlocked(ctx, lease, 10)
		if err != nil || result.TaskStatus != workflow.TaskFailed {
			t.Fatalf("first=%+v err=%v", result, err)
		}
		var wins atomic.Int32
		var wait sync.WaitGroup
		for range 12 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				err := c.RequestRetry(ctx, taskID, "verify", 1)
				if err == nil {
					wins.Add(1)
				} else if !errors.Is(err, workflow.ErrRetryConflict) {
					t.Errorf("retry=%v", err)
				}
			}()
		}
		wait.Wait()
		if wins.Load() != 1 {
			t.Fatalf("winners=%d", wins.Load())
		}
		due(t, stepID)
		fresh := h.acquireExactly(t, 1, "manual-second", 1, 30*time.Second)[0]
		result, err = workflow.NewCoordinator(h.stores[1], workflow.DefaultRegistry()).RunUntilBlocked(ctx, fresh, 10)
		if err != nil || result.TaskStatus != workflow.TaskSucceeded {
			t.Fatalf("second=%+v err=%v", result, err)
		}
		if err := c.RequestRetry(ctx, taskID, "verify", 1); !errors.Is(err, workflow.ErrRetryConflict) {
			t.Fatalf("replay=%v", err)
		}
		rows, err := c.ListAttempts(ctx, taskID, "next")
		if err != nil || len(rows) != 1 || rows[0].Attempt != 1 {
			t.Fatalf("successor=%+v err=%v", rows, err)
		}
	})

	t.Run("bounded automatic retries stop at configured maximum", func(t *testing.T) {
		taskID, stepID := seed(t, "automatic", `{"outcome":"failed"}`, 3)
		lease := h.acquireExactly(t, 0, "bounded", 1, 30*time.Second)[0]
		c := workflow.NewCoordinator(h.stores[0], workflow.DefaultRegistry())
		for attempt := 1; attempt <= 3; attempt++ {
			result, err := c.RunUntilBlocked(ctx, lease, 10)
			if err != nil {
				t.Fatal(err)
			}
			if attempt < 3 {
				if result.StepStatus != workflow.StepRetryWait {
					t.Fatalf("result=%+v", result)
				}
				due(t, stepID)
			} else if result.TaskStatus != workflow.TaskFailed {
				t.Fatalf("result=%+v", result)
			}
		}
		rows, err := c.ListAttempts(ctx, taskID, "verify")
		if err != nil || len(rows) != 3 {
			t.Fatalf("rows=%+v err=%v", rows, err)
		}
		if err := c.RequestRetry(ctx, taskID, "verify", 3); !errors.Is(err, workflow.ErrRetryConflict) {
			t.Fatalf("exhausted=%v", err)
		}
	})
}
