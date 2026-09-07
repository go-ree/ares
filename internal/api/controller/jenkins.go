package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/config"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/jenkins"
)

const sseSessionRevalidatorContextKey = "ares.internal.sse-session-revalidator"

// "error" is reserved by EventSource for transport failures. Keep application
// errors on a distinct event name so browser onerror handlers are not invoked
// for a server-authored semantic error frame as well.
const sseStreamErrorEvent = "stream-error"

var jenkinsSSEAdmission = newConcurrentAdmission(32, 4)

var (
	// ErrSSESessionExpired is returned by a request-scoped revalidation hook
	// when an authenticated SSE session is no longer usable. The handler emits
	// auth-expired and closes the stream without exposing the underlying reason.
	ErrSSESessionExpired = errors.New("SSE session expired")
	errJenkinsStream     = errors.New("Jenkins log stream failed")
)

// SSESessionRevalidator rechecks the already-authenticated browser session.
// Implementations must honor ctx and must not refresh the session's idle
// deadline: SSE heartbeats are transport activity, not user activity.
type SSESessionRevalidator func(ctx context.Context) error

// AttachSSESessionRevalidator lets the authentication middleware attach a
// request-scoped, authorization-aware revalidation function without coupling
// this legacy Jenkins controller to the authentication package.
func AttachSSESessionRevalidator(c *gin.Context, revalidator SSESessionRevalidator) {
	if c == nil {
		return
	}
	c.Set(sseSessionRevalidatorContextKey, revalidator)
}

// GetJenkinsNodeStatus
// @Tags Jenkins
// @Summary 获取jenkins节点的状态
// @Success 200 {object} util.ResponseTemplate{code=int,result=jenkins.Nodes} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
// @Router	/api/v1/status/nodes [get]
func GetJenkinsNodeStatus(c *gin.Context) {
	if !jenkins.IsConfigured() {
		c.JSON(503, util.ResponseFailure("Jenkins 集成未启用", "jenkins integration is disabled"))
		return
	}
	nodeInfo, err := jenkins.GetJenkinsNodeStatus()
	if err != nil {
		respondUpstreamFailure(c, "jenkins", "list_nodes", err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("", nodeInfo))
}

// StreamJenkinsBuildLogHandler
// @Tags Publish
// @Summary 按 Ares 任务获取旧版 Jenkins 流式日志 (SSE格式)
// @Param task_id query int true "Ares 发布任务 ID"
// @Param log_type query string true "日志阶段：ci 或 cd"
// @Param start query int64 false "Jenkins progressiveText 起始 offset"
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]string} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Failure 502 {object} util.ResponseTemplate{code=int} "调用链异常"
// @Router	/api/v1/job/stream/log [get]
func StreamJenkinsBuildLogHandler(c *gin.Context) {
	principal, ok := CurrentPrincipal(c)
	if !ok || (principal.UserID <= 0 && strings.TrimSpace(principal.Username) == "") {
		c.JSON(http.StatusUnauthorized, util.ResponseFailure("未登录或会话已失效", "unauthenticated"))
		return
	}
	principalKey := "user:" + strconv.FormatInt(principal.UserID, 10)
	if principal.UserID <= 0 {
		principalKey = "legacy:" + principal.AuthSource + ":" + principal.Username
	}
	releaseAdmission, admitted := jenkinsSSEAdmission.acquire(principalKey)
	if !admitted {
		c.Header("Retry-After", "5")
		c.JSON(http.StatusTooManyRequests, util.ResponseFailure("日志流连接过多", "stream capacity exceeded"))
		return
	}
	defer releaseAdmission()

	snapshot := jenkins.Acquire()
	if snapshot == nil {
		c.JSON(503, util.ResponseFailure("Jenkins 集成未启用", "jenkins integration is disabled"))
		return
	}
	var request struct {
		TaskID  int    `form:"task_id" binding:"required,min=1"`
		LogType string `form:"log_type" binding:"required,oneof=ci cd"`
		Start   int64  `form:"start" binding:"omitempty,min=0"`
	}
	queryValues, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || !validJenkinsStreamQuery(queryValues) {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数错误", "请求参数无效"))
		return
	}
	if err := c.ShouldBindQuery(&request); err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数错误", "请求参数无效"))
		return
	}
	lastEventID, present, err := parseLastEventID(c.Request.Header)
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数错误", "Last-Event-ID 无效"))
		return
	}
	var task entity.TaskRecord
	has, err := db.Engine.Context(c.Request.Context()).
		Where("task_id = ? AND deleted_at IS NULL", request.TaskID).Get(&task)
	if err != nil {
		c.JSON(http.StatusInternalServerError, util.ResponseFailure("查询任务失败", "internal error"))
		return
	}
	if !has {
		c.JSON(http.StatusNotFound, util.ResponseFailure("任务不存在", fmt.Sprintf("task_id=%d", request.TaskID)))
		return
	}
	SetRequestAuditResourceID(c, strconv.Itoa(request.TaskID))
	if present {
		request.Start = lastEventID
	}
	query, err := taskBuildLogReference(task, request.LogType, request.Start, snapshot.Address())
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, util.ResponseFailure("任务没有对应日志", "build log reference unavailable"))
		return
	}

	streamContext, cancelStream := context.WithTimeout(c.Request.Context(), config.SSEMaxDuration())
	defer cancelStream()
	logChan := make(chan jenkins.BuildLogChunk)
	errChan := make(chan error, 1)
	resultChan := make(chan error, 1)
	go func() {
		if snapshot.StreamJenkinsBuildLog(streamContext, query, logChan, errChan) {
			resultChan <- nil
			return
		}
		select {
		case streamErr := <-errChan:
			if streamErr == nil {
				streamErr = errJenkinsStream
			}
			resultChan <- streamErr
		default:
			resultChan <- errJenkinsStream
		}
	}()

	deadlineController := http.NewResponseController(c.Writer)
	if err := clearSSEWriteDeadline(deadlineController); err != nil {
		cancelStream()
		c.JSON(http.StatusInternalServerError, util.ResponseFailure("日志流不可用", "stream deadline unavailable"))
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-store, no-transform")
	c.Header("X-Accel-Buffering", "no")
	if err := flushSSEHeadersForRequest(c, c.Writer, deadlineController, config.SSEWriteTimeout()); err != nil {
		return
	}
	if !streamJenkinsSSE(
		c.Request.Context(), streamContext, c.Writer, deadlineController,
		query.Start, logChan, resultChan, sseSessionRevalidator(c), configuredSSEStreamLimits(),
	) {
		MarkRequestAuditFailure(c)
	}
}

func flushSSEHeadersForRequest(
	c *gin.Context,
	writer gin.ResponseWriter,
	deadlineController sseWriteDeadlineSetter,
	writeTimeout time.Duration,
) error {
	err := flushSSEHeaders(writer, deadlineController, writeTimeout)
	if err != nil {
		MarkRequestAuditFailure(c)
	}
	return err
}

type sseStreamLimits struct {
	heartbeatInterval time.Duration
	reauthInterval    time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
}

func configuredSSEStreamLimits() sseStreamLimits {
	return sseStreamLimits{
		heartbeatInterval: config.SSEHeartbeatInterval(),
		reauthInterval:    config.SSEReauthInterval(),
		writeTimeout:      config.SSEWriteTimeout(),
		idleTimeout:       config.SSEIdleTimeout(),
	}
}

func streamJenkinsSSE(
	requestContext context.Context,
	streamContext context.Context,
	writer io.Writer,
	deadlineController sseWriteDeadlineSetter,
	initialCursor int64,
	logChan <-chan jenkins.BuildLogChunk,
	resultChan <-chan error,
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
				return writeSSEJSON(writer, deadlineController, limits.writeTimeout, "end", &cursor,
					map[string]string{"reason": "max_duration"}) == nil
			}
			return requestContext.Err() != nil
		case <-idleTimer.C:
			_ = writeSSEJSON(writer, deadlineController, limits.writeTimeout, "end", &cursor,
				map[string]string{"reason": "upstream_idle"})
			return false
		case <-heartbeatTicker.C:
			if err := writeSSEJSON(writer, deadlineController, limits.writeTimeout, "ping", &cursor, struct{}{}); err != nil {
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
					return writeSSEJSON(writer, deadlineController, limits.writeTimeout, "end", &cursor,
						map[string]string{"reason": "max_duration"}) == nil
				}
				if errors.Is(err, ErrSSESessionExpired) {
					_ = writeSSEJSON(writer, deadlineController, limits.writeTimeout, "auth-expired", nil,
						map[string]string{"reason": "session_expired"})
				} else {
					_ = writeSSEJSON(writer, deadlineController, limits.writeTimeout, sseStreamErrorEvent, nil,
						map[string]string{"code": "session_revalidation_failed"})
				}
				return false
			}
		case chunk, ok := <-logChan:
			if !ok {
				logChan = nil
				continue
			}
			if chunk.NextStart < cursor {
				_ = writeSSEJSON(writer, deadlineController, limits.writeTimeout, sseStreamErrorEvent, nil,
					map[string]string{"code": "cursor_regression"})
				return false
			}
			cursor = chunk.NextStart
			resetTimer(idleTimer, limits.idleTimeout)
			// Jenkins' producer heartbeat proves that upstream polling is still
			// alive. The handler owns the client-visible heartbeat cadence.
			if chunk.IsPing {
				continue
			}
			response := util.ResponseSuccessful("", chunk.Lines)
			responseBytes, err := json.Marshal(response)
			if err != nil {
				return false
			}
			if err := writeSSEFrame(writer, deadlineController, limits.writeTimeout, "", &cursor, responseBytes); err != nil {
				return false
			}
		case streamErr, ok := <-resultChan:
			if !ok {
				streamErr = nil
			}
			if requestContext.Err() != nil {
				return true
			}
			if errors.Is(streamContext.Err(), context.DeadlineExceeded) {
				return writeSSEJSON(writer, deadlineController, limits.writeTimeout, "end", &cursor,
					map[string]string{"reason": "max_duration"}) == nil
			}
			if streamErr != nil {
				_ = writeSSEJSON(writer, deadlineController, limits.writeTimeout, sseStreamErrorEvent, nil,
					map[string]string{"code": "upstream_error"})
				// Do not follow an upstream failure with end: completed. Browsers may
				// already have both frames queued, in which case the terminal completed
				// event would race the retry scheduled by the stream-error handler and
				// permanently suppress cursor-based recovery.
				return false
			}
			writeErr := writeSSEJSON(writer, deadlineController, limits.writeTimeout, "end", &cursor,
				map[string]string{"reason": "completed"})
			return writeErr == nil
		}
	}
}

func validJenkinsStreamQuery(values url.Values) bool {
	allowed := map[string]struct{}{"task_id": {}, "log_type": {}, "start": {}}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 || entries[0] == "" {
			return false
		}
	}
	return true
}

func parseLastEventID(header http.Header) (int64, bool, error) {
	values, present := header[http.CanonicalHeaderKey("Last-Event-ID")]
	if !present {
		return 0, false, nil
	}
	if len(values) != 1 || values[0] == "" || len(values[0]) > 19 {
		return 0, true, errors.New("invalid Last-Event-ID")
	}
	for _, character := range []byte(values[0]) {
		if character < '0' || character > '9' {
			return 0, true, errors.New("invalid Last-Event-ID")
		}
	}
	parsed, err := strconv.ParseUint(values[0], 10, 63)
	if err != nil || parsed > math.MaxInt64 {
		return 0, true, errors.New("invalid Last-Event-ID")
	}
	return int64(parsed), true, nil
}

func sseSessionRevalidator(c *gin.Context) SSESessionRevalidator {
	value, exists := c.Get(sseSessionRevalidatorContextKey)
	if !exists {
		return nil
	}
	revalidator, _ := value.(SSESessionRevalidator)
	return revalidator
}

type sseWriteDeadlineSetter interface {
	SetWriteDeadline(time.Time) error
	Flush() error
}

func clearSSEWriteDeadline(controller sseWriteDeadlineSetter) error {
	return controller.SetWriteDeadline(time.Time{})
}

func flushSSEHeaders(writer gin.ResponseWriter, controller sseWriteDeadlineSetter, timeout time.Duration) error {
	return withSSEWriteDeadline(controller, timeout, func() error {
		writer.WriteHeaderNow()
		return controller.Flush()
	})
}

func writeSSEJSON(
	writer io.Writer,
	controller sseWriteDeadlineSetter,
	timeout time.Duration,
	event string,
	id *int64,
	value any,
) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeSSEFrame(writer, controller, timeout, event, id, data)
}

func writeSSEFrame(
	writer io.Writer,
	controller sseWriteDeadlineSetter,
	timeout time.Duration,
	event string,
	id *int64,
	data []byte,
) error {
	var frame strings.Builder
	if event != "" {
		_, _ = fmt.Fprintf(&frame, "event: %s\n", event)
	}
	if id != nil {
		_, _ = fmt.Fprintf(&frame, "id: %d\n", *id)
	}
	_, _ = fmt.Fprintf(&frame, "data: %s\n\n", data)
	return withSSEWriteDeadline(controller, timeout, func() error {
		if _, err := io.WriteString(writer, frame.String()); err != nil {
			return err
		}
		return controller.Flush()
	})
}

func withSSEWriteDeadline(controller sseWriteDeadlineSetter, timeout time.Duration, write func() error) (err error) {
	if timeout <= 0 {
		return errors.New("SSE write timeout must be positive")
	}
	if deadlineErr := controller.SetWriteDeadline(time.Now().Add(timeout)); deadlineErr != nil {
		return deadlineErr
	}
	defer func() {
		if clearErr := clearSSEWriteDeadline(controller); err == nil && clearErr != nil {
			err = clearErr
		}
	}()
	return write()
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}

func taskBuildLogReference(task entity.TaskRecord, logType string, start int64, currentAddress string) (*jenkins.BuildLogQuery, error) {
	storedAddress := strings.TrimRight(strings.TrimSpace(task.JenkinsAddress), "/")
	currentAddress = strings.TrimRight(strings.TrimSpace(currentAddress), "/")
	if storedAddress == "" {
		return nil, fmt.Errorf("任务 %d 未记录原 Jenkins 实例，无法安全查询历史日志", task.TaskId)
	}
	if currentAddress == "" || storedAddress != currentAddress {
		return nil, fmt.Errorf("任务 %d 的 Jenkins 实例与当前连接不匹配", task.TaskId)
	}
	query := &jenkins.BuildLogQuery{Start: start}
	switch strings.ToLower(strings.TrimSpace(logType)) {
	case "ci":
		query.JobName, query.BuildId = strings.TrimSpace(task.CiJobName), task.CiBuildId
	case "cd":
		query.JobName, query.BuildId = strings.TrimSpace(task.CdJobName), task.CdBuildId
	default:
		return nil, fmt.Errorf("log_type 只支持 ci 或 cd")
	}
	if query.JobName == "" || query.BuildId <= 0 {
		return nil, fmt.Errorf("任务 %d 的 %s Job/Build 引用不存在", task.TaskId, strings.ToUpper(logType))
	}
	return query, nil
}
