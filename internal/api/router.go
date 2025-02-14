package api

import (
	"github.com/gin-gonic/gin"
	"github.com/swaggo/files"       // swagger embed files
	"github.com/swaggo/gin-swagger" // gin-swagger middleware
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/controller"
)

func Router(r gin.IRouter) {
	// Swagger 路由
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	apiRouter := r.Group("/api/v1")
	{
		apiRouter.GET("/home", controller.Home)
		apiRouter.GET("/nodes/status", controller.GetJenkinsNodeStatus)
		apiRouter.GET("/job/log/:job/:id", controller.GetJenkinsBuildLog)
		apiRouter.GET("/job/stream/log", controller.StreamJenkinsBuildLogHandler)
		apiRouter.POST("/job/build/:job", controller.CreateBuildTask1)
		apiRouter.POST("/deploy/publish", controller.BuildTask)
		apiRouter.POST("/deploy/publish/v2", controller.CreateBuildTask)
		apiRouter.GET("/deploy/query/status", controller.GetBuildTaskStatus)
	}

}
