package api

import (
	"github.com/gin-gonic/gin"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/controller"
)

func Router(r gin.IRouter) {

	apiRouter := r.Group("/api/v1")
	{
		apiRouter.GET("/home", controller.Home)
		apiRouter.GET("/nodes/status", controller.GetJenkinsNodeStatus)
		apiRouter.GET("/job/log/:job/:id", controller.GetJenkinsBuildLog)
		apiRouter.GET("/job/stream/log", controller.StreamJenkinsBuildLogHandler)
		apiRouter.POST("/job/build/:job", controller.CreateBuildTask)
	}

}
