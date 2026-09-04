package controller

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/app"
	"github.com/go-ree/ares/internal/environment"

	"github.com/gin-gonic/gin"
)

type AppConfigsController struct {
	cfgManager *app.ConfigManager
}

func NewAppConfigsController() *AppConfigsController {
	return &AppConfigsController{
		cfgManager: app.NewConfigManager(),
	}
}

// CreateAppConfig
// @Tags AppConfig
// @Summary 创建应用环境配置（app_id + env）
// @Param app_id path int true "应用ID"
// @Param request body app.CreateAppConfigRequest true "创建参数"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.AppConfigs} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "应用或环境不存在"
// @Failure 409 {object} util.ResponseTemplate{code=int} "环境已停用或配置已存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/{app_id}/configs [post]
func (cc *AppConfigsController) CreateAppConfig(c *gin.Context) {
	ctx := c.Request.Context()
	appIDStr := c.Param("app_id")
	appID, err := strconv.Atoi(appIDStr)
	if err != nil || appID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的应用ID", appIDStr))
		return
	}

	var req app.CreateAppConfigRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	row, err := cc.cfgManager.CreateAppConfigByAppEnv(ctx, appID, req)
	if err != nil {
		writeAppConfigError(c, "创建失败", err, "app_id", appID, "env", req.Env)
		return
	}
	c.JSON(200, util.ResponseSuccessful("创建成功", row))
}

// ListAppConfigs
// @Tags AppConfig
// @Summary 获取应用的所有环境配置
// @Param app_id path int true "应用ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]entity.AppConfigs} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/{app_id}/configs [get]
func (cc *AppConfigsController) ListAppConfigs(c *gin.Context) {
	ctx := c.Request.Context()
	appIDStr := c.Param("app_id")
	appID, err := strconv.Atoi(appIDStr)
	if err != nil || appID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的应用ID", appIDStr))
		return
	}

	rows, err := cc.cfgManager.ListAppConfigs(ctx, appID)
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", rows))
}

// GetAppConfigByEnv
// @Tags AppConfig
// @Summary 获取应用指定环境配置
// @Param app_id path int true "应用ID"
// @Param env path string true "环境目录中的环境代码"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.AppConfigs} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "配置不存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/{app_id}/configs/{env} [get]
func (cc *AppConfigsController) GetAppConfigByEnv(c *gin.Context) {
	ctx := c.Request.Context()
	appIDStr := c.Param("app_id")
	env := c.Param("env")
	appID, err := strconv.Atoi(appIDStr)
	if err != nil || appID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的应用ID", appIDStr))
		return
	}
	if env == "" {
		c.JSON(400, util.ResponseFailure("env 不能为空", ""))
		return
	}

	row, err := cc.cfgManager.GetAppConfigByAppEnv(ctx, appID, env)
	if err != nil {
		writeAppConfigError(c, "查询失败", err, "app_id", appID, "env", env)
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", row))
}

// PatchAppConfigByEnv
// @Tags AppConfig
// @Summary 更新应用指定环境配置（PATCH：只更新传入字段）
// @Param app_id path int true "应用ID"
// @Param env path string true "环境目录中的环境代码"
// @Param request body app.UpdateAppConfigRequest true "更新字段（指针语义）"
// @Success 200 {object} util.ResponseTemplate{code=int} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "配置或环境不存在"
// @Failure 409 {object} util.ResponseTemplate{code=int} "环境已停用"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/apps/{app_id}/configs/{env} [patch]
func (cc *AppConfigsController) PatchAppConfigByEnv(c *gin.Context) {
	ctx := c.Request.Context()
	appIDStr := c.Param("app_id")
	env := c.Param("env")
	appID, err := strconv.Atoi(appIDStr)
	if err != nil || appID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的应用ID", appIDStr))
		return
	}
	if env == "" {
		c.JSON(400, util.ResponseFailure("env 不能为空", ""))
		return
	}

	var req app.UpdateAppConfigRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	if err := cc.cfgManager.PatchAppConfigByAppEnv(ctx, appID, env, req); err != nil {
		writeAppConfigError(c, "更新失败", err, "app_id", appID, "env", env)
		return
	}
	c.JSON(200, util.ResponseSuccessful("更新成功", nil))
}

// GetAppConfigByID
// @Tags AppConfig
// @Summary 按 config_id 获取配置
// @Param config_id path int true "配置ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.AppConfigs} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "配置不存在"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id} [get]
func (cc *AppConfigsController) GetAppConfigByID(c *gin.Context) {
	ctx := c.Request.Context()
	idStr := c.Param("config_id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(400, util.ResponseFailure("无效的配置ID", idStr))
		return
	}

	row, err := cc.cfgManager.GetAppConfigByID(ctx, id)
	if err != nil {
		writeAppConfigError(c, "查询失败", err, "config_id", id)
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", row))
}

// PatchAppConfigByID
// @Tags AppConfig
// @Summary 按 config_id 更新配置（PATCH：只更新传入字段）
// @Param config_id path int true "配置ID"
// @Param request body app.UpdateAppConfigRequest true "更新字段（指针语义）"
// @Success 200 {object} util.ResponseTemplate{code=int} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 404 {object} util.ResponseTemplate{code=int} "配置或环境不存在"
// @Failure 409 {object} util.ResponseTemplate{code=int} "环境已停用"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id} [patch]
func (cc *AppConfigsController) PatchAppConfigByID(c *gin.Context) {
	ctx := c.Request.Context()
	idStr := c.Param("config_id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(400, util.ResponseFailure("无效的配置ID", idStr))
		return
	}

	var req app.UpdateAppConfigRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	if err := cc.cfgManager.PatchAppConfigByID(ctx, id, req); err != nil {
		writeAppConfigError(c, "更新失败", err, "config_id", id)
		return
	}
	c.JSON(200, util.ResponseSuccessful("更新成功", nil))
}

// writeAppConfigError 将可预期的领域错误与基础设施故障区分开。
// 只有未知错误返回 500 并记录错误日志，避免把正常的客户端冲突当作服务故障。
func writeAppConfigError(c *gin.Context, message string, err error, logArgs ...any) {
	status := http.StatusInternalServerError
	var validationError *app.ValidationError
	var appNotFoundError *app.AppNotFoundError
	var configNotFoundError *app.AppConfigNotFoundError
	var duplicateConfigError *app.DuplicateAppConfigError

	switch {
	case errors.As(err, &validationError):
		status = http.StatusBadRequest
	case errors.Is(err, environment.ErrNotFound),
		errors.As(err, &appNotFoundError),
		errors.As(err, &configNotFoundError):
		status = http.StatusNotFound
	case errors.Is(err, environment.ErrDisabled), errors.As(err, &duplicateConfigError):
		status = http.StatusConflict
	}

	if status == http.StatusInternalServerError {
		args := append([]any{"error", err}, logArgs...)
		slog.Error("应用环境配置操作失败", args...)
		c.JSON(status, util.ResponseFailure(message, "内部服务错误"))
		return
	}
	c.JSON(status, util.ResponseFailure(message, err.Error()))
}

// ListDomainsByConfigID
// @Tags AppConfig
// @Summary 获取指定 config_id 的多域名列表
// @Param config_id path int true "配置ID"
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]entity.AppConfigDomain} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id}/domains [get]
func (cc *AppConfigsController) ListDomainsByConfigID(c *gin.Context) {
	ctx := c.Request.Context()
	idStr := c.Param("config_id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(400, util.ResponseFailure("无效的配置ID", idStr))
		return
	}

	rows, err := cc.cfgManager.ListDomainsByConfigID(ctx, id)
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", rows))
}

// OverwriteDomainsByConfigID
// @Tags AppConfig
// @Summary 覆盖写入指定 config_id 的多域名
// @Param config_id path int true "配置ID"
// @Param request body app.UpsertDomainsRequest true "domains 列表"
// @Success 200 {object} util.ResponseTemplate{code=int} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id}/domains [put]
func (cc *AppConfigsController) OverwriteDomainsByConfigID(c *gin.Context) {
	ctx := c.Request.Context()
	idStr := c.Param("config_id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(400, util.ResponseFailure("无效的配置ID", idStr))
		return
	}

	var req app.UpsertDomainsRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	if err := cc.cfgManager.OverwriteDomainsByConfigID(ctx, id, req.Domains); err != nil {
		c.JSON(500, util.ResponseFailure("写入失败", err.Error()))
		slog.Error("覆盖写入多域名失败", "config_id", id, "error", err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("写入成功", nil))
}

// CreateDomain
// @Tags AppConfig
// @Summary 新增单条多域名配置
// @Param config_id path int true "配置ID"
// @Param request body app.DomainItem true "单条域名配置"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.AppConfigDomain} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id}/domains [post]
func (cc *AppConfigsController) CreateDomain(c *gin.Context) {
	ctx := c.Request.Context()
	idStr := c.Param("config_id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		c.JSON(400, util.ResponseFailure("无效的配置ID", idStr))
		return
	}

	var req app.DomainItem
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	row, err := cc.cfgManager.CreateDomain(ctx, id, req)
	if err != nil {
		c.JSON(500, util.ResponseFailure("新增失败", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("新增成功", row))
}

// DeleteDomain
// @Tags AppConfig
// @Summary 删除单条多域名配置
// @Param config_id path int true "配置ID"
// @Param domain_id path int true "域名记录ID"
// @Success 200 {object} util.ResponseTemplate{code=int} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id}/domains/{domain_id} [delete]
func (cc *AppConfigsController) DeleteDomain(c *gin.Context) {
	ctx := c.Request.Context()
	cfgStr := c.Param("config_id")
	cfgID, err := strconv.Atoi(cfgStr)
	if err != nil || cfgID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的配置ID", cfgStr))
		return
	}
	domainStr := c.Param("domain_id")
	domainID, err := strconv.ParseInt(domainStr, 10, 64)
	if err != nil || domainID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的域名记录ID", domainStr))
		return
	}

	if err := cc.cfgManager.DeleteDomainByID(ctx, cfgID, domainID); err != nil {
		c.JSON(500, util.ResponseFailure("删除失败", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("删除成功", nil))
}

// PatchDomain
// @Tags AppConfig
// @Summary 修改单条多域名配置（PATCH：只更新传入字段）
// @Param config_id path int true "配置ID"
// @Param domain_id path int true "域名记录ID"
// @Param request body app.PatchDomainRequest true "更新字段（指针语义）"
// @Success 200 {object} util.ResponseTemplate{code=int,result=entity.AppConfigDomain} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router /api/v1/app-configs/{config_id}/domains/{domain_id} [patch]
func (cc *AppConfigsController) PatchDomain(c *gin.Context) {
	ctx := c.Request.Context()
	cfgStr := c.Param("config_id")
	cfgID, err := strconv.Atoi(cfgStr)
	if err != nil || cfgID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的配置ID", cfgStr))
		return
	}
	domainStr := c.Param("domain_id")
	domainID, err := strconv.ParseInt(domainStr, 10, 64)
	if err != nil || domainID <= 0 {
		c.JSON(400, util.ResponseFailure("无效的域名记录ID", domainStr))
		return
	}

	var req app.PatchDomainRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}

	row, err := cc.cfgManager.PatchDomainByID(ctx, cfgID, domainID, req)
	if err != nil {
		c.JSON(500, util.ResponseFailure("修改失败", err.Error()))
		return
	}
	c.JSON(200, util.ResponseSuccessful("修改成功", row))
}
