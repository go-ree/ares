package controller

import (
	"ares/internal/api/util"
	"ares/internal/home"
	"github.com/gin-gonic/gin"
)

// Home
// @Tags Health
// @Summary 健康检测
// @Success 200 {object} util.ResponseTemplate{code=int,result=string} "成功"
// @Router	/home [get]
func Home(c *gin.Context) {
	home.Home()
	c.JSON(200, util.ResponseSuccessful("", "Hello, World!"))
}
