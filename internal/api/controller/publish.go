package controller

import (
	"github.com/gin-gonic/gin"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/util"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/publish"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/tool"
	"log/slog"
	"net/http"
)

type PublishController struct {
	publishManager publish.PublishManager
}

func NewPublishController() *PublishController {
	return &PublishController{
		publishManager: publish.PublishManager{},
	}
}

func (pc *PublishController) CreateBuildTask(c *gin.Context) {
	var req publish.CreatePublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, util.ResponseError("请求数据格式错误"+err.Error()))
		return
	}
	publishResult, err := pc.publishManager.CreatePublish(&req)
	if err != nil {
		c.JSON(500, util.ResponseError(err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccess(publishResult))

}

func (pc *PublishController) CreateBatchBuildTask(c *gin.Context) {
	var req publish.CreateBatchPublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, util.ResponseError("请求数据格式错误"+err.Error()))
		return
	}
	publishBatchResult, err := pc.publishManager.CreateBatchPublish(&req)
	if err != nil {
		c.JSON(500, util.ResponseError(err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccess(publishBatchResult))
}

// BuildTask godoc
// @Summary 创建单个构建任务
// @Description 创建新的构建任务
// @Tags 构建任务
// @Accept json
// @Produce json
// @Param request body publish.CreateAppRequest true "应用信息"
// @Success 200 {object} util.ResponseSuccess
// @Router /api/v1/deploy/publish [post]
func BuildTask(c *gin.Context) {
	// 识别携带的入参，触发对应的发布动作
	// appid、环境
	var requestData struct {
		AppName string `json:"app_name"`
		Env     string `json:"env"`
		Branch  string `json:"branch"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseError("请求数据格式错误"+err.Error()))
		return
	}
	err := publish.PublishingEntry(requestData.AppName, requestData.Branch, requestData.Env)
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccess("触发构建任务成功"))
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
		c.JSON(http.StatusBadRequest, util.ResponseError("请求数据格式错误"+err.Error()))
		return
	}
	slog.Info("Data", requestData)
	requestDataMap, err := tool.ToMapStringString(requestData)
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseError("转换map类型错误"+err.Error()))
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
	c.JSON(http.StatusOK, util.ResponseSuccess(requestDataMap))
}
