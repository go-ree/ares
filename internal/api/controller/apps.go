package controller

import (
	"ares/internal/api/util"
	"ares/internal/app"
	"github.com/gin-gonic/gin"
)

type AppsController struct {
	appManager app.AppManager
}

func NewAppsController() *AppsController {
	return &AppsController{
		appManager: app.AppManager{},
	}
}

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
