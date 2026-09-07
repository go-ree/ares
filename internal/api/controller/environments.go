package controller

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/environment"
)

type EnvironmentController struct {
	service *environment.Service
}

func NewEnvironmentController() *EnvironmentController {
	return &EnvironmentController{service: environment.NewService()}
}

// ListCatalog returns non-sensitive environment metadata, including disabled
// entries, so historical records remain labelable. Callers must still filter
// Enabled before offering creation or release actions.
// @Tags Environment
// @Summary 获取完整环境目录
// @Description 返回启用与停用环境，供发布选择和历史记录统一展示；发起发布时仍会校验 enabled。
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]environment.View}
// @Failure 500 {object} util.ResponseTemplate{code=int}
// @Router /api/v1/environments [get]
func (ec *EnvironmentController) ListCatalog(c *gin.Context) {
	rows, err := ec.service.List(c.Request.Context(), true)
	if err != nil {
		writeInternalFailure(c, http.StatusInternalServerError, "查询环境失败", "database", "list_environments", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", rows))
}

// ListAll 返回系统设置需要的完整环境目录（包括停用项）。
// @Tags System
// @Summary 获取系统环境目录
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]environment.View}
// @Router /api/v1/system/environments [get]
func (ec *EnvironmentController) ListAll(c *gin.Context) {
	rows, err := ec.service.List(c.Request.Context(), true)
	if err != nil {
		writeInternalFailure(c, http.StatusInternalServerError, "查询环境失败", "database", "list_system_environments", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("查询成功", rows))
}

// Create 创建一个环境目录项，环境代码创建后不可修改。
// @Tags System
// @Summary 创建环境
// @Param request body environment.CreateRequest true "环境信息"
// @Success 201 {object} util.ResponseTemplate{code=int,result=environment.View}
// @Failure 422 {object} util.ResponseTemplate{code=int}
// @Router /api/v1/system/environments [post]
func (ec *EnvironmentController) Create(c *gin.Context) {
	var req environment.CreateRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	row, err := ec.service.Create(c.Request.Context(), req)
	if err != nil {
		writeEnvironmentError(c, "创建环境失败", err)
		return
	}
	c.JSON(http.StatusCreated, util.ResponseSuccessful("创建成功", row))
}

// Update 修改环境名称、排序或启停状态。
// @Tags System
// @Summary 更新环境
// @Param code path string true "环境代码"
// @Param request body environment.UpdateRequest true "待更新字段"
// @Success 200 {object} util.ResponseTemplate{code=int,result=environment.View}
// @Failure 404 {object} util.ResponseTemplate{code=int}
// @Failure 422 {object} util.ResponseTemplate{code=int}
// @Router /api/v1/system/environments/{code} [patch]
func (ec *EnvironmentController) Update(c *gin.Context) {
	var req environment.UpdateRequest
	if !BindJSON(c, &req, defaultJSONRequestBytes) {
		return
	}
	row, err := ec.service.Update(c.Request.Context(), c.Param("code"), req)
	if err != nil {
		writeEnvironmentError(c, "更新环境失败", err)
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("更新成功", row))
}

func writeEnvironmentError(c *gin.Context, message string, err error) {
	var validationError *environment.ValidationError
	switch {
	case errors.As(err, &validationError):
		c.JSON(http.StatusUnprocessableEntity, util.ResponseFailure(message, validationError.Error()))
	case errors.Is(err, environment.ErrNotFound):
		c.JSON(http.StatusNotFound, util.ResponseFailure(message, environment.ErrNotFound.Error()))
	default:
		writeInternalFailure(c, http.StatusInternalServerError, message, "database", "environment", err)
	}
}
