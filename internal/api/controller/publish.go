package controller

import (
	"log/slog"

	"ares/internal/api/util"
	"ares/internal/publish"
	"ares/internal/tool"
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
	c.JSON(200, util.ResponseSuccessful("", publishResult))
}

// CreateBatchBuildTask
// @Tags Publish
// @Summary 应用进行批量发布动作
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
	c.JSON(200, util.ResponseSuccessful("", publishBatchResult))
}

// GetBuildTaskList
// @Tags Publish
// @Summary 获取任务构建列表
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.TaskRecord} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/deploy/publish/jobs/status [get]
func (pc *PublishController) GetBuildTaskList(c *gin.Context) {
	status, err := pc.publishManager.JobStatus()
	if err != nil {
		c.JSON(500, util.ResponseFailure("", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("", status))
}

// CreateBuildTask
// @Tags Publish
// @Summary 创建构建任务
// @Success 200 {object} util.ResponseTemplate{code=int,result=map[string]string} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/deploy/publish/batch [get]
func CreateBuildTask(c *gin.Context) {
	var requestData struct {
		AppName string `json:"app_name"`
		Env     string `json:"env"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(400, util.ResponseFailure("请求数据格式错误", err.Error()))
		return
	}
	slog.Info("Data", requestData)
	requestDataMap, err := tool.ToMapStringString(requestData)
	if err != nil {
		c.JSON(400, util.ResponseFailure("转换map类型错误", err.Error()))
		return
	}
	slog.Info("构建的任务参数为：", requestData)
	slog.Info("构建的任务参数为：", requestDataMap)

	//jobBuildId, _, err := jenkins.CreateBuildTask("job", requestData)
	//if err != nil {
	//	c.JSON(http.StatusInternalServerError, util.ResponseError(err.Error()))
	//	return
	//}
	//c.JSON(http.StatusOK, util.ResponseSuccess(jobBuildId))
	c.JSON(200, util.ResponseSuccessful("", requestDataMap))
}
