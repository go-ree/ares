package api

import (
	"net/http"

	"github.com/go-ree/ares/internal/api/controller"
	"github.com/go-ree/ares/internal/auth"
	"github.com/go-ree/ares/internal/release"
	_ "github.com/go-ree/ares/internal/swagger"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// Router keeps route discovery fail-closed for tests and tooling. Production
// startup must use RouterWithRuntime with a database-backed auth service.
func Router(r gin.IRouter) {
	RouterWithRuntime(r, Runtime{})
}

func RouterWithRuntime(r gin.IRouter, runtime Runtime) {
	runtime = runtime.withSecurityDefaults()
	appsController := controller.NewAppsController()
	appConfigsController := controller.NewAppConfigsController()
	publishController := controller.NewPublishController()
	podController := controller.NewPodController()
	compatibleController := controller.NewCompatibleController()
	environmentController := controller.NewEnvironmentController()
	authController := controller.NewAuthController(runtime.Auth)
	workflowRuntime := release.Shared()
	workflowController := controller.NewWorkflowController(workflowRuntime.Service, workflowRuntime.Coordinator)

	authenticated := runtime.require(routePolicy{Action: "documentation.read", ResourceType: "documentation"})
	r.GET("/wiki", authenticated, func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/swagger/index.html") })
	r.GET("/swagger/*any", authenticated, ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.GET("/home", runtime.require(routePolicy{Action: "home.read", ResourceType: "home"}), controller.Home)

	apiRouter := r.Group("/api/v1")
	authRoutes := apiRouter.Group("/auth")
	{
		authRoutes.GET("/options", authController.Options)
		authRoutes.POST("/bootstrap", authController.Bootstrap)
		authRoutes.POST("/login", authController.Login)
		authRoutes.GET("/oidc/start", authController.OIDCStart)
		authRoutes.GET("/oidc/callback", authController.OIDCCallback)
		authRoutes.GET("/session", runtime.require(routePolicy{Action: "auth.session.read", ResourceType: "authentication"}), authController.Session)
		authRoutes.POST("/logout", runtime.require(routePolicy{Action: "auth.logout", ResourceType: "authentication"}), authController.Logout)
		authRoutes.POST("/password", runtime.require(routePolicy{
			Action: "auth.password.change", ResourceType: "authentication", CredentialCheck: true,
		}), authController.ChangePassword)
	}

	apiRouter.GET("/environments", runtime.require(routePolicy{
		Permission: auth.PermissionApplicationsRead, Action: "environment.catalog.read", ResourceType: "environment",
	}), environmentController.ListCatalog)
	apiRouter.GET("/pipeline-step-types", runtime.require(routePolicy{
		Permission: auth.PermissionWorkflowsRead, Action: "workflow.step-types.read", ResourceType: "workflow",
	}), workflowController.ListPipelineStepTypes)
	apiRouter.GET("/job/stream/log", runtime.require(routePolicy{
		Permission: auth.PermissionLogsRead, Action: "release.log.read", ResourceType: "release-log",
		SensitiveRead: true, SSE: true,
	}), controller.StreamJenkinsBuildLogHandler)

	status := apiRouter.Group("/status")
	status.GET("/nodes", runtime.require(routePolicy{
		Permission: auth.PermissionReleasesRead, Action: "executor.nodes.read", ResourceType: "executor",
	}), controller.GetJenkinsNodeStatus)

	deploy := apiRouter.Group("/deploy")
	{
		deploy.POST("/publish", runtime.require(routePolicy{
			Permission: auth.PermissionReleasesCreate, Action: "release.create", ResourceType: "release",
		}), publishController.CreateBuildTask)
		deploy.POST("/publish/batch", runtime.require(routePolicy{
			Permission: auth.PermissionReleasesCreate, Action: "release.batch.create", ResourceType: "release",
		}), publishController.CreateBatchBuildTask)
		deploy.POST("/publish/query", runtime.require(routePolicy{
			Permission: auth.PermissionTasksRead, Action: "task.list", ResourceType: "task",
		}), publishController.QueryBuildTaskList)
		deploy.GET("/publish/query/:task_id", runtime.require(routePolicy{
			Permission: auth.PermissionTasksRead, Action: "task.read", ResourceType: "task", ResourceParam: "task_id",
		}), publishController.QueryTaskRecordDetails)
		deploy.GET("/publish/query/:task_id/steps", runtime.require(routePolicy{
			Permission: auth.PermissionTasksRead, Action: "task.steps.read", ResourceType: "task", ResourceParam: "task_id",
		}), workflowController.GetTaskSteps)
		deploy.POST("/publish/images/:task_id", runtime.require(routePolicy{
			Permission: auth.PermissionTasksWrite, Action: "task.images.write", ResourceType: "task", ResourceParam: "task_id",
		}), publishController.UpsertTaskAppletImages)
		deploy.GET("/publish/status", runtime.require(routePolicy{
			Permission: auth.PermissionReleasesRead, Action: "release.status.read", ResourceType: "release",
		}), publishController.GetBuildTaskList)
		deploy.GET("/log/stream", runtime.require(routePolicy{
			Permission: auth.PermissionLogsRead, Action: "release.log.read", ResourceType: "release-log",
			SensitiveRead: true, SSE: true,
		}), controller.StreamJenkinsBuildLogHandler)
	}

	apps := apiRouter.Group("/apps")
	{
		apps.POST("", runtime.require(routePolicy{Permission: auth.PermissionApplicationsWrite, Action: "application.create", ResourceType: "application"}), appsController.CreateApp)
		apps.POST("/batch", runtime.require(routePolicy{Permission: auth.PermissionApplicationsWrite, Action: "application.batch.create", ResourceType: "application"}), appsController.CreateApps)
		apps.POST("/query", runtime.require(routePolicy{Permission: auth.PermissionApplicationsRead, Action: "application.list", ResourceType: "application"}), appsController.QueryApps)
		apps.GET("/name/:app_name", runtime.require(routePolicy{Permission: auth.PermissionApplicationsRead, Action: "application.read", ResourceType: "application", ResourceParam: "app_name"}), appsController.GetAppByName)
		apps.GET("/query/appname", runtime.require(routePolicy{Permission: auth.PermissionApplicationsRead, Action: "application.names.read", ResourceType: "application"}), appsController.GetAppNameList)
		apps.GET("/:app_id", runtime.require(routePolicy{Permission: auth.PermissionApplicationsRead, Action: "application.read", ResourceType: "application", ResourceParam: "app_id"}), appsController.GetAppByID)
		apps.GET("/:app_id/config-options", runtime.require(routePolicy{Permission: auth.PermissionAppConfigsRead, Action: "app-config.options.read", ResourceType: "application", ResourceParam: "app_id"}), appsController.GetAppConfigOptions)
		apps.PATCH("/:app_id", runtime.require(routePolicy{Permission: auth.PermissionApplicationsWrite, Action: "application.update", ResourceType: "application", ResourceParam: "app_id"}), appsController.PatchAppByID)
		apps.POST("/:app_id/configs", runtime.require(routePolicy{Permission: auth.PermissionAppConfigsWrite, Action: "app-config.create", ResourceType: "application", ResourceParam: "app_id"}), appConfigsController.CreateAppConfig)
		apps.GET("/:app_id/configs", runtime.require(routePolicy{Permission: auth.PermissionAppConfigsRead, Action: "app-config.list", ResourceType: "application", ResourceParam: "app_id"}), appConfigsController.ListAppConfigs)
		apps.GET("/:app_id/configs/:env", runtime.require(routePolicy{Permission: auth.PermissionAppConfigsRead, Action: "app-config.read", ResourceType: "application", ResourceParam: "app_id"}), appConfigsController.GetAppConfigByEnv)
		apps.PATCH("/:app_id/configs/:env", runtime.require(routePolicy{Permission: auth.PermissionAppConfigsWrite, Action: "app-config.update", ResourceType: "application", ResourceParam: "app_id"}), appConfigsController.PatchAppConfigByEnv)
	}

	appConfigs := apiRouter.Group("/app-configs")
	{
		appConfigs.GET("/:config_id", runtime.require(routePolicy{Permission: auth.PermissionAppConfigsRead, Action: "app-config.read", ResourceType: "app-config", ResourceParam: "config_id"}), appConfigsController.GetAppConfigByID)
		appConfigs.PATCH("/:config_id", runtime.require(routePolicy{Permission: auth.PermissionAppConfigsWrite, Action: "app-config.update", ResourceType: "app-config", ResourceParam: "config_id"}), appConfigsController.PatchAppConfigByID)
		appConfigs.GET("/:config_id/workflow", runtime.require(routePolicy{Permission: auth.PermissionWorkflowsRead, Action: "workflow.read", ResourceType: "app-config", ResourceParam: "config_id"}), workflowController.GetAppConfigWorkflow)
		appConfigs.PUT("/:config_id/workflow", runtime.require(routePolicy{Permission: auth.PermissionWorkflowsWrite, Action: "workflow.update", ResourceType: "app-config", ResourceParam: "config_id", AllowLegacy: true}), workflowController.PutAppConfigWorkflow)
		appConfigs.GET("/:config_id/domains", runtime.require(routePolicy{Permission: auth.PermissionDomainsRead, Action: "domain.list", ResourceType: "app-config", ResourceParam: "config_id"}), appConfigsController.ListDomainsByConfigID)
		appConfigs.PUT("/:config_id/domains", runtime.require(routePolicy{Permission: auth.PermissionDomainsWrite, Action: "domain.replace", ResourceType: "app-config", ResourceParam: "config_id"}), appConfigsController.OverwriteDomainsByConfigID)
		appConfigs.POST("/:config_id/domains", runtime.require(routePolicy{Permission: auth.PermissionDomainsWrite, Action: "domain.create", ResourceType: "app-config", ResourceParam: "config_id"}), appConfigsController.CreateDomain)
		appConfigs.DELETE("/:config_id/domains/:domain_id", runtime.require(routePolicy{Permission: auth.PermissionDomainsWrite, Action: "domain.delete", ResourceType: "domain", ResourceParam: "domain_id"}), appConfigsController.DeleteDomain)
		appConfigs.PATCH("/:config_id/domains/:domain_id", runtime.require(routePolicy{Permission: auth.PermissionDomainsWrite, Action: "domain.update", ResourceType: "domain", ResourceParam: "domain_id"}), appConfigsController.PatchDomain)
	}

	k8s := apiRouter.Group("/k8s")
	{
		k8s.GET("/pod/query", runtime.require(routePolicy{Permission: auth.PermissionKubernetesRead, Action: "kubernetes.pods.read", ResourceType: "kubernetes"}), podController.GetAppPods)
		k8s.GET("/pod/list", runtime.require(routePolicy{Permission: auth.PermissionKubernetesRead, Action: "kubernetes.pods.read", ResourceType: "kubernetes"}), podController.GetAllPods)
		k8s.GET("/deployment/query", runtime.require(routePolicy{Permission: auth.PermissionKubernetesRead, Action: "kubernetes.deployments.read", ResourceType: "kubernetes"}), podController.GetDeploymentsByLabel)
		k8s.GET("/debug", runtime.require(routePolicy{Permission: auth.PermissionKubernetesDebug, Action: "kubernetes.debug.read", ResourceType: "kubernetes", SensitiveRead: true}), podController.GetK8sDebugInfo)
	}

	system := apiRouter.Group("/system")
	environments := system.Group("/environments")
	{
		environments.GET("", runtime.require(routePolicy{Permission: auth.PermissionSystemSettingsRead, Action: "system.environment.list", ResourceType: "system-environment", SensitiveRead: true, AllowLegacy: true}), environmentController.ListAll)
		environments.POST("", runtime.require(routePolicy{Permission: auth.PermissionSystemSettingsWrite, Action: "system.environment.create", ResourceType: "system-environment", AllowLegacy: true}), environmentController.Create)
		environments.PATCH("/:code", runtime.require(routePolicy{Permission: auth.PermissionSystemSettingsWrite, Action: "system.environment.update", ResourceType: "system-environment", ResourceParam: "code", AllowLegacy: true}), environmentController.Update)
	}
	integrations := system.Group("/integrations")
	{
		integrations.GET("", runtime.require(routePolicy{Permission: auth.PermissionSystemSettingsRead, Action: "system.integration.read", ResourceType: "integration", SensitiveRead: true, AllowLegacy: true}), controller.GetIntegrationSettings)
		integrations.PUT("/jenkins", runtime.require(routePolicy{Permission: auth.PermissionSystemSettingsWrite, Action: "system.integration.update", ResourceType: "integration", AllowLegacy: true}), controller.UpdateJenkinsSettings)
		integrations.PUT("/kubernetes", runtime.require(routePolicy{Permission: auth.PermissionSystemSettingsWrite, Action: "system.integration.update", ResourceType: "integration", AllowLegacy: true}), controller.UpdateKubernetesSettings)
	}
	users := system.Group("/users")
	{
		users.GET("", runtime.require(routePolicy{Permission: auth.PermissionUsersRead, Action: "user.list", ResourceType: "user", SensitiveRead: true}), authController.ListUsers)
		users.PATCH("/:user_id", runtime.require(routePolicy{Permission: auth.PermissionUsersWrite, Action: "user.update", ResourceType: "user", ResourceParam: "user_id"}), authController.UpdateUser)
	}
	system.GET("/audit-events", runtime.require(routePolicy{Permission: auth.PermissionAuditRead, Action: "audit.list", ResourceType: "audit", SensitiveRead: true}), authController.ListAudit)

	compatible := apiRouter.Group("/compatible")
	{
		compatible.GET("/metadata/relation/all", runtime.require(routePolicy{Permission: auth.PermissionApplicationsRead, Action: "compatibility.metadata.read", ResourceType: "compatibility"}), func(c *gin.Context) { c.Status(http.StatusOK) })
		compatible.GET("/docker/info/getServiceProjectMap", runtime.require(routePolicy{Permission: auth.PermissionApplicationsRead, Action: "compatibility.application-map.read", ResourceType: "compatibility"}), compatibleController.GetAppInfo)
	}
}
