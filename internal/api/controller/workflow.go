package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/workflow"

	"github.com/gin-gonic/gin"
	"xorm.io/xorm"
)

const maxWorkflowRequestBytes = 512 * 1024

type WorkflowController struct {
	service     *workflow.Service
	coordinator *workflow.Coordinator
	logs        *workflow.LogService
}

func NewWorkflowController(service *workflow.Service, coordinator *workflow.Coordinator, logs ...*workflow.LogService) *WorkflowController {
	controller := &WorkflowController{service: service, coordinator: coordinator}
	if len(logs) > 0 {
		controller.logs = logs[0]
	}
	return controller
}

// NewDefaultWorkflowController wires the built-in registry and XORM store. A
// caller that adds Jenkins or another executor should build a Registry itself
// and use NewWorkflowController instead.
func NewDefaultWorkflowController(engine *xorm.Engine) *WorkflowController {
	registry := workflow.DefaultRegistry()
	store := workflow.NewXORMStore(engine)
	return NewWorkflowController(
		workflow.NewService(store, registry),
		workflow.NewCoordinator(store, registry),
		workflow.NewLogService(store, registry),
	)
}

// ListPipelineStepTypes
// @Tags Pipeline
// @Summary 获取流水线步骤类型目录
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]workflow.Descriptor}
// @Router /api/v1/pipeline-step-types [get]
func (wc *WorkflowController) ListPipelineStepTypes(c *gin.Context) {
	descriptors := wc.service.Registry().Descriptors(c.Request.Context())
	for index := range descriptors {
		if !descriptors[index].Available {
			descriptors[index].UnavailableReason = "executor unavailable"
		}
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", descriptors))
}

// GetAppConfigWorkflow
// @Tags Pipeline
// @Summary 获取应用环境配置当前绑定的工作流
// @Param config_id path int true "应用环境配置 ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=workflow.WorkflowView}
// @Failure 404 {object} util.ResponseTemplate{code=int}
// @Router /api/v1/app-configs/{config_id}/workflow [get]
func (wc *WorkflowController) GetAppConfigWorkflow(c *gin.Context) {
	configID, ok := positivePathID(c, "config_id", "配置ID")
	if !ok {
		return
	}
	view, err := wc.service.GetCurrent(c.Request.Context(), configID)
	if err != nil {
		writeWorkflowError(c, "查询工作流失败", err)
		return
	}
	principal, _ := CurrentPrincipal(c)
	if !principal.Has(auth.PermissionWorkflowsWrite) {
		view = redactWorkflowExecutorConfig(view)
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", view))
}

func redactWorkflowExecutorConfig(view workflow.WorkflowView) workflow.WorkflowView {
	view.Spec.Steps = append([]workflow.StepSpec(nil), view.Spec.Steps...)
	for index := range view.Spec.Steps {
		view.Spec.Steps[index].With = json.RawMessage(`{}`)
	}
	return view
}

type putWorkflowRequest struct {
	Revision int                   `json:"revision"`
	Spec     workflow.WorkflowSpec `json:"spec"`
}

// PutAppConfigWorkflow
// @Tags Pipeline
// @Summary 发布新的不可变工作流版本并切换应用环境绑定
// @Param config_id path int true "应用环境配置 ID"
// @Param request body putWorkflowRequest true "工作流与当前 revision"
// @Success 200 {object} util.ResponseTemplate{code=int,result=workflow.WorkflowView}
// @Failure 409 {object} util.ResponseTemplate{code=int}
// @Failure 422 {object} util.ResponseTemplate{code=int}
// @Router /api/v1/app-configs/{config_id}/workflow [put]
func (wc *WorkflowController) PutAppConfigWorkflow(c *gin.Context) {
	principal, ok := CurrentPrincipal(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, util.ResponseFailure("认证主体不可用", "unauthenticated"))
		return
	}
	configID, ok := positivePathID(c, "config_id", "配置ID")
	if !ok {
		return
	}
	var request putWorkflowRequest
	if !BindJSON(c, &request, maxWorkflowRequestBytes) {
		return
	}
	actor := strings.TrimSpace(principal.DisplayName)
	if actor == "" {
		actor = strings.TrimSpace(principal.Username)
	}
	if actor == "" {
		c.JSON(http.StatusUnauthorized, util.ResponseFailure("认证主体不可用", "unauthenticated"))
		return
	}
	actorRunes := []rune(actor)
	if len(actorRunes) > 100 {
		actor = string(actorRunes[:100])
	}
	view, err := wc.service.Save(c.Request.Context(), configID, request.Revision, actor, principal.UserID, request.Spec)
	if err != nil {
		writeWorkflowError(c, "保存工作流失败", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("工作流版本已发布", view))
}

// GetTaskSteps
// @Tags Publish
// @Summary 获取发布任务的通用步骤快照
// @Param task_id path int true "任务 ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]workflow.TaskStepView}
// @Router /api/v1/deploy/publish/query/{task_id}/steps [get]
func (wc *WorkflowController) GetTaskSteps(c *gin.Context) {
	taskID, ok := positivePathID(c, "task_id", "任务ID")
	if !ok {
		return
	}
	steps, err := wc.coordinator.ListTaskStepViews(c.Request.Context(), taskID)
	if err != nil {
		writeWorkflowError(c, "查询任务步骤失败", err)
		return
	}
	if steps == nil {
		steps = make([]workflow.TaskStepView, 0)
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", steps))
}

func positivePathID(c *gin.Context, name, label string) (int, bool) {
	raw := c.Param(name)
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("无效的"+label, raw))
		return 0, false
	}
	return value, true
}

// GetTaskAttempts
// @Tags Publish
// @Summary 获取步骤执行尝试历史（不包含内部引用或输出）
// @Param task_id path int true "任务 ID"
// @Param step_key path string true "步骤标识"
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]workflow.AttemptView}
// @Router /api/v1/tasks/{task_id}/steps/{step_key}/attempts [get]
func (wc *WorkflowController) GetTaskAttempts(c *gin.Context) {
	taskID, ok := positivePathID(c, "task_id", "任务ID")
	if !ok {
		return
	}
	key := c.Param("step_key")
	if !workflow.ValidStepKey(key) {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("步骤标识无效", "invalid_request"))
		return
	}
	rows, err := wc.coordinator.ListAttempts(c.Request.Context(), taskID, key)
	if err != nil {
		writeWorkflowError(c, "查询尝试历史失败", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", rows))
}

type retryTaskStepRequest struct {
	ExpectedAttempt int `json:"expected_attempt"`
}

// RetryTaskStep
// @Tags Publish
// @Summary 请求安全重试失败步骤
// @Param task_id path int true "任务 ID"
// @Param step_key path string true "步骤标识"
// @Param request body retryTaskStepRequest true "预期尝试编号"
// @Success 202 {object} util.ResponseTemplate{code=int}
// @Failure 409 {object} util.ResponseTemplate{code=int}
// @Router /api/v1/tasks/{task_id}/steps/{step_key}/retry [post]
func (wc *WorkflowController) RetryTaskStep(c *gin.Context) {
	taskID, ok := positivePathID(c, "task_id", "任务ID")
	if !ok {
		return
	}
	key := c.Param("step_key")
	var request retryTaskStepRequest
	if !BindJSON(c, &request, 1024) {
		return
	}
	if !workflow.ValidStepKey(key) || request.ExpectedAttempt < 1 || request.ExpectedAttempt >= 5 {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("重试参数无效", "invalid_request"))
		return
	}
	if err := wc.coordinator.RequestRetry(c.Request.Context(), taskID, key, request.ExpectedAttempt); err != nil {
		writeWorkflowError(c, "无法重试步骤", err)
		return
	}
	c.JSON(http.StatusAccepted, util.ResponseSuccessful("已进入重试等待", nil))
}

func writeWorkflowError(c *gin.Context, message string, err error) {
	var validation *workflow.ValidationError
	switch {
	case errors.Is(err, workflow.ErrNotFound):
		c.JSON(http.StatusNotFound, util.ResponseFailure(message, workflow.ErrNotFound.Error()))
	case errors.Is(err, workflow.ErrRevisionConflict):
		c.JSON(http.StatusConflict, util.ResponseFailure(message, workflow.ErrRevisionConflict.Error()))
	case errors.Is(err, workflow.ErrRetryConflict):
		c.JSON(http.StatusConflict, util.ResponseFailure(message, workflow.ErrRetryConflict.Error()))
	case errors.Is(err, workflow.ErrLegacyTask):
		c.JSON(http.StatusConflict, util.ResponseFailure(message, workflow.ErrLegacyTask.Error()))
	case errors.As(err, &validation):
		c.JSON(http.StatusUnprocessableEntity, util.ResponseFailure(message, validation.Error()))
	default:
		writeInternalFailure(c, http.StatusInternalServerError, message, "database", "workflow", err)
	}
}
