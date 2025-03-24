package api

import (
	"github.com/gin-gonic/gin"
	"github.com/swaggo/files"       // swagger embed files
	"github.com/swaggo/gin-swagger" // gin-swagger middleware
	"gitlab.ttpai.work/sre/pipeline/ares/internal/api/controller"
)

func Router(r gin.IRouter) {
	appsController := controller.NewAppsController()
	publishController := controller.NewPublishController()
	// Swagger 路由
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.GET("/home", controller.Home)

	apiRouter := r.Group("/api/v1")
	{
		apiRouter.GET("/job/log/:job/:id", controller.GetJenkinsBuildLog)
		apiRouter.GET("/job/stream/log", controller.StreamJenkinsBuildLogHandler)
		apiRouter.POST("/job/build/:job", controller.CreateBuildTask1)
		// 状态查询
		status := apiRouter.Group("/status")
		{
			// 查询jenkins中node节点的状态
			status.GET("/nodes", controller.GetJenkinsNodeStatus)
		}
		// 发布相关路由组
		deploy := apiRouter.Group("/deploy")
		{
			// 单个应用进行发布
			deploy.POST("/publish", publishController.CreateBuildTask)
			// 应用批量发布
			deploy.POST("/publish/batch", publishController.CreateBatchBuildTask)

			// 获取当前还在发布中的任务
			deploy.GET("/publish/jobs/status", publishController.GetBuildTaskList)

			// 单个应用进行发布动作（未投产）
			deploy.POST("/publish/v1", controller.CreateBuildTask)
			// 获取构建任务状态
			deploy.GET("/query/status", controller.GetBuildTaskStatus)
			// 获取job任务构建日志
			deploy.GET("/log/stream", controller.StreamJenkinsBuildLogHandler)
		}
		// 应用相关接口
		apps := apiRouter.Group("/apps")
		{
			// 创建单个应用
			apps.POST("", appsController.CreateApp)
			// 批量创建应用
			apps.POST("/batch", appsController.CreateApps)
			// 下线单个应用
			//apps.DELETE("")
			// 批量下线应用
			//apps.DELETE("")
		}
	}

}
