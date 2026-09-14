package controller

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/pipelinetemplate"
	"github.com/go-ree/ares/internal/templatecatalog"
)

type TemplateCatalogStore interface {
	CreateType(context.Context, string, string) error
	GetType(context.Context, string) (templatecatalog.ApplicationType, error)
	UpdateType(context.Context, string, string, bool, uint64) error
	ListTypes(context.Context, string, int) ([]templatecatalog.ApplicationType, error)
	CreateTemplate(context.Context, string, pipelinetemplate.Spec, int64) (int64, error)
	GetTemplate(context.Context, int64) (templatecatalog.Template, error)
	UpdateTemplate(context.Context, int64, uint64, pipelinetemplate.Spec, bool) error
	ListTemplates(context.Context, int64, int) ([]templatecatalog.Template, error)
	Publish(context.Context, int64, uint64, int64) (templatecatalog.Version, error)
	GetVersion(context.Context, int64, uint64) (templatecatalog.Version, error)
	ListVersions(context.Context, int64, uint64, int) ([]templatecatalog.Version, error)
}
type TemplateCatalogController struct{ store TemplateCatalogStore }

// TemplateCatalogDeadline bounds SQL work and lock waits even when a client
// keeps its connection open. The final audit has its own detached deadline.
func TemplateCatalogDeadline() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func NewTemplateCatalogController(store TemplateCatalogStore) *TemplateCatalogController {
	if store == nil && db.Engine != nil {
		store = templatecatalog.New(db.Engine.DB().DB)
	}
	return &TemplateCatalogController{store: store}
}
func (tc *TemplateCatalogController) ready(c *gin.Context) bool {
	if tc.store == nil {
		catalogError(c, templatecatalog.ErrStorage)
		return false
	}
	return true
}

type ApplicationTypeView struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision"`
}
type TemplateView struct {
	ID              string          `json:"id"`
	Key             string          `json:"key"`
	Kind            string          `json:"kind"`
	ApplicationType string          `json:"application_type,omitempty"`
	TargetType      string          `json:"target_type,omitempty"`
	Enabled         bool            `json:"enabled"`
	Revision        string          `json:"revision"`
	Executable      bool            `json:"executable"`
	Draft           json.RawMessage `json:"draft,omitempty" swaggertype:"object"`
}
type TemplateVersionView struct {
	ID             string          `json:"id"`
	TemplateID     string          `json:"template_id"`
	Number         string          `json:"number"`
	SourceRevision string          `json:"source_revision"`
	Checksum       string          `json:"checksum"`
	Executable     bool            `json:"executable"`
	Spec           json.RawMessage `json:"spec,omitempty" swaggertype:"object"`
}
type TypePage struct {
	Items      []ApplicationTypeView `json:"items"`
	NextCursor string                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
}
type TemplatePage struct {
	Items      []TemplateView `json:"items"`
	NextCursor string         `json:"next_cursor"`
	HasMore    bool           `json:"has_more"`
}
type VersionPage struct {
	Items      []TemplateVersionView `json:"items"`
	NextCursor string                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
}
type CreateTypeRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}
type UpdateTypeRequest struct {
	Name             string `json:"name"`
	Enabled          *bool  `json:"enabled"`
	ExpectedRevision string `json:"expected_revision"`
}
type CreateTemplateRequest struct {
	Key  string                `json:"key"`
	Spec pipelinetemplate.Spec `json:"spec"`
}
type UpdateTemplateRequest struct {
	Spec             pipelinetemplate.Spec `json:"spec"`
	Enabled          *bool                 `json:"enabled"`
	ExpectedRevision string                `json:"expected_revision"`
}
type PublishTemplateRequest struct {
	ExpectedRevision string `json:"expected_revision"`
}
type CatalogMutationReceipt struct {
	ID         string `json:"id,omitempty"`
	Key        string `json:"key,omitempty"`
	Revision   string `json:"revision"`
	Executable bool   `json:"executable"`
}

func typeView(v templatecatalog.ApplicationType) ApplicationTypeView {
	return ApplicationTypeView{Key: v.Key, Name: v.Name, Enabled: v.Enabled, Revision: strconv.FormatUint(v.Revision, 10)}
}
func templateView(v templatecatalog.Template) TemplateView {
	return TemplateView{ID: strconv.FormatInt(v.ID, 10), Key: v.Key, Kind: v.Kind, ApplicationType: v.ApplicationType, TargetType: v.TargetType, Enabled: v.Enabled, Revision: strconv.FormatUint(v.Revision, 10), Draft: v.Draft}
}
func versionView(v templatecatalog.Version) TemplateVersionView {
	return TemplateVersionView{ID: strconv.FormatInt(v.ID, 10), TemplateID: strconv.FormatInt(v.TemplateID, 10), Number: strconv.FormatUint(v.Number, 10), SourceRevision: strconv.FormatUint(v.SourceRevision, 10), Checksum: v.Checksum, Spec: v.Spec}
}
func catalogError(c *gin.Context, err error) {
	status, code := http.StatusServiceUnavailable, "catalog_unavailable"
	switch {
	case errors.Is(err, templatecatalog.ErrInvalid):
		status, code = 422, "invalid_catalog_request"
	case errors.Is(err, templatecatalog.ErrNotFound):
		status, code = 404, "catalog_not_found"
	case errors.Is(err, templatecatalog.ErrConflict):
		status, code = 409, "catalog_conflict"
	case errors.Is(err, templatecatalog.ErrDisabled):
		status, code = 409, "catalog_disabled"
	}
	c.JSON(status, util.ResponseFailure("类型或模板操作失败", code))
}
func catalogBadRequest(c *gin.Context) {
	c.JSON(400, util.ResponseFailure("请求参数无效", "invalid_request"))
}
func decimal(s string, max uint64) (uint64, bool) {
	if s == "" || len(s) > 20 || s[0] == '0' {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil && n <= max
}
func catalogID(c *gin.Context) (int64, bool) {
	n, ok := decimal(c.Param("template_id"), math.MaxInt64)
	if !ok {
		catalogBadRequest(c)
	}
	return int64(n), ok
}
func revision(c *gin.Context, s string) (uint64, bool) {
	n, ok := decimal(s, math.MaxUint64-1)
	if !ok {
		catalogBadRequest(c)
	}
	return n, ok
}
func catalogPage(c *gin.Context) (string, int, bool) {
	q, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		catalogBadRequest(c)
		return "", 0, false
	}
	for k, v := range q {
		if (k != "after" && k != "limit") || len(v) != 1 {
			catalogBadRequest(c)
			return "", 0, false
		}
	}
	limit := uint64(20)
	if v, ok := q["limit"]; ok {
		var valid bool
		limit, valid = decimal(v[0], 100)
		if !valid {
			catalogBadRequest(c)
			return "", 0, false
		}
	}
	return q.Get("after"), int(limit), true
}
func afterNumber(c *gin.Context, raw string, max uint64) (uint64, bool) {
	if raw == "" {
		return 0, true
	}
	n, ok := decimal(raw, max)
	if !ok {
		catalogBadRequest(c)
	}
	return n, ok
}
func catalogActor(c *gin.Context) (int64, bool) {
	p, ok := CurrentPrincipal(c)
	if !ok || p.UserID <= 0 {
		c.JSON(401, util.ResponseFailure("请先登录", "unauthorized"))
		return 0, false
	}
	return p.UserID, true
}

// ListTypes lists public metadata, including disabled types.
// @Tags Pipeline
// @Summary 分页查询应用类型（包含停用项）
// @Param after query string false "上一页 next_cursor"
// @Param limit query int false "1～100，默认20"
// @Success 200 {object} util.ResponseTemplate{result=TypePage}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/application-types [get]
func (tc *TemplateCatalogController) ListTypes(c *gin.Context) {
	after, limit, ok := catalogPage(c)
	if !ok || !tc.ready(c) {
		return
	}
	rows, err := tc.store.ListTypes(c.Request.Context(), after, limit+1)
	if err != nil {
		catalogError(c, err)
		return
	}
	out := TypePage{Items: make([]ApplicationTypeView, 0), HasMore: len(rows) > limit}
	if out.HasMore {
		rows = rows[:limit]
		out.NextCursor = rows[len(rows)-1].Key
	}
	for _, v := range rows {
		out.Items = append(out.Items, typeView(v))
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", out))
}

// GetType returns one type's metadata.
// @Tags Pipeline
// @Summary 查询应用类型
// @Param key path string true "稳定类型标识"
// @Success 200 {object} util.ResponseTemplate{result=ApplicationTypeView}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/application-types/{key} [get]
func (tc *TemplateCatalogController) GetType(c *gin.Context) {
	if !tc.ready(c) {
		return
	}
	v, err := tc.store.GetType(c.Request.Context(), c.Param("key"))
	if err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", typeView(v)))
}

// CreateType creates an enabled type with revision one.
// @Tags Pipeline
// @Summary 创建应用类型（管理员）
// @Param request body CreateTypeRequest true "类型"
// @Success 201 {object} util.ResponseTemplate{result=CatalogMutationReceipt}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/application-types [post]
func (tc *TemplateCatalogController) CreateType(c *gin.Context) {
	var req CreateTypeRequest
	if !BindCanonicalJSON(c, &req, 4096) || !tc.ready(c) {
		return
	}
	if err := tc.store.CreateType(c.Request.Context(), req.Key, req.Name); err != nil {
		catalogError(c, err)
		return
	}
	SetRequestAuditResourceID(c, req.Key)
	c.JSON(201, util.ResponseSuccessful("创建成功", CatalogMutationReceipt{Key: req.Key, Revision: "1"}))
}

// UpdateType requires all mutable fields and expected revision.
// @Tags Pipeline
// @Summary 更新应用类型名称与启停（管理员，CAS）
// @Param key path string true "稳定类型标识"
// @Param request body UpdateTypeRequest true "完整可变字段"
// @Success 200 {object} util.ResponseTemplate{result=CatalogMutationReceipt}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/application-types/{key} [put]
func (tc *TemplateCatalogController) UpdateType(c *gin.Context) {
	var req UpdateTypeRequest
	if !BindCanonicalJSON(c, &req, 4096) {
		return
	}
	r, ok := revision(c, req.ExpectedRevision)
	if !ok {
		return
	}
	if req.Enabled == nil {
		catalogBadRequest(c)
		return
	}
	if !tc.ready(c) {
		return
	}
	if err := tc.store.UpdateType(c.Request.Context(), c.Param("key"), req.Name, *req.Enabled, r); err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("更新成功", CatalogMutationReceipt{Key: c.Param("key"), Revision: strconv.FormatUint(r+1, 10)}))
}

// ListTemplates does not return draft or private parameter defaults.
// @Tags Pipeline
// @Summary 分页查询模板元数据
// @Param after query string false "上一页 next_cursor"
// @Param limit query int false "1～100，默认20"
// @Success 200 {object} util.ResponseTemplate{result=TemplatePage}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates [get]
func (tc *TemplateCatalogController) ListTemplates(c *gin.Context) {
	raw, limit, ok := catalogPage(c)
	if !ok {
		return
	}
	after, ok := afterNumber(c, raw, math.MaxInt64)
	if !ok || !tc.ready(c) {
		return
	}
	rows, err := tc.store.ListTemplates(c.Request.Context(), int64(after), limit+1)
	if err != nil {
		catalogError(c, err)
		return
	}
	out := TemplatePage{Items: make([]TemplateView, 0), HasMore: len(rows) > limit}
	if out.HasMore {
		rows = rows[:limit]
		out.NextCursor = strconv.FormatInt(rows[len(rows)-1].ID, 10)
	}
	for _, v := range rows {
		v.Draft = nil
		out.Items = append(out.Items, templateView(v))
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", out))
}

// GetTemplate exposes the full draft only to workflow administrators.
// @Tags Pipeline
// @Summary 查询模板完整草稿（管理员，敏感读取审计）
// @Param template_id path string true "模板ID"
// @Success 200 {object} util.ResponseTemplate{result=TemplateView}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates/{template_id} [get]
func (tc *TemplateCatalogController) GetTemplate(c *gin.Context) {
	id, ok := catalogID(c)
	if !ok || !tc.ready(c) {
		return
	}
	v, err := tc.store.GetTemplate(c.Request.Context(), id)
	if err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", templateView(v)))
}

// CreateTemplate persists a structurally valid definition, without execution.
// @Tags Pipeline
// @Summary 创建模板草稿（管理员，不执行）
// @Param request body CreateTemplateRequest true "稳定标识和规范"
// @Success 201 {object} util.ResponseTemplate{result=CatalogMutationReceipt}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates [post]
func (tc *TemplateCatalogController) CreateTemplate(c *gin.Context) {
	actor, ok := catalogActor(c)
	if !ok {
		return
	}
	var req CreateTemplateRequest
	if !BindCanonicalJSON(c, &req, pipelinetemplate.MaxRequestBytes+4096) || !tc.ready(c) {
		return
	}
	id, err := tc.store.CreateTemplate(c.Request.Context(), req.Key, req.Spec, actor)
	if err != nil {
		catalogError(c, err)
		return
	}
	s := strconv.FormatInt(id, 10)
	SetRequestAuditResourceID(c, s)
	c.JSON(201, util.ResponseSuccessful("草稿已保存，尚不可执行", CatalogMutationReceipt{ID: s, Revision: "1"}))
}

// UpdateTemplate replaces mutable draft and enabled state under CAS.
// @Tags Pipeline
// @Summary 更新模板草稿与启停（管理员，CAS）
// @Param template_id path string true "模板ID"
// @Param request body UpdateTemplateRequest true "完整可变字段"
// @Success 200 {object} util.ResponseTemplate{result=CatalogMutationReceipt}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates/{template_id} [put]
func (tc *TemplateCatalogController) UpdateTemplate(c *gin.Context) {
	id, ok := catalogID(c)
	if !ok {
		return
	}
	var req UpdateTemplateRequest
	if !BindCanonicalJSON(c, &req, pipelinetemplate.MaxRequestBytes+4096) {
		return
	}
	r, ok := revision(c, req.ExpectedRevision)
	if !ok {
		return
	}
	if req.Enabled == nil {
		catalogBadRequest(c)
		return
	}
	if !tc.ready(c) {
		return
	}
	if err := tc.store.UpdateTemplate(c.Request.Context(), id, r, req.Spec, *req.Enabled); err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("草稿已更新，尚不可执行", CatalogMutationReceipt{ID: strconv.FormatInt(id, 10), Revision: strconv.FormatUint(r+1, 10)}))
}

// Publish consumes a revision and creates one immutable definition version.
// @Tags Pipeline
// @Summary 发布不可变模板版本（管理员，不启动任务）
// @Param template_id path string true "模板ID"
// @Param request body PublishTemplateRequest true "预期revision"
// @Success 201 {object} util.ResponseTemplate{result=TemplateVersionView}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates/{template_id}/versions [post]
func (tc *TemplateCatalogController) Publish(c *gin.Context) {
	actor, ok := catalogActor(c)
	if !ok {
		return
	}
	id, ok := catalogID(c)
	if !ok {
		return
	}
	var req PublishTemplateRequest
	if !BindCanonicalJSON(c, &req, 4096) {
		return
	}
	r, ok := revision(c, req.ExpectedRevision)
	if !ok || !tc.ready(c) {
		return
	}
	v, err := tc.store.Publish(c.Request.Context(), id, r, actor)
	if err != nil {
		catalogError(c, err)
		return
	}
	v.Spec = nil
	c.JSON(201, util.ResponseSuccessful("版本已发布，尚不可执行", versionView(v)))
}

// ListVersions returns immutable metadata without raw specifications.
// @Tags Pipeline
// @Summary 分页查询已发布模板版本元数据
// @Param template_id path string true "模板ID"
// @Param after query string false "上一页 next_cursor"
// @Param limit query int false "1～100，默认20"
// @Success 200 {object} util.ResponseTemplate{result=VersionPage}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates/{template_id}/versions [get]
func (tc *TemplateCatalogController) ListVersions(c *gin.Context) {
	id, ok := catalogID(c)
	if !ok {
		return
	}
	raw, limit, ok := catalogPage(c)
	if !ok {
		return
	}
	after, ok := afterNumber(c, raw, math.MaxUint64)
	if !ok || !tc.ready(c) {
		return
	}
	rows, err := tc.store.ListVersions(c.Request.Context(), id, after, limit+1)
	if err != nil {
		catalogError(c, err)
		return
	}
	out := VersionPage{Items: make([]TemplateVersionView, 0), HasMore: len(rows) > limit}
	if out.HasMore {
		rows = rows[:limit]
		out.NextCursor = strconv.FormatUint(rows[len(rows)-1].Number, 10)
	}
	for _, v := range rows {
		v.Spec = nil
		out.Items = append(out.Items, versionView(v))
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", out))
}

// GetVersion is an audited administrator-only specification read.
// @Tags Pipeline
// @Summary 查询不可变模板完整规范（管理员，敏感读取审计）
// @Param template_id path string true "模板ID"
// @Param number path string true "模板内版本号"
// @Success 200 {object} util.ResponseTemplate{result=TemplateVersionView}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 401 {object} util.ResponseTemplate
// @Failure 403 {object} util.ResponseTemplate
// @Failure 404 {object} util.ResponseTemplate
// @Failure 409 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Failure 415 {object} util.ResponseTemplate
// @Failure 422 {object} util.ResponseTemplate
// @Failure 503 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates/{template_id}/versions/{number} [get]
func (tc *TemplateCatalogController) GetVersion(c *gin.Context) {
	id, ok := catalogID(c)
	if !ok {
		return
	}
	n, ok := decimal(c.Param("number"), math.MaxUint64)
	if !ok {
		catalogBadRequest(c)
		return
	}
	if !tc.ready(c) {
		return
	}
	v, err := tc.store.GetVersion(c.Request.Context(), id, n)
	if err != nil {
		catalogError(c, err)
		return
	}
	c.JSON(200, util.ResponseSuccessful("查询成功", versionView(v)))
}
