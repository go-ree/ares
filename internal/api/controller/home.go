package controller

import (
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/util"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/jenkins"
	"net/http"

	"gitlab.ttpai.work/sre/pipeline/ares/internal/home"

	"github.com/gin-gonic/gin"
)

func Home(c *gin.Context) {
	home.Home()
	c.String(http.StatusOK, "Hello, World!")
}

func GetJenkinsNodeStatus(c *gin.Context) {
	nodeInfo, err := jenkins.GetJenkinsNodeStatus()
	if err != nil {
		c.JSON(http.StatusInternalServerError, util.ResponseError(err.Error()))
		return
	}
	c.JSON(http.StatusOK, util.ResponseSuccess(nodeInfo))
}
