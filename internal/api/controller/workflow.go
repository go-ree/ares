package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/entity"
	"github.com/go-ree/ares/internal/workflow"

	"github.com/gin-gonic/gin"
	"xorm.io/xorm"
)

const maxWorkflowRequestBytes = 512 * 1024

type WorkflowController struct {
	service     *workflow.Service
	coordinator *workflow.Coordinator
}

func NewWorkflowController(service *workflow.Service, coordinator *workflow.Coordinator) *WorkflowController {
	return &WorkflowController{service: service, coordinator: coordinator}
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
	)
}

// ListPipelineStepTypes
// @Tags Pipeline
// @Summary 获取流水线步骤类型目录
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]workflow.Descriptor}
// @Router /api/v1/pipeline-step-types [get]
func (wc *WorkflowController) ListPipelineStepTypes(c *gin.Context) {
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", wc.service.Registry().Descriptors(c.Request.Context())))
}

// GetAppConfigWorkflow
// @Tags Pipeline
// @Summary 获取应用环境配置当前绑定的工作流
// @Param config_id path int true "应用环境配置 ID"
// @Param X-Ares-Admin-Token header string true "系统设置管理员令牌"
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
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", view))
}

type putWorkflowRequest struct {
	Revision int                   `json:"revision"`
	Spec     workflow.WorkflowSpec `json:"spec"`
}

// PutAppConfigWorkflow
// @Tags Pipeline
// @Summary 发布新的不可变工作流版本并切换应用环境绑定
// @Param config_id path int true "应用环境配置 ID"
// @Param X-Ares-Admin-Token header string true "系统设置管理员令牌"
// @Param request body putWorkflowRequest true "工作流与当前 revision"
// @Success 200 {object} util.ResponseTemplate{code=int,result=workflow.WorkflowView}
// @Failure 409 {object} util.ResponseTemplate{code=int}
// @Failure 422 {object} util.ResponseTemplate{code=int}
// @Router /api/v1/app-configs/{config_id}/workflow [put]
func (wc *WorkflowController) PutAppConfigWorkflow(c *gin.Context) {
	configID, ok := positivePathID(c, "config_id", "配置ID")
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWorkflowRequestBytes)
	var request putWorkflowRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, util.ResponseFailure("请求数据过大", err.Error()))
			return
		}
		c.JSON(http.StatusBadRequest, util.ResponseFailure("请求参数格式错误", err.Error()))
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("请求参数格式错误", "请求只能包含一个 JSON 对象"))
		return
	}
	view, err := wc.service.Save(c.Request.Context(), configID, request.Revision, "settings-admin", request.Spec)
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
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]entity.TaskStepRecord}
// @Router /api/v1/deploy/publish/query/{task_id}/steps [get]
func (wc *WorkflowController) GetTaskSteps(c *gin.Context) {
	taskID, ok := positivePathID(c, "task_id", "任务ID")
	if !ok {
		return
	}
	steps, err := wc.coordinator.ListTaskSteps(c.Request.Context(), taskID)
	if err != nil {
		writeWorkflowError(c, "查询任务步骤失败", err)
		return
	}
	if steps == nil {
		steps = make([]entity.TaskStepRecord, 0)
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

func writeWorkflowError(c *gin.Context, message string, err error) {
	status := http.StatusInternalServerError
	var validation *workflow.ValidationError
	switch {
	case errors.Is(err, workflow.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, workflow.ErrRevisionConflict):
		status = http.StatusConflict
	case errors.As(err, &validation):
		status = http.StatusUnprocessableEntity
	}
	c.JSON(status, util.ResponseFailure(message, fmt.Sprint(err)))
}
