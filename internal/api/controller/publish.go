package controller

import (
	"ares/internal/api/util"
	"ares/internal/publish"
	"log/slog"
	"strconv"

	"github.com/gin-gonic/gin"
)

type PublishController struct {
	publishManager publish.PublishManager
}

func NewPublishController() *PublishController {
	return &PublishController{
		publishManager: publish.PublishManager{},
	}
}

// CreateBuildTask
// @Tags Publish
// @Summary 单应用进行发布动作
// @Param request body publish.CreatePublishRequest true "发布请求参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.TaskRecord} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/deploy/publish [post]
func (pc *PublishController) CreateBuildTask(c *gin.Context) {
	var req publish.CreatePublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, util.ResponseFailure("请求数据格式错误", err.Error()))
		return
	}
	publishResult, err := pc.publishManager.CreatePublish(&req)
	if err != nil {
		c.JSON(500, util.ResponseFailure("", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("发布任务创建成功", publishResult))
}

// CreateBatchBuildTask
// @Tags Publish
// @Summary 应用进行批量发布动作
// @Param request body publish.CreateBatchPublishRequest true "发布请求参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=publish.CreateBatchPublishResponse} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/deploy/publish/batch [post]
func (pc *PublishController) CreateBatchBuildTask(c *gin.Context) {
	var req publish.CreateBatchPublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, util.ResponseFailure("请求数据格式错误", err.Error()))
		return
	}
	publishBatchResult, err := pc.publishManager.CreateBatchPublish(&req)
	if err != nil {
		c.JSON(500, util.ResponseFailure("", err.Error()))
		return
	}
	if publishBatchResult.FailureCount != 0 {
		c.JSON(200, util.ResponseFailure("部分任务创建失败", publishBatchResult))
		return
	}
	c.JSON(200, util.ResponseSuccessful("发布任务创建成功", publishBatchResult))
}

// GetBuildTaskList
// @Tags Publish
// @Summary 获取发布中的任务列表
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]entity.TaskRecord} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/deploy/publish/status [get]
func (pc *PublishController) GetBuildTaskList(c *gin.Context) {
	status, err := pc.publishManager.JobStatus()
	if err != nil {
		c.JSON(500, util.ResponseFailure("", err.Error()))
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
	if err := c.ShouldBindJSON(&params); err != nil {
		c.JSON(400, util.ResponseFailure("请求参数格式错误", err.Error()))
		return
	}

	// 调用管理器查询
	result, err := pc.publishManager.QueryBuildPublish(ctx, params)
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		slog.Error("查询失败", "error", err)
		return
	}

	c.JSON(200, util.ResponseSuccessful("查询成功", result))
}

// QueryTaskRecordDetails
// @Tags Publish
// @Summary 查询构建任务详情
// @Description 根据任务的执行id，查询构建任务详情信息
// @Param task_id path int true "构建任务ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.TaskRecord} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/deploy/publish/query/{task_id} [get]
func (pc *PublishController) QueryTaskRecordDetails(c *gin.Context) {
	// 获取路径参数中的应用ID
	taskIDStr := c.Param("task_id")
	taskID, err := strconv.Atoi(taskIDStr)
	if err != nil {
		c.JSON(400, util.ResponseFailure("无效的任务ID", err.Error()))
		return
	}

	// 调用管理器查询
	result, err := pc.publishManager.GetTaskRecordDetails(taskID)
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		slog.Error("查询失败", "error", err)
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
	if err != nil {
		c.JSON(400, util.ResponseFailure("无效的任务ID", err.Error()))
		return
	}

	var req publish.UpsertTaskAppletImagesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, util.ResponseFailure("请求参数格式错误", err.Error()))
		return
	}

	if err := pc.publishManager.UpsertTaskAppletImages(taskID, req.AppletImages); err != nil {
		c.JSON(500, util.ResponseFailure("写入失败", err.Error()))
		slog.Error("写入任务图片失败", "task_id", taskID, "error", err)
		return
	}

	c.JSON(200, util.ResponseSuccessful("写入成功", nil))
}
