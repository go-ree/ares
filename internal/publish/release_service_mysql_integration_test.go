package publish

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	schemadb "github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/workflow"
	"github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

const releaseMySQLIntegrationDSNEnv = "ARES_TEST_MYSQL_DSN"

type releaseMySQLFixture struct {
	actorOne        PublishActor
	actorTwo        PublishActor
	readyConfigID   int
	unboundConfigID int
	workflow        workflow.WorkflowView
}

type releaseCreateResult struct {
	receipt  ReleaseReceipt
	replayed bool
	err      error
}

func TestMySQLIdempotentRelease(t *testing.T) {
	harness := newReleaseMySQLHarness(t)
	harness.migrate(t)
	fixture := harness.seed(t)
	harness.createRuntimeAccount(t)
	harness.assertImmutableWorkflowVersionLockDenied(t, fixture.workflow.WorkflowVersionID)

	engine := harness.openRuntimeEngine(t)
	service := NewIdempotentReleaseService(engine, workflow.DefaultRegistry())
	legacyCtx, legacyCancel := context.WithTimeout(context.Background(), 30*time.Second)
	legacyCommands, legacyErrors, err := service.ResolveLegacyCommands(legacyCtx, []CreatePublishRequest{
		{AppName: "W05-IDEMPOTENT-READY", Env: "integration", Branch: "main"},
		{AppName: "w05-idempotent-unbound", Env: "integration", Branch: "release/v1"},
		{AppName: "w05-missing", Env: "integration", Branch: "main"},
	})
	legacyCancel()
	if err != nil || len(legacyCommands) != 3 || len(legacyErrors) != 3 ||
		legacyCommands[0].ConfigID != fixture.readyConfigID ||
		legacyCommands[1].ConfigID != fixture.unboundConfigID ||
		legacyErrors[0] != nil || legacyErrors[1] != nil || legacyErrors[2] == nil {
		t.Fatalf("bulk legacy resolution = commands:%#v item_errors:%#v error:%v",
			legacyCommands, legacyErrors, err)
	}
	versionID := fixture.workflow.WorkflowVersionID
	command := ReleaseCommand{
		ConfigID:                  fixture.readyConfigID,
		Ref:                       "refs/heads/main",
		Inputs:                    json.RawMessage(`{"build_number":9007199254740993,"source":"mysql-integration"}`),
		ExpectedWorkflowVersionID: &versionID,
	}
	token := mustReleaseIdempotencyToken(t, "w05-concurrent-release-0001")

	const workers = 32
	results := make([]releaseCreateResult, workers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := range results {
		go func(index int) {
			defer wait.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			results[index].receipt, results[index].replayed, results[index].err =
				service.Create(ctx, fixture.actorOne, token, command)
		}(index)
	}
	close(start)
	wait.Wait()

	original := assertConcurrentReleaseResults(t, results)
	assertReceiptShape(t, original, 1, 1, 0)
	assertAcceptedTask(t, harness.database, original.Items[0], fixture.actorOne.UserID, 2)
	assertReleaseTableCounts(t, harness.database, 1, 1, 1, 2)

	// A replay must be resolved entirely from durable state. Closing this engine
	// rules out an in-memory result or lock surviving between the two requests.
	if err := engine.Close(); err != nil {
		t.Fatalf("close first xorm engine: %v", err)
	}
	engine = harness.openRuntimeEngine(t)
	service = NewIdempotentReleaseService(engine, workflow.DefaultRegistry())
	replayCtx, replayCancel := context.WithTimeout(context.Background(), 30*time.Second)
	replayedReceipt, replayed, err := service.Create(replayCtx, fixture.actorOne, token, command)
	replayCancel()
	if err != nil {
		t.Fatalf("replay after service and engine restart: %v", err)
	}
	if !replayed {
		t.Fatal("replay after service and engine restart was reported as a first submission")
	}
	if !reflect.DeepEqual(replayedReceipt, original) {
		t.Fatalf("replayed receipt changed after restart\ngot:  %#v\nwant: %#v", replayedReceipt, original)
	}
	assertReleaseTableCounts(t, harness.database, 1, 1, 1, 2)

	// Schema compatibility is based on index semantics, not physical names.
	// Replay therefore must not parse a particular name from MySQL error text.
	if _, err := harness.database.Exec(`ALTER TABLE release_idempotency_records
		RENAME INDEX uk_release_idempotency_scope_actor_key TO uk_release_idempotency_equivalent`); err != nil {
		t.Fatalf("rename equivalent idempotency index: %v", err)
	}
	renamedIndexCtx, renamedIndexCancel := context.WithTimeout(context.Background(), 30*time.Second)
	renamedIndexReceipt, renamedIndexReplay, err := service.Create(renamedIndexCtx, fixture.actorOne, token, command)
	renamedIndexCancel()
	if err != nil || !renamedIndexReplay || !reflect.DeepEqual(renamedIndexReceipt, original) {
		t.Fatalf("replay with equivalent renamed index = receipt:%#v replayed:%t error:%v",
			renamedIndexReceipt, renamedIndexReplay, err)
	}
	if _, err := harness.database.Exec(`ALTER TABLE release_idempotency_records
		RENAME INDEX uk_release_idempotency_equivalent TO uk_release_idempotency_scope_actor_key`); err != nil {
		t.Fatalf("restore idempotency index name: %v", err)
	}

	// Receipts outlive mutable task-list visibility. A historical soft delete
	// must not make a valid canonical receipt unreadable or create a new task.
	if _, err := harness.database.Exec("UPDATE task_record SET deleted_at = NOW() WHERE task_id = ?", *original.Items[0].TaskID); err != nil {
		t.Fatalf("soft delete accepted task: %v", err)
	}
	deletedTaskCtx, deletedTaskCancel := context.WithTimeout(context.Background(), 30*time.Second)
	deletedTaskReceipt, deletedTaskReplay, err := service.Create(deletedTaskCtx, fixture.actorOne, token, command)
	deletedTaskCancel()
	if err != nil || !deletedTaskReplay || !reflect.DeepEqual(deletedTaskReceipt, original) {
		t.Fatalf("replay after task soft delete = receipt:%#v replayed:%t error:%v",
			deletedTaskReceipt, deletedTaskReplay, err)
	}
	if _, err := harness.database.Exec("UPDATE task_record SET deleted_at = NULL WHERE task_id = ?", *original.Items[0].TaskID); err != nil {
		t.Fatalf("restore accepted task visibility: %v", err)
	}

	conflictingCommand := command
	conflictingCommand.Ref = "refs/heads/conflicting-payload"
	conflictCtx, conflictCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, _, err = service.Create(conflictCtx, fixture.actorOne, token, conflictingCommand)
	conflictCancel()
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)
	assertReleaseTableCounts(t, harness.database, 1, 1, 1, 2)

	isolationCtx, isolationCancel := context.WithTimeout(context.Background(), 30*time.Second)
	isolatedReceipt, isolatedReplay, err := service.Create(isolationCtx, fixture.actorTwo, token, command)
	isolationCancel()
	if err != nil {
		t.Fatalf("same raw key for another actor: %v", err)
	}
	if isolatedReplay {
		t.Fatal("same raw key for another actor was incorrectly treated as a replay")
	}
	assertReceiptShape(t, isolatedReceipt, 1, 1, 0)
	if isolatedReceipt.RecordID == original.RecordID || *isolatedReceipt.Items[0].TaskID == *original.Items[0].TaskID {
		t.Fatalf("actor-isolated release reused durable identity: first=%#v isolated=%#v", original, isolatedReceipt)
	}
	assertAcceptedTask(t, harness.database, isolatedReceipt.Items[0], fixture.actorTwo.UserID, 2)
	assertReleaseTableCounts(t, harness.database, 2, 2, 2, 4)

	batchToken := mustReleaseIdempotencyToken(t, "w05-mixed-batch-release-0001")
	batchCommands := []ReleaseCommand{
		command,
		{
			ConfigID: fixture.unboundConfigID,
			Ref:      "refs/tags/v1.0.0",
			Inputs:   json.RawMessage(`{"reason":"expected-unconfigured-target"}`),
		},
	}
	batchCtx, batchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	batchReceipt, batchReplay, err := service.CreateBatch(batchCtx, fixture.actorOne, batchToken, batchCommands)
	batchCancel()
	if err != nil {
		t.Fatalf("create mixed batch: %v", err)
	}
	if batchReplay {
		t.Fatal("first mixed batch submission was reported as replayed")
	}
	assertReceiptShape(t, batchReceipt, 2, 1, 1)
	if batchReceipt.Items[0].RequestIndex != 0 || batchReceipt.Items[0].ConfigID != fixture.readyConfigID ||
		!batchReceipt.Items[0].Success || batchReceipt.Items[0].TaskID == nil ||
		batchReceipt.Items[0].WorkflowVersionID == nil || batchReceipt.Items[0].ErrorCode != "" {
		t.Fatalf("mixed batch accepted item = %#v", batchReceipt.Items[0])
	}
	if batchReceipt.Items[1].RequestIndex != 1 || batchReceipt.Items[1].ConfigID != fixture.unboundConfigID ||
		batchReceipt.Items[1].Success || batchReceipt.Items[1].TaskID != nil ||
		batchReceipt.Items[1].WorkflowVersionID != nil ||
		batchReceipt.Items[1].ErrorCode != ReleaseCodeWorkflowNotConfigured {
		t.Fatalf("mixed batch rejected item = %#v", batchReceipt.Items[1])
	}
	assertAcceptedTask(t, harness.database, batchReceipt.Items[0], fixture.actorOne.UserID, 2)
	assertPersistedMixedBatch(t, harness.database, batchReceipt, fixture)
	assertReleaseTableCounts(t, harness.database, 3, 4, 3, 6)

	batchReplayCtx, batchReplayCancel := context.WithTimeout(context.Background(), 30*time.Second)
	replayedBatch, replayed, err := service.CreateBatch(batchReplayCtx, fixture.actorOne, batchToken, batchCommands)
	batchReplayCancel()
	if err != nil {
		t.Fatalf("replay mixed batch: %v", err)
	}
	if !replayed {
		t.Fatal("mixed batch replay was reported as a first submission")
	}
	if !reflect.DeepEqual(replayedBatch, batchReceipt) {
		t.Fatalf("mixed batch replay changed the ordered receipt\ngot:  %#v\nwant: %#v", replayedBatch, batchReceipt)
	}
	assertPersistedMixedBatch(t, harness.database, replayedBatch, fixture)
	assertReleaseTableCounts(t, harness.database, 3, 4, 3, 6)

	blockedToken := mustReleaseIdempotencyToken(t, "w05-pending-reservation-0001")
	normalizedCommand, err := normalizeReleaseCommand(command)
	if err != nil {
		t.Fatalf("normalize blocked release command: %v", err)
	}
	blockedDigest, err := releaseRequestDigest(SemanticReleaseCreate, []ReleaseCommand{normalizedCommand})
	if err != nil {
		t.Fatalf("digest blocked release command: %v", err)
	}
	holderDatabase, err := sql.Open("mysql", harness.runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	holderCtx, holderCancel := context.WithTimeout(context.Background(), 30*time.Second)
	holderTransaction, err := holderDatabase.BeginTx(holderCtx, nil)
	if err != nil {
		holderCancel()
		_ = holderDatabase.Close()
		t.Fatal(err)
	}
	if _, err := holderTransaction.ExecContext(holderCtx, `INSERT INTO release_idempotency_records
		(actor_user_id, semantic_operation, key_digest, request_digest, item_count)
		VALUES (?, ?, ?, ?, ?)`, fixture.actorOne.UserID, SemanticReleaseCreate,
		blockedToken.bytes(), blockedDigest[:], 1); err != nil {
		_ = holderTransaction.Rollback()
		holderCancel()
		_ = holderDatabase.Close()
		t.Fatalf("hold uncommitted idempotency reservation: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	waitStartedAt := time.Now()
	blockedReceipt, blockedReplay, blockedErr := service.Create(
		waitCtx, fixture.actorOne, blockedToken, command)
	waitElapsed := time.Since(waitStartedAt)
	waitCancel()
	rollbackErr := holderTransaction.Rollback()
	holderCancel()
	closeErr := holderDatabase.Close()
	if rollbackErr != nil {
		t.Fatalf("rollback uncommitted idempotency reservation: %v", rollbackErr)
	}
	if closeErr != nil {
		t.Fatalf("close reservation holder database: %v", closeErr)
	}
	if blockedReplay || !reflect.DeepEqual(blockedReceipt, ReleaseReceipt{}) {
		t.Fatalf("blocked reservation result = receipt:%#v replayed:%t, want empty/non-replayed",
			blockedReceipt, blockedReplay)
	}
	assertReleaseError(t, blockedErr, ReleaseCodeIdempotencyRequestInProgress, 409)
	if minimum := idempotencyReservationTimeout - 750*time.Millisecond; waitElapsed < minimum {
		t.Fatalf("blocked reservation returned after %s, want a real wait of at least %s", waitElapsed, minimum)
	}
	if maximum := idempotencyReservationTimeout + 3*time.Second; waitElapsed > maximum {
		t.Fatalf("blocked reservation returned after %s, want at most %s", waitElapsed, maximum)
	}
	assertReleaseTableCounts(t, harness.database, 3, 4, 3, 6)

	// A failure after one step snapshot proves reservation, task, and partial
	// step writes share the same rollback boundary.
	if _, err := harness.database.Exec(`CREATE TRIGGER w05_fail_second_step
		BEFORE INSERT ON task_step_records FOR EACH ROW
		BEGIN
			IF NEW.position = 1 THEN
				SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'w05 injected step failure';
			END IF;
		END`); err != nil {
		t.Fatalf("create step failure trigger: %v", err)
	}
	stepFailureToken := mustReleaseIdempotencyToken(t, "w05-step-failure-release-0001")
	stepFailureCtx, stepFailureCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, _, stepFailureErr := service.Create(stepFailureCtx, fixture.actorOne, stepFailureToken, command)
	stepFailureCancel()
	if stepFailureErr == nil {
		t.Fatal("injected second-step failure unexpectedly succeeded")
	}
	if _, err := harness.database.Exec("DROP TRIGGER w05_fail_second_step"); err != nil {
		t.Fatalf("drop step failure trigger: %v", err)
	}
	assertReleaseTableCounts(t, harness.database, 3, 4, 3, 6)

	// A failure on the second receipt item occurs after an accepted task, all of
	// its steps, and the first item; every one of those writes must roll back.
	if _, err := harness.database.Exec(`CREATE TRIGGER w05_fail_second_receipt_item
		BEFORE INSERT ON release_idempotency_items FOR EACH ROW
		BEGIN
			IF NEW.request_index = 1 THEN
				SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'w05 injected receipt item failure';
			END IF;
		END`); err != nil {
		t.Fatalf("create receipt item failure trigger: %v", err)
	}
	itemFailureToken := mustReleaseIdempotencyToken(t, "w05-item-failure-release-0001")
	itemFailureCtx, itemFailureCancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, _, itemFailureErr := service.CreateBatch(itemFailureCtx, fixture.actorOne, itemFailureToken, batchCommands)
	itemFailureCancel()
	if itemFailureErr == nil {
		t.Fatal("injected second-item failure unexpectedly succeeded")
	}
	if _, err := harness.database.Exec("DROP TRIGGER w05_fail_second_receipt_item"); err != nil {
		t.Fatalf("drop receipt item failure trigger: %v", err)
	}
	assertReleaseTableCounts(t, harness.database, 3, 4, 3, 6)

	// Simulate a lost COMMIT acknowledgement: commit reaches MySQL, while the
	// injected client hook reports an error. The service must resolve by the
	// durable key and return the committed receipt, never create a second task.
	ambiguousService := NewIdempotentReleaseService(engine, workflow.DefaultRegistry())
	ambiguousService.commit = func(session *xorm.Session) error {
		if err := session.Commit(); err != nil {
			return err
		}
		return errors.New("injected lost commit acknowledgement")
	}
	ambiguousToken := mustReleaseIdempotencyToken(t, "w05-commit-ambiguity-0001")
	ambiguousCtx, ambiguousCancel := context.WithTimeout(context.Background(), 30*time.Second)
	ambiguousReceipt, ambiguousReplay, ambiguousErr := ambiguousService.Create(
		ambiguousCtx, fixture.actorOne, ambiguousToken, command)
	ambiguousCancel()
	if ambiguousErr != nil || !ambiguousReplay {
		t.Fatalf("commit ambiguity result = receipt:%#v replayed:%t error:%v",
			ambiguousReceipt, ambiguousReplay, ambiguousErr)
	}
	assertReceiptShape(t, ambiguousReceipt, 1, 1, 0)
	assertReleaseTableCounts(t, harness.database, 4, 5, 4, 8)

	// Exercise the protocol maximum and the replay bulk read without any N+1
	// task lookups. Unbound targets deliberately produce 100 durable failures.
	maxCommands := make([]ReleaseCommand, maxBatchReleaseItems)
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 60*time.Second)
	for index := range maxCommands {
		configID := harness.insertAppConfig(t, seedCtx, fmt.Sprintf("w05-max-batch-%03d", index))
		maxCommands[index] = ReleaseCommand{ConfigID: configID, Ref: "refs/heads/main"}
	}
	seedCancel()
	maxToken := mustReleaseIdempotencyToken(t, "w05-maximum-batch-release-0001")
	maxCtx, maxCancel := context.WithTimeout(context.Background(), 45*time.Second)
	maxReceipt, maxReplay, maxErr := service.CreateBatch(maxCtx, fixture.actorOne, maxToken, maxCommands)
	maxCancel()
	if maxErr != nil || maxReplay {
		t.Fatalf("maximum batch result = receipt:%#v replayed:%t error:%v", maxReceipt, maxReplay, maxErr)
	}
	assertReceiptShape(t, maxReceipt, maxBatchReleaseItems, 0, maxBatchReleaseItems)
	maxReplayCtx, maxReplayCancel := context.WithTimeout(context.Background(), 30*time.Second)
	replayedMaximum, replayedMaximumFlag, maxReplayErr := service.CreateBatch(
		maxReplayCtx, fixture.actorOne, maxToken, maxCommands)
	maxReplayCancel()
	if maxReplayErr != nil || !replayedMaximumFlag || !reflect.DeepEqual(replayedMaximum, maxReceipt) {
		t.Fatalf("maximum batch replay = receipt:%#v replayed:%t error:%v",
			replayedMaximum, replayedMaximumFlag, maxReplayErr)
	}
	assertReleaseTableCounts(t, harness.database, 5, 105, 4, 8)

	// BeginTx must inherit request cancellation even before a connection is
	// acquired from an exhausted pool.
	poolEngine := harness.openRuntimeEngine(t)
	poolEngine.SetMaxOpenConns(1)
	heldConnection, err := poolEngine.DB().DB.Conn(context.Background())
	if err != nil {
		t.Fatalf("hold only runtime connection: %v", err)
	}
	poolService := NewIdempotentReleaseService(poolEngine, workflow.DefaultRegistry())
	poolCtx, poolCancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	poolStartedAt := time.Now()
	_, _, poolErr := poolService.Create(poolCtx, fixture.actorOne,
		mustReleaseIdempotencyToken(t, "w05-begin-cancel-release-0001"), command)
	poolElapsed := time.Since(poolStartedAt)
	poolCancel()
	if !errors.Is(poolErr, context.DeadlineExceeded) {
		t.Fatalf("pool-exhausted Begin error = %v, want context deadline", poolErr)
	}
	if poolElapsed > 2*time.Second {
		t.Fatalf("pool-exhausted Begin observed cancellation after %s", poolElapsed)
	}
	if err := heldConnection.Close(); err != nil {
		t.Fatalf("release held runtime connection: %v", err)
	}
	if err := poolEngine.Close(); err != nil {
		t.Fatalf("close pool cancellation engine: %v", err)
	}

	if err := engine.Close(); err != nil {
		t.Fatalf("close second xorm engine: %v", err)
	}
}

func TestMySQLKeyedLegacyHistoricalReplay(t *testing.T) {
	harness := newReleaseMySQLHarness(t)
	harness.migrate(t)
	fixture := harness.seed(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	batchReadyConfigID := harness.insertAppConfig(t, ctx, "w05-legacy-batch-ready")
	saveNoopWorkflow := func(configID int, name string) workflow.WorkflowView {
		engine := harness.openAdminEngine(t)
		defer engine.Close()
		definitions := workflow.NewService(workflow.NewXORMStore(engine), workflow.DefaultRegistry())
		view, err := definitions.Save(ctx, configID, 0, fixture.actorOne.DisplayName, fixture.actorOne.UserID,
			workflow.WorkflowSpec{
				SchemaVersion: workflow.SchemaVersionV1,
				Name:          name,
				Steps: []workflow.StepSpec{
					{Key: "deploy", Name: "Deploy", Uses: workflow.NoopUses},
				},
			})
		if err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
		return view
	}
	saveNoopWorkflow(batchReadyConfigID, "Legacy batch historical replay")

	harness.createRuntimeAccount(t)
	engine := harness.openRuntimeEngine(t)
	defer engine.Close()
	service := NewIdempotentReleaseService(engine, workflow.DefaultRegistry())

	singleRequest := CreatePublishRequest{
		AppName: "w05-idempotent-ready", Env: "integration", Branch: "refs/heads/main",
		ExtraData: map[string]any{"release_reason": "historical-single"},
	}
	singleToken := mustReleaseIdempotencyToken(t, "w05-legacy-historical-single-0001")
	singleReceipt, replayed, err := service.CreateLegacyRelease(
		ctx, fixture.actorOne, singleToken, singleRequest)
	if err != nil || replayed {
		t.Fatalf("create keyed legacy single = receipt:%#v replayed:%t error:%v", singleReceipt, replayed, err)
	}
	assertReceiptShape(t, singleReceipt, 1, 1, 0)
	assertReleaseTableCounts(t, harness.database, 1, 1, 1, 2)

	// Once the actor-scoped receipt is visible, a transient failure while reading
	// its historical alias must fail closed as outcome_unknown. A generic 500 or
	// fallback to active resolution could invite an unsafe retry/remap.
	lockConnection, err := harness.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close()
	locked := false
	if _, err := lockConnection.ExecContext(ctx, "LOCK TABLES app_configs WRITE"); err != nil {
		t.Fatal(err)
	}
	locked = true
	defer func() {
		if locked {
			_, _ = lockConnection.ExecContext(context.Background(), "UNLOCK TABLES")
		}
	}()
	unknownReceipt, unknownReplay, unknownErr := service.CreateLegacyRelease(
		ctx, fixture.actorOne, singleToken, singleRequest)
	if !reflect.DeepEqual(unknownReceipt, ReleaseReceipt{}) || unknownReplay {
		t.Fatalf("historical lookup failure returned a receipt: receipt=%#v replayed=%t", unknownReceipt, unknownReplay)
	}
	assertReleaseError(t, unknownErr, ReleaseCodeOutcomeUnknown, http.StatusServiceUnavailable)
	if _, err := lockConnection.ExecContext(ctx, "UNLOCK TABLES"); err != nil {
		t.Fatal(err)
	}
	locked = false
	assertReleaseTableCounts(t, harness.database, 1, 1, 1, 2)

	var originalAppID int
	if err := harness.database.QueryRowContext(ctx,
		"SELECT app_id FROM app_configs WHERE config_id = ?", fixture.readyConfigID).Scan(&originalAppID); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.database.ExecContext(ctx,
		"UPDATE app_configs SET deleted_at = NOW(6) WHERE config_id = ?", fixture.readyConfigID); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.database.ExecContext(ctx,
		"UPDATE apps SET deleted_at = NOW(6) WHERE app_id = ?", originalAppID); err != nil {
		t.Fatal(err)
	}

	// Recreate the same active alias with a different stable target. The original
	// actor/key must still replay the deleted historical target, while another
	// actor using the same raw key receives an independent release on the new one.
	replacementConfigID := harness.insertAppConfig(t, ctx, singleRequest.AppName)
	replacementWorkflow := saveNoopWorkflow(replacementConfigID, "Legacy single replacement")
	replayedSingle, replayed, err := service.CreateLegacyRelease(
		ctx, fixture.actorOne, singleToken, singleRequest)
	if err != nil || !replayed || !reflect.DeepEqual(replayedSingle, singleReceipt) {
		t.Fatalf("legacy single replay after soft delete/remap = receipt:%#v replayed:%t error:%v",
			replayedSingle, replayed, err)
	}
	assertReleaseTableCounts(t, harness.database, 1, 1, 1, 2)

	caseEquivalentRequest := singleRequest
	caseEquivalentRequest.AppName = strings.ToUpper(singleRequest.AppName)
	caseEquivalentRequest.Env = "INTEGRATION"
	caseEquivalentReceipt, caseEquivalentReplay, err := service.CreateLegacyRelease(
		ctx, fixture.actorOne, singleToken, caseEquivalentRequest)
	if err != nil || !caseEquivalentReplay || !reflect.DeepEqual(caseEquivalentReceipt, singleReceipt) {
		t.Fatalf("case-equivalent historical alias replay = receipt:%#v replayed:%t error:%v",
			caseEquivalentReceipt, caseEquivalentReplay, err)
	}

	changedSingle := singleRequest
	changedSingle.Branch = "refs/heads/changed"
	_, _, err = service.CreateLegacyRelease(ctx, fixture.actorOne, singleToken, changedSingle)
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)
	changedSingle = singleRequest
	changedSingle.ExtraData = map[string]any{"release_reason": "different-input"}
	_, _, err = service.CreateLegacyRelease(ctx, fixture.actorOne, singleToken, changedSingle)
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)
	changedSingle = singleRequest
	changedSingle.AppName = "w05-idempotent-unbound"
	_, _, err = service.CreateLegacyRelease(ctx, fixture.actorOne, singleToken, changedSingle)
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)
	assertReleaseTableCounts(t, harness.database, 1, 1, 1, 2)

	isolatedReceipt, isolatedReplay, err := service.CreateLegacyRelease(
		ctx, fixture.actorTwo, singleToken, singleRequest)
	if err != nil || isolatedReplay {
		t.Fatalf("actor-isolated legacy single = receipt:%#v replayed:%t error:%v",
			isolatedReceipt, isolatedReplay, err)
	}
	if isolatedReceipt.RecordID == singleReceipt.RecordID ||
		*isolatedReceipt.Items[0].TaskID == *singleReceipt.Items[0].TaskID ||
		isolatedReceipt.Items[0].ConfigID != replacementConfigID ||
		*isolatedReceipt.Items[0].WorkflowVersionID != replacementWorkflow.WorkflowVersionID {
		t.Fatalf("actor-isolated legacy single reused another actor's receipt: original=%#v isolated=%#v",
			singleReceipt, isolatedReceipt)
	}
	assertReleaseTableCounts(t, harness.database, 2, 2, 2, 3)

	batchRequests := []CreatePublishRequest{
		{
			AppName: "w05-legacy-batch-ready", Env: "integration", Branch: "refs/heads/main",
			ExtraData: map[string]any{"request": "accepted"},
		},
		{
			AppName: "w05-idempotent-unbound", Env: "integration", Branch: "refs/tags/v1.0.0",
			ExtraData: map[string]any{"request": "rejected"},
		},
	}
	batchToken := mustReleaseIdempotencyToken(t, "w05-legacy-historical-batch-0001")
	batchReceipt, replayed, err := service.CreateLegacyBatchRelease(
		ctx, fixture.actorOne, batchToken, batchRequests)
	if err != nil || replayed {
		t.Fatalf("create keyed legacy batch = receipt:%#v replayed:%t error:%v", batchReceipt, replayed, err)
	}
	assertReceiptShape(t, batchReceipt, 2, 1, 1)
	if batchReceipt.Items[0].ConfigID != batchReadyConfigID || !batchReceipt.Items[0].Success ||
		batchReceipt.Items[1].ConfigID != fixture.unboundConfigID || batchReceipt.Items[1].Success ||
		batchReceipt.Items[1].ErrorCode != ReleaseCodeWorkflowNotConfigured {
		t.Fatalf("keyed legacy batch receipt = %#v", batchReceipt)
	}
	assertReleaseTableCounts(t, harness.database, 3, 4, 3, 4)

	var batchReadyAppID int
	if err := harness.database.QueryRowContext(ctx,
		"SELECT app_id FROM app_configs WHERE config_id = ?", batchReadyConfigID).Scan(&batchReadyAppID); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.database.ExecContext(ctx,
		"UPDATE app_configs SET deleted_at = NOW(6) WHERE config_id = ?", batchReadyConfigID); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.database.ExecContext(ctx,
		"UPDATE apps SET deleted_at = NOW(6) WHERE app_id = ?", batchReadyAppID); err != nil {
		t.Fatal(err)
	}
	replayedBatch, replayed, err := service.CreateLegacyBatchRelease(
		ctx, fixture.actorOne, batchToken, batchRequests)
	if err != nil || !replayed || !reflect.DeepEqual(replayedBatch, batchReceipt) {
		t.Fatalf("legacy batch replay after soft delete = receipt:%#v replayed:%t error:%v",
			replayedBatch, replayed, err)
	}

	// Two new active app/config rows with the same legacy alias make the mutable
	// resolver ambiguous. Receipt-indexed historical resolution must remain exact.
	_ = harness.insertAppConfig(t, ctx, batchRequests[0].AppName)
	_ = harness.insertAppConfig(t, ctx, batchRequests[0].AppName)
	replayedBatch, replayed, err = service.CreateLegacyBatchRelease(
		ctx, fixture.actorOne, batchToken, batchRequests)
	if err != nil || !replayed || !reflect.DeepEqual(replayedBatch, batchReceipt) {
		t.Fatalf("legacy batch replay after alias ambiguity = receipt:%#v replayed:%t error:%v",
			replayedBatch, replayed, err)
	}

	changedBatch := append([]CreatePublishRequest(nil), batchRequests...)
	changedBatch[1].Branch = "refs/tags/v2.0.0"
	_, _, err = service.CreateLegacyBatchRelease(ctx, fixture.actorOne, batchToken, changedBatch)
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)
	changedBatch = append([]CreatePublishRequest(nil), batchRequests...)
	changedBatch[0].ExtraData = map[string]any{"request": "different"}
	_, _, err = service.CreateLegacyBatchRelease(ctx, fixture.actorOne, batchToken, changedBatch)
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)
	reorderedBatch := []CreatePublishRequest{batchRequests[1], batchRequests[0]}
	_, _, err = service.CreateLegacyBatchRelease(ctx, fixture.actorOne, batchToken, reorderedBatch)
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)
	_, _, err = service.CreateLegacyBatchRelease(ctx, fixture.actorOne, batchToken, batchRequests[:1])
	assertReleaseError(t, err, ReleaseCodeIdempotencyKeyConflict, 409)

	isolatedBatchReceipt, isolatedBatchReplay, isolatedBatchErr := service.CreateLegacyBatchRelease(
		ctx, fixture.actorTwo, batchToken, batchRequests)
	if isolatedBatchErr == nil || isolatedBatchReplay || !reflect.DeepEqual(isolatedBatchReceipt, ReleaseReceipt{}) {
		t.Fatalf("actor-isolated ambiguous legacy batch = receipt:%#v replayed:%t error:%v",
			isolatedBatchReceipt, isolatedBatchReplay, isolatedBatchErr)
	}
	var isolatedBatchInputError *InputError
	if !errors.As(isolatedBatchErr, &isolatedBatchInputError) ||
		isolatedBatchInputError.Message != "批量发布目标无法唯一解析" {
		t.Fatalf("another actor did not take the active ambiguous-target path: %v", isolatedBatchErr)
	}
	assertReleaseTableCounts(t, harness.database, 3, 4, 3, 4)
}

func TestMySQLCrossBatchLockOrder(t *testing.T) {
	harness := newReleaseMySQLHarness(t)
	harness.migrate(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := harness.database.ExecContext(ctx, `INSERT INTO env_configs (env, description_cn, enabled)
		VALUES ('lock-a', 'Lock order A', 1), ('lock-b', 'Lock order B', 1)`); err != nil {
		t.Fatal(err)
	}
	insertApp := func(name string) int64 {
		result, err := harness.database.ExecContext(ctx, `INSERT INTO apps
			(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
			VALUES (?, ?, 'integration-test', 'Integration Test', 'golang', ?)`,
			name, name, "https://example.invalid/"+name)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	insertConfig := func(appID int64, envCode string) int {
		result, err := harness.database.ExecContext(ctx, `INSERT INTO app_configs
			(app_id, env, code_package_type) VALUES (?, ?, 'golang')`, appID, envCode)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return int(id)
	}
	appOne, appTwo := insertApp("w05-lock-app-one"), insertApp("w05-lock-app-two")
	configOne := insertConfig(appOne, "lock-a")
	configTwo := insertConfig(appTwo, "lock-b")
	configThree := insertConfig(appTwo, "lock-a")
	configFour := insertConfig(appOne, "lock-b")
	actor := harness.insertActor(t, ctx, "w05-lock-actor", "W05 Lock Actor")

	adminEngine := harness.openAdminEngine(t)
	definitions := workflow.NewService(workflow.NewXORMStore(adminEngine), workflow.DefaultRegistry())
	workflowOne, err := definitions.Save(ctx, configOne, 0, actor.DisplayName, actor.UserID, workflow.WorkflowSpec{
		SchemaVersion: workflow.SchemaVersionV1, Name: "Lock workflow one",
		Steps: []workflow.StepSpec{{Key: "deploy", Name: "Deploy", Uses: workflow.NoopUses}},
	})
	if err != nil {
		t.Fatal(err)
	}
	workflowTwo, err := definitions.Save(ctx, configTwo, 0, actor.DisplayName, actor.UserID, workflow.WorkflowSpec{
		SchemaVersion: workflow.SchemaVersionV1, Name: "Lock workflow two",
		Steps: []workflow.StepSpec{{Key: "deploy", Name: "Deploy", Uses: workflow.NoopUses}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := adminEngine.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.database.ExecContext(ctx, `INSERT INTO app_config_workflows
		(app_config_id, workflow_id, version_id, revision) VALUES
		(?, ?, ?, 1), (?, ?, ?, 1)`,
		configThree, workflowTwo.WorkflowID, workflowTwo.WorkflowVersionID,
		configFour, workflowOne.WorkflowID, workflowOne.WorkflowVersionID); err != nil {
		t.Fatal(err)
	}

	harness.createRuntimeAccount(t)
	engine := harness.openRuntimeEngine(t)
	defer engine.Close()
	service := NewIdempotentReleaseService(engine, workflow.DefaultRegistry())
	batches := [][]ReleaseCommand{
		{{ConfigID: configOne, Ref: "main"}, {ConfigID: configTwo, Ref: "main"}},
		{{ConfigID: configThree, Ref: "main"}, {ConfigID: configFour, Ref: "main"}},
	}
	for round := 0; round < 12; round++ {
		start := make(chan struct{})
		results := make([]releaseCreateResult, len(batches))
		var wait sync.WaitGroup
		for index := range batches {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				<-start
				runCtx, runCancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer runCancel()
				key := fmt.Sprintf("w05-cross-lock-%02d-%02d", round, index)
				results[index].receipt, results[index].replayed, results[index].err =
					service.CreateBatch(runCtx, actor, mustReleaseIdempotencyToken(t, key), batches[index])
			}(index)
		}
		close(start)
		wait.Wait()
		for index, result := range results {
			if result.err != nil || result.replayed {
				t.Fatalf("round %d batch %d = replayed:%t error:%v", round, index, result.replayed, result.err)
			}
			assertReceiptShape(t, result.receipt, 2, 2, 0)
		}
	}
	assertReleaseTableCounts(t, harness.database, 24, 48, 48, 48)
}

func assertConcurrentReleaseResults(t *testing.T, results []releaseCreateResult) ReleaseReceipt {
	t.Helper()
	firstCount := 0
	var original ReleaseReceipt
	for index, result := range results {
		if result.err != nil {
			t.Fatalf("concurrent release %d failed: %v", index, result.err)
		}
		if !result.replayed {
			firstCount++
			original = result.receipt
		}
	}
	if firstCount != 1 {
		t.Fatalf("first-submission count = %d, want 1", firstCount)
	}
	for index, result := range results {
		if !reflect.DeepEqual(result.receipt, original) {
			t.Fatalf("concurrent receipt %d differs from the durable original\ngot:  %#v\nwant: %#v", index, result.receipt, original)
		}
	}
	return original
}

func assertReceiptShape(t *testing.T, receipt ReleaseReceipt, total, success, failure int) {
	t.Helper()
	if receipt.RecordID <= 0 || receipt.TotalCount != total || receipt.SuccessCount != success ||
		receipt.FailureCount != failure || len(receipt.Items) != total {
		t.Fatalf("receipt shape = %#v, want total/success/failure=%d/%d/%d", receipt, total, success, failure)
	}
}

func assertAcceptedTask(t *testing.T, database *sql.DB, item ReleaseReceiptItem, actorUserID int64, stepCount int) {
	t.Helper()
	if !item.Success || item.TaskID == nil || item.WorkflowVersionID == nil {
		t.Fatalf("accepted item is incomplete: %#v", item)
	}
	var tasks int
	if err := database.QueryRow(`SELECT COUNT(*) FROM task_record
		WHERE task_id = ? AND app_config_id = ? AND publisher_user_id = ?
			AND workflow_version_id = ? AND engine_version = 2 AND status = ?`,
		*item.TaskID, item.ConfigID, actorUserID, *item.WorkflowVersionID, workflow.TaskQueued).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("accepted task relation count = %d, want 1 for item %#v", tasks, item)
	}
	var steps int
	if err := database.QueryRow(`SELECT COUNT(*) FROM task_step_records
		WHERE task_id = ? AND workflow_version_id = ?`, *item.TaskID, *item.WorkflowVersionID).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if steps != stepCount {
		t.Fatalf("task %d step snapshot count = %d, want %d", *item.TaskID, steps, stepCount)
	}
}

func assertPersistedMixedBatch(t *testing.T, database *sql.DB, receipt ReleaseReceipt, fixture releaseMySQLFixture) {
	t.Helper()
	var itemCount int
	if err := database.QueryRow(`SELECT item_count FROM release_idempotency_records
		WHERE idempotency_id = ? AND actor_user_id = ? AND semantic_operation = ?`,
		receipt.RecordID, fixture.actorOne.UserID, SemanticReleaseBatchCreate).Scan(&itemCount); err != nil {
		t.Fatal(err)
	}
	if itemCount != 2 {
		t.Fatalf("mixed batch durable item_count = %d, want 2", itemCount)
	}

	rows, err := database.Query(`SELECT request_index, config_id, task_id, workflow_version_id, outcome, error_code
		FROM release_idempotency_items WHERE record_id = ? ORDER BY request_index`, receipt.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type persistedItem struct {
		requestIndex      int
		configID          int
		taskID            sql.NullInt64
		workflowVersionID sql.NullInt64
		outcome           string
		errorCode         sql.NullString
	}
	persisted := make([]persistedItem, 0, 2)
	for rows.Next() {
		var item persistedItem
		if err := rows.Scan(&item.requestIndex, &item.configID, &item.taskID, &item.workflowVersionID, &item.outcome, &item.errorCode); err != nil {
			t.Fatal(err)
		}
		persisted = append(persisted, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 {
		t.Fatalf("mixed batch durable item rows = %d, want 2", len(persisted))
	}
	accepted, rejected := persisted[0], persisted[1]
	if accepted.requestIndex != 0 || accepted.configID != fixture.readyConfigID ||
		!accepted.taskID.Valid || !accepted.workflowVersionID.Valid || accepted.outcome != ReleaseOutcomeAccepted || accepted.errorCode.Valid {
		t.Fatalf("durable accepted batch item = %#v", accepted)
	}
	if rejected.requestIndex != 1 || rejected.configID != fixture.unboundConfigID ||
		rejected.taskID.Valid || rejected.workflowVersionID.Valid || rejected.outcome != ReleaseOutcomeRejected ||
		!rejected.errorCode.Valid || rejected.errorCode.String != ReleaseCodeWorkflowNotConfigured {
		t.Fatalf("durable rejected batch item = %#v", rejected)
	}
}

func assertReleaseTableCounts(t *testing.T, database *sql.DB, records, items, tasks, steps int) {
	t.Helper()
	checks := []struct {
		name  string
		query string
		want  int
	}{
		{name: "release_idempotency_records", query: "SELECT COUNT(*) FROM release_idempotency_records", want: records},
		{name: "release_idempotency_items", query: "SELECT COUNT(*) FROM release_idempotency_items", want: items},
		{name: "task_record", query: "SELECT COUNT(*) FROM task_record", want: tasks},
		{name: "task_step_records", query: "SELECT COUNT(*) FROM task_step_records", want: steps},
	}
	for _, check := range checks {
		var got int
		if err := database.QueryRow(check.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s row count = %d, want %d", check.name, got, check.want)
		}
	}
}

func assertReleaseError(t *testing.T, err error, code string, status int) {
	t.Helper()
	var releaseErr *ReleaseCommandError
	if !errors.As(err, &releaseErr) {
		t.Fatalf("error = %v, want ReleaseCommandError", err)
	}
	if releaseErr.Code != code || releaseErr.HTTPStatus != status {
		t.Fatalf("release error = code:%s status:%d, want code:%s status:%d",
			releaseErr.Code, releaseErr.HTTPStatus, code, status)
	}
}

func mustReleaseIdempotencyToken(t *testing.T, key string) IdempotencyToken {
	t.Helper()
	token, err := ParseIdempotencyKey([]string{key})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type releaseMySQLHarness struct {
	admin        *sql.DB
	database     *sql.DB
	databaseName string
	adminDSN     string
	runtimeDSN   string
}

func newReleaseMySQLHarness(t *testing.T) *releaseMySQLHarness {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(releaseMySQLIntegrationDSNEnv))
	if dsn == "" {
		t.Skipf("set %s to a MySQL 8.4 administrative DSN to run integration tests", releaseMySQLIntegrationDSNEnv)
	}
	configuration, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", releaseMySQLIntegrationDSNEnv, err)
	}
	configuration.ParseTime = true
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
		t.Fatalf("generate integration database suffix: %v", err)
	}
	// Avoid MySQL database grant wildcards ('_' and '%') entirely. The name is
	// generated locally and consists only of a fixed ASCII prefix plus hex.
	databaseName := "aresw05releaseit" + hex.EncodeToString(randomSuffix)
	if len(databaseName) > 64 || !isSafeReleaseSQLIdentifier(databaseName) {
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
	harness := &releaseMySQLHarness{
		admin: admin, databaseName: databaseName, adminDSN: target.FormatDSN(),
	}
	t.Cleanup(func() {
		if harness.database != nil {
			_ = harness.database.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := harness.admin.ExecContext(cleanupCtx,
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", harness.databaseName)); err != nil {
			t.Errorf("drop integration database %s: %v", harness.databaseName, err)
		}
		_ = harness.admin.Close()
	})
	return harness
}

func (h *releaseMySQLHarness) migrate(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := schemadb.MigrateUp(ctx, h.adminDSN, "", 45*time.Second, 10*time.Second)
	if err != nil {
		t.Fatalf("migrate release integration database: %s", schemadb.SafeMigrationErrorText(err))
	}
	if !status.Compatible() {
		t.Fatalf("release integration schema is incompatible after migration: %s", status.String())
	}
	database, err := sql.Open("mysql", h.adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	h.database = database
}

func (h *releaseMySQLHarness) seed(t *testing.T) releaseMySQLFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.database.ExecContext(ctx, `INSERT INTO env_configs (env, description_cn, enabled)
		VALUES ('integration', 'Idempotent release integration', 1)`); err != nil {
		t.Fatal(err)
	}
	readyConfigID := h.insertAppConfig(t, ctx, "w05-idempotent-ready")
	unboundConfigID := h.insertAppConfig(t, ctx, "w05-idempotent-unbound")
	actorOne := h.insertActor(t, ctx, "w05-release-actor-one", "W05 Release Actor One")
	actorTwo := h.insertActor(t, ctx, "w05-release-actor-two", "W05 Release Actor Two")

	engine := h.openAdminEngine(t)
	definitionService := workflow.NewService(workflow.NewXORMStore(engine), workflow.DefaultRegistry())
	view, err := definitionService.Save(ctx, readyConfigID, 0, actorOne.DisplayName, actorOne.UserID, workflow.WorkflowSpec{
		SchemaVersion: workflow.SchemaVersionV1,
		Name:          "W05 MySQL idempotent release",
		Steps: []workflow.StepSpec{
			{Key: "build", Name: "Build", Uses: workflow.NoopUses, With: json.RawMessage(`{"message":"build"}`)},
			{Key: "deploy", Name: "Deploy", Uses: workflow.NoopUses, With: json.RawMessage(`{"message":"deploy"}`)},
		},
	})
	if err != nil {
		_ = engine.Close()
		t.Fatalf("seed release workflow: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("close seed xorm engine: %v", err)
	}
	return releaseMySQLFixture{
		actorOne: actorOne, actorTwo: actorTwo,
		readyConfigID: readyConfigID, unboundConfigID: unboundConfigID, workflow: view,
	}
}

func (h *releaseMySQLHarness) insertAppConfig(t *testing.T, ctx context.Context, appName string) int {
	t.Helper()
	result, err := h.database.ExecContext(ctx, `INSERT INTO apps
		(app_name, app_name_cn, owner, owner_cn, dev_language, git_url)
		VALUES (?, ?, 'integration-test', 'Integration Test', 'golang', ?)`,
		appName, appName, "https://example.invalid/"+appName)
	if err != nil {
		t.Fatal(err)
	}
	appID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	result, err = h.database.ExecContext(ctx, `INSERT INTO app_configs (app_id, env, code_package_type)
		VALUES (?, 'integration', 'golang')`, appID)
	if err != nil {
		t.Fatal(err)
	}
	configID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return int(configID)
}

func (h *releaseMySQLHarness) insertActor(t *testing.T, ctx context.Context, username, displayName string) PublishActor {
	t.Helper()
	result, err := h.database.ExecContext(ctx, `INSERT INTO auth_users
		(username, display_name, auth_source) VALUES (?, ?, 'bootstrap')`, username, displayName)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return PublishActor{UserID: userID, DisplayName: displayName}
}

func (h *releaseMySQLHarness) createRuntimeAccount(t *testing.T) {
	t.Helper()
	randomSuffix := make([]byte, 8)
	if _, err := rand.Read(randomSuffix); err != nil {
		t.Fatalf("generate runtime account suffix: %v", err)
	}
	username := "aresw05rt" + hex.EncodeToString(randomSuffix)
	passwordBytes := make([]byte, 18)
	if _, err := rand.Read(passwordBytes); err != nil {
		t.Fatalf("generate runtime account password: %v", err)
	}
	password := "aresw05" + hex.EncodeToString(passwordBytes)
	if !isSafeReleaseSQLIdentifier(username) || !isSafeReleaseSQLIdentifier(password) {
		t.Fatal("generated unsafe runtime account credentials")
	}
	account := fmt.Sprintf("'%s'@'%%'", username)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := h.admin.ExecContext(ctx, "CREATE USER "+account+" IDENTIFIED BY '"+password+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := h.admin.ExecContext(cleanupCtx, "DROP USER IF EXISTS "+account); err != nil {
			t.Errorf("drop integration runtime account %s: %v", username, err)
		}
	})

	grants := []string{
		fmt.Sprintf("GRANT SELECT ON `%s`.* TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE ON `%s`.`apps` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE ON `%s`.`app_configs` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE, DELETE ON `%s`.`app_config_domains` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE ON `%s`.`env_configs` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE ON `%s`.`release_workflows` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT ON `%s`.`release_workflow_versions` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE ON `%s`.`app_config_workflows` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE ON `%s`.`task_record` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT, UPDATE ON `%s`.`task_step_records` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT ON `%s`.`release_idempotency_records` TO %s", h.databaseName, account),
		fmt.Sprintf("GRANT INSERT ON `%s`.`release_idempotency_items` TO %s", h.databaseName, account),
	}
	for _, statement := range grants {
		if _, err := h.admin.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	runtimeConfig, err := mysql.ParseDSN(h.adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig.User = username
	runtimeConfig.Passwd = password
	runtimeConfig.ParseTime = true
	h.runtimeDSN = runtimeConfig.FormatDSN()
}

// The production runtime account has INSERT but deliberately has no UPDATE on
// immutable workflow versions. A direct locking read therefore has to fail;
// the canonical release service must still work using a consistent plain read.
func (h *releaseMySQLHarness) assertImmutableWorkflowVersionLockDenied(t *testing.T, versionID int64) {
	t.Helper()
	database, err := sql.Open("mysql", h.runtimeDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var foundVersionID int64
	err = transaction.QueryRowContext(ctx,
		"SELECT version_id FROM release_workflow_versions WHERE version_id = ? FOR UPDATE", versionID).
		Scan(&foundVersionID)
	_ = transaction.Rollback()
	if err == nil {
		t.Fatalf("runtime account unexpectedly acquired a lock on immutable workflow version %d", foundVersionID)
	}
	var mysqlError *mysql.MySQLError
	if !errors.As(err, &mysqlError) || mysqlError.Number != 1142 {
		t.Fatalf("immutable workflow locking read error = %v, want MySQL command-denied error 1142", err)
	}
}

func (h *releaseMySQLHarness) openAdminEngine(t *testing.T) *xorm.Engine {
	t.Helper()
	return openReleaseMySQLEngine(t, h.adminDSN)
}

func (h *releaseMySQLHarness) openRuntimeEngine(t *testing.T) *xorm.Engine {
	t.Helper()
	if h.runtimeDSN == "" {
		t.Fatal("runtime account has not been initialized")
	}
	return openReleaseMySQLEngine(t, h.runtimeDSN)
}

func openReleaseMySQLEngine(t *testing.T, dsn string) *xorm.Engine {
	t.Helper()
	engine, err := xorm.NewEngine("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetMaxOpenConns(64)
	engine.SetMaxIdleConns(32)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := engine.PingContext(ctx); err != nil {
		_ = engine.Close()
		t.Fatal(err)
	}
	return engine
}

func isSafeReleaseSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}
