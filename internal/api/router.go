package api

import (
	"net/http"

	"ares/internal/api/controller"
	_ "ares/internal/swagger"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"     // swagger embed files
	ginSwagger "github.com/swaggo/gin-swagger" // gin-swagger middleware
)

func Router(r gin.IRouter) {
	appsController := controller.NewAppsController()
	appConfigsController := controller.NewAppConfigsController()
	publishController := controller.NewPublishController()
	podController := controller.NewPodController()
	compatibleController := controller.NewCompatibleController()
	// Swagger 路由
	r.GET("/wiki", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/swagger/index.html") })
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.GET("/home", controller.Home)

	apiRouter := r.Group("/api/v1")
	{
		//apiRouter.GET("/job/log/:job/:id", controller.GetJenkinsBuildLog)
		// 获取构建日志，以流式获取
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
			// 获取job任务构建详情
			deploy.GET("/publish/query/:task_id", publishController.QueryTaskRecordDetails)
			// 覆盖写入任务图片
			deploy.POST("/publish/images/:task_id", publishController.UpsertTaskAppletImages)

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
			// 获取所有应用名称列表
			apps.GET("/query/appname", appsController.GetAppNameList)
			// 根据APPID获取应用详情
			apps.GET(":app_id", appsController.GetAppByID)
			// 环境配置可选项（dev_language -> code_package_type）
			apps.GET("/:app_id/config-options", appsController.GetAppConfigOptions)
			// 应用基本信息变更（PATCH 指针语义）
			apps.PATCH("/:app_id", appsController.PatchAppByID)

			// 应用环境配置（app_id + env）
			apps.POST("/:app_id/configs", appConfigsController.CreateAppConfig)
			apps.GET("/:app_id/configs", appConfigsController.ListAppConfigs)
			apps.GET("/:app_id/configs/:env", appConfigsController.GetAppConfigByEnv)
			apps.PATCH("/:app_id/configs/:env", appConfigsController.PatchAppConfigByEnv)

			// 下线单个应用
			//apps.DELETE("")
			// 批量下线应用
			//apps.DELETE("")
		}

		// 应用配置（config_id）
		appConfigs := apiRouter.Group("/app-configs")
		{
			appConfigs.GET("/:config_id", appConfigsController.GetAppConfigByID)
			appConfigs.PATCH("/:config_id", appConfigsController.PatchAppConfigByID)
			// 域名相关配置
			appConfigs.GET("/:config_id/domains", appConfigsController.ListDomainsByConfigID)
			appConfigs.PUT("/:config_id/domains", appConfigsController.OverwriteDomainsByConfigID)
			// 多域名单条增删改
			appConfigs.POST("/:config_id/domains", appConfigsController.CreateDomain)
			appConfigs.DELETE("/:config_id/domains/:domain_id", appConfigsController.DeleteDomain)
			appConfigs.PATCH("/:config_id/domains/:domain_id", appConfigsController.PatchDomain)
		}
		// k8s相关接口
		k8s := apiRouter.Group("/k8s")
		{
			// 获取pods信息
			k8s.GET("/pod/query", podController.GetAppPods)
			// 获取所有pods信息（调试用）
			k8s.GET("/pod/list", podController.GetAllPods)
			// 通过标签查询Deployment
			k8s.GET("/deployment/query", podController.GetDeploymentsByLabel)
			// K8s调试信息
			k8s.GET("/debug", podController.GetK8sDebugInfo)
		}

		system := apiRouter.Group("/system")
		integrations := system.Group("/integrations", controller.RequireSettingsAdminToken)
		{
			integrations.GET("", controller.GetIntegrationSettings)
			integrations.PUT("/jenkins", controller.UpdateJenkinsSettings)
			integrations.PUT("/kubernetes", controller.UpdateKubernetesSettings)
		}

		// 特殊兼容接口
		compatible := apiRouter.Group("/compatible")
		{
			// Historical placeholder route returned an empty 200 through global
			// middleware. Keep that contract explicitly instead of relying on the
			// middleware chain to provide a handler.
			compatible.GET("/metadata/relation/all", func(c *gin.Context) { c.Status(http.StatusOK) })
			compatible.GET("/docker/info/getServiceProjectMap", compatibleController.GetAppInfo)
		}
	}

}
