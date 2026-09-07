package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/config"
	"github.com/go-ree/ares/internal/workflow"
)

const logReadPollInterval = 300 * time.Millisecond

// StreamTaskStepLogs exposes an executor-neutral, cursor-addressed SSE stream.
// Provider identity is resolved exclusively from the immutable task snapshot.
// @Tags Publish
// @Summary 按工作流任务步骤流式读取日志
// @Produce text/event-stream
// @Param task_id path int true "任务 ID"
// @Param step_key path string true "工作流步骤 key"
// @Param cursor query string false "不透明日志游标；不得与不同值的 Last-Event-ID 同时提交"
// @Param Last-Event-ID header string false "断线续传游标；不得与不同值的 cursor 同时提交"
// @Success 200 {string} string "SSE events: log, ping, end, stream-error, auth-expired"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求或游标无效"
// @Failure 404 {object} util.ResponseTemplate{code=int} "任务或步骤不存在"
// @Failure 409 {object} util.ResponseTemplate{code=int} "日志尚未就绪或来源不匹配"
// @Failure 422 {object} util.ResponseTemplate{code=int} "步骤不支持日志"
// @Failure 429 {object} util.ResponseTemplate{code=int} "日志流容量已满"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "上游日志响应错误"
// @Failure 503 {object} util.ResponseTemplate{code=int} "步骤执行器不可用"
// @Router /api/v1/tasks/{task_id}/steps/{step_key}/logs/stream [get]
func (wc *WorkflowController) StreamTaskStepLogs(c *gin.Context) {
	taskID, ok := canonicalPositiveTaskID(c.Param("task_id"))
	if !ok {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数错误", "invalid_request"))
		return
	}
	stepKey := c.Param("step_key")
	if !workflow.ValidStepKey(stepKey) {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数错误", "invalid_request"))
		return
	}
	cursor, err := taskStepLogCursor(c.Request.URL.RawQuery, c.Request.Header)
	if err != nil {
		code := "invalid_cursor"
		if errors.Is(err, errLogCursorConflict) {
			code = "cursor_conflict"
		} else if errors.Is(err, errInvalidLogQuery) {
			code = "invalid_request"
		}
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数错误", code))
		return
	}
	SetRequestAuditResourceID(c, strconv.Itoa(taskID)+"/"+stepKey)
	releaseAdmission, admitted := acquireLogSSE(c)
	if !admitted {
		return
	}
	defer releaseAdmission()
	if wc == nil || wc.logs == nil {
		c.JSON(http.StatusInternalServerError, util.ResponseFailure("日志服务不可用", "internal_error"))
		return
	}

	streamContext, cancelStream := context.WithTimeout(c.Request.Context(), config.SSEMaxDuration())
	defer cancelStream()
	// The provider's bounded first read deliberately happens before HTTP 200 so
	// stable source/readiness failures retain HTTP semantics. Clear the server's
	// ordinary response WriteTimeout first: integration timeouts may be longer,
	// while streamContext still bounds this pre-stream work and the full stream.
	deadlineController := http.NewResponseController(c.Writer)
	if err := clearSSEWriteDeadline(deadlineController); err != nil {
		c.JSON(http.StatusInternalServerError, util.ResponseFailure("日志流不可用", "internal_error"))
		return
	}
	session, err := wc.logs.Open(streamContext, taskID, stepKey)
	if err != nil {
		respondTaskStepLogError(c, err, false)
		return
	}
	// Perform the first bounded read before committing HTTP 200 so deterministic
	// source, cursor, readiness, and integration failures retain HTTP semantics.
	first, err := session.Read(streamContext, cursor)
	if err != nil {
		respondTaskStepLogError(c, err, true)
		return
	}

	chunks := make(chan workflow.LogChunk)
	results := make(chan error, 1)
	go pumpTaskStepLogs(streamContext, session, first, chunks, results)

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-store, no-transform")
	c.Header("X-Accel-Buffering", "no")
	if err := flushSSEHeadersForRequest(c, c.Writer, deadlineController, config.SSEWriteTimeout()); err != nil {
		return
	}
	if !streamTaskStepSSE(
		c.Request.Context(), streamContext, c.Writer, deadlineController, cursor,
		chunks, results, sseSessionRevalidator(c), configuredSSEStreamLimits(),
	) {
		MarkRequestAuditFailure(c)
	}
}

func canonicalPositiveTaskID(raw string) (int, bool) {
	if raw == "" || raw[0] < '1' || raw[0] > '9' {
		return 0, false
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value > 0
}

var (
	errInvalidLogQuery   = errors.New("invalid task log query")
	errLogCursorConflict = errors.New("log cursor conflict")
)

func taskStepLogCursor(rawQuery string, header http.Header) (string, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", errInvalidLogQuery
	}
	for key, entries := range values {
		if key != "cursor" || len(entries) != 1 || entries[0] == "" {
			return "", errInvalidLogQuery
		}
	}
	queryCursor := values.Get("cursor")
	headerValues, headerPresent := header[http.CanonicalHeaderKey("Last-Event-ID")]
	headerCursor := ""
	if headerPresent {
		if len(headerValues) != 1 || headerValues[0] == "" {
			return "", workflow.ErrInvalidLogCursor
		}
		headerCursor = headerValues[0]
	}
	if err := workflow.ValidateLogCursor(queryCursor); err != nil {
		return "", err
	}
	if err := workflow.ValidateLogCursor(headerCursor); err != nil {
		return "", err
	}
	if queryCursor != "" && headerCursor != "" && queryCursor != headerCursor {
		return "", errLogCursorConflict
	}
	if headerCursor != "" {
		return headerCursor, nil
	}
	return queryCursor, nil
}

func pumpTaskStepLogs(
	ctx context.Context,
	session *workflow.LogSession,
	first workflow.LogChunk,
	chunks chan<- workflow.LogChunk,
	results chan<- error,
) {
	chunk := first
	for {
		select {
		case chunks <- chunk:
		case <-ctx.Done():
			return
		}
		if chunk.EOF {
			sendTaskLogResult(ctx, results, nil)
			return
		}
		cursor := chunk.Cursor
		if chunk.Content == "" {
			timer := time.NewTimer(logReadPollInterval)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			}
		}
		var err error
		chunk, err = session.Read(ctx, cursor)
		if err != nil {
			sendTaskLogResult(ctx, results, err)
			return
		}
	}
}

func sendTaskLogResult(ctx context.Context, results chan<- error, err error) {
	select {
	case results <- err:
	case <-ctx.Done():
	}
}

func streamTaskStepSSE(
	requestContext context.Context,
	streamContext context.Context,
	writer io.Writer,
	deadlineController sseWriteDeadlineSetter,
	initialCursor string,
	chunks <-chan workflow.LogChunk,
	results <-chan error,
	revalidator SSESessionRevalidator,
	limits sseStreamLimits,
) bool {
	heartbeatTicker := time.NewTicker(limits.heartbeatInterval)
	defer heartbeatTicker.Stop()
	idleTimer := time.NewTimer(limits.idleTimeout)
	defer idleTimer.Stop()
	var reauthTicker *time.Ticker
	var reauth <-chan time.Time
	if revalidator != nil {
		reauthTicker = time.NewTicker(limits.reauthInterval)
		defer reauthTicker.Stop()
		reauth = reauthTicker.C
	}

	cursor := initialCursor
	for {
		select {
		case <-streamContext.Done():
			if requestContext.Err() == nil && errors.Is(streamContext.Err(), context.DeadlineExceeded) {
				return writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "end", textCursorID(cursor),
					map[string]string{"reason": "max_duration"}) == nil
			}
			return requestContext.Err() != nil
		case <-idleTimer.C:
			_ = writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "end", textCursorID(cursor),
				map[string]string{"reason": "upstream_idle"})
			return false
		case <-heartbeatTicker.C:
			if err := writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "ping", textCursorID(cursor), struct{}{}); err != nil {
				return false
			}
		case <-reauth:
			revalidationContext, cancelRevalidation := context.WithTimeout(streamContext, limits.writeTimeout)
			err := revalidator(revalidationContext)
			cancelRevalidation()
			if err != nil {
				if requestContext.Err() != nil {
					return true
				}
				if errors.Is(streamContext.Err(), context.DeadlineExceeded) {
					return writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "end", textCursorID(cursor),
						map[string]string{"reason": "max_duration"}) == nil
				}
				switch {
				case errors.Is(err, ErrSSESessionExpired):
					_ = writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "auth-expired", nil,
						map[string]string{"reason": "session_expired"})
				case errors.Is(err, ErrSSEPermissionRevoked):
					_ = writeSSETextJSON(writer, deadlineController, limits.writeTimeout, sseStreamErrorEvent, nil,
						map[string]string{"code": "forbidden"})
				default:
					_ = writeSSETextJSON(writer, deadlineController, limits.writeTimeout, sseStreamErrorEvent, nil,
						map[string]string{"code": "session_revalidation_failed"})
				}
				return false
			}
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
				continue
			}
			cursor = chunk.Cursor
			if chunk.Content == "" {
				continue
			}
			resetTimer(idleTimer, limits.idleTimeout)
			if err := writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "log", textCursorID(cursor), chunk); err != nil {
				return false
			}
		case streamErr, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			if requestContext.Err() != nil {
				return true
			}
			if errors.Is(streamContext.Err(), context.DeadlineExceeded) {
				return writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "end", textCursorID(cursor),
					map[string]string{"reason": "max_duration"}) == nil
			}
			if streamErr != nil {
				_ = writeSSETextJSON(writer, deadlineController, limits.writeTimeout, sseStreamErrorEvent, nil,
					map[string]string{"code": taskStepLogStreamErrorCode(streamErr)})
				return false
			}
			return writeSSETextJSON(writer, deadlineController, limits.writeTimeout, "end", textCursorID(cursor),
				map[string]string{"reason": "completed"}) == nil
		}
	}
}

func textCursorID(cursor string) *string {
	if cursor == "" {
		return nil
	}
	return &cursor
}

func writeSSETextJSON(
	writer io.Writer,
	controller sseWriteDeadlineSetter,
	timeout time.Duration,
	event string,
	id *string,
	value any,
) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var frame string
	if event != "" {
		frame += "event: " + event + "\n"
	}
	if id != nil {
		if err := workflow.ValidateLogCursor(*id); err != nil || *id == "" {
			return workflow.ErrInvalidLogCursor
		}
		frame += "id: " + *id + "\n"
	}
	frame += "data: " + string(data) + "\n\n"
	return withSSEWriteDeadline(controller, timeout, func() error {
		if _, err := io.WriteString(writer, frame); err != nil {
			return err
		}
		return controller.Flush()
	})
}

func respondTaskStepLogError(c *gin.Context, err error, readerCalled bool) {
	status, message, code := http.StatusInternalServerError, "日志服务不可用", "internal_error"
	switch {
	case errors.Is(err, workflow.ErrInvalidLogCursor):
		status, message, code = http.StatusBadRequest, "日志游标无效", "invalid_cursor"
	case errors.Is(err, workflow.ErrNotFound):
		status, message, code = http.StatusNotFound, "任务或步骤不存在", "task_or_step_not_found"
	case errors.Is(err, workflow.ErrLegacyTask):
		status, message, code = http.StatusConflict, "旧版任务请使用兼容日志入口", "legacy_task"
	case errors.Is(err, workflow.ErrLogsNotReady):
		status, message, code = http.StatusConflict, "步骤日志尚未就绪", "logs_not_ready"
	case errors.Is(err, workflow.ErrLogSourceMismatch):
		status, message, code = http.StatusConflict, "步骤日志来源不可用", "log_source_mismatch"
	case errors.Is(err, workflow.ErrLogsUnsupported):
		status, message, code = http.StatusUnprocessableEntity, "步骤不支持日志", "logs_unsupported"
	case errors.Is(err, workflow.ErrExecutorUnavailable):
		status, message, code = http.StatusServiceUnavailable, "步骤执行器不可用", "executor_unavailable"
	case errors.Is(err, workflow.ErrInvalidLogChunk):
		status, message, code = http.StatusBadGateway, "执行器日志响应无效", "invalid_log_chunk"
	default:
		if readerCalled {
			status, message, code = http.StatusBadGateway, "上游日志服务不可用", "upstream_unavailable"
		}
	}
	if status >= http.StatusInternalServerError {
		slog.Error("task step log request failed", "request_id", RequestID(c), "error_code", code)
	}
	c.JSON(status, util.ResponseFailure(message, code))
}

func taskStepLogStreamErrorCode(err error) string {
	switch {
	case errors.Is(err, workflow.ErrInvalidLogCursor):
		return "invalid_log_chunk"
	case errors.Is(err, workflow.ErrLogsNotReady):
		return "upstream_unavailable"
	case errors.Is(err, workflow.ErrLogSourceMismatch):
		return "log_source_mismatch"
	case errors.Is(err, workflow.ErrExecutorUnavailable):
		return "executor_unavailable"
	case errors.Is(err, workflow.ErrInvalidLogChunk):
		return "invalid_log_chunk"
	default:
		return "upstream_unavailable"
	}
}
