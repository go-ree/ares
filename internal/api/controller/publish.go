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
// @Summary 单应用进行下发布动作
// @Router	/api/v1/deploy/publish [post]
func (pc *PublishController) CreateBuildTask(c *gin.Context) {
	var req publish.CreatePublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, util.Response(400, "请求数据格式错误:"+err.Error(), ""))
		return
	}
	publishResult, err := pc.publishManager.CreatePublish(&req)
	if err != nil {
		c.JSON(200, util.Response(500, err.Error(), ""))
		return
	}
	c.JSON(200, util.Response(200, "", publishResult))
}

func (pc *PublishController) CreateBatchBuildTask(c *gin.Context) {
	var req publish.CreateBatchPublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, util.Response(400, "请求数据格式错误:"+err.Error(), ""))
		return
	}
	publishBatchResult, err := pc.publishManager.CreateBatchPublish(&req)
	if err != nil {
		c.JSON(200, util.Response(500, err.Error(), ""))
		return
	}
	c.JSON(200, util.Response(200, "", publishBatchResult))
}

// GetBuildTaskList 获取发布任务列表
func (pc *PublishController) GetBuildTaskList(c *gin.Context) {
	status, err := pc.publishManager.JobStatus()
	if err != nil {
		c.JSON(200, util.Response(500, err.Error(), ""))
		return
	}
	c.JSON(200, util.Response(200, "", status))

}

// CreateBuildTask 创建Job的构建任务
// 传入：应用名称、发布环境
// 返回：构建任务的创建结果
func CreateBuildTask(c *gin.Context) {
	var requestData struct {
		AppName string `json:"app_name"`
		Env     string `json:"env"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(200, util.Response(400, "请求数据格式错误:"+err.Error(), ""))
		return
	}
	slog.Info("Data", requestData)
	requestDataMap, err := tool.ToMapStringString(requestData)
	if err != nil {
		c.JSON(200, util.Response(400, "转换map类型错误:"+err.Error(), ""))
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
	c.JSON(200, util.Response(200, "", requestDataMap))
}
