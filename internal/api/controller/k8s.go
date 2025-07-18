package controller

import (
	"ares/internal/api/util"
	"ares/internal/k8s"
	"fmt"
	"github.com/gin-gonic/gin"
	"log/slog"
)

type PodController struct {
	podManager *k8s.PodManager
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

	// 构造标签选择器
	labelSelector := fmt.Sprintf("app=%s", appName)

	// 调用PodManager获取Pod列表
	podInfo, err := pc.podManager.GetPodsInNamespace(c.Request.Context(), namespace, env, labelSelector)
	if err != nil {
		c.JSON(500, util.ResponseFailure("查询失败", err.Error()))
		slog.Error("获取pod信息失败",
			"app_name", appName,
			"env", env,
			"namespace", namespace,
			"error", err.Error())
		return
	}

	slog.Info("Pod列表获取成功",
		"app_name", appName,
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

	// 检查是否已初始化
	debugInfo["initialized"] = k8s.IsInitialized()

	// 检查各环境状态
	envStatus := k8s.GetAllEnvsStatus()
	debugInfo["env_status"] = envStatus

	// 检查全局客户端状态
	clientStatus := map[string]bool{
		"dev_client":  k8s.Dev != nil,
		"test_client": k8s.Test != nil,
		"moni_client": k8s.Moni != nil,
	}
	debugInfo["client_status"] = clientStatus

	// 详细的环境检查
	for _, env := range []string{"dev", "test", "moni"} {
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
