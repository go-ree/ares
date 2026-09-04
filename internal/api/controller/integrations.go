package controller

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/config"
	"github.com/go-ree/ares/internal/integration"

	"github.com/gin-gonic/gin"
)

const settingsAdminTokenHeader = "X-Ares-Admin-Token"

const (
	maxJenkinsSettingsRequestBytes    = 128 * 1024
	maxKubernetesSettingsRequestBytes = 4 * 1024 * 1024
)

// RequireSettingsAdminToken protects integration credentials until Ares has a
// real server-side authentication and RBAC implementation.
func RequireSettingsAdminToken(c *gin.Context) {
	expected := config.SettingsAdminToken()
	if expected == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, util.ResponseFailure(
			"系统配置接口未启用",
			"set ARES_SETTINGS_ADMIN_TOKEN before managing integration settings",
		))
		return
	}
	provided := c.GetHeader(settingsAdminTokenHeader)
	if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, util.ResponseFailure("管理员令牌无效", "unauthorized"))
		return
	}
	c.Next()
}

// GetIntegrationSettings
// @Tags System
// @Summary 获取外部集成配置与运行状态（敏感字段不回显）
// @Param X-Ares-Admin-Token header string true "系统设置管理员令牌"
// @Success 200 {object} util.ResponseTemplate{code=int,result=integration.Snapshot}
// @Router /api/v1/system/integrations [get]
func GetIntegrationSettings(c *gin.Context) {
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", integration.SnapshotView()))
}

// UpdateJenkinsSettings
// @Tags System
// @Summary 保存并热加载 Jenkins 配置
// @Param X-Ares-Admin-Token header string true "系统设置管理员令牌"
// @Param request body integration.UpdateJenkinsRequest true "Jenkins 配置"
// @Success 200 {object} util.ResponseTemplate{code=int,result=integration.JenkinsView}
// @Router /api/v1/system/integrations/jenkins [put]
func UpdateJenkinsSettings(c *gin.Context) {
	var req integration.UpdateJenkinsRequest
	if !bindSettingsJSON(c, &req, maxJenkinsSettingsRequestBytes) {
		return
	}
	view, err := integration.UpdateJenkins(c.Request.Context(), req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, integration.ErrSettingsChanged) {
			status = http.StatusConflict
		}
		c.JSON(status, util.ResponseFailure("Jenkins 配置未生效", err.Error()))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("Jenkins 配置已保存并生效", view))
}

// UpdateKubernetesSettings
// @Tags System
// @Summary 保存并热加载 Kubernetes 配置
// @Param X-Ares-Admin-Token header string true "系统设置管理员令牌"
// @Param request body integration.UpdateKubernetesRequest true "Kubernetes 配置"
// @Success 200 {object} util.ResponseTemplate{code=int,result=integration.KubernetesView}
// @Router /api/v1/system/integrations/kubernetes [put]
func UpdateKubernetesSettings(c *gin.Context) {
	var req integration.UpdateKubernetesRequest
	if !bindSettingsJSON(c, &req, maxKubernetesSettingsRequestBytes) {
		return
	}
	view, err := integration.UpdateKubernetes(c.Request.Context(), req)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, integration.ErrSettingsChanged) {
			status = http.StatusConflict
		}
		c.JSON(status, util.ResponseFailure("Kubernetes 配置未生效", err.Error()))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("Kubernetes 配置已保存并生效", view))
}

func bindSettingsJSON(c *gin.Context, target any, maxBytes int64) bool {
	return BindJSON(c, target, maxBytes)
}
