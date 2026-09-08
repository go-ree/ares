package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/environment"
	"github.com/go-ree/ares/internal/workflow"
	"github.com/go-sql-driver/mysql"
	"xorm.io/xorm"
)

const (
	maxBatchStepSnapshots         = 2000
	idempotencyReservationTimeout = 3 * time.Second
)

type IdempotentReleaseService struct {
	engine   *xorm.Engine
	registry *workflow.Registry
	now      func() time.Time
	commit   func(*xorm.Session) error
}

func NewIdempotentReleaseService(engine *xorm.Engine, registry *workflow.Registry) *IdempotentReleaseService {
	return &IdempotentReleaseService{
		engine: engine, registry: registry, now: time.Now,
		commit: func(session *xorm.Session) error { return session.Commit() },
	}
}

type legacyReleaseTargetRow struct {
	RequestIndex int `xorm:"request_index"`
	ConfigID     int `xorm:"config_id"`
}

type normalizedLegacyReleaseTarget struct {
	appName string
	env     string
}

// CreateLegacyRelease gives the deprecated name-scoped API the same durable
// replay contract as the canonical config-scoped API. A committed key is
// resolved against the config IDs frozen in its receipt, so deleting a target
// or making its active alias ambiguous cannot hide an already-created task.
func (s *IdempotentReleaseService) CreateLegacyRelease(ctx context.Context, actor PublishActor, token IdempotencyToken, request CreatePublishRequest) (ReleaseReceipt, bool, error) {
	commands, itemErrors, err := s.resolveKeyedLegacyCommands(
		ctx, actor, token, SemanticReleaseCreate, []CreatePublishRequest{request},
	)
	if err != nil {
		return ReleaseReceipt{}, false, err
	}
	if len(commands) != 1 || len(itemErrors) != 1 {
		return ReleaseReceipt{}, false, errors.New("legacy release resolver returned an invalid result")
	}
	if itemErrors[0] != nil {
		return ReleaseReceipt{}, false, itemErrors[0]
	}
	return s.Create(ctx, actor, token, commands[0])
}

// CreateLegacyBatchRelease is the keyed compatibility boundary for legacy
// batches. Keyless legacy partial batches intentionally remain in the
// controller because they create one independent canonical receipt per item.
func (s *IdempotentReleaseService) CreateLegacyBatchRelease(ctx context.Context, actor PublishActor, token IdempotencyToken, requests []CreatePublishRequest) (ReleaseReceipt, bool, error) {
	if len(requests) == 0 {
		return ReleaseReceipt{}, false, invalidReleaseRequest("批量发布至少需要 1 项")
	}
	if len(requests) > maxBatchReleaseItems {
		return ReleaseReceipt{}, false, releaseError(ReleaseCodeRequestTooLarge, 413, "批量发布不能超过 100 项")
	}
	commands, itemErrors, err := s.resolveKeyedLegacyCommands(
		ctx, actor, token, SemanticReleaseBatchCreate, requests,
	)
	if err != nil {
		return ReleaseReceipt{}, false, err
	}
	for _, itemErr := range itemErrors {
		var commandError *ReleaseCommandError
		if errors.As(itemErr, &commandError) {
			return ReleaseReceipt{}, false, itemErr
		}
	}
	for _, itemErr := range itemErrors {
		if itemErr != nil {
			return ReleaseReceipt{}, false, newInputError("批量发布目标无法唯一解析")
		}
	}
	return s.CreateBatch(ctx, actor, token, commands)
}

func (s *IdempotentReleaseService) resolveKeyedLegacyCommands(ctx context.Context, actor PublishActor, token IdempotencyToken, operation string, requests []CreatePublishRequest) ([]ReleaseCommand, []error, error) {
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	if actor.UserID <= 0 || actor.DisplayName == "" {
		return nil, nil, releaseError(ReleaseCodeInvalidRequest, 401, "认证主体不可用")
	}
	if err := s.ready(); err != nil {
		return nil, nil, err
	}

	commands, targets, itemErrors := normalizeLegacyReleaseRequests(requests)
	for _, itemErr := range itemErrors {
		if itemErr != nil {
			return commands, itemErrors, nil
		}
	}

	configIDs, found, err := s.lookupLegacyReceiptConfigIDs(ctx, actor.UserID, operation, token)
	if err != nil {
		return nil, nil, err
	}
	if found {
		return s.resolveHistoricalLegacyCommands(ctx, operation, commands, targets, configIDs)
	}

	activeCommands, activeErrors, activeErr := s.ResolveLegacyCommands(ctx, requests)
	// A competing request may have committed while the mutable alias lookup was
	// running. Recheck the durable scope afterwards before trusting either its
	// result or error; the winner's config IDs are the authoritative identity.
	configIDs, found, lookupErr := s.lookupLegacyReceiptConfigIDs(ctx, actor.UserID, operation, token)
	if lookupErr != nil {
		return nil, nil, lookupErr
	}
	if found {
		return s.resolveHistoricalLegacyCommands(ctx, operation, commands, targets, configIDs)
	}
	if activeErr != nil {
		return nil, nil, activeErr
	}
	return activeCommands, activeErrors, nil
}

func normalizeLegacyReleaseRequests(requests []CreatePublishRequest) ([]ReleaseCommand, []normalizedLegacyReleaseTarget, []error) {
	commands := make([]ReleaseCommand, len(requests))
	targets := make([]normalizedLegacyReleaseTarget, len(requests))
	itemErrors := make([]error, len(requests))
	for index, request := range requests {
		command, target, err := normalizeLegacyReleaseRequest(request)
		commands[index] = command
		targets[index] = target
		itemErrors[index] = err
	}
	return commands, targets, itemErrors
}

func normalizeLegacyReleaseRequest(request CreatePublishRequest) (ReleaseCommand, normalizedLegacyReleaseTarget, error) {
	appName := strings.TrimSpace(request.AppName)
	if appName == "" || appName != request.AppName || len(appName) > 255 {
		return ReleaseCommand{}, normalizedLegacyReleaseTarget{}, newInputError("app_name 无效")
	}
	envCode, err := environment.NormalizeCode(request.Env)
	if err != nil {
		return ReleaseCommand{}, normalizedLegacyReleaseTarget{}, newInputError("环境代码无效")
	}
	inputs := json.RawMessage(`{}`)
	if request.ExtraData != nil {
		inputs, err = json.Marshal(request.ExtraData)
		if err != nil {
			return ReleaseCommand{}, normalizedLegacyReleaseTarget{}, invalidReleaseRequest("extra_data 无效")
		}
	}
	normalized, err := normalizeReleaseCommand(ReleaseCommand{
		ConfigID: 1, Ref: request.Branch, Inputs: inputs,
	})
	if err != nil {
		return ReleaseCommand{}, normalizedLegacyReleaseTarget{}, err
	}
	return normalized, normalizedLegacyReleaseTarget{appName: appName, env: envCode}, nil
}

// ResolveLegacyCommand is the only app_name+env compatibility boundary. The
// resulting command enters the same canonical service as config-scoped calls.
func (s *IdempotentReleaseService) ResolveLegacyCommand(ctx context.Context, request CreatePublishRequest) (ReleaseCommand, error) {
	commands, itemErrors, err := s.ResolveLegacyCommands(ctx, []CreatePublishRequest{request})
	if err != nil {
		return ReleaseCommand{}, err
	}
	if len(commands) != 1 || len(itemErrors) != 1 {
		return ReleaseCommand{}, errors.New("legacy release resolver returned an invalid result")
	}
	if itemErrors[0] != nil {
		return ReleaseCommand{}, itemErrors[0]
	}
	return commands[0], nil
}

// ResolveLegacyCommands validates every legacy request before storage access,
// then resolves all unique app_name+env targets with one bounded query. Item
// errors are safe compatibility failures; the final error is infrastructure.
func (s *IdempotentReleaseService) ResolveLegacyCommands(ctx context.Context, requests []CreatePublishRequest) ([]ReleaseCommand, []error, error) {
	if len(requests) > maxBatchReleaseItems {
		return nil, nil, releaseError(ReleaseCodeRequestTooLarge, 413, "批量发布不能超过 100 项")
	}
	commands := make([]ReleaseCommand, len(requests))
	itemErrors := make([]error, len(requests))
	queryParts := make([]string, 0, len(requests))
	queryArguments := make([]any, 0, len(requests)*3)
	validCount := 0

	for index, request := range requests {
		appName := strings.TrimSpace(request.AppName)
		if appName == "" || appName != request.AppName || len(appName) > 255 {
			itemErrors[index] = newInputError("app_name 无效")
			continue
		}
		envCode, err := environment.NormalizeCode(request.Env)
		if err != nil {
			itemErrors[index] = newInputError("环境代码无效")
			continue
		}
		inputs := json.RawMessage(`{}`)
		if request.ExtraData != nil {
			inputs, err = json.Marshal(request.ExtraData)
			if err != nil {
				itemErrors[index] = invalidReleaseRequest("extra_data 无效")
				continue
			}
		}
		// A positive placeholder lets the canonical validator reject malformed
		// ref/inputs before a database lookup. The resolved config ID replaces it
		// below and is the only target identity used by the release service.
		normalized, err := normalizeReleaseCommand(ReleaseCommand{
			ConfigID: 1, Ref: request.Branch, Inputs: inputs,
		})
		if err != nil {
			itemErrors[index] = err
			continue
		}
		commands[index] = normalized
		queryParts = append(queryParts, "SELECT ? AS request_index, ? AS app_name, ? AS env")
		queryArguments = append(queryArguments, index, appName, envCode)
		validCount++
	}

	if validCount == 0 {
		return commands, itemErrors, nil
	}
	if err := s.ready(); err != nil {
		return nil, nil, err
	}
	rows := make([]legacyReleaseTargetRow, 0, validCount)
	// Match each request inside MySQL rather than comparing returned names in
	// Go. This preserves the installed app_name collation (including its case
	// semantics), avoids an N+1 lookup, and does not create an IN cross product.
	query := `SELECT requested.request_index, config.config_id
		FROM (` + strings.Join(queryParts, " UNION ALL ") + `) requested
		INNER JOIN apps app ON app.app_name = requested.app_name AND app.deleted_at IS NULL
		INNER JOIN app_configs config ON config.app_id = app.app_id
			AND config.env = requested.env AND config.deleted_at IS NULL
		ORDER BY requested.request_index, config.config_id`
	err := s.engine.Context(ctx).SQL(query, queryArguments...).Find(&rows)
	if err != nil {
		return nil, nil, err
	}
	resolved := make(map[int][]int, len(rows))
	for _, row := range rows {
		if row.RequestIndex < 0 || row.RequestIndex >= len(requests) || itemErrors[row.RequestIndex] != nil {
			return nil, nil, errors.New("legacy release resolver returned an invalid request index")
		}
		resolved[row.RequestIndex] = append(resolved[row.RequestIndex], row.ConfigID)
	}
	for index := range requests {
		if itemErrors[index] != nil {
			continue
		}
		configIDs := resolved[index]
		if len(configIDs) != 1 || configIDs[0] <= 0 {
			itemErrors[index] = newInputError("未找到唯一的应用环境配置")
			continue
		}
		commands[index].ConfigID = configIDs[0]
	}
	return commands, itemErrors, nil
}

// lookupLegacyReceiptConfigIDs returns only target identities from a committed
// receipt in the caller's actor/operation/key scope. It deliberately does not
// expose request digests or accept an unscoped key lookup.
func (s *IdempotentReleaseService) lookupLegacyReceiptConfigIDs(ctx context.Context, actorUserID int64, operation string, token IdempotencyToken) ([]int, bool, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, idempotencyReservationTimeout)
	defer cancel()

	var record entity.ReleaseIdempotencyRecord
	has, err := s.engine.Context(lookupCtx).
		Where("actor_user_id = ? AND semantic_operation = ? AND key_digest = ?", actorUserID, operation, token.bytes()).
		Get(&record)
	if err != nil {
		return nil, false, releaseOutcomeUnknown()
	}
	if !has {
		return nil, false, nil
	}
	if record.ActorUserID != actorUserID || record.SemanticOperation != operation ||
		record.ItemCount < 1 || record.ItemCount > maxBatchReleaseItems ||
		(operation == SemanticReleaseCreate && record.ItemCount != 1) ||
		(operation != SemanticReleaseCreate && operation != SemanticReleaseBatchCreate) {
		return nil, false, errors.New("幂等发布记录语义损坏")
	}

	var rows []entity.ReleaseIdempotencyItem
	if err := s.engine.Context(lookupCtx).
		Where("record_id = ?", record.IdempotencyID).Asc("request_index").Find(&rows); err != nil {
		return nil, false, releaseOutcomeUnknown()
	}
	if len(rows) != int(record.ItemCount) {
		return nil, false, errors.New("幂等发布结果不完整")
	}
	configIDs := make([]int, len(rows))
	for index, row := range rows {
		if int(row.RequestIndex) != index || row.ConfigID <= 0 {
			return nil, false, errors.New("幂等发布结果索引损坏")
		}
		configIDs[index] = row.ConfigID
	}
	return configIDs, true, nil
}

func (s *IdempotentReleaseService) resolveHistoricalLegacyCommands(ctx context.Context, operation string, commands []ReleaseCommand, targets []normalizedLegacyReleaseTarget, configIDs []int) ([]ReleaseCommand, []error, error) {
	if len(configIDs) != len(commands) || len(targets) != len(commands) {
		return nil, nil, releaseError(ReleaseCodeIdempotencyKeyConflict, 409,
			"Idempotency-Key 已用于不同的发布请求")
	}
	if operation == SemanticReleaseCreate && len(commands) != 1 {
		return nil, nil, errors.New("幂等发布记录语义损坏")
	}

	queryParts := make([]string, len(commands))
	queryArguments := make([]any, 0, len(commands)*4)
	for index := range commands {
		queryParts[index] = "SELECT ? AS request_index, ? AS config_id, ? AS app_name, ? AS env"
		queryArguments = append(queryArguments, index, configIDs[index], targets[index].appName, targets[index].env)
	}
	// Deliberately omit deleted_at predicates: a durable receipt names historical
	// immutable IDs. The app_name comparison remains inside MySQL so it uses the
	// same installed collation semantics as the original active alias resolver.
	query := `SELECT requested.request_index, config.config_id
		FROM (` + strings.Join(queryParts, " UNION ALL ") + `) requested
		INNER JOIN app_configs config ON config.config_id = requested.config_id
			AND config.env = requested.env
		INNER JOIN apps app ON app.app_id = config.app_id
			AND app.app_name = requested.app_name
		ORDER BY requested.request_index`
	var rows []legacyReleaseTargetRow
	lookupCtx, cancel := context.WithTimeout(ctx, idempotencyReservationTimeout)
	defer cancel()
	if err := s.engine.Context(lookupCtx).SQL(query, queryArguments...).Find(&rows); err != nil {
		// The scoped durable record was already observed. Never turn a transient
		// historical-alias read failure into a generic 500 (or fall back to the
		// mutable active resolver): the caller can only recover safely by retrying
		// the exact key and body.
		return nil, nil, releaseOutcomeUnknown()
	}
	if len(rows) != len(commands) {
		return nil, nil, releaseError(ReleaseCodeIdempotencyKeyConflict, 409,
			"Idempotency-Key 已用于不同的发布请求")
	}
	for index, row := range rows {
		if row.RequestIndex != index || row.ConfigID != configIDs[index] {
			return nil, nil, releaseError(ReleaseCodeIdempotencyKeyConflict, 409,
				"Idempotency-Key 已用于不同的发布请求")
		}
		commands[index].ConfigID = row.ConfigID
	}
	return commands, make([]error, len(commands)), nil
}

type releaseTargetSnapshot struct {
	app      entity.Apps
	config   entity.AppConfigs
	env      entity.EnvConfigs
	workflow workflow.WorkflowView
	domains  []entity.AppConfigDomain
}

func (s *IdempotentReleaseService) Preflight(ctx context.Context, command ReleaseCommand) (ReleasePreflight, error) {
	normalized, err := normalizeReleaseCommand(command)
	if err != nil {
		return ReleasePreflight{}, err
	}
	if err := s.ready(); err != nil {
		return ReleasePreflight{}, err
	}
	session := s.engine.NewSession()
	defer session.Close()
	_, result, err := s.inspectTarget(ctx, session, normalized, false)
	if err != nil {
		return ReleasePreflight{}, err
	}
	if result.ErrorCode == ReleaseCodeTargetNotFound {
		return ReleasePreflight{}, releaseError(result.ErrorCode, 404, "发布目标不存在")
	}
	return result, nil
}

func (s *IdempotentReleaseService) PreflightBatch(ctx context.Context, commands []ReleaseCommand) (BatchReleasePreflight, error) {
	normalized, err := normalizeReleaseCommands(commands)
	if err != nil {
		return BatchReleasePreflight{}, err
	}
	if _, err := releaseRequestDigest(SemanticReleaseBatchCreate, normalized); err != nil {
		return BatchReleasePreflight{}, err
	}
	if err := s.ready(); err != nil {
		return BatchReleasePreflight{}, err
	}
	result := BatchReleasePreflight{TotalCount: len(normalized), Items: make([]ReleasePreflight, len(normalized))}
	session := s.engine.NewSession()
	defer session.Close()
	snapshots, preflights, err := s.inspectTargets(ctx, session, normalized, false)
	if err != nil {
		return BatchReleasePreflight{}, err
	}
	totalSteps := 0
	for _, command := range normalized {
		if preflights[command.ConfigID].Ready {
			totalSteps += len(snapshots[command.ConfigID].workflow.Spec.Steps)
			if totalSteps > maxBatchStepSnapshots {
				return BatchReleasePreflight{}, releaseError(ReleaseCodeRequestTooLarge, 413,
					"批量发布步骤快照总数不能超过 2000")
			}
		}
	}
	for index, command := range normalized {
		item := preflights[command.ConfigID]
		if item.Ready {
			item, err = s.applyTargetAvailability(ctx, snapshots[command.ConfigID], item)
			if err != nil {
				return BatchReleasePreflight{}, err
			}
		}
		item.RequestIndex = index
		result.Items[index] = item
		if item.Ready {
			result.ReadyCount++
		} else {
			result.FailureCount++
		}
	}
	return result, nil
}

func (s *IdempotentReleaseService) Create(ctx context.Context, actor PublishActor, token IdempotencyToken, command ReleaseCommand) (ReleaseReceipt, bool, error) {
	normalized, err := normalizeReleaseCommand(command)
	if err != nil {
		return ReleaseReceipt{}, false, err
	}
	return s.create(ctx, actor, token, SemanticReleaseCreate, []ReleaseCommand{normalized}, false)
}

func (s *IdempotentReleaseService) CreateBatch(ctx context.Context, actor PublishActor, token IdempotencyToken, commands []ReleaseCommand) (ReleaseReceipt, bool, error) {
	normalized, err := normalizeReleaseCommands(commands)
	if err != nil {
		return ReleaseReceipt{}, false, err
	}
	return s.create(ctx, actor, token, SemanticReleaseBatchCreate, normalized, true)
}

func (s *IdempotentReleaseService) create(ctx context.Context, actor PublishActor, token IdempotencyToken, operation string, commands []ReleaseCommand, allowItemFailures bool) (ReleaseReceipt, bool, error) {
	if err := s.ready(); err != nil {
		return ReleaseReceipt{}, false, err
	}
	actor.DisplayName = strings.TrimSpace(actor.DisplayName)
	if actor.UserID <= 0 || actor.DisplayName == "" {
		return ReleaseReceipt{}, false, releaseError(ReleaseCodeInvalidRequest, 401, "认证主体不可用")
	}
	digest, err := releaseRequestDigest(operation, commands)
	if err != nil {
		return ReleaseReceipt{}, false, fmt.Errorf("计算发布请求摘要: %w", err)
	}

	for attempt := 0; attempt < 3; attempt++ {
		receipt, replayed, createErr := s.createOnce(ctx, actor, token, digest, operation, commands, allowItemFailures)
		if createErr == nil {
			return receipt, replayed, nil
		}
		if !isRetryableTransactionError(createErr) || attempt == 2 {
			return ReleaseReceipt{}, false, createErr
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ReleaseReceipt{}, false, releaseError(ReleaseCodeIdempotencyRequestInProgress, 409, "相同发布请求仍在处理中")
		case <-timer.C:
		}
	}
	return ReleaseReceipt{}, false, fmt.Errorf("创建发布任务失败")
}

func (s *IdempotentReleaseService) createOnce(ctx context.Context, actor PublishActor, token IdempotencyToken, digest [sha256.Size]byte, operation string, commands []ReleaseCommand, allowItemFailures bool) (receipt ReleaseReceipt, replayed bool, err error) {
	session := s.engine.NewSession()
	defer session.Close()
	session.Context(ctx)
	if err = session.Begin(); err != nil {
		return ReleaseReceipt{}, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = session.Rollback()
		}
	}()

	record := entity.ReleaseIdempotencyRecord{
		ActorUserID: actor.UserID, SemanticOperation: operation,
		KeyDigest: token.bytes(), RequestDigest: append([]byte(nil), digest[:]...), ItemCount: uint16(len(commands)),
	}
	reservationContext, cancelReservation := context.WithTimeout(ctx, idempotencyReservationTimeout)
	_, insertErr := session.Context(reservationContext).Insert(&record)
	reservationContextErr := reservationContext.Err()
	cancelReservation()
	if insertErr != nil {
		_ = session.Rollback()
		if isIdempotencyUniqueConflict(insertErr) {
			replayedReceipt, lookupErr := s.loadReceipt(ctx, actor.UserID, operation, token.bytes(), digest[:])
			if lookupErr != nil {
				return ReleaseReceipt{}, false, lookupErr
			}
			return replayedReceipt, true, nil
		}
		if ctx.Err() != nil {
			return ReleaseReceipt{}, false, ctx.Err()
		}
		if errors.Is(insertErr, context.DeadlineExceeded) ||
			errors.Is(reservationContextErr, context.DeadlineExceeded) || isLockWaitTimeout(insertErr) {
			return ReleaseReceipt{}, false, releaseError(ReleaseCodeIdempotencyRequestInProgress, 409,
				"相同发布请求仍在处理中")
		}
		if isRetryableTransactionError(insertErr) {
			return ReleaseReceipt{}, false, insertErr
		}
		// The duplicate-key error may contain a printable representation of the
		// binary key digest. Never forward unclassified reservation errors into
		// application logs.
		return ReleaseReceipt{}, false, errors.New("写入发布幂等记录失败")
	}
	if record.IdempotencyID <= 0 {
		return ReleaseReceipt{}, false, fmt.Errorf("数据库未返回 idempotency_id")
	}

	// Lock every mutable table in one global table/row order. Sorting only the
	// AppConfig rows is insufficient: two configurations may reference the same
	// app, environment, or workflow in a different graph shape.
	ordered := append([]ReleaseCommand(nil), commands...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ConfigID < ordered[j].ConfigID })
	snapshots, preflights, inspectErr := s.inspectTargets(ctx, session, ordered, true)
	if inspectErr != nil {
		return ReleaseReceipt{}, false, inspectErr
	}
	totalSteps := 0
	for _, command := range ordered {
		snapshot, preflight := snapshots[command.ConfigID], preflights[command.ConfigID]
		if !preflight.Ready {
			if !allowItemFailures {
				return ReleaseReceipt{}, false, releaseErrorForPreflight(preflight)
			}
			continue
		}
		totalSteps += len(snapshot.workflow.Spec.Steps)
		if totalSteps > maxBatchStepSnapshots {
			return ReleaseReceipt{}, false, releaseError(ReleaseCodeRequestTooLarge, 413, "批量发布步骤快照总数不能超过 2000")
		}
	}
	// Availability checks are deliberately deferred until the entire immutable
	// step budget is known. Implementations may only read a committed in-process
	// integration snapshot; they must never perform network I/O here.
	for _, command := range ordered {
		preflight := preflights[command.ConfigID]
		if !preflight.Ready {
			continue
		}
		preflight, inspectErr = s.applyTargetAvailability(ctx, snapshots[command.ConfigID], preflight)
		if inspectErr != nil {
			return ReleaseReceipt{}, false, inspectErr
		}
		preflights[command.ConfigID] = preflight
		if !preflight.Ready && !allowItemFailures {
			return ReleaseReceipt{}, false, releaseErrorForPreflight(preflight)
		}
	}

	receipt = ReleaseReceipt{RecordID: record.IdempotencyID, TotalCount: len(commands), Items: make([]ReleaseReceiptItem, len(commands))}
	for index, command := range commands {
		preflight := preflights[command.ConfigID]
		item := ReleaseReceiptItem{RequestIndex: index, ConfigID: command.ConfigID}
		row := entity.ReleaseIdempotencyItem{
			RecordID: record.IdempotencyID, RequestIndex: uint16(index), ConfigID: command.ConfigID,
		}
		if !preflight.Ready {
			item.ErrorCode = preflight.ErrorCode
			row.Outcome = ReleaseOutcomeRejected
			errorCode := preflight.ErrorCode
			row.ErrorCode = &errorCode
			receipt.FailureCount++
		} else {
			snapshot := snapshots[command.ConfigID]
			task, composeErr := composeCanonicalTask(command, snapshot, actor, s.now())
			if composeErr != nil {
				return ReleaseReceipt{}, false, composeErr
			}
			if insertErr := workflow.InsertTaskWithSnapshotInSession(ctx, session, task, snapshot.workflow); insertErr != nil {
				return ReleaseReceipt{}, false, insertErr
			}
			taskID, workflowVersionID := task.TaskId, snapshot.workflow.WorkflowVersionID
			item.Success = true
			item.TaskID = &taskID
			item.WorkflowVersionID = &workflowVersionID
			row.TaskID = &taskID
			row.WorkflowVersionID = &workflowVersionID
			row.Outcome = ReleaseOutcomeAccepted
			receipt.SuccessCount++
		}
		if _, insertErr := session.Context(ctx).Insert(&row); insertErr != nil {
			return ReleaseReceipt{}, false, insertErr
		}
		receipt.Items[index] = item
	}

	if err = s.commit(session); err != nil {
		// COMMIT may have reached MySQL even when the client did not receive the
		// acknowledgement. Resolve by the durable key before reporting unknown.
		resolved, lookupErr := s.loadReceipt(context.WithoutCancel(ctx), actor.UserID, operation, token.bytes(), digest[:])
		if lookupErr == nil {
			committed = true
			return resolved, true, nil
		}
		return ReleaseReceipt{}, false, releaseError(ReleaseCodeOutcomeUnknown, 503,
			"发布结果暂时无法确认，请仅使用原 Idempotency-Key 重试")
	}
	committed = true
	return receipt, false, nil
}

func (s *IdempotentReleaseService) ready() error {
	if s == nil || s.engine == nil || s.registry == nil || s.commit == nil {
		return fmt.Errorf("发布服务未初始化")
	}
	return nil
}

func (s *IdempotentReleaseService) inspectTarget(ctx context.Context, session *xorm.Session, command ReleaseCommand, forUpdate bool) (releaseTargetSnapshot, ReleasePreflight, error) {
	snapshots, results, err := s.inspectTargets(ctx, session, []ReleaseCommand{command}, forUpdate)
	if err != nil {
		return releaseTargetSnapshot{}, ReleasePreflight{}, err
	}
	result := results[command.ConfigID]
	if !result.Ready {
		return releaseTargetSnapshot{}, result, nil
	}
	result, err = s.applyTargetAvailability(ctx, snapshots[command.ConfigID], result)
	return snapshots[command.ConfigID], result, err
}

// inspectTargets reads target metadata in a bounded number of queries. When
// forUpdate is true, each mutable table is locked once in this exact order:
// app_configs, apps, env_configs, app_config_workflows, release_workflows,
// app_config_domains. Immutable workflow versions are plain reads between the
// workflow and domain phases.
func (s *IdempotentReleaseService) inspectTargets(ctx context.Context, session *xorm.Session, commands []ReleaseCommand, forUpdate bool) (map[int]releaseTargetSnapshot, map[int]ReleasePreflight, error) {
	snapshots := make(map[int]releaseTargetSnapshot, len(commands))
	results := make(map[int]ReleasePreflight, len(commands))
	configIDs := make([]int, 0, len(commands))
	for _, command := range commands {
		configIDs = append(configIDs, command.ConfigID)
		results[command.ConfigID] = ReleasePreflight{ConfigID: command.ConfigID, Steps: make([]ReleaseStepPreflight, 0)}
	}
	if len(configIDs) == 0 {
		return snapshots, results, nil
	}

	var configs []entity.AppConfigs
	query := session.Context(ctx).Unscoped().In("config_id", configIDs).Asc("config_id")
	if forUpdate {
		query = query.ForUpdate()
	}
	if err := query.Find(&configs); err != nil {
		return nil, nil, err
	}
	configsByID := make(map[int]entity.AppConfigs, len(configs))
	appIDSet := make(map[int]struct{}, len(configs))
	envSet := make(map[string]struct{}, len(configs))
	activeConfigIDs := make([]int, 0, len(configs))
	for _, config := range configs {
		configsByID[config.ConfigID] = config
		if config.DeletedTime == nil {
			appIDSet[config.AppID] = struct{}{}
			envSet[config.Env] = struct{}{}
			activeConfigIDs = append(activeConfigIDs, config.ConfigID)
		}
	}

	appIDs := sortedIntKeys(appIDSet)
	var apps []entity.Apps
	if len(appIDs) > 0 {
		query = session.Context(ctx).Unscoped().In("app_id", appIDs).Asc("app_id")
		if forUpdate {
			query = query.ForUpdate()
		}
		if err := query.Find(&apps); err != nil {
			return nil, nil, err
		}
	}
	appsByID := make(map[int]entity.Apps, len(apps))
	for _, app := range apps {
		appsByID[app.AppId] = app
	}

	envCodes := sortedStringKeys(envSet)
	var envs []entity.EnvConfigs
	if len(envCodes) > 0 {
		query = session.Context(ctx).Unscoped().In("env", envCodes).Asc("env")
		if forUpdate {
			query = query.ForUpdate()
		}
		if err := query.Find(&envs); err != nil {
			return nil, nil, err
		}
	}
	envsByCode := make(map[string]entity.EnvConfigs, len(envs))
	for _, env := range envs {
		envsByCode[env.Env] = env
	}

	sort.Ints(activeConfigIDs)
	var bindings []entity.AppConfigWorkflow
	if len(activeConfigIDs) > 0 {
		query = session.Context(ctx).In("app_config_id", activeConfigIDs).Asc("app_config_id")
		if forUpdate {
			query = query.ForUpdate()
		}
		if err := query.Find(&bindings); err != nil {
			return nil, nil, err
		}
	}
	bindingsByConfigID := make(map[int]entity.AppConfigWorkflow, len(bindings))
	workflowIDSet := make(map[int64]struct{}, len(bindings))
	versionIDSet := make(map[int64]struct{}, len(bindings))
	for _, binding := range bindings {
		bindingsByConfigID[binding.AppConfigID] = binding
		workflowIDSet[binding.WorkflowID] = struct{}{}
		versionIDSet[binding.VersionID] = struct{}{}
	}

	workflowIDs := sortedInt64Keys(workflowIDSet)
	var workflowRows []entity.ReleaseWorkflow
	if len(workflowIDs) > 0 {
		query = session.Context(ctx).Unscoped().In("workflow_id", workflowIDs).Asc("workflow_id")
		if forUpdate {
			query = query.ForUpdate()
		}
		if err := query.Find(&workflowRows); err != nil {
			return nil, nil, err
		}
	}
	workflowsByID := make(map[int64]entity.ReleaseWorkflow, len(workflowRows))
	for _, row := range workflowRows {
		workflowsByID[row.WorkflowID] = row
	}

	// Workflow versions are immutable append-only rows. A locking read would
	// require UPDATE privilege and add no consistency guarantee.
	versionIDs := sortedInt64Keys(versionIDSet)
	var versions []entity.ReleaseWorkflowVersion
	if len(versionIDs) > 0 {
		if err := session.Context(ctx).In("version_id", versionIDs).Asc("version_id").Find(&versions); err != nil {
			return nil, nil, err
		}
	}
	versionsByID := make(map[int64]entity.ReleaseWorkflowVersion, len(versions))
	for _, version := range versions {
		versionsByID[version.VersionID] = version
	}

	var domainRows []entity.AppConfigDomain
	if len(activeConfigIDs) > 0 {
		query = session.Context(ctx).Unscoped().In("config_id", activeConfigIDs).Asc("config_id", "id")
		if forUpdate {
			query = query.ForUpdate()
		}
		if err := query.Find(&domainRows); err != nil {
			return nil, nil, err
		}
	}
	domainsByConfigID := make(map[int][]entity.AppConfigDomain, len(activeConfigIDs))
	for _, domain := range domainRows {
		if domain.DeletedTime == nil {
			domainsByConfigID[domain.ConfigID] = append(domainsByConfigID[domain.ConfigID], domain)
		}
	}

	for _, command := range commands {
		result := results[command.ConfigID]
		config, ok := configsByID[command.ConfigID]
		if !ok || config.DeletedTime != nil {
			result.ErrorCode = ReleaseCodeTargetNotFound
			results[command.ConfigID] = result
			continue
		}
		result.AppID, result.Env = config.AppID, config.Env
		app, ok := appsByID[config.AppID]
		if !ok || app.DeletedTime != nil {
			result.ErrorCode = ReleaseCodeTargetNotFound
			results[command.ConfigID] = result
			continue
		}
		result.AppName, result.AppNameCN = app.AppName, app.AppNameCn
		env, ok := envsByCode[config.Env]
		if !ok || env.DeletedTime != nil {
			result.ErrorCode = ReleaseCodeTargetNotFound
			results[command.ConfigID] = result
			continue
		}
		if !env.Enabled {
			result.ErrorCode = ReleaseCodeEnvironmentDisabled
			results[command.ConfigID] = result
			continue
		}
		binding, ok := bindingsByConfigID[config.ConfigID]
		if !ok {
			result.ErrorCode = ReleaseCodeWorkflowNotConfigured
			results[command.ConfigID] = result
			continue
		}
		workflowRow, ok := workflowsByID[binding.WorkflowID]
		if !ok || workflowRow.DeletedTime != nil {
			result.ErrorCode = ReleaseCodeWorkflowInvalid
			results[command.ConfigID] = result
			continue
		}
		version, ok := versionsByID[binding.VersionID]
		if !ok || version.WorkflowID != binding.WorkflowID {
			result.ErrorCode = ReleaseCodeWorkflowInvalid
			results[command.ConfigID] = result
			continue
		}
		result.WorkflowID = binding.WorkflowID
		result.WorkflowVersionID = binding.VersionID
		result.WorkflowVersion = version.Version
		result.WorkflowRevision = binding.Revision
		if command.ExpectedWorkflowVersionID != nil && *command.ExpectedWorkflowVersionID != binding.VersionID {
			result.ErrorCode = ReleaseCodeWorkflowVersionChanged
			results[command.ConfigID] = result
			continue
		}
		spec, decodeErr := workflow.DecodeStoredWorkflowVersion(version)
		if decodeErr != nil {
			result.ErrorCode = ReleaseCodeWorkflowInvalid
			results[command.ConfigID] = result
			continue
		}
		workflowView := workflow.WorkflowView{
			ConfigID: config.ConfigID, WorkflowID: binding.WorkflowID,
			WorkflowVersionID: binding.VersionID, Version: version.Version,
			Revision: binding.Revision, Spec: spec,
		}
		result.Ready = true
		results[command.ConfigID] = result
		snapshots[command.ConfigID] = releaseTargetSnapshot{
			app: app, config: config, env: env, workflow: workflowView,
			domains: append([]entity.AppConfigDomain(nil), domainsByConfigID[config.ConfigID]...),
		}
	}
	return snapshots, results, nil
}

func (s *IdempotentReleaseService) applyTargetAvailability(ctx context.Context, snapshot releaseTargetSnapshot, result ReleasePreflight) (ReleasePreflight, error) {
	result.Ready = false
	result.Steps = make([]ReleaseStepPreflight, 0, len(snapshot.workflow.Spec.Steps))
	allAvailable := true
	overallErrorCode := ""
	for _, step := range snapshot.workflow.Spec.Steps {
		stepResult := ReleaseStepPreflight{Key: step.Key, Name: step.Name, Uses: step.Uses, Available: true}
		executor, found := s.registry.Get(step.Uses)
		if !found {
			stepResult.Available = false
			stepResult.ErrorCode = ReleaseCodeExecutorUnavailable
			if overallErrorCode == "" {
				overallErrorCode = ReleaseCodeExecutorUnavailable
			}
			allAvailable = false
		} else {
			stepResult.Capabilities = executor.Descriptor().Capabilities
			// Stored versions are immutable, but an upgraded executor may tighten
			// its configuration contract. Revalidate the snapshot before claiming
			// it is publishable; the private validator error is never exposed.
			if validateErr := executor.Validate(step.With); validateErr != nil {
				stepResult.Available = false
				stepResult.ErrorCode = ReleaseCodeWorkflowInvalid
				overallErrorCode = ReleaseCodeWorkflowInvalid
				allAvailable = false
			} else if checker, ok := executor.(workflow.AvailabilityChecker); ok {
				if availableErr := checker.Available(ctx); availableErr != nil {
					if ctx.Err() != nil || errors.Is(availableErr, context.Canceled) ||
						errors.Is(availableErr, context.DeadlineExceeded) {
						return result, availableErr
					}
					stepResult.Available = false
					stepResult.ErrorCode = ReleaseCodeExecutorUnavailable
					if overallErrorCode == "" {
						overallErrorCode = ReleaseCodeExecutorUnavailable
					}
					allAvailable = false
				}
			}
		}
		result.Steps = append(result.Steps, stepResult)
	}
	if !allAvailable {
		result.ErrorCode = overallErrorCode
		return result, nil
	}
	result.ErrorCode = ""
	result.Ready = true
	return result, nil
}

func sortedIntKeys(values map[int]struct{}) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Ints(result)
	return result
}

func sortedInt64Keys(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sortedStringKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func composeCanonicalTask(command ReleaseCommand, snapshot releaseTargetSnapshot, actor PublishActor, now time.Time) (*entity.TaskRecord, error) {
	decoder := json.NewDecoder(bytes.NewReader(command.Inputs))
	decoder.UseNumber()
	extra := make(map[string]any)
	if err := decoder.Decode(&extra); err != nil {
		return nil, invalidReleaseRequest("inputs 必须是 JSON 对象")
	}
	request := &PublishRequest{
		AppName: snapshot.app.AppName, Branch: command.Ref, Env: snapshot.env.Env,
		Publisher: actor.DisplayName, PublisherUserID: actor.UserID,
		AppId: snapshot.app.AppId, CodePackageType: snapshot.config.CodePackageType, ExtraData: extra,
	}
	_, task, err := composePublishData(request, &snapshot.app, &snapshot.config, &snapshot.env, snapshot.domains, now)
	return task, err
}

func releaseErrorForPreflight(preflight ReleasePreflight) error {
	switch preflight.ErrorCode {
	case ReleaseCodeTargetNotFound:
		return releaseError(preflight.ErrorCode, 404, "发布目标不存在")
	case ReleaseCodeEnvironmentDisabled:
		return releaseError(preflight.ErrorCode, 409, "发布环境已停用")
	case ReleaseCodeWorkflowVersionChanged:
		return releaseError(preflight.ErrorCode, 409, "发布流程已变更，请重新预检")
	case ReleaseCodeWorkflowNotConfigured:
		return releaseError(preflight.ErrorCode, 422, "发布目标尚未配置工作流")
	case ReleaseCodeExecutorUnavailable:
		return releaseError(preflight.ErrorCode, 422, "发布流程所需执行器不可用")
	default:
		return releaseError(ReleaseCodeWorkflowInvalid, 422, "发布流程无效")
	}
}

func (s *IdempotentReleaseService) loadReceipt(ctx context.Context, actorUserID int64, operation string, keyDigest, requestDigest []byte) (ReleaseReceipt, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var record entity.ReleaseIdempotencyRecord
	has, err := s.engine.Context(lookupCtx).
		Where("actor_user_id = ? AND semantic_operation = ? AND key_digest = ?", actorUserID, operation, keyDigest).
		Get(&record)
	if err != nil {
		if lookupCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return ReleaseReceipt{}, releaseError(ReleaseCodeIdempotencyRequestInProgress, 409,
				"相同发布请求仍在处理中")
		}
		return ReleaseReceipt{}, releaseOutcomeUnknown()
	}
	if !has {
		if lookupCtx.Err() != nil {
			return ReleaseReceipt{}, releaseError(ReleaseCodeIdempotencyRequestInProgress, 409, "相同发布请求仍在处理中")
		}
		return ReleaseReceipt{}, releaseError(ReleaseCodeOutcomeUnknown, 503,
			"发布结果暂时无法确认，请仅使用原 Idempotency-Key 重试")
	}
	if record.ActorUserID != actorUserID || record.SemanticOperation != operation ||
		record.ItemCount < 1 || record.ItemCount > maxBatchReleaseItems ||
		(operation == SemanticReleaseCreate && record.ItemCount != 1) ||
		(operation != SemanticReleaseCreate && operation != SemanticReleaseBatchCreate) {
		return ReleaseReceipt{}, fmt.Errorf("幂等发布记录语义损坏")
	}
	if len(record.RequestDigest) != sha256.Size || len(requestDigest) != sha256.Size ||
		subtle.ConstantTimeCompare(record.RequestDigest, requestDigest) != 1 {
		return ReleaseReceipt{}, releaseError(ReleaseCodeIdempotencyKeyConflict, 409,
			"Idempotency-Key 已用于不同的发布请求")
	}
	var rows []entity.ReleaseIdempotencyItem
	if err := s.engine.Context(lookupCtx).Where("record_id = ?", record.IdempotencyID).Asc("request_index").Find(&rows); err != nil {
		if lookupCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
			return ReleaseReceipt{}, releaseError(ReleaseCodeOutcomeUnknown, 503,
				"发布结果暂时无法确认，请仅使用原 Idempotency-Key 重试")
		}
		return ReleaseReceipt{}, releaseOutcomeUnknown()
	}
	if record.ItemCount == 0 || len(rows) != int(record.ItemCount) {
		return ReleaseReceipt{}, fmt.Errorf("幂等发布结果不完整")
	}
	acceptedTaskIDs := make([]int, 0, len(rows))
	for index, row := range rows {
		if int(row.RequestIndex) != index || row.ConfigID <= 0 {
			return ReleaseReceipt{}, fmt.Errorf("幂等发布结果索引损坏")
		}
		if row.Outcome == ReleaseOutcomeAccepted && row.TaskID != nil {
			acceptedTaskIDs = append(acceptedTaskIDs, *row.TaskID)
		}
	}
	tasksByID := make(map[int]entity.TaskRecord, len(acceptedTaskIDs))
	if len(acceptedTaskIDs) > 0 {
		var tasks []entity.TaskRecord
		if err := s.engine.Context(lookupCtx).Unscoped().In("task_id", acceptedTaskIDs).Find(&tasks); err != nil {
			if lookupCtx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
				return ReleaseReceipt{}, releaseError(ReleaseCodeOutcomeUnknown, 503,
					"发布结果暂时无法确认，请仅使用原 Idempotency-Key 重试")
			}
			return ReleaseReceipt{}, releaseOutcomeUnknown()
		}
		for _, task := range tasks {
			tasksByID[task.TaskId] = task
		}
	}
	receipt := ReleaseReceipt{RecordID: record.IdempotencyID, TotalCount: int(record.ItemCount), Items: make([]ReleaseReceiptItem, int(record.ItemCount))}
	for index, row := range rows {
		item := ReleaseReceiptItem{RequestIndex: index, ConfigID: row.ConfigID, TaskID: row.TaskID, WorkflowVersionID: row.WorkflowVersionID}
		switch row.Outcome {
		case ReleaseOutcomeAccepted:
			if row.TaskID == nil || row.WorkflowVersionID == nil || row.ErrorCode != nil {
				return ReleaseReceipt{}, fmt.Errorf("幂等发布成功结果损坏")
			}
			task, taskExists := tasksByID[*row.TaskID]
			if !taskExists || task.AppConfigID == nil || *task.AppConfigID != row.ConfigID ||
				task.WorkflowVersionID != *row.WorkflowVersionID || task.PublisherUserID == nil || *task.PublisherUserID != actorUserID {
				return ReleaseReceipt{}, fmt.Errorf("幂等发布任务关联损坏")
			}
			item.Success = true
			receipt.SuccessCount++
		case ReleaseOutcomeRejected:
			if row.TaskID != nil || row.WorkflowVersionID != nil || row.ErrorCode == nil || *row.ErrorCode == "" {
				return ReleaseReceipt{}, fmt.Errorf("幂等发布失败结果损坏")
			}
			if operation == SemanticReleaseCreate || !validPersistedReleaseFailureCode(*row.ErrorCode) {
				return ReleaseReceipt{}, fmt.Errorf("幂等发布失败结果语义损坏")
			}
			item.ErrorCode = *row.ErrorCode
			receipt.FailureCount++
		default:
			return ReleaseReceipt{}, fmt.Errorf("幂等发布结果状态损坏")
		}
		receipt.Items[index] = item
	}
	return receipt, nil
}

func releaseOutcomeUnknown() error {
	return releaseError(ReleaseCodeOutcomeUnknown, 503,
		"发布结果暂时无法确认，请仅使用原 Idempotency-Key 重试")
}

func validPersistedReleaseFailureCode(code string) bool {
	switch code {
	case ReleaseCodeTargetNotFound, ReleaseCodeEnvironmentDisabled,
		ReleaseCodeWorkflowNotConfigured, ReleaseCodeWorkflowVersionChanged,
		ReleaseCodeWorkflowInvalid, ReleaseCodeExecutorUnavailable:
		return true
	default:
		return false
	}
}

func isIdempotencyUniqueConflict(err error) bool {
	var mysqlError *mysql.MySQLError
	// This INSERT supplies no primary key and the table has exactly one logical
	// unique key. Schema manifests intentionally compare index semantics rather
	// than names, so replay must remain correct after an equivalent index rename.
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

func isRetryableTransactionError(err error) bool {
	var mysqlError *mysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1213
}

func isLockWaitTimeout(err error) bool {
	var mysqlError *mysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1205
}

type releaseTargetRow struct {
	ConfigID  int    `xorm:"config_id"`
	AppID     int    `xorm:"app_id"`
	AppName   string `xorm:"app_name"`
	AppNameCN string `xorm:"app_name_cn"`
}

func (s *IdempotentReleaseService) ListTargets(ctx context.Context, envCode, keyword string, pageNum, pageSize int) (ReleaseTargetPage, error) {
	envCode, err := environment.NormalizeCode(envCode)
	if err != nil {
		return ReleaseTargetPage{}, invalidReleaseRequest("环境代码无效")
	}
	keyword = strings.TrimSpace(keyword)
	if !utf8.ValidString(keyword) || utf8.RuneCountInString(keyword) > 100 ||
		pageNum < 1 || pageSize < 1 || pageSize > 100 {
		return ReleaseTargetPage{}, invalidReleaseRequest("分页或搜索参数无效")
	}
	if err := s.ready(); err != nil {
		return ReleaseTargetPage{}, err
	}
	var env entity.EnvConfigs
	has, err := s.engine.Context(ctx).Where("env = ? AND deleted_at IS NULL", envCode).Get(&env)
	if err != nil {
		return ReleaseTargetPage{}, err
	}
	if !has {
		return ReleaseTargetPage{}, releaseError(ReleaseCodeTargetNotFound, 404, "环境不存在")
	}

	countQuery := s.engine.Context(ctx).Where("deleted_at IS NULL")
	if keyword != "" {
		like := "%" + keyword + "%"
		countQuery = countQuery.And("(app_name LIKE ? OR app_name_cn LIKE ?)", like, like)
	}
	total, err := countQuery.Count(new(entity.Apps))
	if err != nil {
		return ReleaseTargetPage{}, err
	}
	page := ReleaseTargetPage{Total: total, PageNum: pageNum, PageSize: pageSize, Targets: make([]ReleaseTarget, 0)}
	if total > 0 {
		page.TotalPages = int(math.Ceil(float64(total) / float64(pageSize)))
	}
	if total == 0 || pageNum > page.TotalPages {
		return page, nil
	}
	query := s.engine.Context(ctx).Table([]string{entity.TableApps, "app"}).
		Select("app.app_id, app.app_name, app.app_name_cn, COALESCE(config.config_id, 0) AS config_id").
		Join("LEFT", []string{entity.TableAppConfigs, "config"},
			"config.app_id = app.app_id AND config.env = ? AND config.deleted_at IS NULL", envCode).
		Where("app.deleted_at IS NULL")
	if keyword != "" {
		like := "%" + keyword + "%"
		query = query.And("(app.app_name LIKE ? OR app.app_name_cn LIKE ?)", like, like)
	}
	var rows []releaseTargetRow
	if err := query.Asc("app.app_name", "app.app_id").Limit(pageSize, (pageNum-1)*pageSize).Find(&rows); err != nil {
		return ReleaseTargetPage{}, err
	}
	session := s.engine.NewSession()
	defer session.Close()
	commands := make([]ReleaseCommand, 0, len(rows))
	if env.Enabled {
		for _, row := range rows {
			if row.ConfigID > 0 {
				commands = append(commands, ReleaseCommand{ConfigID: row.ConfigID})
			}
		}
	}
	snapshots, inspectedByConfig, err := s.inspectTargets(ctx, session, commands, false)
	if err != nil {
		return ReleaseTargetPage{}, err
	}
	for _, row := range rows {
		target := ReleaseTarget{ConfigID: row.ConfigID, AppID: row.AppID, AppName: row.AppName, AppNameCN: row.AppNameCN, Env: envCode, Steps: make([]ReleaseStepPreflight, 0)}
		if !env.Enabled {
			target.UnavailableCode = ReleaseCodeEnvironmentDisabled
		} else if row.ConfigID <= 0 {
			target.UnavailableCode = ReleaseCodeTargetNotFound
		} else {
			inspected := inspectedByConfig[row.ConfigID]
			if inspected.Ready {
				inspected, err = s.applyTargetAvailability(ctx, snapshots[row.ConfigID], inspected)
				if err != nil {
					return ReleaseTargetPage{}, err
				}
			}
			target.WorkflowVersionID = inspected.WorkflowVersionID
			target.Available = inspected.Ready
			target.UnavailableCode = inspected.ErrorCode
			target.Steps = inspected.Steps
		}
		page.Targets = append(page.Targets, target)
	}
	return page, nil
}
