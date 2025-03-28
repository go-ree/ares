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

// CreateApp
// @Tags App
// @Summary 创建单应用
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.Apps} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/apps [post]
func (ac *AppsController) CreateApp(c *gin.Context) {
	ctx := c.Request.Context()
	var req app.CreateAppRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, util.ResponseFailure("请求数据格式错误", err.Error()))
		return
	}

	// 使用验证器模式进行业务规则校验
	validator := app.NewAppValidator()
	if err := validator.ValidateCreateApp(&req); err != nil {
		c.JSON(400, util.ResponseFailure("业务规则校验失败", err.Error()))
		return
	}

	// 使用事务确保数据一致性
	appResult, err := ac.appManager.CreateAppWithTx(ctx, &req)
	if err != nil {
		// 细分错误类型，提供更精确的HTTP状态码
		switch err.(type) {
		case *app.DuplicateAppError:
			c.JSON(409, util.ResponseFailure("应用已存在", err.Error()))
		case *app.ValidationError:
			c.JSON(400, util.ResponseFailure("参数验证失败", err.Error()))
		default:
			c.JSON(500, util.ResponseFailure("应用创建失败", err.Error()))
		}
		return
	}

	// 异步触发后续流程
	go ac.appManager.TriggerPostCreateHooks(appResult)

	c.JSON(200, util.ResponseSuccessful("应用基本信息创建成功，请根据实际发布参数调整", appResult))
}

// CreateApps
// @Tags App
// @Summary 批量创建应用
// @Success 200 {object} util.ResponseTemplate{code=int,result=app.CreateAppsResponse} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/apps/batch [post]
func (ac *AppsController) CreateApps(c *gin.Context) {
	var req app.CreateAppsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, util.ResponseFailure("请求数据格式错误", err.Error()))
		return
	}

	appsResult, err := ac.appManager.CreateApps(&req)
	if err != nil {
		c.JSON(500, util.ResponseFailure("", err.Error()))
		return
	}

	c.JSON(200, util.ResponseSuccessful("", appsResult))
}
