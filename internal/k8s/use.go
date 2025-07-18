package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewManagerUsageExample 展示新的管理器模式使用方式
func NewManagerUsageExample() {
	slog.Info("=== 新的管理器模式使用示例 ===")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 🎯 应用管理器使用示例
	slog.Info("1. 应用管理器使用示例")
	appManager := GetApplicationManager()

	// 部署一个新应用
	appReq := &ApplicationRequest{
		AppName:       "demo-app",
		Namespace:     "default",
		Env:           "dev",
		Image:         "nginx:1.20",
		Replicas:      2,
		Port:          80,
		LimitsMemory:  "256Mi",
		LimitsCPU:     "200m",
		RequestMemory: "128Mi",
		RequestCPU:    "100m",
		Labels: map[string]string{
			"team": "platform",
			"env":  "dev",
		},
	}

	if result, err := appManager.DeployApplication(ctx, appReq); err == nil {
		slog.Info("✅ 应用部署成功",
			"action", result.Action,
			"app", result.AppName,
			"message", result.Message)
	} else {
		slog.Error("❌ 应用部署失败", "error", err)
	}

	// 获取应用状态
	if status, err := appManager.GetApplicationStatus(ctx, "demo-app", "default", "dev"); err == nil {
		slog.Info("📊 应用状态",
			"app", status.AppName,
			"exists", status.Exists,
			"replicas", status.Replicas,
			"ready", status.ReadyReplicas,
			"image", status.Image)
	} else {
		slog.Error("获取应用状态失败", "error", err)
	}

	// 扩缩容应用
	if result, err := appManager.ScaleApplication(ctx, "demo-app", "default", "dev", 3); err == nil {
		slog.Info("✅ 应用扩容成功",
			"action", result.Action,
			"message", result.Message)
	} else {
		slog.Error("应用扩容失败", "error", err)
	}

	// 🎯 Pod管理器使用示例
	slog.Info("2. Pod管理器使用示例")
	podManager := GetPodManager()

	// 获取Pod列表
	pods, err := podManager.GetPodsInNamespace(ctx, "default", "dev", "app=demo-app")
	if err == nil {
		slog.Info("📋 Pod列表", "count", len(pods))
		for _, pod := range pods {
			slog.Info("Pod详情",
				"name", pod.Name,
				"status", pod.Status,
				"restarts", pod.RestartCount,
				"node", pod.NodeName,
				"ip", pod.PodIP)
		}

		// 获取Pod日志
		if len(pods) > 0 {
			tailLines := int64(10)
			logReq := &PodLogRequest{
				PodName:   pods[0].Name,
				Namespace: "default",
				Env:       "dev",
				TailLines: &tailLines,
			}

			if logs, err := podManager.GetPodLogs(ctx, logReq); err == nil {
				slog.Info("📜 Pod日志获取成功", "size", len(logs))
			} else {
				slog.Error("获取Pod日志失败", "error", err)
			}
		}
	} else {
		slog.Error("获取Pod列表失败", "error", err)
	}

	// 🎯 Service管理器使用示例
	slog.Info("3. Service管理器使用示例")
	serviceManager := GetServiceManager()

	// 创建Service
	serviceReq := &ServiceRequest{
		ServiceName: "demo-app",
		Namespace:   "default",
		Env:         "dev",
		Selector: map[string]string{
			"app": "demo-app",
		},
		Ports: []ServicePort{
			{
				Name: "http",
				Port: 80,
			},
		},
		ServiceType: corev1.ServiceTypeClusterIP,
		Labels: map[string]string{
			"team": "platform",
		},
	}

	if result, err := serviceManager.CreateOrUpdateService(ctx, serviceReq); err == nil {
		slog.Info("✅ Service创建成功",
			"action", result.Action,
			"service", result.Service.Name,
			"cluster_ip", result.Service.Spec.ClusterIP)
	} else {
		slog.Error("Service创建失败", "error", err)
	}

	// 获取Service端点
	if endpoints, err := serviceManager.GetServiceEndpoints(ctx, "demo-app", "default", "dev"); err == nil {
		slog.Info("🔗 Service端点", "count", len(endpoints))
		for _, endpoint := range endpoints {
			slog.Info("端点详情",
				"ip", endpoint.IP,
				"port", endpoint.Port,
				"ready", endpoint.Ready,
				"node", endpoint.NodeName)
		}
	} else {
		slog.Error("获取Service端点失败", "error", err)
	}
}

// AdvancedManagerExample 高级管理器使用示例
func AdvancedManagerExample() {
	slog.Info("=== 高级管理器使用示例 ===")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. 完整的应用部署流程
	slog.Info("1. 完整应用部署流程")

	// 使用便捷函数部署应用
	appReq := &ApplicationRequest{
		AppName:   "web-app",
		Namespace: "demo",
		Env:       "dev",
		Image:     "nginx:alpine",
		Replicas:  2,
		Port:      80,
	}

	if result, err := DeployApp(ctx, appReq); err == nil {
		slog.Info("✅ 应用部署完成", "action", result.Action)

		// 等待应用就绪
		time.Sleep(5 * time.Second)

		// 为应用创建Service
		if serviceResult, err := CreateAppService(ctx, "web-app", "demo", "dev", 80); err == nil {
			slog.Info("✅ Service创建完成", "action", serviceResult.Action)
		}

		// 获取应用日志
		tailLines := int64(5)
		if logs, err := GetAppLogs(ctx, "web-app", "demo", "dev", &tailLines); err == nil {
			slog.Info("📜 应用日志", "preview", logs[:min(len(logs), 100)])
		}
	} else {
		slog.Error("应用部署失败", "error", err)
	}

	// 2. Pod管理高级操作
	slog.Info("2. Pod管理高级操作")
	podManager := GetPodManager()

	// 重启应用的所有Pod
	if restartedPods, err := RestartApp(ctx, "web-app", "demo", "dev"); err == nil {
		slog.Info("🔄 Pod重启完成", "restarted_pods", len(restartedPods))
	}

	// 等待Pod重新就绪
	time.Sleep(10 * time.Second)

	// 等待特定Pod就绪
	if pods, err := podManager.GetPodsInNamespace(ctx, "demo", "dev", "app=web-app"); err == nil && len(pods) > 0 {
		if err := podManager.WaitForPodReady(ctx, pods[0].Name, "demo", "dev", 30*time.Second); err == nil {
			slog.Info("✅ Pod就绪检查完成")
		} else {
			slog.Error("Pod就绪检查超时", "error", err)
		}
	}

	// 3. Service高级管理
	slog.Info("3. Service高级管理")
	serviceManager := GetServiceManager()

	// 暴露Service为NodePort类型
	if result, err := serviceManager.ExposeService(ctx, "web-app", "demo", "dev", corev1.ServiceTypeNodePort); err == nil {
		slog.Info("✅ Service暴露完成",
			"type", result.Service.Spec.Type,
			"ports", len(result.Service.Spec.Ports))
	}

	// 获取Service信息
	if serviceInfo, err := GetAppService(ctx, "web-app", "demo", "dev"); err == nil {
		slog.Info("📊 Service信息",
			"name", serviceInfo.Name,
			"type", serviceInfo.Type,
			"cluster_ip", serviceInfo.ClusterIP,
			"ports", len(serviceInfo.Ports))
	}
}

// ProductionWorkflow 生产级工作流示例
func ProductionWorkflow() {
	slog.Info("=== 生产级工作流示例 ===")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	appName := "production-app"
	namespace := "production"
	env := "moni" // 使用监控环境模拟生产

	// 步骤1: 环境预检查
	slog.Info("🔍 步骤1: 环境预检查")
	if !IsEnvAvailable(env) {
		slog.Error("❌ 环境不可用，流程终止", "env", env)
		return
	}

	if !CanAccessNamespace(env, namespace) {
		slog.Warn("⚠️ 命名空间不可访问，可能需要创建", "namespace", namespace)
	}

	// 步骤2: 检查现有应用状态
	slog.Info("🔍 步骤2: 检查现有应用")
	appManager := GetApplicationManager()
	currentStatus, err := appManager.GetApplicationStatus(ctx, appName, namespace, env)
	if err == nil && currentStatus.Exists {
		slog.Info("📊 发现现有应用",
			"replicas", currentStatus.Replicas,
			"ready", currentStatus.ReadyReplicas,
			"current_image", currentStatus.Image)
	} else {
		slog.Info("💡 应用不存在，将进行首次部署")
	}

	// 步骤3: 应用部署/更新
	slog.Info("🚀 步骤3: 应用部署")
	deployReq := &ApplicationRequest{
		AppName:       appName,
		Namespace:     namespace,
		Env:           env,
		Image:         "nginx:1.21-alpine",
		Replicas:      3,
		Port:          80,
		LimitsMemory:  "512Mi",
		LimitsCPU:     "500m",
		RequestMemory: "256Mi",
		RequestCPU:    "200m",
		Labels: map[string]string{
			"app":     appName,
			"env":     env,
			"version": "v1.0.0",
			"tier":    "frontend",
		},
	}

	if result, err := appManager.DeployApplication(ctx, deployReq); err == nil {
		slog.Info("✅ 应用部署成功", "action", result.Action)
	} else {
		slog.Error("❌ 应用部署失败", "error", err)
		return
	}

	// 步骤4: 创建Service
	slog.Info("🌐 步骤4: 创建Service")
	if _, err := CreateAppService(ctx, appName, namespace, env, 80); err == nil {
		slog.Info("✅ Service创建成功")
	} else {
		slog.Error("❌ Service创建失败", "error", err)
	}

	// 步骤5: 健康检查和验证
	slog.Info("🏥 步骤5: 健康检查")
	podManager := GetPodManager()

	// 等待Pod就绪
	time.Sleep(15 * time.Second)

	if pods, err := podManager.GetPodsInNamespace(ctx, namespace, env, fmt.Sprintf("app=%s", appName)); err == nil {
		readyCount := 0
		for _, pod := range pods {
			if pod.Status == "Running,Ready" {
				readyCount++
			}
		}

		slog.Info("📊 Pod状态检查",
			"total_pods", len(pods),
			"ready_pods", readyCount,
			"target_replicas", deployReq.Replicas)

		if int32(readyCount) == deployReq.Replicas {
			slog.Info("✅ 所有Pod已就绪，部署成功")
		} else {
			slog.Warn("⚠️ 部分Pod未就绪，需要进一步检查")
		}
	}

	// 步骤6: 生产验证
	slog.Info("✅ 步骤6: 生产验证完成")
	slog.Info("🎉 生产级部署流程完成！")
}

// BusinessUsageExample 展示面向业务的使用方式（保持向后兼容）
func BusinessUsageExample() {
	slog.Info("=== 业务层K8s操作示例 ===")

	// 🎯 您关心的实际问题

	// 1. 检查环境是否可用
	slog.Info("1. 检查环境可用性")
	if IsEnvAvailable("dev") {
		slog.Info("✅ dev环境可用")
	} else {
		slog.Warn("❌ dev环境不可用")
	}

	// 2. 检查能否访问特定命名空间
	slog.Info("2. 检查命名空间访问")
	if CanAccessNamespace("dev", "default") {
		slog.Info("✅ 可以访问dev环境的default命名空间")
	} else {
		slog.Warn("❌ 无法访问dev环境的default命名空间")
	}

	// 3. 获取环境下的所有命名空间
	slog.Info("3. 获取命名空间列表")
	if namespaces, err := GetNamespacesInEnv("dev"); err == nil {
		slog.Info("dev环境命名空间列表", "count", len(namespaces))
		for _, ns := range namespaces {
			slog.Info("命名空间详情",
				"name", ns.Name,
				"status", ns.Status,
				"pods", ns.PodCount,
				"apps", ns.AppCount)
		}
	} else {
		slog.Error("获取命名空间列表失败", "error", err)
	}

	// 4. 获取指定命名空间下的应用列表
	slog.Info("4. 获取应用列表")
	if apps, err := GetAppsInNamespace("dev", "default"); err == nil {
		slog.Info("default命名空间应用列表", "count", len(apps))
		for _, app := range apps {
			slog.Info("应用详情",
				"name", app.Name,
				"status", app.Status,
				"replicas", app.Replicas,
				"ready", app.ReadyReplicas,
				"image", app.Image)
		}
	} else {
		slog.Error("获取应用列表失败", "error", err)
	}

	// 5. 检查特定应用状态
	slog.Info("5. 检查应用状态")
	if app, err := CheckAppStatus("dev", "default", "my-app"); err == nil {
		slog.Info("应用状态检查",
			"name", app.Name,
			"status", app.Status,
			"replicas", app.Replicas,
			"ready", app.ReadyReplicas)
	} else {
		slog.Error("检查应用状态失败", "error", err)
	}
}

// RealWorldUsage 真实业务场景使用示例
func RealWorldUsage() {
	slog.Info("=== 真实业务场景示例 ===")

	// 场景1: 发布前检查环境状态
	slog.Info("场景1: 发布前环境检查")
	envStatus := CheckEnvHealth("dev")
	if envStatus.Available {
		slog.Info("环境检查通过",
			"env", envStatus.Name,
			"nodes", envStatus.NodeCount,
			"pods", envStatus.PodCount,
			"namespaces", envStatus.Namespaces)
	} else {
		slog.Error("环境检查失败", "env", envStatus.Name, "error", envStatus.Error)
		return
	}

	// 场景2: 检查目标命名空间是否存在且可访问
	targetNamespace := "my-app"
	if !CanAccessNamespace("dev", targetNamespace) {
		slog.Error("目标命名空间不可访问", "namespace", targetNamespace)
		return
	}

	// 场景3: 检查同名应用是否已存在
	appName := "my-service"
	if existingApp, err := CheckAppStatus("dev", targetNamespace, appName); err == nil {
		slog.Warn("应用已存在",
			"name", existingApp.Name,
			"status", existingApp.Status,
			"current_image", existingApp.Image)
		// 可以决定是否更新还是跳过
	} else {
		slog.Info("应用不存在，可以创建新应用", "name", appName)
	}

	// 场景4: 监控环境整体状况
	slog.Info("场景4: 环境监控")
	allStatus := GetAllEnvsStatus()
	for envName, status := range allStatus {
		if status.Available {
			slog.Info("环境正常",
				"env", envName,
				"nodes", status.NodeCount,
				"pods", status.PodCount)
		} else {
			slog.Error("环境异常", "env", envName, "error", status.Error)
		}
	}
}

// DeploymentWorkflow 部署工作流示例（增强版）
func DeploymentWorkflow(env, namespace, appName, image string) error {
	slog.Info("开始部署工作流", "env", env, "namespace", namespace, "app", appName)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// 步骤1: 检查环境是否可用
	if !IsEnvAvailable(env) {
		slog.Error("环境不可用，部署终止", "env", env)
		return fmt.Errorf("环境 %s 不可用", env)
	}
	slog.Info("✅ 环境检查通过", "env", env)

	// 步骤2: 检查命名空间是否可访问
	if !CanAccessNamespace(env, namespace) {
		slog.Error("命名空间不可访问，部署终止", "namespace", namespace)
		return fmt.Errorf("命名空间 %s 不可访问", namespace)
	}
	slog.Info("✅ 命名空间检查通过", "namespace", namespace)

	// 步骤3: 检查应用当前状态
	if currentApp, err := CheckAppStatus(env, namespace, appName); err == nil {
		slog.Info("发现已存在应用",
			"name", currentApp.Name,
			"status", currentApp.Status,
			"current_image", currentApp.Image,
			"new_image", image)
		// 这里执行更新逻辑
	} else {
		slog.Info("应用不存在，将创建新应用", "name", appName)
		// 这里执行创建逻辑
	}

	// 步骤4: 使用新的管理器执行部署
	slog.Info("🚀 开始部署应用...", "app", appName, "image", image)

	appReq := &ApplicationRequest{
		AppName:   appName,
		Namespace: namespace,
		Env:       env,
		Image:     image,
		Replicas:  2,
		Port:      80,
	}

	if result, err := DeployApp(ctx, appReq); err == nil {
		slog.Info("✅ 应用部署成功", "action", result.Action)

		// 创建Service
		if _, err := CreateAppService(ctx, appName, namespace, env, 80); err == nil {
			slog.Info("✅ Service创建成功")
		}
	} else {
		return fmt.Errorf("部署失败: %w", err)
	}

	slog.Info("✅ 部署完成", "app", appName)
	return nil
}

// MonitoringWorkflow 监控工作流示例
func MonitoringWorkflow() {
	slog.Info("=== 监控工作流示例 ===")

	environments := []string{"dev", "test", "moni"}

	for _, env := range environments {
		slog.Info("监控环境", "env", env)

		// 检查环境整体健康状况
		status := CheckEnvHealth(env)
		if !status.Available {
			slog.Error("环境异常", "env", env, "error", status.Error)
			continue
		}

		// 获取关键命名空间的应用状态
		keyNamespaces := []string{"default", "kube-system", "my-app"}
		for _, ns := range keyNamespaces {
			if CanAccessNamespace(env, ns) {
				if apps, err := GetAppsInNamespace(env, ns); err == nil {
					runningApps := 0
					for _, app := range apps {
						if app.Status == "Running" {
							runningApps++
						}
					}
					slog.Info("命名空间应用状态",
						"env", env,
						"namespace", ns,
						"total_apps", len(apps),
						"running_apps", runningApps)
				}
			}
		}
	}
}

// QuickStatusCheck 快速状态检查
func QuickStatusCheck() {
	slog.Info("=== 快速状态检查 ===")

	// 快速检查所有环境是否在线
	envs := map[string]string{
		"dev":  "开发环境",
		"test": "测试环境",
		"moni": "监控环境",
	}

	for env, desc := range envs {
		if IsEnvAvailable(env) {
			slog.Info("✅ 环境在线", "env", desc)
		} else {
			slog.Error("❌ 环境离线", "env", desc)
		}
	}

	// 检查关键命名空间
	criticalNamespaces := []string{"default", "kube-system"}
	for _, env := range []string{"dev", "test", "moni"} {
		if IsEnvAvailable(env) {
			for _, ns := range criticalNamespaces {
				if CanAccessNamespace(env, ns) {
					slog.Info("✅ 命名空间可访问", "env", env, "namespace", ns)
				} else {
					slog.Warn("⚠️ 命名空间不可访问", "env", env, "namespace", ns)
				}
			}
		}
	}
}

// ExampleUsage 保持原有的底层示例（可选使用）
func ExampleUsage() {
	slog.Info("=== 底层K8s客户端示例 ===")

	// 🚀 直接通过全局变量访问（底层操作）

	// 开发环境操作
	if Dev != nil {
		pods, err := Dev.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
		if err != nil {
			slog.Error("获取dev环境Pod列表失败", "error", err)
		} else {
			slog.Info("dev环境Pod数量", "count", len(pods.Items))
		}
	}

	// 测试环境操作
	if Test != nil {
		namespaces, err := Test.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		if err != nil {
			slog.Error("获取test环境命名空间失败", "error", err)
		} else {
			slog.Info("test环境命名空间数量", "count", len(namespaces.Items))
		}
	}
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
