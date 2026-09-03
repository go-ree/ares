package controller

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/go-ree/ares/internal/api/util"
	"github.com/go-ree/ares/internal/k8s"
	"log/slog"
	"strings"
)

type PodController struct {
	podManager *k8s.PodManager
}

func ensureK8sEnabled(c *gin.Context) bool {
	if k8s.IsInitialized() {
		return true
	}
	c.JSON(503, util.ResponseFailure("Kubernetes 集成未启用", "kubernetes integration is disabled"))
	return false
}

func NewPodController() *PodController {
	return &PodController{
		podManager: k8s.NewPodManager(),
	}
}

// GetAppPods
// @Tags K8S
// @Summary 获取应用的pod列表
// @Param app_name query string true "应用名称"
// @Param env query string true "环境"
// @Param namespace query string false "命名空间" default(default)
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]k8s.PodInfo} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/k8s/pod/query [get]
func (pc *PodController) GetAppPods(c *gin.Context) {
	if !ensureK8sEnabled(c) {
		return
	}
	// 从查询参数获取值
	appName := c.Query("app_name")
	env := c.Query("env")
	namespace := c.DefaultQuery("namespace", "default")

	// 参数验证
	if appName == "" {
		c.JSON(400, util.ResponseFailure("参数错误", "app_name不能为空"))
		return
	}
	if env == "" {
		c.JSON(400, util.ResponseFailure("参数错误", "env不能为空"))
		return
	}

	slog.Info("获取应用Pod列表",
		"app_name", appName,
		"env", env,
		"namespace", namespace)

	// 尝试多种查询方式
	var podInfo []k8s.PodInfo
	var err error

	// 方式1: 通过app.kubernetes.io/name标签查询（标准K8s标签）
	labelSelector := fmt.Sprintf("app.kubernetes.io/name=%s", appName)
	podInfo, err = pc.podManager.GetPodsInNamespace(c.Request.Context(), namespace, env, labelSelector)
	if err == nil && len(podInfo) > 0 {
		slog.Info("通过app.kubernetes.io/name标签查询成功",
			"app_name", appName,
			"env", env,
			"namespace", namespace,
			"pod_count", len(podInfo))
		c.JSON(200, util.ResponseSuccessful("查询成功", podInfo))
		return
	}

	// 方式2: 通过app标签查询（传统标签）
	labelSelector = fmt.Sprintf("app=%s", appName)
	podInfo, err = pc.podManager.GetPodsInNamespace(c.Request.Context(), namespace, env, labelSelector)
	if err == nil && len(podInfo) > 0 {
		slog.Info("通过app标签查询成功",
			"app_name", appName,
			"env", env,
			"namespace", namespace,
			"pod_count", len(podInfo))
		c.JSON(200, util.ResponseSuccessful("查询成功", podInfo))
		return
	}

	// 方式3: 通过pod名称前缀查询（如果app_name看起来像pod名称）
	if strings.Contains(appName, "-") {
		// 构造pod名称前缀查询
		podNamePrefix := appName
		podInfo, err = pc.podManager.GetPodsByNamePrefix(c.Request.Context(), namespace, env, podNamePrefix)
		if err == nil && len(podInfo) > 0 {
			slog.Info("通过pod名称前缀查询成功",
				"app_name", appName,
				"env", env,
				"namespace", namespace,
				"pod_count", len(podInfo))
			c.JSON(200, util.ResponseSuccessful("查询成功", podInfo))
			return
		}
	}

	// 方式4: 获取所有pod，然后过滤
	podInfo, err = pc.podManager.GetPodsInNamespace(c.Request.Context(), namespace, env, "")
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		slog.Error("获取pod信息失败",
			"app_name", appName,
			"env", env,
			"namespace", namespace,
			"error", err.Error())
		return
	}

	// 过滤包含app_name的pod
	var filteredPods []k8s.PodInfo
	for _, pod := range podInfo {
		if strings.Contains(pod.Name, appName) {
			filteredPods = append(filteredPods, pod)
		}
	}

	slog.Info("Pod列表获取完成",
		"app_name", appName,
		"env", env,
		"namespace", namespace,
		"total_pods", len(podInfo),
		"filtered_pods", len(filteredPods))

	c.JSON(200, util.ResponseSuccessful("查询成功", filteredPods))
}

// GetAllPods
// @Tags K8S
// @Summary 获取命名空间中的所有pod列表（调试用）
// @Param env query string true "环境"
// @Param namespace query string false "命名空间" default(default)
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]k8s.PodInfo} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/k8s/pod/list [get]
func (pc *PodController) GetAllPods(c *gin.Context) {
	if !ensureK8sEnabled(c) {
		return
	}
	// 从查询参数获取值
	env := c.Query("env")
	namespace := c.DefaultQuery("namespace", "default")

	// 参数验证
	if env == "" {
		c.JSON(400, util.ResponseFailure("参数错误", "env不能为空"))
		return
	}

	slog.Info("获取所有Pod列表",
		"env", env,
		"namespace", namespace)

	// 获取所有Pod
	podInfo, err := pc.podManager.GetPodsInNamespace(c.Request.Context(), namespace, env, "")
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		slog.Error("获取pod信息失败",
			"env", env,
			"namespace", namespace,
			"error", err.Error())
		return
	}

	slog.Info("所有Pod列表获取成功",
		"env", env,
		"namespace", namespace,
		"pod_count", len(podInfo))

	c.JSON(200, util.ResponseSuccessful("查询成功", podInfo))
}

// GetK8sDebugInfo
// @Tags K8S
// @Summary 获取K8s环境调试信息
// @Success 200 {object} util.ResponseTemplate{code=int,result=map[string]interface{}} "成功"
// @Router	/api/v1/k8s/debug [get]
func (pc *PodController) GetK8sDebugInfo(c *gin.Context) {
	debugInfo := make(map[string]interface{})
	debugInfo["enabled"] = k8s.IsInitialized()

	// 检查是否已初始化
	debugInfo["initialized"] = k8s.IsInitialized()

	// 检查各环境状态
	envStatus := k8s.GetAllEnvsStatus()
	debugInfo["env_status"] = envStatus

	// 检查运行时实际配置的客户端，不假设固定环境集合。
	clientStatus := make(map[string]bool)
	for _, environment := range k8s.ListEnvironments() {
		code := string(environment)
		clientStatus[code] = k8s.DefaultClient(environment) != nil
	}
	debugInfo["client_status"] = clientStatus

	// 详细的环境检查
	for _, environment := range k8s.ListEnvironments() {
		env := string(environment)
		available := k8s.IsEnvAvailable(env)
		debugInfo[fmt.Sprintf("%s_available", env)] = available

		if available {
			// 尝试获取版本信息
			if envStatus[env] != nil && envStatus[env].Available {
				debugInfo[fmt.Sprintf("%s_details", env)] = map[string]interface{}{
					"nodes":      envStatus[env].NodeCount,
					"pods":       envStatus[env].PodCount,
					"namespaces": envStatus[env].Namespaces,
					"check_time": envStatus[env].CheckTime,
				}
			}
		} else {
			if envStatus[env] != nil {
				debugInfo[fmt.Sprintf("%s_error", env)] = envStatus[env].Error
			}
		}
	}

	slog.Info("K8s调试信息获取完成", "debug_info", debugInfo)
	c.JSON(200, util.ResponseSuccessful("调试信息获取成功", debugInfo))
}

// GetDeploymentsByLabel
// @Tags K8S
// @Summary 通过标签查询Deployment
// @Param label_selector query string true "标签选择器，如：app.kubernetes.io/name=abtesting-ms"
// @Param env query string true "环境"
// @Param namespace query string false "命名空间" default(default)
// @Success 200 {object} util.ResponseTemplate{code=int,result=[]k8s.ApplicationStatus} "成功"
// @Failure 400 {object} util.ResponseTemplate{code=int} "请求错误"
// @Failure 500 {object} util.ResponseTemplate{code=int} "内部错误"
// @Router	/api/v1/k8s/deployment/query [get]
func (pc *PodController) GetDeploymentsByLabel(c *gin.Context) {
	if !ensureK8sEnabled(c) {
		return
	}
	// 从查询参数获取值
	labelSelector := c.Query("label_selector")
	env := c.Query("env")
	namespace := c.DefaultQuery("namespace", "default")

	// 参数验证
	if labelSelector == "" {
		c.JSON(400, util.ResponseFailure("参数错误", "label_selector不能为空"))
		return
	}
	if env == "" {
		c.JSON(400, util.ResponseFailure("参数错误", "env不能为空"))
		return
	}

	slog.Info("通过标签查询Deployment",
		"label_selector", labelSelector,
		"env", env,
		"namespace", namespace)

	// 创建ApplicationManager实例
	appManager := k8s.NewApplicationManager()

	// 调用ApplicationManager查询Deployment
	deployments, err := appManager.GetDeploymentsByLabel(c.Request.Context(), namespace, env, labelSelector)
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		slog.Error("查询Deployment失败",
			"label_selector", labelSelector,
			"env", env,
			"namespace", namespace,
			"error", err.Error())
		return
	}

	slog.Info("Deployment查询成功",
		"label_selector", labelSelector,
		"env", env,
		"namespace", namespace,
		"count", len(deployments))

	c.JSON(200, util.ResponseSuccessful("查询成功", deployments))
}
