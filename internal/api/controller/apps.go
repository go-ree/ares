package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/app"
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
// @Param request body app.CreateAppRequest true "创建单应用参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=app.CreateAppResult} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/apps [post]
func (ac *AppsController) CreateApp(c *gin.Context) {
	ctx := c.Request.Context()
	var req app.CreateAppRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	appResult, err := ac.appManager.CreateApp(ctx, req)
	if err != nil {
		// 细分错误类型，提供更精确的HTTP状态码
		handleAppCreationError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("应用基本信息创建成功，请根据实际发布参数调整应用配置", appResult))
}

// CreateApps
// @Tags App
// @Summary 批量创建应用
// @Param request body app.CreateAppsRequest true "批量创建应用参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=app.CreateAppsResponse} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/apps/batch [post]
func (ac *AppsController) CreateApps(c *gin.Context) {
	ctx := c.Request.Context()
	var req app.CreateAppsRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	appResult, err := ac.appManager.CreateApps(ctx, req)
	if err != nil {
		// 细分错误类型，提供更精确的HTTP状态码
		handleAppCreationError(c, err)
		return
	}
	if appResult.FailureCount != 0 {
		c.JSON(200, util.ResponseSuccessful("部分应用创建失败，请根据报错信息进行修改", appResult))
		return
	}
	c.JSON(200, util.ResponseSuccessful("应用基本信息创建成功，请根据实际发布参数调整应用配置", appResult))
}

// handleAppCreationError 辅助函数，处理应用创建错误
func handleAppCreationError(c *gin.Context, err error) {
	var duplicateAppError *app.DuplicateAppError
	var validationError *app.ValidationError
	switch {
	case errors.As(err, &validationError):
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数验证失败", validationError.Error()))
	case errors.As(err, &duplicateAppError):
		c.JSON(http.StatusBadRequest, util.ResponseFailure("应用已存在", duplicateAppError.Error()))
	default:
		writeInternalFailure(c, http.StatusInternalServerError, "应用创建失败", "database", "create_app", err)
	}
}

// QueryApps
// @Tags App
// @Summary 查询应用列表
// @Description 支持多条件组合查询应用列表，包括应用ID、应用名称、开发语言、负责人等
// @Param request body app.AppQuery true "应用查询参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=app.AppQueryResult} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/query [post]
func (ac *AppsController) QueryApps(c *gin.Context) {
	ctx := c.Request.Context()

	// 从请求中绑定查询参数
	var params app.AppQuery
	if !BindJSON(c, &params, defaultJSONRequestBytes) {
		return
	}

	// 调用应用管理器查询应用
	result, err := ac.appManager.QueryApps(ctx, params)
	if err != nil {
		ac.handleAppError(c, err)
		return
	}

	c.JSON(200, util.ResponseSuccessful("查询成功", result))
}

// GetAppByID
// @Tags App
// @Summary 根据ID获取应用详情
// @Description 根据应用ID获取单个应用的详细信息
// @Accept json
// @Produce json
// @Param app_id path int true "应用ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.Apps} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "应用不存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/{app_id} [get]
func (ac *AppsController) GetAppByID(c *gin.Context) {
	ctx := c.Request.Context()

	// 获取路径参数中的应用ID
	appIDStr := c.Param("app_id")
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("无效的应用ID", "invalid app id"))
		return
	}

	// 调用应用管理器获取应用
	apps, err := ac.appManager.GetAppByID(ctx, appID)
	if err != nil {
		ac.handleAppError(c, err)
		return
	}

	c.JSON(200, util.ResponseSuccessful("获取成功", apps))
}

// GetAppByName
// @Tags App
// @Summary 根据名称获取应用详情
// @Description 根据应用名称获取单个应用的详细信息
// @Accept json
// @Produce json
// @Param app_name path string true "应用名称"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.Apps} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "应用不存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/name/{app_name} [get]
func (ac *AppsController) GetAppByName(c *gin.Context) {
	ctx := c.Request.Context()

	// 获取路径参数中的应用名称
	appName := c.Param("app_name")
	if appName == "" {
		c.JSON(400, util.ResponseFailure("应用名称不能为空", ""))
		return
	}

	// 调用应用管理器获取应用
	apps, err := ac.appManager.GetAppByName(ctx, appName)
	if err != nil {
		ac.handleAppError(c, err)
		return
	}

	c.JSON(200, util.ResponseSuccessful("获取成功", apps))
}

// GetAppNameList
// @Tags App
// @Summary 获取全量应用名称列表
// @Description 获取全量应用名称列表
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]string} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "应用不存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/query/appname [get]
func (ac *AppsController) GetAppNameList(c *gin.Context) {
	ctx := c.Request.Context()

	appNames, err := ac.appManager.GetAppNameList(ctx)
	if err != nil {
		writeInternalFailure(c, http.StatusInternalServerError, "查询应用失败", "database", "list_app_names", err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("", appNames))
}

// GetAppConfigOptions
// @Tags AppConfig
// @Summary 获取应用环境配置可选项（按 dev_language 规则）
// @Description 用于前端下拉框：返回该应用 dev_language 下允许的 code_package_type 列表与默认值
// @Accept json
// @Produce json
// @Param app_id path int true "应用ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=app.AppConfigOptions} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/{app_id}/config-options [get]
func (ac *AppsController) GetAppConfigOptions(c *gin.Context) {
	ctx := c.Request.Context()
	appIDStr := c.Param("app_id")
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("无效的应用ID", "invalid app id"))
		return
	}
	opts, err := ac.appManager.GetAppConfigOptions(ctx, appID)
	if err != nil {
		ac.handleAppError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", opts))
}

// PatchAppByID
// @Tags App
// @Summary 应用基本信息变更（仅更新传入字段）
// @Description 根据应用ID更新应用基本信息（PATCH 指针语义），不允许修改 app_name/app_id
// @Accept json
// @Produce json
// @Param app_id path int true "应用ID"
// @Param request body app.PatchAppRequest true "更新字段（指针语义）"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.Apps} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "应用不存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/{app_id} [patch]
func (ac *AppsController) PatchAppByID(c *gin.Context) {
	ctx := c.Request.Context()

	appIDStr := c.Param("app_id")
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, util.ResponseFailure("无效的应用ID", "invalid app id"))
		return
	}

	var req app.PatchAppRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	row, err := ac.appManager.PatchAppByID(ctx, appID, req)
	if err != nil {
		ac.handleAppError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("更新成功", row))
}

// handleAppError 统一处理应用相关错误
func (ac *AppsController) handleAppError(c *gin.Context, err error) {
	var validationError *app.ValidationError
	var notFoundError *app.AppNotFoundError

	switch {
	case errors.As(err, &validationError):
		c.JSON(http.StatusBadRequest, util.ResponseFailure("参数验证失败", validationError.Error()))
	case errors.As(err, &notFoundError):
		c.JSON(http.StatusOK, util.ResponseFailure("应用不存在", notFoundError.Error()))
	default:
		writeInternalFailure(c, http.StatusInternalServerError, "获取应用失败", "database", "get_app", err)
	}
}
