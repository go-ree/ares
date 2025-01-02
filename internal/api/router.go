package api

import (
	"github.com/gin-gonic/gin"
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/controller"
)

func Router(r gin.IRouter) {

	apiRouter := r.Group("/api")
	{
		apiRouter.GET("/home", controller.Home)
		apiRouter.GET("/v1/nodes/status", controller.GetJenkinsNodeStatus)
		apiRouter.GET("/v1/job/log/:job/:id", controller.GetJenkinsBuildLog)
		apiRouter.GET("/v1/job/stream/log", controller.StreamJenkinsBuildLogHandler)
	}

}
