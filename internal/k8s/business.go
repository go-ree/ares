package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// 环境名称标准化映射
var envNameMap = map[string]string{
	"dev":         "dev",
	"development": "dev",
	"test":        "test",
	"testing":     "test",
	"moni":        "moni",
	"monitor":     "moni",
	"staging":     "moni",
}

// 全局管理器实例
var (
	appManager     *ApplicationManager
	podManager     *PodManager
	serviceManager *ServiceManager
)

// EnvStatus 环境状态
type EnvStatus struct {
	Name       string `json:"name"`       // 环境名称
	Available  bool   `json:"available"`  // 是否可用
	Error      string `json:"error"`      // 错误信息
	NodeCount  int    `json:"node_count"` // 节点数量
	PodCount   int    `json:"pod_count"`  // Pod总数
	Namespaces int    `json:"namespaces"` // 命名空间数量
	CheckTime  string `json:"check_time"` // 检查时间
}

// AppInfo 应用信息（为了向后兼容保留这个类型）
type AppInfo struct {
	Name          string `json:"name"`           // 应用名称
	Namespace     string `json:"namespace"`      // 命名空间
	Replicas      int32  `json:"replicas"`       // 副本数
	ReadyReplicas int32  `json:"ready_replicas"` // 就绪副本数
	Status        string `json:"status"`         // 状态
	Image         string `json:"image"`          // 镜像
	CreatedAt     string `json:"created_at"`     // 创建时间
}

// NamespaceInfo 命名空间信息
type NamespaceInfo struct {
	Name      string `json:"name"`       // 命名空间名称
	Status    string `json:"status"`     // 状态
	PodCount  int    `json:"pod_count"`  // Pod数量
	AppCount  int    `json:"app_count"`  // 应用数量
	CreatedAt string `json:"created_at"` // 创建时间
}

// GetApplicationManager 获取应用管理器实例
func GetApplicationManager() *ApplicationManager {
	if appManager == nil {
		appManager = NewApplicationManager()
	}
	return appManager
}

// GetPodManager 获取Pod管理器实例
func GetPodManager() *PodManager {
	if podManager == nil {
		podManager = NewPodManager()
	}
	return podManager
}

// GetServiceManager 获取Service管理器实例
func GetServiceManager() *ServiceManager {
	if serviceManager == nil {
		serviceManager = NewServiceManager()
	}
	return serviceManager
}

// CheckEnvHealth 检查指定环境的健康状态
func CheckEnvHealth(env string) *EnvStatus {
	envKey := normalizeEnvironmentName(env)
	client := getClientByEnv(envKey)
	envName := envNameMap[envKey]

	if client == nil && envName == "" {
		return &EnvStatus{
			Name:      env,
			Available: false,
			Error:     fmt.Sprintf("未知环境: %s", env),
			CheckTime: time.Now().Format("2006-01-02 15:04:05"),
		}
	}

	status := &EnvStatus{
		Name:      envName,
		Available: false,
		CheckTime: time.Now().Format("2006-01-02 15:04:05"),
	}

	if client == nil {
		status.Error = "客户端未初始化"
		return status
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 检查集群连通性（ServerVersion 不支持 context）
	if _, err := client.Discovery().ServerVersion(); err != nil {
		status.Error = fmt.Sprintf("集群连接失败: %v", err)
		return status
	}

	// 获取节点信息
	if nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		status.NodeCount = len(nodes.Items)
	}

	// 获取所有Pod数量
	if pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		status.PodCount = len(pods.Items)
	}

	// 获取命名空间数量
	if namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		status.Namespaces = len(namespaces.Items)
	}

	status.Available = true
	slog.Info("环境健康检查完成", "env", envName, "available", status.Available)
	return status
}

// GetAppsInNamespace 获取指定环境和命名空间下的应用列表（使用ApplicationManager）
func GetAppsInNamespace(env, namespace string) ([]AppInfo, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := getClientByEnv(env)
	if client == nil {
		return nil, fmt.Errorf("环境 %s 客户端未初始化", env)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 直接使用K8s客户端获取Deployment列表（向后兼容）
	deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取应用列表失败: %v", err)
	}

	var apps []AppInfo
	for _, deploy := range deployments.Items {
		app := AppInfo{
			Name:          deploy.Name,
			Namespace:     deploy.Namespace,
			Replicas:      *deploy.Spec.Replicas,
			ReadyReplicas: deploy.Status.ReadyReplicas,
			CreatedAt:     deploy.CreationTimestamp.Format("2006-01-02 15:04:05"),
		}

		// 获取镜像信息
		if len(deploy.Spec.Template.Spec.Containers) > 0 {
			app.Image = deploy.Spec.Template.Spec.Containers[0].Image
		}

		// 判断应用状态
		if deploy.Status.ReadyReplicas == *deploy.Spec.Replicas && deploy.Status.ReadyReplicas > 0 {
			app.Status = "Running"
		} else if deploy.Status.ReadyReplicas == 0 {
			app.Status = "Stopped"
		} else {
			app.Status = "Partial"
		}

		apps = append(apps, app)
	}

	slog.Info("获取应用列表成功", "env", env, "namespace", namespace, "app_count", len(apps))
	return apps, nil
}

// GetNamespacesInEnv 获取指定环境下的命名空间列表（使用PodManager）
func GetNamespacesInEnv(env string) ([]NamespaceInfo, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := getClientByEnv(env)
	if client == nil {
		return nil, fmt.Errorf("环境 %s 客户端未初始化", env)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	namespaces, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取命名空间列表失败: %v", err)
	}

	var nsInfos []NamespaceInfo
	for _, ns := range namespaces.Items {
		nsInfo := NamespaceInfo{
			Name:      ns.Name,
			Status:    string(ns.Status.Phase),
			CreatedAt: ns.CreationTimestamp.Format("2006-01-02 15:04:05"),
		}

		// 使用PodManager获取Pod数量
		if podManager != nil {
			if pods, err := podManager.GetPodsInNamespace(ctx, ns.Name, env, ""); err == nil {
				nsInfo.PodCount = len(pods)
			}
		} else {
			// 回退到直接API调用
			if pods, err := client.CoreV1().Pods(ns.Name).List(ctx, metav1.ListOptions{}); err == nil {
				nsInfo.PodCount = len(pods.Items)
			}
		}

		// 获取该命名空间下的应用数量
		if deployments, err := client.AppsV1().Deployments(ns.Name).List(ctx, metav1.ListOptions{}); err == nil {
			nsInfo.AppCount = len(deployments.Items)
		}

		nsInfos = append(nsInfos, nsInfo)
	}

	slog.Info("获取命名空间列表成功", "env", env, "namespace_count", len(nsInfos))
	return nsInfos, nil
}

// CheckAppStatus 检查指定环境、命名空间下的应用状态（使用ApplicationManager）
func CheckAppStatus(env, namespace, appName string) (*AppInfo, error) {
	if appManager == nil {
		appManager = NewApplicationManager()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 使用ApplicationManager获取应用状态
	appStatus, err := appManager.GetApplicationStatus(ctx, appName, namespace, env)
	if err != nil {
		return nil, fmt.Errorf("获取应用 %s 状态失败: %v", appName, err)
	}

	// 转换为兼容的AppInfo格式
	app := &AppInfo{
		Name:          appStatus.AppName,
		Namespace:     appStatus.Namespace,
		Replicas:      appStatus.Replicas,
		ReadyReplicas: appStatus.ReadyReplicas,
		Image:         appStatus.Image,
		CreatedAt:     appStatus.CreatedAt.Format("2006-01-02 15:04:05"),
	}

	// 判断应用状态
	if appStatus.ReadyReplicas == appStatus.Replicas && appStatus.ReadyReplicas > 0 {
		app.Status = "Running"
	} else if appStatus.ReadyReplicas == 0 {
		app.Status = "Stopped"
	} else {
		app.Status = "Partial"
	}

	slog.Info("检查应用状态完成", "env", env, "namespace", namespace, "app", appName, "status", app.Status)
	return app, nil
}

// IsEnvAvailable 快速检查环境是否可用
func IsEnvAvailable(env string) bool {
	client := getClientByEnv(env)
	if client == nil {
		return false
	}

	// ServerVersion() 不支持 context，所以直接调用
	_, err := client.Discovery().ServerVersion()
	return err == nil
}

// CanAccessNamespace 检查是否能访问指定环境的命名空间
func CanAccessNamespace(env, namespace string) bool {
	client := getClientByEnv(env)
	if client == nil {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	return err == nil
}

// GetAllEnvsStatus 获取所有环境的状态概览
func GetAllEnvsStatus() map[string]*EnvStatus {
	envs := []string{"dev", "test", "moni"}
	results := make(map[string]*EnvStatus)

	for _, env := range envs {
		results[env] = CheckEnvHealth(env)
	}

	return results
}

// getClientByEnv 内部函数：根据环境名获取客户端
func getClientByEnv(env string) *kubernetes.Clientset {
	switch normalizeEnvironmentName(env) {
	case "dev":
		return DefaultClient(EnvDev)
	case "test":
		return DefaultClient(EnvTest)
	case "moni":
		return DefaultClient(EnvStage)
	default:
		return nil
	}
}

func normalizeEnvironmentName(env string) string {
	return envNameMap[strings.ToLower(strings.TrimSpace(env))]
}

// ---- 新增便捷函数，使用新的管理器模式 ----

// DeployApp 部署应用（使用ApplicationManager）
func DeployApp(ctx context.Context, req *ApplicationRequest) (*ApplicationResult, error) {
	if appManager == nil {
		appManager = NewApplicationManager()
	}
	return appManager.DeployApplication(ctx, req)
}

// DeleteApp 删除应用（使用ApplicationManager）
func DeleteApp(ctx context.Context, appName, namespace, env string) (*ApplicationResult, error) {
	if appManager == nil {
		appManager = NewApplicationManager()
	}
	return appManager.DeleteApplication(ctx, appName, namespace, env)
}

// ScaleApp 扩缩容应用（使用ApplicationManager）
func ScaleApp(ctx context.Context, appName, namespace, env string, replicas int32) (*ApplicationResult, error) {
	if appManager == nil {
		appManager = NewApplicationManager()
	}
	return appManager.ScaleApplication(ctx, appName, namespace, env, replicas)
}

// RestartApp 重启应用的所有Pod（使用PodManager）
func RestartApp(ctx context.Context, appName, namespace, env string) ([]string, error) {
	if podManager == nil {
		podManager = NewPodManager()
	}
	return podManager.RestartPodsBySelector(ctx, namespace, env, fmt.Sprintf("app=%s", appName))
}

// GetAppLogs 获取应用日志（使用PodManager）
func GetAppLogs(ctx context.Context, appName, namespace, env string, tailLines *int64) (string, error) {
	if podManager == nil {
		podManager = NewPodManager()
	}

	// 获取应用的Pod列表
	pods, err := podManager.GetPodsInNamespace(ctx, namespace, env, fmt.Sprintf("app=%s", appName))
	if err != nil {
		return "", fmt.Errorf("获取应用Pod列表失败: %w", err)
	}

	if len(pods) == 0 {
		return "", fmt.Errorf("应用 %s 没有运行中的Pod", appName)
	}

	// 获取第一个Pod的日志
	logReq := &PodLogRequest{
		PodName:   pods[0].Name,
		Namespace: namespace,
		Env:       env,
		TailLines: tailLines,
	}

	return podManager.GetPodLogs(ctx, logReq)
}

// CreateAppService 为应用创建Service（使用ServiceManager）
func CreateAppService(ctx context.Context, appName, namespace, env string, port int32) (*ServiceResult, error) {
	if serviceManager == nil {
		serviceManager = NewServiceManager()
	}

	serviceReq := &ServiceRequest{
		ServiceName: appName,
		Namespace:   namespace,
		Env:         env,
		Selector: map[string]string{
			"app": appName,
		},
		Ports: []ServicePort{
			{
				Port: port,
			},
		},
	}

	return serviceManager.CreateOrUpdateService(ctx, serviceReq)
}

// GetAppService 获取应用的Service信息（使用ServiceManager）
func GetAppService(ctx context.Context, appName, namespace, env string) (*ServiceInfo, error) {
	if serviceManager == nil {
		serviceManager = NewServiceManager()
	}
	return serviceManager.GetService(ctx, appName, namespace, env)
}
