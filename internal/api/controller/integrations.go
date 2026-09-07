package controller

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/integration"

	"github.com/gin-gonic/gin"
)

const (
	maxJenkinsSettingsRequestBytes    = 128 * 1024
	maxKubernetesSettingsRequestBytes = 4 * 1024 * 1024
)

// GetIntegrationSettings
// @Tags System
// @Summary 获取外部集成配置与运行状态（敏感字段不回显）
// @Success 200 {object} util.ResponseTemplate{code=int,result=integration.Snapshot}
// @Router /api/v1/system/integrations [get]
func GetIntegrationSettings(c *gin.Context) {
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", integration.SnapshotView()))
}

// UpdateJenkinsSettings
// @Tags System
// @Summary 保存并热加载 Jenkins 配置
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
		respondIntegrationSettingsFailure(c, "jenkins", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("Jenkins 配置已保存并生效", view))
}

// UpdateKubernetesSettings
// @Tags System
// @Summary 保存并热加载 Kubernetes 配置
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
		respondIntegrationSettingsFailure(c, "kubernetes", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("Kubernetes 配置已保存并生效", view))
}

func bindSettingsJSON(c *gin.Context, target any, maxBytes int64) bool {
	return BindJSON(c, target, maxBytes)
}

func respondIntegrationSettingsFailure(c *gin.Context, provider string, err error) {
	status := http.StatusUnprocessableEntity
	errorClass := "rejected"
	switch {
	case errors.Is(err, integration.ErrSettingsChanged):
		status = http.StatusConflict
		errorClass = "settings_changed"
	case errors.Is(err, integration.ErrCredentialReentryRequired):
		errorClass = "credential_reentry_required"
	case errors.Is(err, context.Canceled):
		errorClass = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		errorClass = "timeout"
	}
	slog.Warn("integration settings update rejected",
		"request_id", RequestID(c),
		"provider", provider,
		"error_class", errorClass,
	)
	publicError := "integration settings rejected"
	if errors.Is(err, integration.ErrCredentialReentryRequired) {
		publicError = "saved credential must be re-entered"
	}
	c.JSON(status, util.ResponseFailure("外部集成配置未生效", publicError))
}
