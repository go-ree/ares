package controller

import (
	"ares/internal/compatible"
	"github.com/gin-gonic/gin"
)

type CompatibleController struct {
	cfgManager *compatible.AppInfoManager
}

func NewCompatibleController() *CompatibleController {
	return &CompatibleController{
		cfgManager: compatible.NewAppInfoManager(),
	}
}

// GetAppInfo
// @Tags Compatible
// @Summary 兼容性的获取应用配置详情信息
// @Description 用于替换老接口 getServiceProjectMap：按 env 查询 app_configs.code_package_type，并返回 gitAddress/projectName/projectServiceName/projectType 映射（legacy 响应：code 为字符串）
// @Param env query string false "环境（不传默认 dev）"
// @Success 200 {object} compatible.LegacyServiceProjectMapResponse "成功"
// @Failure 200 {object} compatible.LegacyServiceProjectMapResponse "失败（legacy：HTTP 仍返回 200，code 为 500）"
// @Router	/api/v1/compatible/docker/info/getServiceProjectMap [get]
func (cc *CompatibleController) GetAppInfo(c *gin.Context) {
	ctx := c.Request.Context()
	env := c.Query("env")
	rows, err := cc.cfgManager.QueryServiceProjectMap(ctx, env)
	if err != nil {
		// legacy 兼容：HTTP 仍返回 200，错误用 code="500" 表达；message 内容保持
		c.JSON(200, compatible.LegacyServiceProjectMapResponse{
			Code:    "500",
			Message: "查询失败",
			Result:  nil,
		})
		return
	}
	// legacy 兼容：code 为字符串 "200"，message 为空字符串
	c.JSON(200, compatible.LegacyServiceProjectMapResponse{
		Code:    "200",
		Message: "",
		Result:  rows,
	})
}
