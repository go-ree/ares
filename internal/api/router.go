package api

import (
	"net/http"

	_ "ares/docs"
	"ares/internal/api/controller"
	"github.com/gin-gonic/gin"
	"github.com/swaggo/files"                  // swagger embed files
	ginSwagger "github.com/swaggo/gin-swagger" // gin-swagger middleware
)

func Router(r gin.IRouter) {
	appsController := controller.NewAppsController()
	publishController := controller.NewPublishController()
	// Swagger 路由
	r.GET("/wiki", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/swagger/index.html") })
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.GET("/home", controller.Home)

	apiRouter := r.Group("/api/v1")
	{
		apiRouter.GET("/job/log/:job/:id", controller.GetJenkinsBuildLog)
		apiRouter.GET("/job/stream/log", controller.StreamJenkinsBuildLogHandler)
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

			// 多条件分页查询，查询所有任务列表
			deploy.POST("/publish/query", publishController.QueryBuildTaskList)

			// 获取当前还在发布中的任务
			deploy.GET("/publish/status", publishController.GetBuildTaskList)

			// 获取构建任务状态
			//deploy.GET("/query/status", controller.GetBuildTaskStatus)
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

			// 查询应用列表
			// 支持多条件组合查询应用列表，包括应用ID、应用名称、开发语言、负责人等
			apps.POST("/query", appsController.QueryApps)
			// 根据应用名称获取应用详情
			apps.GET("/name/:app_name", appsController.GetAppByName)
			// 根据APPID获取应用详情
			apps.GET(":app_id", appsController.GetAppByID)

			// 下线单个应用
			//apps.DELETE("")
			// 批量下线应用
			//apps.DELETE("")
		}
	}

}
