package controller

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/publish"
	"github.com/go-ree/ares/internal/release"
)

type PublishController struct {
	publishManager publish.PublishManager
	releases       *publish.IdempotentReleaseService
}

func NewPublishController() *PublishController {
	return &PublishController{
		publishManager: *publish.NewPublishManager(),
		releases:       publish.NewIdempotentReleaseService(db.Engine, release.Shared().Service.Registry()),
	}
}

// ListReleaseTargets returns every active application for an environment and
// explains why a specific AppConfig is not currently publishable.
// @Tags Publish
// @Summary 查询环境下的发布目标
// @Description 分页返回环境下的应用及其 AppConfig 发布可用性；available 仅供选择，创建前仍须预检
// @Produce json
// @Param env query string true "环境代码"
// @Param q query string false "应用名称或中文名称关键字（最长 100 个字符）"
// @Param page_num query int false "页码" default(1) minimum(1)
// @Param page_size query int false "每页条数" default(20) minimum(1) maximum(100)
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.ReleaseTargetPage} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "查询参数无效"
// @Failure 401 {object} util.ResponseTemplate{code=int} "未认证"
// @Failure 403 {object} util.ResponseTemplate{code=int} "缺少发布权限"
// @Failure 404 {object} util.ResponseTemplate{code=int} "环境不存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/releases/targets [get]
func (pc *PublishController) ListReleaseTargets(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || !validSingleQuery(values, map[string]struct{}{
		"env": {}, "q": {}, "page_num": {}, "page_size": {},
	}) {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("查询参数无效", publish.ReleaseCodeInvalidRequest))
		return
	}
	pageNum, pageSize := 1, 20
	if raw := values.Get("page_num"); raw != "" {
		pageNum, err = strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("查询参数无效", publish.ReleaseCodeInvalidRequest))
			return
		}
	}
	if raw := values.Get("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, util.ResponseFailure("查询参数无效", publish.ReleaseCodeInvalidRequest))
			return
		}
	}
	result, err := pc.releases.ListTargets(c.Request.Context(), values.Get("env"), values.Get("q"), pageNum, pageSize)
	if err != nil {
		writeReleaseCommandError(c, "发布目标查询失败", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("发布目标查询成功", result))
}

// PreflightRelease
// @Tags Publish
// @Summary 预检单个 AppConfig 发布
// @Description 只读校验目标、环境、工作流及执行器；业务不可发布仍以 HTTP 200 和 ready=false 返回
// @Accept json
// @Produce json
// @Param config_id path int true "AppConfig ID"
// @Param X-CSRF-Token header string true "当前会话的 CSRF token"
// @Param request body publish.CreateReleaseRequest true "发布意图"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.ReleasePreflight} "预检完成"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求无效"
// @Failure 401 {object} util.ResponseTemplate{code=int} "未认证"
// @Failure 403 {object} util.ResponseTemplate{code=int} "缺少发布权限或 CSRF 校验失败"
// @Failure 404 {object} util.ResponseTemplate{code=int} "AppConfig 不存在"
// @Failure 413 {object} util.ResponseTemplate{code=int} "请求或 inputs 超限"
// @Failure 415 {object} util.ResponseTemplate{code=int} "Content-Type 不是 application/json"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id}/releases/preflight [post]
func (pc *PublishController) PreflightRelease(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	configID, ok := positivePathInt(c, "config_id", "配置 ID")
	if !ok {
		return
	}
	var req publish.CreateReleaseRequest
	if !BindCanonicalJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	result, err := pc.releases.Preflight(c.Request.Context(), publish.ReleaseCommand{
		ConfigID: configID, Ref: req.Ref, Inputs: req.Inputs,
		ExpectedWorkflowVersionID: req.ExpectedWorkflowVersionID,
	})
	if err != nil {
		writeReleaseCommandError(c, "发布预检失败", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("预检完成", result))
}

// PreflightBatchRelease
// @Tags Publish
// @Summary 预检一组 AppConfig 发布
// @Description 按原请求顺序逐项返回资格结果；预检不创建任务，也不消费 Idempotency-Key
// @Accept json
// @Produce json
// @Param X-CSRF-Token header string true "当前会话的 CSRF token"
// @Param request body publish.CreateBatchReleaseRequest true "有序批量发布意图"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.BatchReleasePreflight} "预检完成"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求、条目数或目标重复无效"
// @Failure 401 {object} util.ResponseTemplate{code=int} "未认证"
// @Failure 403 {object} util.ResponseTemplate{code=int} "缺少发布权限或 CSRF 校验失败"
// @Failure 413 {object} util.ResponseTemplate{code=int} "请求或 inputs 超限"
// @Failure 415 {object} util.ResponseTemplate{code=int} "Content-Type 不是 application/json"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/releases/batch/preflight [post]
func (pc *PublishController) PreflightBatchRelease(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var req publish.CreateBatchReleaseRequest
	if !BindCanonicalJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	result, err := pc.releases.PreflightBatch(c.Request.Context(), req.Items)
	if err != nil {
		writeReleaseCommandError(c, "批量发布预检失败", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("预检完成", result))
}

// CreateRelease
// @Tags Publish
// @Summary 幂等创建单个 AppConfig 发布任务
// @Description 原子写入发布回执、任务与步骤快照；相同主体、操作、key 和意图只重放已提交结果
// @Accept json
// @Produce json
// @Param config_id path int true "AppConfig ID"
// @Param Idempotency-Key header string true "16～128 字节的 Ares 幂等 token"
// @Param X-CSRF-Token header string true "当前会话的 CSRF token"
// @Param request body publish.CreateReleaseRequest true "发布意图"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.ReleaseReceipt} "已接纳或重放"
// @Header 200 {string} Location "单发成功时的任务详情路径"
// @Header 200 {string} Idempotency-Replayed "重放时为 true，首次创建不返回"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求或 Idempotency-Key 无效"
// @Failure 401 {object} util.ResponseTemplate{code=int} "未认证"
// @Failure 403 {object} util.ResponseTemplate{code=int} "缺少发布权限或 CSRF 校验失败"
// @Failure 404 {object} util.ResponseTemplate{code=int} "AppConfig 不存在"
// @Failure 409 {object} util.ResponseTemplate{code=int} "版本变化、环境停用、key 冲突或请求处理中"
// @Header 409 {string} Retry-After "相同请求仍在处理时为 1 秒"
// @Failure 413 {object} util.ResponseTemplate{code=int} "请求、inputs 或步骤快照超限"
// @Failure 415 {object} util.ResponseTemplate{code=int} "Content-Type 不是 application/json"
// @Failure 422 {object} util.ResponseTemplate{code=int} "工作流未配置、无效或执行器不可用"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 503 {object} util.ResponseTemplate{code=int} "提交结果暂时无法确认"
// @Router /api/v1/app-configs/{config_id}/releases [post]
func (pc *PublishController) CreateRelease(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, ok := currentPublishActor(c)
	if !ok {
		return
	}
	configID, ok := positivePathInt(c, "config_id", "配置 ID")
	if !ok {
		return
	}
	token, err := publish.ParseIdempotencyKey(c.Request.Header.Values("Idempotency-Key"))
	if err != nil {
		writeReleaseCommandError(c, "发布任务创建失败", err)
		return
	}
	var req publish.CreateReleaseRequest
	if !BindCanonicalJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	result, replayed, err := pc.releases.Create(c.Request.Context(), actor, token, publish.ReleaseCommand{
		ConfigID: configID, Ref: req.Ref, Inputs: req.Inputs,
		ExpectedWorkflowVersionID: req.ExpectedWorkflowVersionID,
	})
	if err != nil {
		writeReleaseCommandError(c, "发布任务创建失败", err)
		return
	}
	writeIdempotentReleaseHeaders(c, replayed, result)
	c.JSON(http.StatusOK, util.ResponseSuccessful("发布任务已接纳", result))
}

// CreateBatchRelease
// @Tags Publish
// @Summary 幂等创建一组 AppConfig 发布任务
// @Description 在同一事务中按原请求顺序提交完整回执及所有可发布任务；业务失败以 receipt item 返回
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "16～128 字节的 Ares 幂等 token"
// @Param X-CSRF-Token header string true "当前会话的 CSRF token"
// @Param request body publish.CreateBatchReleaseRequest true "有序批量发布意图"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.ReleaseReceipt} "已接纳或重放"
// @Header 200 {string} Idempotency-Replayed "重放时为 true，首次创建不返回"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求、条目数、重复目标或 Idempotency-Key 无效"
// @Failure 401 {object} util.ResponseTemplate{code=int} "未认证"
// @Failure 403 {object} util.ResponseTemplate{code=int} "缺少发布权限或 CSRF 校验失败"
// @Failure 409 {object} util.ResponseTemplate{code=int} "key 冲突或相同请求仍在处理中"
// @Header 409 {string} Retry-After "相同请求仍在处理时为 1 秒"
// @Failure 413 {object} util.ResponseTemplate{code=int} "请求、inputs 或步骤快照超限"
// @Failure 415 {object} util.ResponseTemplate{code=int} "Content-Type 不是 application/json"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 503 {object} util.ResponseTemplate{code=int} "提交结果暂时无法确认"
// @Router /api/v1/releases/batch [post]
func (pc *PublishController) CreateBatchRelease(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	actor, ok := currentPublishActor(c)
	if !ok {
		return
	}
	token, err := publish.ParseIdempotencyKey(c.Request.Header.Values("Idempotency-Key"))
	if err != nil {
		writeReleaseCommandError(c, "批量发布任务创建失败", err)
		return
	}
	var req publish.CreateBatchReleaseRequest
	if !BindCanonicalJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	result, replayed, err := pc.releases.CreateBatch(c.Request.Context(), actor, token, req.Items)
	if err != nil {
		writeReleaseCommandError(c, "批量发布任务创建失败", err)
		return
	}
	writeIdempotentReleaseHeaders(c, replayed, result)
	c.JSON(http.StatusOK, util.ResponseSuccessful("批量发布任务已接纳", result))
}

func positivePathInt(c *gin.Context, name, label string) (int, bool) {
	value, err := strconv.Atoi(c.Param(name))
	if err != nil || value <= 0 {
		c.JSON(http.StatusBadRequest, util.ResponseFailure(label+" 无效", publish.ReleaseCodeInvalidRequest))
		return 0, false
	}
	return value, true
}

func writeIdempotentReleaseHeaders(c *gin.Context, replayed bool, receipt publish.ReleaseReceipt) {
	outcome := "created"
	if replayed {
		c.Header("Idempotency-Replayed", "true")
		outcome = "replayed"
	}
	auditResource := "receipt:" + strconv.FormatInt(receipt.RecordID, 10) +
		";outcome:" + outcome +
		";accepted:" + strconv.Itoa(receipt.SuccessCount) +
		";rejected:" + strconv.Itoa(receipt.FailureCount)
	if len(receipt.Items) == 1 && receipt.Items[0].TaskID != nil {
		c.Header("Location", "/api/v1/deploy/publish/query/"+strconv.Itoa(*receipt.Items[0].TaskID))
		auditResource += ";config:" + strconv.Itoa(receipt.Items[0].ConfigID) +
			";task:" + strconv.Itoa(*receipt.Items[0].TaskID)
	}
	SetRequestAuditResourceID(c, auditResource)
}

func writeReleaseCommandError(c *gin.Context, message string, err error) {
	var commandError *publish.ReleaseCommandError
	if errors.As(err, &commandError) {
		setReleaseFailureAuditResource(c, commandError.Code)
		if commandError.Code == publish.ReleaseCodeIdempotencyRequestInProgress {
			c.Header("Retry-After", "1")
		}
		c.JSON(commandError.HTTPStatus, util.ResponseFailure(message, commandError.Code))
		return
	}
	setReleaseFailureAuditResource(c, publish.ReleaseCodeInternalError)
	writeInternalFailure(c, http.StatusInternalServerError, message, "database", "release_command", err)
}

func setReleaseFailureAuditResource(c *gin.Context, code string) {
	resource := "outcome:" + code
	if configID, err := strconv.Atoi(c.Param("config_id")); err == nil && configID > 0 {
		resource = "config:" + strconv.Itoa(configID) + ";" + resource
	}
	SetRequestAuditResourceID(c, resource)
}

// CreateBuildTask
// @Tags Publish
// @Summary 单应用进行发布动作
// @Description 已弃用的 app_name+env 兼容入口；成功时返回任务详情，若提交后详情暂时不可读则至少返回已持久化的 task_id
// @Deprecated
// @Accept json
// @Produce json
// @Param Idempotency-Key header string false "可选；提供时必须是 16～128 字节的 Ares 幂等 token，缺失时保持旧客户端非幂等语义"
// @Param X-CSRF-Token header string true "当前会话的 CSRF token"
// @Param request body publish.CreatePublishRequest true "发布请求参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.TaskRecordView} "成功；详情暂时不可读时 result 仅保证 task_id"
// @Header 200 {string} Location "任务详情路径"
// @Header 200 {string} Idempotency-Replayed "带 key 的重放时为 true"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求或 Idempotency-Key 无效"
// @Failure 401 {object} util.ResponseTemplate{code=int} "未认证"
// @Failure 403 {object} util.ResponseTemplate{code=int} "缺少发布权限或 CSRF 校验失败"
// @Failure 404 {object} util.ResponseTemplate{code=int} "解析后的 AppConfig 已不存在"
// @Failure 409 {object} util.ResponseTemplate{code=int} "环境停用、key 冲突、版本变化或请求处理中"
// @Header 409 {string} Retry-After "相同请求仍在处理时为 1 秒"
// @Failure 413 {object} util.ResponseTemplate{code=int} "请求、inputs 或步骤快照超限"
// @Failure 415 {object} util.ResponseTemplate{code=int} "Content-Type 不是 application/json"
// @Failure 422 {object} util.ResponseTemplate{code=int} "目标无法唯一解析、工作流未配置/无效或执行器不可用"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 503 {object} util.ResponseTemplate{code=int} "提交结果暂时无法确认"
// @Header all {string} Deprecation "固定为 true"
// @Header all {string} Warning "299 弃用警告"
// @Router	/api/v1/deploy/publish [post]
func (pc *PublishController) CreateBuildTask(c *gin.Context) {
	actor, ok := currentPublishActor(c)
	if !ok {
		return
	}
	var req publish.CreatePublishRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	var explicitToken publish.IdempotencyToken
	var err error
	hasExplicitToken := len(c.Request.Header.Values("Idempotency-Key")) > 0
	if hasExplicitToken {
		explicitToken, err = legacyIdempotencyToken(c)
		if err != nil {
			writeReleaseCommandError(c, "发布任务创建失败", err)
			return
		}
	}
	var receipt publish.ReleaseReceipt
	var replayed bool
	if hasExplicitToken {
		receipt, replayed, err = pc.releases.CreateLegacyRelease(c.Request.Context(), actor, explicitToken, req)
	} else {
		var command publish.ReleaseCommand
		command, err = pc.releases.ResolveLegacyCommand(c.Request.Context(), req)
		if err == nil {
			var token publish.IdempotencyToken
			token, err = publish.NewEphemeralIdempotencyToken()
			if err == nil {
				receipt, replayed, err = pc.releases.Create(c.Request.Context(), actor, token, command)
			}
		}
	}
	if err != nil {
		writeLegacyResolutionError(c, "发布任务创建失败", err)
		return
	}
	writeIdempotentReleaseHeaders(c, replayed, receipt)
	if len(receipt.Items) != 1 || receipt.Items[0].TaskID == nil {
		writeInternalFailure(c, http.StatusInternalServerError, "发布任务创建失败", "database", "legacy_release_receipt", errors.New("missing accepted task"))
		return
	}
	legacyResult := &publish.CreateBatchPublishResponse{TaskRecords: make([]publish.CreatePublishResult, 1)}
	if err := pc.fillLegacyBatchResult(c.Request.Context(), []publish.CreatePublishRequest{req}, receipt, legacyResult); err != nil {
		writeInternalFailure(c, http.StatusInternalServerError, "发布任务读取失败", "database", "legacy_release_task", err)
		return
	}
	if legacyResult.TaskRecords[0].TaskRecord != nil {
		c.JSON(http.StatusOK, util.ResponseSuccessful("发布任务创建成功", legacyResult.TaskRecords[0].TaskRecord))
		return
	}
	// The transaction is already durable. If the compatibility projection cannot
	// be re-read, return its stable identity instead of a false 500 or fabricated
	// timestamps/status that could cause a keyless client to submit a duplicate.
	c.JSON(http.StatusOK, util.ResponseSuccessful("发布任务创建成功", gin.H{
		"task_id": legacyResult.TaskRecords[0].TaskID,
	}))
}

// CreateBatchBuildTask
// @Tags Publish
// @Summary 应用进行批量发布动作
// @Description 已弃用的 app_name+env 兼容入口；按请求顺序返回成功任务与稳定的逐项失败代码
// @Deprecated
// @Accept json
// @Produce json
// @Param Idempotency-Key header string false "可选；提供时必须是 16～128 字节的 Ares 幂等 token，缺失时保持旧客户端非幂等语义"
// @Param X-CSRF-Token header string true "当前会话的 CSRF token"
// @Param request body publish.CreateBatchPublishRequest true "发布请求参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.CreateBatchPublishResponse} "成功"
// @Header 200 {string} Location "单条成功批次的任务详情路径"
// @Header 200 {string} Idempotency-Replayed "带 key 的重放时为 true"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求、重复目标或 Idempotency-Key 无效"
// @Failure 401 {object} util.ResponseTemplate{code=int} "未认证"
// @Failure 403 {object} util.ResponseTemplate{code=int} "缺少发布权限或 CSRF 校验失败"
// @Failure 409 {object} util.ResponseTemplate{code=int} "key 冲突或相同请求仍在处理"
// @Header 409 {string} Retry-After "相同请求仍在处理时为 1 秒"
// @Failure 413 {object} util.ResponseTemplate{code=int} "请求、inputs 或步骤快照超限"
// @Failure 415 {object} util.ResponseTemplate{code=int} "Content-Type 不是 application/json"
// @Failure 422 {object} util.ResponseTemplate{code=int} "目标无法唯一解析"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 503 {object} util.ResponseTemplate{code=int} "提交结果暂时无法确认"
// @Header all {string} Deprecation "固定为 true"
// @Header all {string} Warning "299 弃用警告"
// @Router	/api/v1/deploy/publish/batch [post]
func (pc *PublishController) CreateBatchBuildTask(c *gin.Context) {
	actor, ok := currentPublishActor(c)
	if !ok {
		return
	}
	var req publish.CreateBatchPublishRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	if len(req.BatchPublish) == 0 || len(req.BatchPublish) > 100 {
		writePublishError(c, "批量发布任务创建失败", http.StatusUnprocessableEntity,
			newLegacyInputError("批量发布必须包含 1～100 项"))
		return
	}
	var explicitToken publish.IdempotencyToken
	hasExplicitToken := len(c.Request.Header.Values("Idempotency-Key")) > 0
	if hasExplicitToken {
		var tokenErr error
		explicitToken, tokenErr = legacyIdempotencyToken(c)
		if tokenErr != nil {
			writeReleaseCommandError(c, "批量发布任务创建失败", tokenErr)
			return
		}
	}
	publishBatchResult := &publish.CreateBatchPublishResponse{
		TotalCount: len(req.BatchPublish), TaskRecords: make([]publish.CreatePublishResult, len(req.BatchPublish)),
	}
	if hasExplicitToken {
		receipt, replayed, createErr := pc.releases.CreateLegacyBatchRelease(
			c.Request.Context(), actor, explicitToken, req.BatchPublish,
		)
		if createErr != nil {
			writeLegacyResolutionError(c, "批量发布任务创建失败", createErr)
			return
		}
		writeIdempotentReleaseHeaders(c, replayed, receipt)
		if err := pc.fillLegacyBatchResult(c.Request.Context(), req.BatchPublish, receipt, publishBatchResult); err != nil {
			writeInternalFailure(c, http.StatusInternalServerError, "批量发布任务读取失败", "database", "legacy_batch_release", err)
			return
		}
	} else {
		commands, resolutionErrors, resolveErr := pc.releases.ResolveLegacyCommands(c.Request.Context(), req.BatchPublish)
		if resolveErr != nil {
			writeReleaseCommandError(c, "批量发布任务创建失败", resolveErr)
			return
		}
		allResolved := true
		for _, resolutionErr := range resolutionErrors {
			if resolutionErr != nil {
				allResolved = false
			}
		}
		if allResolved {
			token, tokenErr := publish.NewEphemeralIdempotencyToken()
			if tokenErr != nil {
				writeInternalFailure(c, http.StatusInternalServerError, "批量发布任务创建失败", "runtime", "legacy_idempotency", tokenErr)
				return
			}
			receipt, _, createErr := pc.releases.CreateBatch(c.Request.Context(), actor, token, commands)
			if createErr != nil {
				writeReleaseCommandError(c, "批量发布任务创建失败", createErr)
				return
			}
			writeIdempotentReleaseHeaders(c, false, receipt)
			if err := pc.fillLegacyBatchResult(c.Request.Context(), req.BatchPublish, receipt, publishBatchResult); err != nil {
				writeInternalFailure(c, http.StatusInternalServerError, "批量发布任务读取失败", "database", "legacy_batch_release", err)
				return
			}
		} else {
			// A legacy request without a top-level key cannot name an unresolved
			// AppConfig. Preserve its historical partial-result behavior while every
			// resolvable item still uses the canonical atomic single-release path.
			resolvedCommands := make([]publish.ReleaseCommand, 0, len(commands))
			for index, command := range commands {
				if resolutionErrors[index] == nil {
					resolvedCommands = append(resolvedCommands, command)
				}
			}
			// Preserve partial legacy results, but do not let an unresolved item
			// bypass canonical duplicate-target or aggregate step-budget limits.
			if len(resolvedCommands) > 0 {
				if _, preflightErr := pc.releases.PreflightBatch(c.Request.Context(), resolvedCommands); preflightErr != nil {
					writeReleaseCommandError(c, "批量发布任务预检失败", preflightErr)
					return
				}
			}
			type committedLegacyItem struct {
				requestIndex int
				request      publish.CreatePublishRequest
				receipt      publish.ReleaseReceipt
			}
			committed := make([]committedLegacyItem, 0, len(resolvedCommands))
			firstReceiptID, lastReceiptID, receiptCount := int64(0), int64(0), 0
			for index, item := range req.BatchPublish {
				if resolutionErrors[index] != nil {
					publishBatchResult.TaskRecords[index] = publish.CreatePublishResult{
						RequestIndex: index, AppName: item.AppName, Env: item.Env, Error: legacyReleaseErrorCode(resolutionErrors[index]),
					}
					publishBatchResult.FailureCount++
					continue
				}
				token, tokenErr := publish.NewEphemeralIdempotencyToken()
				if tokenErr != nil {
					writeInternalFailure(c, http.StatusInternalServerError, "批量发布任务创建失败", "runtime", "legacy_idempotency", tokenErr)
					return
				}
				receipt, _, createErr := pc.releases.Create(c.Request.Context(), actor, token, commands[index])
				if createErr != nil {
					var commandError *publish.ReleaseCommandError
					_, legacySafe := publish.ClientErrorMessage(createErr)
					if (errors.As(createErr, &commandError) && commandError.HTTPStatus >= 500) ||
						(commandError == nil && !legacySafe) {
						writeReleaseCommandError(c, "批量发布任务创建失败", createErr)
						return
					}
					publishBatchResult.TaskRecords[index] = publish.CreatePublishResult{
						RequestIndex: index, AppName: item.AppName, Env: item.Env, Error: legacyReleaseErrorCode(createErr),
					}
					publishBatchResult.FailureCount++
					continue
				}
				if receiptCount == 0 {
					firstReceiptID = receipt.RecordID
				}
				lastReceiptID = receipt.RecordID
				receiptCount++
				committed = append(committed, committedLegacyItem{
					requestIndex: index, request: item, receipt: receipt,
				})
			}
			taskIDs := make([]int, 0, len(committed))
			for _, item := range committed {
				if len(item.receipt.Items) != 1 || item.receipt.Items[0].TaskID == nil {
					writeInternalFailure(c, http.StatusInternalServerError, "批量发布任务读取失败", "database", "legacy_batch_release",
						errors.New("release receipt has no accepted task"))
					return
				}
				taskIDs = append(taskIDs, *item.receipt.Items[0].TaskID)
			}
			details := pc.loadLegacyTaskDetails(c.Request.Context(), taskIDs)
			for _, item := range committed {
				one := &publish.CreateBatchPublishResponse{TaskRecords: make([]publish.CreatePublishResult, 1)}
				if err := fillLegacyBatchResultFromDetails(
					[]publish.CreatePublishRequest{item.request}, item.receipt, one, details,
				); err != nil {
					writeInternalFailure(c, http.StatusInternalServerError, "批量发布任务读取失败", "database", "legacy_batch_release", err)
					return
				}
				publishBatchResult.TaskRecords[item.requestIndex] = one.TaskRecords[0]
				publishBatchResult.TaskRecords[item.requestIndex].RequestIndex = item.requestIndex
				publishBatchResult.SuccessCount += one.SuccessCount
			}
			setLegacyPartialReleaseAuditResource(c, receiptCount, firstReceiptID, lastReceiptID,
				publishBatchResult.SuccessCount, publishBatchResult.FailureCount)
		}
	}
	if publishBatchResult.FailureCount != 0 {
		// The batch request itself succeeded and the per-item result is the
		// contract. Keep it in result even for partial failure so clients do not
		// discard task IDs that were successfully created.
		c.JSON(200, util.ResponseSuccessful("批量发布完成，部分任务创建失败", publishBatchResult))
		return
	}
	c.JSON(200, util.ResponseSuccessful("发布任务创建成功", publishBatchResult))
}

func LegacyReleaseDeprecationHeaders(c *gin.Context) {
	c.Header("Deprecation", "true")
	c.Header("Warning", `299 ares "旧发布接口已弃用，请迁移到 AppConfig canonical 发布 API"`)
	c.Next()
}

func legacyIdempotencyToken(c *gin.Context) (publish.IdempotencyToken, error) {
	values := c.Request.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return publish.NewEphemeralIdempotencyToken()
	}
	return publish.ParseIdempotencyKey(values)
}

func (pc *PublishController) fillLegacyBatchResult(ctx context.Context, requests []publish.CreatePublishRequest, receipt publish.ReleaseReceipt, result *publish.CreateBatchPublishResponse) error {
	if len(receipt.Items) != len(requests) || len(result.TaskRecords) < len(requests) {
		return errors.New("release receipt length mismatch")
	}
	taskIDs := make([]int, 0, receipt.SuccessCount)
	for _, item := range receipt.Items {
		if item.Success && item.TaskID != nil {
			taskIDs = append(taskIDs, *item.TaskID)
		}
	}
	details := pc.loadLegacyTaskDetails(ctx, taskIDs)
	return fillLegacyBatchResultFromDetails(requests, receipt, result, details)
}

func (pc *PublishController) loadLegacyTaskDetails(ctx context.Context, taskIDs []int) map[int]*publish.TaskRecordView {
	if len(taskIDs) == 0 {
		return map[int]*publish.TaskRecordView{}
	}
	readContext, cancelRead := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	details, detailsErr := pc.publishManager.GetTaskRecordDetailsByIDs(readContext, taskIDs)
	cancelRead()
	if detailsErr == nil {
		return details
	}
	// The atomic release transaction is already committed. Returning a 500
	// here would invite a keyless legacy client to create duplicates. Preserve
	// the durable task identity and omit optional detail instead.
	slog.Warn("旧发布响应详情读取失败，返回已持久化任务标识",
		"task_count", len(taskIDs), "error_class", "database_failure")
	return map[int]*publish.TaskRecordView{}
}

func fillLegacyBatchResultFromDetails(requests []publish.CreatePublishRequest, receipt publish.ReleaseReceipt,
	result *publish.CreateBatchPublishResponse, details map[int]*publish.TaskRecordView,
) error {
	if len(receipt.Items) != len(requests) || len(result.TaskRecords) < len(requests) {
		return errors.New("release receipt length mismatch")
	}
	for index, item := range receipt.Items {
		legacy := publish.CreatePublishResult{
			RequestIndex: index, AppName: requests[index].AppName, Env: requests[index].Env,
			Success: item.Success, Error: item.ErrorCode, TaskID: item.TaskID,
		}
		if item.Success {
			if item.TaskID == nil || item.WorkflowVersionID == nil {
				return errors.New("accepted release receipt has no task")
			}
			detail := details[*item.TaskID]
			legacy.TaskRecord = detail
			result.SuccessCount++
		} else {
			result.FailureCount++
		}
		result.TaskRecords[index] = legacy
	}
	return nil
}

func setLegacyPartialReleaseAuditResource(c *gin.Context, recordCount int, firstRecordID, lastRecordID int64, accepted, rejected int) {
	resource := "outcome:created;records:" + strconv.Itoa(recordCount) +
		";accepted:" + strconv.Itoa(accepted) + ";rejected:" + strconv.Itoa(rejected)
	if recordCount > 0 {
		resource += ";receipt_sample_first:" + strconv.FormatInt(firstRecordID, 10) +
			";receipt_sample_last:" + strconv.FormatInt(lastRecordID, 10)
	}
	SetRequestAuditResourceID(c, resource)
}

func legacyReleaseErrorCode(err error) string {
	var commandError *publish.ReleaseCommandError
	if errors.As(err, &commandError) {
		return commandError.Code
	}
	if _, safe := publish.ClientErrorMessage(err); safe {
		return publish.ReleaseCodeInvalidRequest
	}
	return publish.ReleaseCodeInternalError
}

func writeLegacyResolutionError(c *gin.Context, message string, err error) {
	var commandError *publish.ReleaseCommandError
	if errors.As(err, &commandError) {
		writeReleaseCommandError(c, message, err)
		return
	}
	if _, safe := publish.ClientErrorMessage(err); safe {
		setReleaseFailureAuditResource(c, publish.ReleaseCodeInvalidRequest)
		writePublishError(c, message, http.StatusUnprocessableEntity, err)
		return
	}
	writeReleaseCommandError(c, message, err)
}

func newLegacyInputError(message string) error {
	return &publish.InputError{Message: message}
}

func currentPublishActor(c *gin.Context) (publish.PublishActor, bool) {
	principal, ok := CurrentPrincipal(c)
	if !ok || principal.UserID <= 0 {
		c.JSON(http.StatusUnauthorized, util.ResponseFailure("认证主体不可用", "unauthenticated"))
		return publish.PublishActor{}, false
	}
	displayName := strings.TrimSpace(principal.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(principal.Username)
	}
	if displayName == "" {
		c.JSON(http.StatusUnauthorized, util.ResponseFailure("认证主体不可用", "unauthenticated"))
		return publish.PublishActor{}, false
	}
	return publish.PublishActor{UserID: principal.UserID, DisplayName: displayName}, true
}

// GetBuildTaskList
// @Tags Publish
// @Summary 获取发布中的任务列表
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]publish.ActiveTaskView} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/deploy/publish/status [get]
func (pc *PublishController) GetBuildTaskList(c *gin.Context) {
	status, err := pc.publishManager.JobStatus()
	if err != nil {
		writeInternalFailure(c, http.StatusInternalServerError, "任务列表获取失败", "database", "active_publish_tasks", err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("任务列表获取成功", status))
}

// QueryBuildTaskList
// @Tags Publish
// @Summary 查询构建任务历史
// @Description 支持多条件组合查询，如：应用名称、环境、发布人、分支、发布起始时间等
// @Param request body publish.PublishQuery true "查询参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.PublishQueryResult} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/deploy/publish/query [post]
func (pc *PublishController) QueryBuildTaskList(c *gin.Context) {
	ctx := c.Request.Context()

	// 从请求中绑定查询参数
	var params publish.PublishQuery
	if !BindJSON(c, &params, defaultJSONRequestBytes) {
		return
	}

	// 调用管理器查询
	result, err := pc.publishManager.QueryBuildPublish(ctx, params)
	if err != nil {
		writePublishError(c, "查询失败", http.StatusBadRequest, err)
		return
	}

	c.JSON(200, util.ResponseSuccessful("查询成功", result))
}

// QueryTaskRecordDetails
// @Tags Publish
// @Summary 查询构建任务详情
// @Description 根据任务的执行id，查询构建任务详情信息
// @Param task_id path int true "构建任务ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.TaskRecordView} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/deploy/publish/query/{task_id} [get]
func (pc *PublishController) QueryTaskRecordDetails(c *gin.Context) {
	// 获取路径参数中的应用ID
	taskIDStr := c.Param("task_id")
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("无效的任务ID", "invalid task id"))
		return
	}

	// 调用管理器查询
	result, err := pc.publishManager.GetTaskRecordDetails(taskID)
	if err != nil {
		if publicError, notFound := publish.NotFoundErrorMessage(err); notFound {
			c.JSON(http.StatusNotFound, util.ResponseFailure("查询失败", publicError))
		} else {
			writeInternalFailure(c, http.StatusInternalServerError, "查询失败", "database", "get_publish_task", err)
		}
		return
	}

	c.JSON(200, util.ResponseSuccessful("查询成功", result))
}

// UpsertTaskAppletImages
// @Tags Publish
// @Summary 覆盖写入任务的小程序图片
// @Description 覆盖写入指定 task_id 的图片列表（按 type 维度去重，后者覆盖前者）
// @Param task_id path int true "构建任务ID"
// @Param request body publish.UpsertTaskAppletImagesRequest true "图片列表"
// @Success 200 {object} util.ResponseTemplate{code=int} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/deploy/publish/images/{task_id} [post]
func (pc *PublishController) UpsertTaskAppletImages(c *gin.Context) {
	taskIDStr := c.Param("task_id")
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("无效的任务ID", "invalid task id"))
		return
	}

	var req publish.UpsertTaskAppletImagesRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	if err := pc.publishManager.UpsertTaskAppletImages(taskID, req.AppletImages); err != nil {
		writeInternalFailure(c, http.StatusInternalServerError, "写入失败", "database", "upsert_task_images", err)
		return
	}

	c.JSON(200, util.ResponseSuccessful("写入成功", nil))
}

func writePublishError(c *gin.Context, message string, clientStatus int, err error) {
	if publicError, safe := publish.ClientErrorMessage(err); safe {
		c.JSON(clientStatus, util.ResponseFailure(message, publicError))
		return
	}
	writeInternalFailure(c, http.StatusInternalServerError, message, "database", "publish", err)
}
