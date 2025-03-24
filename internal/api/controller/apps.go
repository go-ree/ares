package controller

import (
	"github.com/gin-gonic/gin"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/util"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/app"
)

type AppsController struct {
	appManager app.AppManager
}

func NewAppsController() *AppsController {
	return &AppsController{
		appManager: app.AppManager{},
	}
}

// CreateApp godoc
// @Summary 创建单个应用
// @Description 创建新的应用
// @Tags 应用管理
// @Accept json
// @Produce json
// @Param request body publish.CreateAppRequest true "应用信息"
// @Success 200 {object} publish.App
// @Router /api/v1/apps [post]
func (ac *AppsController) CreateApp(c *gin.Context) {
	var req app.CreateAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, util.Response(400, "请求数据格式错误:"+err.Error(), ""))
		return
	}

	appResult, err := ac.appManager.CreateApp(&req)
	if err != nil {
		c.JSON(200, util.Response(500, err.Error(), ""))
		return
	}

	c.JSON(200, util.Response(200, "", appResult))
}

// CreateApps godoc
// @Summary 批量创建应用
// @Description 批量创建多个应用
// @Tags 应用管理
// @Accept json
// @Produce json
// @Param request body publish.CreateAppsRequest true "应用信息列表"
// @Success 200 {array} publish.App
// @Router /api/v1/apps/batch [post]
func (ac *AppsController) CreateApps(c *gin.Context) {
	var req app.CreateAppsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(200, util.Response(400, err.Error(), ""))
		return
	}

	appsResult, err := ac.appManager.CreateApps(&req)
	if err != nil {
		c.JSON(200, util.Response(500, err.Error(), ""))
		return
	}

	c.JSON(200, util.Response(200, "", appsResult))
}
