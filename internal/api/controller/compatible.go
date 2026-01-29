package controller

import (
	"ares/internal/api/util"
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
// @Description 用于替换老接口 getServiceProjectMap：按 env 查询 app_configs.code_package_type，并返回 gitAddress/projectName/projectServiceName/projectType 映射
// @Param env query string false "环境（不传默认 dev）"
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]compatible.ServiceProjectMapItem} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/compatible/docker/info/getServiceProjectMap [get]
func (cc *CompatibleController) GetAppInfo(c *gin.Context) {
	ctx := c.Request.Context()
	env := c.Query("env")
	rows, err := cc.cfgManager.QueryServiceProjectMap(ctx, env)
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		return
	}
	// 兼容老接口：message 保持为空字符串；code 按项目规范由 ResponseSuccessful 生成
	c.JSON(200, util.ResponseSuccessful("", rows))
}
