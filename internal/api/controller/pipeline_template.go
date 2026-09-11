package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/pipelinetemplate"
)

type TemplateValidationResult struct {
	Valid           bool                       `json:"valid"`
	Executable      bool                       `json:"executable"`
	ValidationScope string                     `json:"validation_scope"`
	Problems        []pipelinetemplate.Problem `json:"problems"`
}

// ValidatePipelineTemplate validates the new contract without persistence.
// @Tags Pipeline
// @Summary 校验 CI/CD 模板结构（不保存、不执行、不检查外部能力）
// @Param request body pipelinetemplate.Spec true "模板规范"
// @Success 200 {object} util.ResponseTemplate{result=TemplateValidationResult}
// @Failure 422 {object} util.ResponseTemplate{result=TemplateValidationResult}
// @Failure 400 {object} util.ResponseTemplate
// @Failure 413 {object} util.ResponseTemplate
// @Router /api/v1/pipeline-templates/validate [post]
func ValidatePipelineTemplate(c *gin.Context) {
	var spec pipelinetemplate.Spec
	if !BindCanonicalJSON(c, &spec, pipelinetemplate.MaxRequestBytes) {
		return
	}
	problems := pipelinetemplate.Validate(spec)
	result := TemplateValidationResult{Valid: len(problems) == 0, Executable: false, ValidationScope: "structure_only", Problems: problems}
	if !result.Valid {
		c.JSON(http.StatusUnprocessableEntity, util.ResponseTemplate{Code: 0, Message: "模板结构校验失败", Result: result, Error: "invalid_template"})
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccessful("结构校验通过，尚不可执行或发布", result))
}
