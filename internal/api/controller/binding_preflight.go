package controller

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/pipelinebinding"
)

type BindingPreflightService interface {
	Check(context.Context, pipelinebinding.Intent) (pipelinebinding.Preflight, error)
}
type BindingPreflightController struct{ service BindingPreflightService }

func NewBindingPreflightController(service BindingPreflightService) *BindingPreflightController {
	if service == nil && db.Engine != nil {
		service = pipelinebinding.New(db.Engine.DB().DB)
	}
	return &BindingPreflightController{service: service}
}

type CIBindingPreflightRequest struct {
	VersionID       string                     `json:"version_id"`
	ApplicationType string                     `json:"application_type"`
	Parameters      map[string]json.RawMessage `json:"parameters,omitempty" swaggertype:"object"`
}
type CDBindingPreflightRequest struct {
	VersionID  string                     `json:"version_id"`
	TargetType string                     `json:"target_type"`
	Parameters map[string]json.RawMessage `json:"parameters,omitempty" swaggertype:"object"`
}

// CI validates an intent, never stores a binding or creates a run.
// @Tags Pipeline
// @Summary 预检应用 CI 固定版本绑定意图（不保存、不执行）
// @Param app_id path string true "应用ID"
// @Param request body CIBindingPreflightRequest true "意图"
// @Success 200 {object} util.ResponseTemplate{result=pipelinebinding.Preflight}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/apps/{app_id}/ci-binding/preflight [post]
func (bc *BindingPreflightController) CI(c *gin.Context) {
	var req CIBindingPreflightRequest
	if !BindCanonicalJSON(c, &req, pipelinebinding.MaxParameterBytes+4096) {
		return
	}
	bc.check(c, "ci", "app_id", req.VersionID, req.ApplicationType, req.Parameters)
}

// CD validates the environment and selected CD version without input artifacts.
// @Tags Pipeline
// @Summary 预检环境 CD 固定版本绑定意图（不保存、不执行）
// @Param config_id path string true "应用环境配置ID"
// @Param request body CDBindingPreflightRequest true "意图"
// @Success 200 {object} util.ResponseTemplate{result=pipelinebinding.Preflight}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/app-configs/{config_id}/cd-binding/preflight [post]
func (bc *BindingPreflightController) CD(c *gin.Context) {
	var req CDBindingPreflightRequest
	if !BindCanonicalJSON(c, &req, pipelinebinding.MaxParameterBytes+4096) {
		return
	}
	bc.check(c, "cd", "config_id", req.VersionID, req.TargetType, req.Parameters)
}
func (bc *BindingPreflightController) check(c *gin.Context, kind, param, version, ownership string, parameters map[string]json.RawMessage) {
	targetID, ok := decimal(c.Param(param), math.MaxInt32)
	if !ok {
		catalogBadRequest(c)
		return
	}
	versionID, ok := decimal(version, math.MaxInt64)
	if !ok {
		catalogBadRequest(c)
		return
	}
	if bc.service == nil {
		c.JSON(503, util.ResponseFailure("绑定预检不可用", "binding_preflight_unavailable"))
		return
	}
	result, err := bc.service.Check(c.Request.Context(), pipelinebinding.Intent{Kind: kind, TargetID: int64(targetID), VersionID: int64(versionID), Ownership: ownership, Parameters: parameters})
	if err != nil {
		status, code := http.StatusServiceUnavailable, "binding_preflight_unavailable"
		switch {
		case errors.Is(err, pipelinebinding.ErrTarget):
			status, code = 404, "binding_resource_not_found"
		case errors.Is(err, pipelinebinding.ErrDisabled):
			status, code = 409, "binding_resource_disabled"
		case errors.Is(err, pipelinebinding.ErrMismatch):
			status, code = 422, "binding_ownership_mismatch"
		case errors.Is(err, pipelinebinding.ErrParameters):
			status, code = 422, "invalid_binding_parameters"
		}
		c.JSON(status, util.ResponseFailure("绑定预检失败", code))
		return
	}
	// This endpoint can never promise persistence or execution, even if a future
	// service adapter accidentally populates those flags.
	result.Persisted = false
	result.Executable = false
	result.ValidationScope = "binding_intent_only"
	c.JSON(200, util.ResponseSuccessful("意图预检通过，未保存绑定且尚不可执行", result))
}
