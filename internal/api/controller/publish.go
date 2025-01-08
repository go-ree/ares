package controller

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/util"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/publish"
	"net/http"
)

// BuildTask 处理前端请求，触发构建任务
func BuildTask(c *gin.Context) {
	// 识别携带的入参，触发对应的发布动作
	// appid、环境
	var requestData struct {
		Appid int    `json:"appid"`
		Env   string `json:"env"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseError("请求数据格式错误"+err.Error()))
		return
	}
	fmt.Println(requestData.Env, requestData.Appid)
	publish.PublishingEntry(requestData.Appid, requestData.Env)
}
