package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/workflow"
)

// Reuse the isolated real-MySQL harness to verify the crash boundary between
// step completion and task completion, not just the happy-path state labels.
func TestMySQLTaskLifecycle(t *testing.T) {
	h := newWorkerLeaseMySQLHarness(t)
	for _, status := range []string{workflow.StepTimedOut, workflow.StepOutcomeUnknown} {
		t.Run(status, func(t *testing.T) {
			h.retireActiveTasks(t)
			taskID := h.insertDueTask(t, -time.Second, 0)
			stepID := h.insertRunningStep(t, taskID, workflow.NoopUses)
			if _, err := h.database.Exec(`UPDATE task_step_records SET on_failure = 'continue' WHERE step_record_id = ?`, stepID); err != nil {
				t.Fatal(err)
			}
			h.insertStep(t, taskID)
			oldLease := h.acquireExactly(t, 0, "w07-original", 1, 30*time.Second)[0]
			reference := json.RawMessage(`{"provider_id":"stable-side-effect"}`)
			if saved, err := h.stores[0].SaveStepResult(context.Background(), oldLease, stepID, workflow.Result{State: status, ExternalReference: reference}); err != nil || !saved {
				t.Fatalf("save=%t err=%v", saved, err)
			}
			// Crash after the terminal step was committed: expire only the lease.
			if _, err := h.database.Exec(`UPDATE task_record SET lease_expires_at = UTC_TIMESTAMP(6) - INTERVAL 1 SECOND, next_poll_at = UTC_TIMESTAMP(6) - INTERVAL 1 SECOND WHERE task_id = ?`, taskID); err != nil {
				t.Fatal(err)
			}
			lease := h.acquireExactly(t, 1, "w07-recovery", 1, 30*time.Second)[0]
			if _, err := h.stores[0].SaveStepResult(context.Background(), oldLease, stepID, workflow.Result{State: workflow.ResultSucceeded}); !errors.Is(err, workflow.ErrLeaseLost) {
				t.Fatalf("stale save=%v", err)
			}
			result, err := workflow.NewCoordinator(h.stores[1], workflow.DefaultRegistry()).Advance(context.Background(), lease)
			if err != nil || !result.Terminal || result.TaskStatus != status {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			var taskStatus string
			var cleared bool
			if err := h.database.QueryRow(`SELECT status, next_poll_at IS NULL AND lease_owner IS NULL AND lease_expires_at IS NULL FROM task_record WHERE task_id = ?`, taskID).Scan(&taskStatus, &cleared); err != nil {
				t.Fatal(err)
			}
			if taskStatus != status || !cleared {
				t.Fatalf("status=%s cleared=%t", taskStatus, cleared)
			}
			steps, err := h.stores[1].ListTaskSteps(context.Background(), taskID)
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range steps {
				if step.FinishedTime == nil {
					t.Fatalf("missing completion time: %+v", step)
				}
				if step.StepRecordID == stepID {
					if step.Status != status || len(step.ExternalRef) == 0 {
						t.Fatalf("lost history: %+v", step)
					}
				} else if step.Status != workflow.StepSkipped {
					t.Fatalf("continue advanced: %+v", step)
				}
			}
			if leases, err := h.stores[2].AcquireTaskLeases(context.Background(), "w07-after-terminal", 1, time.Second); err != nil || len(leases) != 0 {
				t.Fatalf("rescheduled=%+v err=%v", leases, err)
			}
		})
	}
	t.Run("database deadline survives takeover", func(t *testing.T) {
		h.retireActiveTasks(t)
		taskID := h.insertDueTask(t, -time.Second, 0)
		stepID := h.insertRunningStep(t, taskID, workflow.NoopUses)
		if _, err := h.database.Exec(`UPDATE task_step_records SET started_at = UTC_TIMESTAMP(6) - INTERVAL 31 SECOND WHERE step_record_id = ?`, stepID); err != nil {
			t.Fatal(err)
		}
		lease := h.acquireExactly(t, 0, "w07-deadline", 1, 30*time.Second)[0]
		result, err := workflow.NewCoordinator(h.stores[0], workflow.DefaultRegistry()).Advance(context.Background(), lease)
		if err != nil || result.TaskStatus != workflow.TaskTimedOut {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}
