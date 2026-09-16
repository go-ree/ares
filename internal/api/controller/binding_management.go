package controller

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/pipelinebinding"
)

// BindingStore persists the fixed-version CI/CD bindings. It never starts a
// run, registers an artifact or adapts a legacy workflow.
type BindingStore interface {
	Save(context.Context, pipelinebinding.SaveIntent) (pipelinebinding.Binding, error)
	Get(context.Context, string, int64) (pipelinebinding.Binding, error)
	ReadParameters(context.Context, string, int64) (pipelinebinding.StoredParameters, error)
}

type BindingManagementController struct{ store BindingStore }

func NewBindingManagementController(store BindingStore) *BindingManagementController {
	if store == nil && db.Engine != nil {
		store = pipelinebinding.NewStore(db.Engine.DB().DB)
	}
	return &BindingManagementController{store: store}
}

type SaveCIBindingRequest struct {
	VersionID        string                     `json:"version_id"`
	ApplicationType  string                     `json:"application_type"`
	Parameters       map[string]json.RawMessage `json:"parameters,omitempty" swaggertype:"object"`
	Enabled          *bool                      `json:"enabled"`
	ExpectedRevision string                     `json:"expected_revision,omitempty"`
}
type SaveCDBindingRequest struct {
	VersionID        string                     `json:"version_id"`
	TargetType       string                     `json:"target_type"`
	Parameters       map[string]json.RawMessage `json:"parameters,omitempty" swaggertype:"object"`
	Enabled          *bool                      `json:"enabled"`
	ExpectedRevision string                     `json:"expected_revision,omitempty"`
}

// BindingView is binding metadata. The stored parameter layer is deliberately
// absent: reading it is a separate, audited operation.
type BindingView struct {
	BindingID     string    `json:"binding_id"`
	Kind          string    `json:"kind"`
	TargetID      string    `json:"target_id"`
	VersionID     string    `json:"version_id"`
	TemplateID    string    `json:"template_id"`
	VersionNumber string    `json:"version_number"`
	Checksum      string    `json:"checksum"`
	Enabled       bool      `json:"enabled"`
	Revision      string    `json:"revision"`
	UpdatedBy     string    `json:"updated_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Persisted     bool      `json:"persisted"`
	Executable    bool      `json:"executable"`
}
type BindingParametersView struct {
	BindingID  string                     `json:"binding_id"`
	Revision   string                     `json:"revision"`
	Parameters map[string]json.RawMessage `json:"parameters" swaggertype:"object"`
	Executable bool                       `json:"executable"`
}

func bindingView(v pipelinebinding.Binding) BindingView {
	return BindingView{
		BindingID:     strconv.FormatInt(v.ID, 10),
		Kind:          v.Kind,
		TargetID:      strconv.FormatInt(v.TargetID, 10),
		VersionID:     strconv.FormatInt(v.VersionID, 10),
		TemplateID:    strconv.FormatInt(v.TemplateID, 10),
		VersionNumber: strconv.FormatUint(v.VersionNumber, 10),
		Checksum:      v.Checksum,
		Enabled:       v.Enabled,
		Revision:      strconv.FormatUint(v.Revision, 10),
		UpdatedBy:     strconv.FormatInt(v.UpdatedBy, 10),
		CreatedAt:     v.CreatedAt,
		UpdatedAt:     v.UpdatedAt,
		Persisted:     true,
	}
}

func bindingError(c *gin.Context, err error) {
	status, code := http.StatusServiceUnavailable, "binding_store_unavailable"
	switch {
	case errors.Is(err, pipelinebinding.ErrTarget):
		status, code = 404, "binding_resource_not_found"
	case errors.Is(err, pipelinebinding.ErrDisabled):
		status, code = 409, "binding_resource_disabled"
	case errors.Is(err, pipelinebinding.ErrConflict):
		status, code = 409, "binding_conflict"
	case errors.Is(err, pipelinebinding.ErrMismatch):
		status, code = 422, "binding_ownership_mismatch"
	case errors.Is(err, pipelinebinding.ErrParameters):
		status, code = 422, "invalid_binding_parameters"
	case errors.Is(err, pipelinebinding.ErrDefinition):
		status, code = 503, "binding_store_unavailable"
	}
	c.JSON(status, util.ResponseFailure("绑定操作失败", code))
}

// bindingTargetID parses the path identifier and rejects a missing store before
// touching the database.
func (bc *BindingManagementController) target(c *gin.Context, param string) (int64, bool) {
	if bc.store == nil {
		c.JSON(503, util.ResponseFailure("绑定存储不可用", "binding_store_unavailable"))
		return 0, false
	}
	id, ok := decimal(c.Param(param), math.MaxInt32)
	if !ok {
		catalogBadRequest(c)
		return 0, false
	}
	return int64(id), true
}

// expectedRevision treats an absent field as "no binding may exist yet".
// Creating and rebinding are therefore the same fail-closed CAS operation.
func expectedRevision(c *gin.Context, raw string) (uint64, bool) {
	if raw == "" {
		return 0, true
	}
	return revision(c, raw)
}

func (bc *BindingManagementController) save(c *gin.Context, kind, param, version, ownership string, parameters map[string]json.RawMessage, enabled *bool, expected uint64) {
	if enabled == nil {
		catalogBadRequest(c)
		return
	}
	targetID, ok := bc.target(c, param)
	if !ok {
		return
	}
	versionID, ok := decimal(version, math.MaxInt64)
	if !ok {
		catalogBadRequest(c)
		return
	}
	actor, ok := catalogActor(c)
	if !ok {
		return
	}
	result, err := bc.store.Save(c.Request.Context(), pipelinebinding.SaveIntent{
		Kind: kind, TargetID: targetID, VersionID: int64(versionID), Ownership: ownership,
		Parameters: parameters, Enabled: *enabled, Expected: expected, Actor: actor,
	})
	if err != nil {
		bindingError(c, err)
		return
	}
	view := bindingView(result)
	// B2 stores intent only. No executor consumes a binding yet, so the response
	// must never imply that a run is possible.
	view.Executable = false
	status, message := 200, "绑定已更新，尚不可执行"
	if expected == 0 {
		status, message = 201, "绑定已保存，尚不可执行"
	}
	c.JSON(status, util.ResponseSuccessful(message, view))
}

// SaveCI creates or replaces one application CI binding under revision CAS.
// @Tags Pipeline
// @Summary 保存应用 CI 固定版本绑定（不执行）
// @Param app_id path string true "应用ID"
// @Param request body SaveCIBindingRequest true "固定版本、参数与预期revision"
// @Success 200,201 {object} util.ResponseTemplate{result=BindingView}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/apps/{app_id}/ci-binding [put]
func (bc *BindingManagementController) SaveCI(c *gin.Context) {
	var req SaveCIBindingRequest
	if !BindCanonicalJSON(c, &req, pipelinebinding.MaxParameterBytes+4096) {
		return
	}
	expected, ok := expectedRevision(c, req.ExpectedRevision)
	if !ok {
		return
	}
	bc.save(c, "ci", "app_id", req.VersionID, req.ApplicationType, req.Parameters, req.Enabled, expected)
}

// SaveCD creates or replaces one application configuration CD binding.
// @Tags Pipeline
// @Summary 保存环境 CD 固定版本绑定（不执行）
// @Param config_id path string true "应用环境配置ID"
// @Param request body SaveCDBindingRequest true "固定版本、参数与预期revision"
// @Success 200,201 {object} util.ResponseTemplate{result=BindingView}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/app-configs/{config_id}/cd-binding [put]
func (bc *BindingManagementController) SaveCD(c *gin.Context) {
	var req SaveCDBindingRequest
	if !BindCanonicalJSON(c, &req, pipelinebinding.MaxParameterBytes+4096) {
		return
	}
	expected, ok := expectedRevision(c, req.ExpectedRevision)
	if !ok {
		return
	}
	bc.save(c, "cd", "config_id", req.VersionID, req.TargetType, req.Parameters, req.Enabled, expected)
}

func (bc *BindingManagementController) read(c *gin.Context, kind, param string) {
	targetID, ok := bc.target(c, param)
	if !ok {
		return
	}
	result, err := bc.store.Get(c.Request.Context(), kind, targetID)
	if err != nil {
		bindingError(c, err)
		return
	}
	view := bindingView(result)
	view.Executable = false
	c.JSON(200, util.ResponseSuccessful("查询成功", view))
}

// GetCI returns application CI binding metadata without the stored parameters.
// @Tags Pipeline
// @Summary 查询应用 CI 绑定元数据（不含参数）
// @Param app_id path string true "应用ID"
// @Success 200 {object} util.ResponseTemplate{result=BindingView}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/apps/{app_id}/ci-binding [get]
func (bc *BindingManagementController) GetCI(c *gin.Context) { bc.read(c, "ci", "app_id") }

// GetCD returns application configuration CD binding metadata.
// @Tags Pipeline
// @Summary 查询环境 CD 绑定元数据（不含参数）
// @Param config_id path string true "应用环境配置ID"
// @Success 200 {object} util.ResponseTemplate{result=BindingView}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/app-configs/{config_id}/cd-binding [get]
func (bc *BindingManagementController) GetCD(c *gin.Context) { bc.read(c, "cd", "config_id") }

func (bc *BindingManagementController) parameters(c *gin.Context, kind, param string) {
	targetID, ok := bc.target(c, param)
	if !ok {
		return
	}
	stored, err := bc.store.ReadParameters(c.Request.Context(), kind, targetID)
	if err != nil {
		bindingError(c, err)
		return
	}
	view := BindingParametersView{
		BindingID:  strconv.FormatInt(stored.BindingID, 10),
		Revision:   strconv.FormatUint(stored.Revision, 10),
		Parameters: stored.Document,
	}
	c.JSON(200, util.ResponseSuccessful("查询成功；仅返回绑定层参数，未合并模板默认值", view))
}

// GetCIParameters returns the stored binding layer for one application CI
// binding. Template defaults are never merged into this response.
// @Tags Pipeline
// @Summary 查询应用 CI 绑定参数（敏感读取审计）
// @Param app_id path string true "应用ID"
// @Success 200 {object} util.ResponseTemplate{result=BindingParametersView}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/apps/{app_id}/ci-binding/parameters [get]
func (bc *BindingManagementController) GetCIParameters(c *gin.Context) {
	bc.parameters(c, "ci", "app_id")
}

// GetCDParameters returns the stored binding layer for one configuration CD
// binding.
// @Tags Pipeline
// @Summary 查询环境 CD 绑定参数（敏感读取审计）
// @Param config_id path string true "应用环境配置ID"
// @Success 200 {object} util.ResponseTemplate{result=BindingParametersView}
// @Failure 400,401,403,404,409,413,415,422,503 {object} util.ResponseTemplate
// @Router /api/v1/app-configs/{config_id}/cd-binding/parameters [get]
func (bc *BindingManagementController) GetCDParameters(c *gin.Context) {
	bc.parameters(c, "cd", "config_id")
}
