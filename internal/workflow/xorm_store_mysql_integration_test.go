package workflow_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/workflow"
	"github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

const workerLeaseMySQLIntegrationDSNEnv = "ARES_TEST_MYSQL_DSN"

// TestMySQLWorkerLeases is deliberately opt-in because it verifies MySQL's
// UTC clock, row locking and SKIP LOCKED behavior rather than a SQL mock. Every
// run gets an isolated database so it cannot mutate the schema or data named by
// ARES_TEST_MYSQL_DSN.
func TestMySQLWorkerLeases(t *testing.T) {
	harness := newWorkerLeaseMySQLHarness(t)

	t.Run("concurrent claims are fair unique and skip locked rows", func(t *testing.T) {
		harness.retireActiveTasks(t)
		const (
			candidateCount = 15
			claimers       = 3
			batchSize      = 4
		)
		candidateIDs := make([]int, 0, candidateCount)
		for index := 0; index < candidateCount; index++ {
			// One-second gaps make the expected order independent of client and
			// database call latency.
			candidateIDs = append(candidateIDs, harness.insertDueTask(t,
				-time.Duration(candidateCount-index+30)*time.Second, 0))
		}

		type claimResult struct {
			leases []workflow.TaskLease
			err    error
		}
		results := make([]claimResult, claimers)
		owners := []string{"w06-concurrent-a", "w06-concurrent-b", "w06-concurrent-c"}
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(claimers)
		for index := 0; index < claimers; index++ {
			go func(index int) {
				defer wait.Done()
				<-start
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				results[index].leases, results[index].err = harness.stores[index].AcquireTaskLeases(
					ctx, owners[index], batchSize, 30*time.Second)
			}(index)
		}
		close(start)
		wait.Wait()

		claimedIDs := make([]int, 0, claimers*batchSize)
		seen := make(map[int]string, claimers*batchSize)
		for index, result := range results {
			if result.err != nil {
				t.Fatalf("claimer %d: %v", index, result.err)
			}
			if len(result.leases) != batchSize {
				t.Fatalf("claimer %d leases = %d, want %d", index, len(result.leases), batchSize)
			}
			for _, lease := range result.leases {
				if lease.Owner != owners[index] || lease.FencingToken != 1 || lease.FailureCount != 0 {
					t.Fatalf("claimer %d returned malformed lease: %#v", index, lease)
				}
				if previousOwner, duplicate := seen[lease.TaskID]; duplicate {
					t.Fatalf("task %d was claimed by both %q and %q", lease.TaskID, previousOwner, lease.Owner)
				}
				seen[lease.TaskID] = lease.Owner
				claimedIDs = append(claimedIDs, lease.TaskID)
			}
		}
		sort.Ints(claimedIDs)
		if want := candidateIDs[:claimers*batchSize]; !reflect.DeepEqual(claimedIDs, want) {
			t.Fatalf("concurrent claims did not select the oldest due tasks\ngot:  %v\nwant: %v", claimedIDs, want)
		}
		for _, taskID := range candidateIDs[claimers*batchSize:] {
			var unclaimed bool
			if err := harness.database.QueryRow(`SELECT lease_owner IS NULL AND lease_expires_at IS NULL
				FROM task_record WHERE task_id = ?`, taskID).Scan(&unclaimed); err != nil {
				t.Fatal(err)
			}
			if !unclaimed {
				t.Fatalf("newer task %d was claimed before all older due tasks", taskID)
			}
		}
		remaining := harness.acquireExactly(t, 0, "w06-next-fair-batch",
			candidateCount-claimers*batchSize, 30*time.Second)
		remainingIDs := make([]int, 0, len(remaining))
		for _, lease := range remaining {
			remainingIDs = append(remainingIDs, lease.TaskID)
		}
		sort.Ints(remainingIDs)
		if want := candidateIDs[claimers*batchSize:]; !reflect.DeepEqual(remainingIDs, want) {
			t.Fatalf("tasks beyond the first scan starved\ngot:  %v\nwant: %v", remainingIDs, want)
		}

		// Hold the oldest row in another transaction. FOR UPDATE SKIP LOCKED
		// must let the worker claim the next row instead of waiting for it.
		harness.retireActiveTasks(t)
		lockedID := harness.insertDueTask(t, -2*time.Minute, 0)
		nextID := harness.insertDueTask(t, -119*time.Second, 0)
		lockCtx, lockCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer lockCancel()
		blocker, err := harness.database.BeginTx(lockCtx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback()
		var selectedID int
		if err := blocker.QueryRowContext(lockCtx,
			"SELECT task_id FROM task_record WHERE task_id = ? FOR UPDATE", lockedID).Scan(&selectedID); err != nil {
			t.Fatal(err)
		}

		claimCtx, claimCancel := context.WithTimeout(context.Background(), 3*time.Second)
		startedAt := time.Now()
		leases, err := harness.stores[0].AcquireTaskLeases(claimCtx, "w06-skip-locked", 1, 30*time.Second)
		claimCancel()
		if err != nil {
			t.Fatalf("claim while oldest row is locked: %v", err)
		}
		if len(leases) != 1 || leases[0].TaskID != nextID {
			t.Fatalf("claim while task %d is locked = %#v, want task %d", lockedID, leases, nextID)
		}
		if elapsed := time.Since(startedAt); elapsed >= 3*time.Second {
			t.Fatalf("SKIP LOCKED claim waited for the locked row: %s", elapsed)
		}
		if err := blocker.Rollback(); err != nil {
			t.Fatal(err)
		}
		leases = harness.acquireExactly(t, 1, "w06-oldest-after-unlock", 1, 30*time.Second)
		if leases[0].TaskID != lockedID {
			t.Fatalf("oldest row after unlock = task %d, want %d", leases[0].TaskID, lockedID)
		}
	})

	t.Run("every leased read and write rejects a stale token", func(t *testing.T) {
		harness.retireActiveTasks(t)
		taskID := harness.insertDueTask(t, -time.Second, 0)
		stepID := harness.insertStep(t, taskID)
		stale := harness.acquireExactly(t, 0, "w06-stale-owner", 1, 30*time.Second)[0]
		harness.expireLease(t, stale.TaskID)
		current := harness.acquireExactly(t, 1, "w06-current-owner", 1, 30*time.Second)[0]
		if current.FencingToken != stale.FencingToken+1 {
			t.Fatalf("takeover token = %d, want %d", current.FencingToken, stale.FencingToken+1)
		}

		checks := []struct {
			name string
			call func() error
		}{
			{"GetTaskReleaseContext", func() error {
				_, err := harness.stores[0].GetTaskReleaseContext(context.Background(), stale)
				return err
			}},
			{"ListTaskStepsForLease", func() error {
				_, err := harness.stores[0].ListTaskStepsForLease(context.Background(), stale)
				return err
			}},
			{"StepTimeRemaining", func() error {
				_, err := harness.stores[0].StepTimeRemaining(context.Background(), stale, stepID, 1)
				return err
			}},
			{"ClaimStep", func() error {
				_, err := harness.stores[0].ClaimStep(context.Background(), stale, stepID)
				return err
			}},
			{"ReleaseStep", func() error {
				_, err := harness.stores[0].ReleaseStep(context.Background(), stale, stepID, "must not persist")
				return err
			}},
			{"SaveStepResult", func() error {
				_, err := harness.stores[0].SaveStepResult(context.Background(), stale, stepID,
					workflow.Result{State: workflow.ResultSucceeded, Message: "must not persist"})
				return err
			}},
			{"SkipPendingSteps", func() error {
				return harness.stores[0].SkipPendingSteps(context.Background(), stale, "must not persist")
			}},
			{"SetTaskStatus", func() error {
				return harness.stores[0].SetTaskStatus(context.Background(), stale,
					workflow.TaskSucceeded, "must not persist")
			}},
			{"RenewTaskLease", func() error {
				return harness.stores[0].RenewTaskLease(context.Background(), stale, 30*time.Second)
			}},
			{"ReleaseTaskLease", func() error {
				return harness.stores[0].ReleaseTaskLease(context.Background(), stale, time.Second, true)
			}},
		}
		for _, check := range checks {
			t.Run(check.name, func(t *testing.T) {
				if err := check.call(); !errors.Is(err, workflow.ErrLeaseLost) {
					t.Fatalf("error = %v, want ErrLeaseLost", err)
				}
			})
		}

		var owner []byte
		var token uint64
		var taskStatus, stepStatus string
		var failureCount uint32
		if err := harness.database.QueryRow(`SELECT lease_owner, lease_fencing_token,
			status, poll_failure_count FROM task_record WHERE task_id = ?`, taskID).
			Scan(&owner, &token, &taskStatus, &failureCount); err != nil {
			t.Fatal(err)
		}
		if err := harness.database.QueryRow(`SELECT status FROM task_step_records
			WHERE step_record_id = ?`, stepID).Scan(&stepStatus); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(owner, []byte(current.Owner)) || token != current.FencingToken ||
			taskStatus != workflow.TaskQueued || failureCount != 0 || stepStatus != workflow.StepPending {
			t.Fatalf("stale calls changed current generation: owner=%q token=%d task=%q failures=%d step=%q",
				owner, token, taskStatus, failureCount, stepStatus)
		}
	})

	t.Run("expired leases cannot renew and takeover tokens are monotonic", func(t *testing.T) {
		harness.retireActiveTasks(t)
		taskID := harness.insertDueTask(t, -time.Second, 0)
		first := harness.acquireExactly(t, 0, "w06-generation-one", 1, 30*time.Second)[0]
		harness.expireLease(t, taskID)
		if err := harness.stores[0].RenewTaskLease(context.Background(), first, 30*time.Second); !errors.Is(err, workflow.ErrLeaseLost) {
			t.Fatalf("renew expired generation: %v, want ErrLeaseLost", err)
		}
		var expired bool
		if err := harness.database.QueryRow(`SELECT lease_expires_at <= UTC_TIMESTAMP(6)
			FROM task_record WHERE task_id = ?`, taskID).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if !expired {
			t.Fatal("failed renewal revived an expired lease")
		}

		second := harness.acquireExactly(t, 1, "w06-generation-two", 1, 30*time.Second)[0]
		if second.FencingToken != first.FencingToken+1 {
			t.Fatalf("second fencing token = %d, want %d", second.FencingToken, first.FencingToken+1)
		}
		if err := harness.stores[0].ReleaseTaskLease(context.Background(), first, 0, false); !errors.Is(err, workflow.ErrLeaseLost) {
			t.Fatalf("release first generation after takeover: %v, want ErrLeaseLost", err)
		}
		harness.expireLease(t, taskID)
		third := harness.acquireExactly(t, 2, "w06-generation-three", 1, 30*time.Second)[0]
		if third.FencingToken != second.FencingToken+1 {
			t.Fatalf("third fencing token = %d, want %d", third.FencingToken, second.FencingToken+1)
		}
	})

	t.Run("release resets or saturates persistent poll failures", func(t *testing.T) {
		harness.retireActiveTasks(t)
		normalTaskID := harness.insertDueTask(t, -time.Second, 7)
		normal := harness.acquireExactly(t, 0, "w06-normal-release", 1, 30*time.Second)[0]
		if normal.TaskID != normalTaskID || normal.FailureCount != 7 {
			t.Fatalf("normal lease = %#v, want task %d with seven failures", normal, normalTaskID)
		}
		if err := harness.stores[0].ReleaseTaskLease(context.Background(), normal, 5*time.Second, false); err != nil {
			t.Fatal(err)
		}
		var ownerCleared, expiryCleared bool
		var failureCount uint32
		var untilNextPoll int64
		if err := harness.database.QueryRow(`SELECT lease_owner IS NULL, lease_expires_at IS NULL,
			poll_failure_count, TIMESTAMPDIFF(MICROSECOND, UTC_TIMESTAMP(6), next_poll_at)
			FROM task_record WHERE task_id = ?`, normalTaskID).
			Scan(&ownerCleared, &expiryCleared, &failureCount, &untilNextPoll); err != nil {
			t.Fatal(err)
		}
		if !ownerCleared || !expiryCleared || failureCount != 0 ||
			untilNextPoll < 1_000_000 || untilNextPoll > 5_000_000 {
			t.Fatalf("normal release state = owner_null:%t expiry_null:%t failures:%d next_poll_us:%d",
				ownerCleared, expiryCleared, failureCount, untilNextPoll)
		}

		harness.retireActiveTasks(t)
		failedTaskID := harness.insertDueTask(t, -time.Second, workflow.MaxPollFailureCount-1)
		failed := harness.acquireExactly(t, 1, "w06-failed-release-one", 1, 30*time.Second)[0]
		if failed.TaskID != failedTaskID || failed.FailureCount != workflow.MaxPollFailureCount-1 {
			t.Fatalf("failed lease = %#v", failed)
		}
		if err := harness.stores[1].ReleaseTaskLease(context.Background(), failed, 0, true); err != nil {
			t.Fatal(err)
		}
		saturated := harness.acquireExactly(t, 2, "w06-failed-release-two", 1, 30*time.Second)[0]
		if saturated.FailureCount != workflow.MaxPollFailureCount {
			t.Fatalf("failure count after first failed release = %d, want %d",
				saturated.FailureCount, workflow.MaxPollFailureCount)
		}
		if err := harness.stores[2].ReleaseTaskLease(context.Background(), saturated, 0, true); err != nil {
			t.Fatal(err)
		}
		if err := harness.database.QueryRow(`SELECT poll_failure_count FROM task_record
			WHERE task_id = ?`, failedTaskID).Scan(&failureCount); err != nil {
			t.Fatal(err)
		}
		if failureCount != workflow.MaxPollFailureCount {
			t.Fatalf("saturated failure count = %d, want %d", failureCount, workflow.MaxPollFailureCount)
		}
	})

	t.Run("terminal status atomically clears scheduling state", func(t *testing.T) {
		harness.retireActiveTasks(t)
		taskID := harness.insertDueTask(t, -time.Second, 5)
		stepID := harness.insertStep(t, taskID)
		lease := harness.acquireExactly(t, 0, "w06-terminal-owner", 1, 30*time.Second)[0]
		claimed, err := harness.stores[0].ClaimStep(context.Background(), lease, stepID)
		if err != nil || !claimed {
			t.Fatalf("claim terminal-test step = %t, %v", claimed, err)
		}
		saved, err := harness.stores[0].SaveStepResult(context.Background(), lease, stepID,
			workflow.Result{State: workflow.ResultSucceeded, Message: "step done"})
		if err != nil || !saved {
			t.Fatalf("save terminal-test step = %t, %v", saved, err)
		}
		if err := harness.stores[0].SetTaskStatus(context.Background(), lease,
			workflow.TaskSucceeded, "task done"); err != nil {
			t.Fatal(err)
		}

		var status, message, stepStatus string
		var nextPollCleared, ownerCleared, expiryCleared bool
		var fencingToken uint64
		var failureCount uint32
		if err := harness.database.QueryRow(`SELECT status, message, next_poll_at IS NULL,
			lease_owner IS NULL, lease_expires_at IS NULL, lease_fencing_token, poll_failure_count
			FROM task_record WHERE task_id = ?`, taskID).Scan(&status, &message, &nextPollCleared,
			&ownerCleared, &expiryCleared, &fencingToken, &failureCount); err != nil {
			t.Fatal(err)
		}
		if err := harness.database.QueryRow(`SELECT status FROM task_step_records
			WHERE step_record_id = ?`, stepID).Scan(&stepStatus); err != nil {
			t.Fatal(err)
		}
		if status != workflow.TaskSucceeded || message != "task done" || stepStatus != workflow.StepSucceeded ||
			!nextPollCleared || !ownerCleared || !expiryCleared || failureCount != 0 ||
			fencingToken != lease.FencingToken {
			t.Fatalf("terminal state is inconsistent: status=%q message=%q step=%q next_null=%t owner_null=%t expiry_null=%t token=%d failures=%d",
				status, message, stepStatus, nextPollCleared, ownerCleared, expiryCleared, fencingToken, failureCount)
		}
		if _, err := harness.stores[0].ListTaskStepsForLease(context.Background(), lease); !errors.Is(err, workflow.ErrLeaseLost) {
			t.Fatalf("terminal generation remained usable: %v", err)
		}
		leases, err := harness.stores[1].AcquireTaskLeases(context.Background(), "w06-terminal-reclaim", 1, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if len(leases) != 0 {
			t.Fatalf("terminal task was scheduled again: %#v", leases)
		}
	})

	t.Run("step claim waits for the integration configuration fence", func(t *testing.T) {
		harness.retireActiveTasks(t)
		taskID := harness.insertDueTask(t, -time.Second, 0)
		stepID := harness.insertStep(t, taskID)
		lease := harness.acquireExactly(t, 0, "w06-provider-fence", 1, 30*time.Second)[0]

		lockCtx, cancelLock := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelLock()
		configWriter, err := harness.database.BeginTx(lockCtx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		defer configWriter.Rollback()
		var revision uint64
		if err := configWriter.QueryRowContext(lockCtx, `SELECT revision
			FROM integration_settings WHERE provider = 'jenkins' FOR UPDATE`).Scan(&revision); err != nil {
			t.Fatal(err)
		}

		type claimResult struct {
			claimed bool
			err     error
		}
		claimDone := make(chan claimResult, 1)
		go func() {
			claimed, claimErr := harness.stores[1].ClaimStep(lockCtx, lease, stepID)
			claimDone <- claimResult{claimed: claimed, err: claimErr}
		}()
		select {
		case result := <-claimDone:
			t.Fatalf("ClaimStep returned before the configuration transaction committed: %#v", result)
		case <-time.After(100 * time.Millisecond):
		}
		if err := configWriter.Commit(); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-claimDone:
			if result.err != nil || !result.claimed {
				t.Fatalf("ClaimStep after configuration commit = %t, %v", result.claimed, result.err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("ClaimStep remained blocked after the configuration transaction committed")
		}
	})

	t.Run("step timeout uses the database clock", func(t *testing.T) {
		harness.retireActiveTasks(t)
		taskID := harness.insertDueTask(t, -time.Second, 0)
		stepID := harness.insertStep(t, taskID)
		lease := harness.acquireExactly(t, 0, "w06-db-clock", 1, 30*time.Second)[0]
		if _, err := harness.database.Exec(`UPDATE task_step_records
			SET status = ?, started_at = TIMESTAMPADD(SECOND, -2, UTC_TIMESTAMP(6))
			WHERE step_record_id = ?`, workflow.StepRunning, stepID); err != nil {
			t.Fatal(err)
		}
		remaining, err := harness.stores[0].StepTimeRemaining(context.Background(), lease, stepID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if remaining != 0 {
			t.Fatal("step started two database seconds ago was not timed out at one second")
		}

		if _, err := harness.database.Exec(`UPDATE task_step_records
			SET started_at = TIMESTAMPADD(SECOND, 2, UTC_TIMESTAMP(6))
			WHERE step_record_id = ?`, stepID); err != nil {
			t.Fatal(err)
		}
		remaining, err = harness.stores[0].StepTimeRemaining(context.Background(), lease, stepID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if remaining <= 0 {
			t.Fatal("step whose database timestamp is in the future was reported timed out")
		}
	})

	t.Run("step and task business timestamps follow a non UTC MySQL session", func(t *testing.T) {
		harness.retireActiveTasks(t)
		engine, err := xorm.NewEngine("mysql", harness.targetDSN)
		if err != nil {
			t.Fatal(err)
		}
		engine.SetMaxOpenConns(1)
		engine.SetMaxIdleConns(1)
		harness.engines = append(harness.engines, engine)
		if _, err := engine.Exec("SET time_zone = '+08:00'"); err != nil {
			t.Fatal(err)
		}
		store := workflow.NewXORMStore(engine)
		taskID := harness.insertDueTask(t, -time.Second, 0)
		stepID := harness.insertStep(t, taskID)
		if _, err := harness.database.Exec(`UPDATE task_record
			SET updated_at = TIMESTAMPADD(SECOND, -10, CURRENT_TIMESTAMP)
			WHERE task_id = ?`, taskID); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		leases, err := store.AcquireTaskLeases(ctx, "w06-timezone", 1, 30*time.Second)
		if err != nil || len(leases) != 1 || leases[0].TaskID != taskID {
			t.Fatalf("timezone lease = %#v, %v", leases, err)
		}
		claimed, err := store.ClaimStep(ctx, leases[0], stepID)
		if err != nil || !claimed {
			t.Fatalf("timezone ClaimStep() = %t, %v", claimed, err)
		}
		var startedAge, runningTaskAge int64
		if err := engine.DB().DB.QueryRowContext(ctx, `SELECT
			ABS(TIMESTAMPDIFF(SECOND, step.started_at, CURRENT_TIMESTAMP)),
			ABS(TIMESTAMPDIFF(SECOND, task.updated_at, CURRENT_TIMESTAMP))
			FROM task_step_records step JOIN task_record task USING (task_id)
			WHERE step.step_record_id = ?`, stepID).Scan(&startedAge, &runningTaskAge); err != nil {
			t.Fatal(err)
		}
		if startedAge > 1 || runningTaskAge > 1 {
			t.Fatalf("+08:00 claim timestamps are stale: step=%ds task=%ds", startedAge, runningTaskAge)
		}

		if _, err := engine.Exec(`UPDATE task_step_records
			SET started_at = TIMESTAMPADD(SECOND, -2, CURRENT_TIMESTAMP)
			WHERE step_record_id = ?`, stepID); err != nil {
			t.Fatal(err)
		}
		remaining, err := store.StepTimeRemaining(ctx, leases[0], stepID, 1)
		if err != nil || remaining != 0 {
			t.Fatalf("+08:00 StepTimeRemaining() = %v, %v, want timed out", remaining, err)
		}
		saved, err := store.SaveStepResult(ctx, leases[0], stepID, workflow.Result{
			State: workflow.ResultSucceeded, Message: "timezone complete",
		})
		if err != nil || !saved {
			t.Fatalf("timezone SaveStepResult() = %t, %v", saved, err)
		}
		if err := store.SetTaskStatus(ctx, leases[0], workflow.TaskSucceeded, "timezone complete"); err != nil {
			t.Fatal(err)
		}
		var finishedAge, terminalTaskAge int64
		if err := engine.DB().DB.QueryRowContext(ctx, `SELECT
			ABS(TIMESTAMPDIFF(SECOND, step.finished_at, CURRENT_TIMESTAMP)),
			ABS(TIMESTAMPDIFF(SECOND, task.updated_at, CURRENT_TIMESTAMP))
			FROM task_step_records step JOIN task_record task USING (task_id)
			WHERE step.step_record_id = ?`, stepID).Scan(&finishedAge, &terminalTaskAge); err != nil {
			t.Fatal(err)
		}
		if finishedAge > 1 || terminalTaskAge > 1 {
			t.Fatalf("+08:00 terminal timestamps are stale: step=%ds task=%ds", finishedAge, terminalTaskAge)
		}
	})

	t.Run("three workers take over an expired reconcile without duplicate side effects", func(t *testing.T) {
		harness.retireActiveTasks(t)
		taskID := harness.insertDueTask(t, -time.Second, 0)
		stepID := harness.insertRunningStep(t, taskID, mysqlOverlapExecutorUses)

		executor := newMySQLOverlapExecutor()
		registry := workflow.NewRegistry()
		if err := registry.Register(executor); err != nil {
			t.Fatal(err)
		}

		firstStore := &renewFailingWorkerStore{
			WorkerStore: harness.stores[0],
			failed:      make(chan struct{}),
		}
		stores := []workflow.WorkerStore{firstStore, harness.stores[1], harness.stores[2]}
		contexts := make([]context.CancelFunc, 0, len(stores))
		done := make([]chan error, 0, len(stores))
		startWorker := func(index int) {
			options := workflow.WorkerOptions{
				Owner:              fmt.Sprintf("w06-overlap-%d", index+1),
				Concurrency:        1,
				ClaimBatchSize:     1,
				ScanInterval:       10 * time.Millisecond,
				LeaseDuration:      300 * time.Millisecond,
				RenewInterval:      50 * time.Millisecond,
				NormalPollInterval: 20 * time.Millisecond,
				BackoffMin:         20 * time.Millisecond,
				BackoffMax:         100 * time.Millisecond,
				DrainTimeout:       time.Second,
				MaxTransitions:     10,
				Jitter:             func(time.Duration) time.Duration { return 0 },
			}
			worker, err := workflow.NewWorker(
				stores[index], workflow.NewCoordinator(stores[index], registry), options,
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			contexts = append(contexts, cancel)
			runDone := make(chan error, 1)
			done = append(done, runDone)
			go func() { runDone <- worker.Run(ctx) }()
		}

		var releaseFirstOnce sync.Once
		releaseFirst := func() { releaseFirstOnce.Do(func() { close(executor.releaseFirst) }) }
		t.Cleanup(func() {
			releaseFirst()
			for _, cancel := range contexts {
				cancel()
			}
		})

		// Start the sabotaged worker first so it deterministically owns generation
		// one. Its executor deliberately ignores cancellation, modelling a provider
		// call that outlives the lease.
		startWorker(0)
		waitMySQLWorkerSignal(t, executor.firstStarted, "first reconcile")
		startWorker(1)
		startWorker(2)
		waitMySQLWorkerSignal(t, firstStore.failed, "lease renewal failure")
		waitMySQLWorkerSignal(t, executor.secondStarted, "takeover reconcile")

		deadline := time.Now().Add(5 * time.Second)
		for {
			var status string
			if err := harness.database.QueryRow(`SELECT status FROM task_record WHERE task_id = ?`, taskID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status == workflow.TaskSucceeded {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("task status = %q, want succeeded after takeover", status)
			}
			time.Sleep(10 * time.Millisecond)
		}

		if got := executor.sideEffectCount(); got != 1 {
			t.Fatalf("idempotent provider side effects = %d, want 1", got)
		}
		if keys := executor.idempotencyKeys(); len(keys) != 2 || keys[0] != keys[1] {
			t.Fatalf("reconcile idempotency keys = %#v, want two identical keys", keys)
		}

		// Let the stale call return only after generation two committed. The
		// coordinator sees its cancelled context and the database fencing boundary
		// would reject any attempted late write in either case.
		releaseFirst()
		var taskStatus, stepStatus, message string
		var token uint64
		if err := harness.database.QueryRow(`SELECT status, lease_fencing_token
			FROM task_record WHERE task_id = ?`, taskID).Scan(&taskStatus, &token); err != nil {
			t.Fatal(err)
		}
		if err := harness.database.QueryRow(`SELECT status, message FROM task_step_records
			WHERE step_record_id = ?`, stepID).Scan(&stepStatus, &message); err != nil {
			t.Fatal(err)
		}
		if taskStatus != workflow.TaskSucceeded || stepStatus != workflow.StepSucceeded ||
			message != "takeover completed" || token != 2 {
			t.Fatalf("final takeover state = task:%q step:%q message:%q token:%d",
				taskStatus, stepStatus, message, token)
		}

		for _, cancel := range contexts {
			cancel()
		}
		for index, runDone := range done {
			select {
			case err := <-runDone:
				if err != nil {
					t.Fatalf("worker %d shutdown error: %v", index+1, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("worker %d did not stop", index+1)
			}
		}
	})
}

const mysqlOverlapExecutorUses = "test.mysql-overlap@v1"

type renewFailingWorkerStore struct {
	workflow.WorkerStore
	once   sync.Once
	failed chan struct{}
}

func (s *renewFailingWorkerStore) RenewTaskLease(context.Context, workflow.TaskLease, time.Duration) error {
	s.once.Do(func() { close(s.failed) })
	return workflow.ErrLeaseLost
}

type mysqlOverlapExecutor struct {
	firstStarted  chan struct{}
	secondStarted chan struct{}
	releaseFirst  chan struct{}
	calls         atomic.Int32

	mu          sync.Mutex
	sideEffects map[string]struct{}
	keys        []string
}

func newMySQLOverlapExecutor() *mysqlOverlapExecutor {
	return &mysqlOverlapExecutor{
		firstStarted: make(chan struct{}), secondStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}), sideEffects: make(map[string]struct{}),
	}
}

func (e *mysqlOverlapExecutor) Descriptor() workflow.Descriptor {
	return workflow.Descriptor{
		Uses: mysqlOverlapExecutorUses, Name: "MySQL overlap test",
		ConfigSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (e *mysqlOverlapExecutor) Validate(json.RawMessage) error { return nil }

func (e *mysqlOverlapExecutor) Start(context.Context, workflow.StartRequest) (workflow.Result, error) {
	return workflow.Result{}, errors.New("overlap test must reconcile an existing running step")
}

func (e *mysqlOverlapExecutor) Reconcile(_ context.Context, request workflow.ReconcileRequest) (workflow.Result, error) {
	call := e.calls.Add(1)
	e.mu.Lock()
	e.keys = append(e.keys, request.IdempotencyKey)
	e.sideEffects[request.IdempotencyKey] = struct{}{}
	e.mu.Unlock()

	switch call {
	case 1:
		close(e.firstStarted)
		// Deliberately ignore context cancellation: this models an external call
		// that cannot be recalled after the worker loses its lease.
		<-e.releaseFirst
		return workflow.Result{State: workflow.ResultSucceeded, Message: "stale completion"}, nil
	case 2:
		close(e.secondStarted)
		return workflow.Result{State: workflow.ResultSucceeded, Message: "takeover completed"}, nil
	default:
		return workflow.Result{State: workflow.ResultSucceeded, Message: "unexpected extra reconcile"}, nil
	}
}

func (e *mysqlOverlapExecutor) sideEffectCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.sideEffects)
}

func (e *mysqlOverlapExecutor) idempotencyKeys() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.keys...)
}

func waitMySQLWorkerSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

type workerLeaseMySQLHarness struct {
	admin        *sql.DB
	database     *sql.DB
	databaseName string
	targetDSN    string
	engines      []*xorm.Engine
	stores       []*workflow.XORMStore
}

func newWorkerLeaseMySQLHarness(t *testing.T) *workerLeaseMySQLHarness {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(workerLeaseMySQLIntegrationDSNEnv))
	if dsn == "" {
		t.Skipf("set %s to a MySQL 8.4 administrative DSN to run integration tests",
			workerLeaseMySQLIntegrationDSNEnv)
	}
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", workerLeaseMySQLIntegrationDSNEnv, err)
	}
	configuration.ParseTime = true
	configuration.Loc = time.UTC
	admin, err := sql.Open("mysql", configuration.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.PingContext(ctx); err != nil {
		_ = admin.Close()
		t.Fatalf("connect to MySQL integration server: %v", err)
	}
	var version string
	if err := admin.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	if !strings.HasPrefix(version, "8.4.") {
		_ = admin.Close()
		t.Fatalf("integration server version = %q, want MySQL 8.4.x", version)
	}

	randomSuffix := make([]byte, 8)
	if _, err := rand.Read(randomSuffix); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	databaseName := "aresw06leaseit" + hex.EncodeToString(randomSuffix)
	if !safeWorkerLeaseSQLIdentifier(databaseName) || len(databaseName) > 64 {
		_ = admin.Close()
		t.Fatalf("generated unsafe database name %q", databaseName)
	}
	if _, err := admin.ExecContext(ctx, fmt.Sprintf(
		"CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci", databaseName)); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	target := configuration.Clone()
	target.DBName = databaseName
	target.ParseTime = true
	target.Loc = time.UTC
	targetDSN := target.FormatDSN()

	migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	status, err := schemadb.MigrateUp(migrateCtx, targetDSN, "", 45*time.Second, 10*time.Second)
	migrateCancel()
	if err != nil {
		_, _ = admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", databaseName))
		_ = admin.Close()
		t.Fatalf("migrate worker lease integration database: %s", schemadb.SafeMigrationErrorText(err))
	}
	if !status.Compatible() {
		_, _ = admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", databaseName))
		_ = admin.Close()
		t.Fatalf("worker lease integration schema is incompatible: %s", status.String())
	}
	database, err := sql.Open("mysql", targetDSN)
	if err != nil {
		_, _ = admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", databaseName))
		_ = admin.Close()
		t.Fatal(err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		_, _ = admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", databaseName))
		_ = admin.Close()
		t.Fatal(err)
	}

	harness := &workerLeaseMySQLHarness{
		admin: admin, database: database, databaseName: databaseName, targetDSN: targetDSN,
	}
	t.Cleanup(func() {
		for _, engine := range harness.engines {
			_ = engine.Close()
		}
		_ = harness.database.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := harness.admin.ExecContext(cleanupCtx,
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", harness.databaseName)); err != nil {
			t.Errorf("drop worker lease integration database %s: %v", harness.databaseName, err)
		}
		_ = harness.admin.Close()
	})
	for index := 0; index < 3; index++ {
		engine, err := xorm.NewEngine("mysql", targetDSN)
		if err != nil {
			t.Fatal(err)
		}
		engine.SetMaxOpenConns(8)
		engine.SetMaxIdleConns(4)
		if err := engine.PingContext(ctx); err != nil {
			_ = engine.Close()
			t.Fatal(err)
		}
		harness.engines = append(harness.engines, engine)
		harness.stores = append(harness.stores, workflow.NewXORMStore(engine))
	}
	return harness
}

func (h *workerLeaseMySQLHarness) insertDueTask(t *testing.T, dueOffset time.Duration, failures int) int {
	t.Helper()
	result, err := h.database.Exec(`INSERT INTO task_record
		(app_name, branch, env, publisher, pipeline_param, status, engine_version,
		 workflow_version_id, next_poll_at, poll_failure_count)
		VALUES ('w06-worker-test', 'main', 'integration', 'worker-test', '{"source":"w06"}',
			?, 2, 0, TIMESTAMPADD(MICROSECOND, ?, UTC_TIMESTAMP(6)), ?)`,
		workflow.TaskQueued, dueOffset.Microseconds(), failures)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return int(taskID)
}

func (h *workerLeaseMySQLHarness) insertStep(t *testing.T, taskID int) int64 {
	t.Helper()
	result, err := h.database.Exec(`INSERT INTO task_step_records
		(task_id, workflow_version_id, step_key, name, uses, position, config,
		 timeout_seconds, on_failure, status, attempt)
		VALUES (?, 0, 'verify', 'Verify', 'builtin/noop', 0, '{}', 30, 'stop', ?, 1)`,
		taskID, workflow.StepPending)
	if err != nil {
		t.Fatal(err)
	}
	stepID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return stepID
}

func (h *workerLeaseMySQLHarness) insertRunningStep(t *testing.T, taskID int, uses string) int64 {
	t.Helper()
	if _, err := h.database.Exec(`UPDATE task_record SET status = ? WHERE task_id = ?`,
		workflow.TaskRunning, taskID); err != nil {
		t.Fatal(err)
	}
	result, err := h.database.Exec(`INSERT INTO task_step_records
		(task_id, workflow_version_id, step_key, name, uses, position, config,
		 timeout_seconds, on_failure, status, attempt, external_ref, started_at)
		VALUES (?, 0, 'overlap', 'Overlap', ?, 0, '{}', 30, 'stop', ?, 1,
			'{"provider_id":"stable-side-effect"}', UTC_TIMESTAMP(6))`,
		taskID, uses, workflow.StepRunning)
	if err != nil {
		t.Fatal(err)
	}
	stepID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return stepID
}

func (h *workerLeaseMySQLHarness) acquireExactly(
	t *testing.T,
	storeIndex int,
	owner string,
	want int,
	duration time.Duration,
) []workflow.TaskLease {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	leases, err := h.stores[storeIndex].AcquireTaskLeases(ctx, owner, want, duration)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != want {
		t.Fatalf("acquired %d leases, want %d", len(leases), want)
	}
	return leases
}

func (h *workerLeaseMySQLHarness) expireLease(t *testing.T, taskID int) {
	t.Helper()
	result, err := h.database.Exec(`UPDATE task_record
		SET lease_expires_at = TIMESTAMPADD(MICROSECOND, -1, UTC_TIMESTAMP(6)),
			next_poll_at = TIMESTAMPADD(MICROSECOND, -1, UTC_TIMESTAMP(6))
		WHERE task_id = ? AND lease_owner IS NOT NULL`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("expired %d task rows for task %d, want 1", updated, taskID)
	}
}

func (h *workerLeaseMySQLHarness) retireActiveTasks(t *testing.T) {
	t.Helper()
	if _, err := h.database.Exec(`UPDATE task_record
		SET status = ?, next_poll_at = NULL, lease_owner = NULL,
			lease_expires_at = NULL, poll_failure_count = 0
		WHERE engine_version = 2 AND deleted_at IS NULL AND status IN (?, ?)`,
		workflow.TaskSucceeded, workflow.TaskQueued, workflow.TaskRunning); err != nil {
		t.Fatal(err)
	}
}

func safeWorkerLeaseSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}
