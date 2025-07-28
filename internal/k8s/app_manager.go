package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// ApplicationManager 应用管理器
type ApplicationManager struct {
	logger *slog.Logger
}

// NewApplicationManager 创建新的应用管理器
func NewApplicationManager() *ApplicationManager {
	return &ApplicationManager{
		logger: slog.Default(),
	}
}

// ApplicationRequest 应用部署请求
type ApplicationRequest struct {
	AppName       string            `json:"app_name" validate:"required"`
	Namespace     string            `json:"namespace" validate:"required"`
	Env           string            `json:"env" validate:"required,oneof=dev test moni"`
	Image         string            `json:"image" validate:"required"`
	Replicas      int32             `json:"replicas" validate:"min=1"`
	Port          int32             `json:"port" validate:"min=1,max=65535"`
	LimitsMemory  string            `json:"limits_memory"`
	LimitsCPU     string            `json:"limits_cpu"`
	RequestMemory string            `json:"request_memory"`
	RequestCPU    string            `json:"request_cpu"`
	Labels        map[string]string `json:"labels"`
	EnvVars       []corev1.EnvVar   `json:"env_vars,omitempty"`
}

// ApplicationResult 应用操作结果
type ApplicationResult struct {
	Action     string             `json:"action"` // 创建/更新/删除
	AppName    string             `json:"app_name"`
	Namespace  string             `json:"namespace"`
	Env        string             `json:"env"`
	Deployment *appsv1.Deployment `json:"deployment,omitempty"`
	Service    *corev1.Service    `json:"service,omitempty"`
	Message    string             `json:"message"`
}

// DeployApplication 部署应用（声明式）
func (am *ApplicationManager) DeployApplication(ctx context.Context, req *ApplicationRequest) (*ApplicationResult, error) {
	// 1. 参数验证
	if err := am.validateRequest(req); err != nil {
		return nil, fmt.Errorf("参数验证失败: %w", err)
	}

	// 2. 环境检查
	if !IsEnvAvailable(req.Env) {
		return nil, fmt.Errorf("环境 %s 不可用", req.Env)
	}

	// 3. 获取对应客户端
	client := am.getClientForEnv(req.Env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", req.Env)
	}

	am.logger.Info("开始部署应用",
		"app", req.AppName,
		"namespace", req.Namespace,
		"env", req.Env,
		"image", req.Image)

	// 4. 确保命名空间存在
	if err := am.ensureNamespace(ctx, client, req.Namespace); err != nil {
		return nil, fmt.Errorf("创建命名空间失败: %w", err)
	}

	// 5. 部署Deployment
	deployment, deployAction, err := am.ensureDeployment(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("部署Deployment失败: %w", err)
	}

	// 6. 部署Service
	service, serviceAction, err := am.ensureService(ctx, client, req)
	if err != nil {
		return nil, fmt.Errorf("部署Service失败: %w", err)
	}

	result := &ApplicationResult{
		Action:     fmt.Sprintf("%s/%s", deployAction, serviceAction),
		AppName:    req.AppName,
		Namespace:  req.Namespace,
		Env:        req.Env,
		Deployment: deployment,
		Service:    service,
		Message:    fmt.Sprintf("应用 %s 在环境 %s 中%s成功", req.AppName, req.Env, deployAction),
	}

	am.logger.Info("应用部署完成",
		"app", req.AppName,
		"env", req.Env,
		"action", result.Action)

	return result, nil
}

// DeleteApplication 删除应用
func (am *ApplicationManager) DeleteApplication(ctx context.Context, appName, namespace, env string) (*ApplicationResult, error) {
	// 环境检查
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := am.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	am.logger.Info("开始删除应用",
		"app", appName,
		"namespace", namespace,
		"env", env)

	// 删除Service
	err := client.CoreV1().Services(namespace).Delete(ctx, appName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("删除Service失败: %w", err)
	}

	// 删除Deployment
	err = client.AppsV1().Deployments(namespace).Delete(ctx, appName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("删除Deployment失败: %w", err)
	}

	result := &ApplicationResult{
		Action:    "删除",
		AppName:   appName,
		Namespace: namespace,
		Env:       env,
		Message:   fmt.Sprintf("应用 %s 在环境 %s 中删除成功", appName, env),
	}

	am.logger.Info("应用删除完成",
		"app", appName,
		"env", env)

	return result, nil
}

// ScaleApplication 扩缩容应用
func (am *ApplicationManager) ScaleApplication(ctx context.Context, appName, namespace, env string, replicas int32) (*ApplicationResult, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := am.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	am.logger.Info("开始扩缩容应用",
		"app", appName,
		"namespace", namespace,
		"env", env,
		"replicas", replicas)

	// 获取当前Deployment
	deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Deployment失败: %w", err)
	}

	// 检查是否需要更新
	if *deployment.Spec.Replicas == replicas {
		return &ApplicationResult{
			Action:     "无变化",
			AppName:    appName,
			Namespace:  namespace,
			Env:        env,
			Deployment: deployment,
			Message:    fmt.Sprintf("应用 %s 副本数已经是 %d，无需变更", appName, replicas),
		}, nil
	}

	// 更新副本数
	deployment.Spec.Replicas = &replicas
	updatedDeployment, err := client.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("更新Deployment副本数失败: %w", err)
	}

	result := &ApplicationResult{
		Action:     "扩缩容",
		AppName:    appName,
		Namespace:  namespace,
		Env:        env,
		Deployment: updatedDeployment,
		Message:    fmt.Sprintf("应用 %s 副本数更新为 %d", appName, replicas),
	}

	am.logger.Info("应用扩缩容完成",
		"app", appName,
		"env", env,
		"replicas", replicas)

	return result, nil
}

// GetApplicationStatus 获取应用状态
func (am *ApplicationManager) GetApplicationStatus(ctx context.Context, appName, namespace, env string) (*ApplicationStatus, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := am.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	// 获取Deployment状态
	deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &ApplicationStatus{
				AppName:   appName,
				Namespace: namespace,
				Env:       env,
				Exists:    false,
				Message:   "应用不存在",
			}, nil
		}
		return nil, fmt.Errorf("获取Deployment状态失败: %w", err)
	}

	// 获取Service状态
	service, err := client.CoreV1().Services(namespace).Get(ctx, appName, metav1.GetOptions{})
	serviceExists := err == nil

	status := &ApplicationStatus{
		AppName:           appName,
		Namespace:         namespace,
		Env:               env,
		Exists:            true,
		Replicas:          *deployment.Spec.Replicas,
		ReadyReplicas:     deployment.Status.ReadyReplicas,
		AvailableReplicas: deployment.Status.AvailableReplicas,
		Image:             deployment.Spec.Template.Spec.Containers[0].Image,
		ServiceExists:     serviceExists,
		CreatedAt:         deployment.CreationTimestamp.Time,
		Message:           fmt.Sprintf("应用运行中: %d/%d 副本就绪", deployment.Status.ReadyReplicas, *deployment.Spec.Replicas),
	}

	if serviceExists {
		status.ServiceType = string(service.Spec.Type)
		if len(service.Spec.Ports) > 0 {
			status.ServicePort = service.Spec.Ports[0].Port
		}
	}

	return status, nil
}

// GetDeploymentsByLabel 通过标签查询Deployment
func (am *ApplicationManager) GetDeploymentsByLabel(ctx context.Context, namespace, env, labelSelector string) ([]ApplicationStatus, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := am.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	am.logger.Info("开始通过标签查询Deployment",
		"namespace", namespace,
		"env", env,
		"label_selector", labelSelector)

	// 查询Deployment
	deployments, err := client.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("查询Deployment失败: %w", err)
	}

	var results []ApplicationStatus
	for _, deployment := range deployments.Items {
		status := am.convertDeploymentToStatus(&deployment, env)

		// 检查对应的Service是否存在
		service, err := client.CoreV1().Services(namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
		if err == nil {
			status.ServiceExists = true
			status.ServiceType = string(service.Spec.Type)
			if len(service.Spec.Ports) > 0 {
				status.ServicePort = service.Spec.Ports[0].Port
			}
		} else if !errors.IsNotFound(err) {
			am.logger.Warn("查询Service失败",
				"deployment", deployment.Name,
				"namespace", namespace,
				"error", err.Error())
		}

		results = append(results, *status)
	}

	am.logger.Info("通过标签查询Deployment完成",
		"namespace", namespace,
		"env", env,
		"label_selector", labelSelector,
		"count", len(results))

	return results, nil
}

// convertDeploymentToStatus 将Deployment转换为ApplicationStatus
func (am *ApplicationManager) convertDeploymentToStatus(deployment *appsv1.Deployment, env string) *ApplicationStatus {
	status := &ApplicationStatus{
		AppName:           deployment.Name,
		Namespace:         deployment.Namespace,
		Env:               env,
		Exists:            true,
		Replicas:          *deployment.Spec.Replicas,
		ReadyReplicas:     deployment.Status.ReadyReplicas,
		AvailableReplicas: deployment.Status.AvailableReplicas,
		CreatedAt:         deployment.CreationTimestamp.Time,
		ServiceExists:     false,
	}

	// 获取镜像信息
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		status.Image = deployment.Spec.Template.Spec.Containers[0].Image
	}

	// 设置状态消息
	if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas && deployment.Status.ReadyReplicas > 0 {
		status.Message = "运行正常"
	} else if deployment.Status.ReadyReplicas == 0 {
		status.Message = "无可用副本"
	} else {
		status.Message = fmt.Sprintf("部分就绪 (%d/%d)", deployment.Status.ReadyReplicas, *deployment.Spec.Replicas)
	}

	return status
}

// ensureNamespace 确保命名空间存在
func (am *ApplicationManager) ensureNamespace(ctx context.Context, client *kubernetes.Clientset, namespace string) error {
	_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// 创建命名空间
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			_, err = client.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("创建命名空间失败: %w", err)
			}
			am.logger.Info("命名空间创建成功", "namespace", namespace)
		} else {
			return fmt.Errorf("检查命名空间失败: %w", err)
		}
	}
	return nil
}

// ensureDeployment 确保Deployment存在且状态正确
func (am *ApplicationManager) ensureDeployment(ctx context.Context, client *kubernetes.Clientset, req *ApplicationRequest) (*appsv1.Deployment, string, error) {
	// 检查当前Deployment是否存在
	existing, err := client.AppsV1().Deployments(req.Namespace).Get(ctx, req.AppName, metav1.GetOptions{})

	// 构造期望的Deployment
	desired := am.buildDeploymentSpec(req)

	if err != nil && errors.IsNotFound(err) {
		// 创建新的Deployment
		created, err := client.AppsV1().Deployments(req.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return nil, "", fmt.Errorf("创建Deployment失败: %w", err)
		}
		return created, "创建", nil
	} else if err != nil {
		return nil, "", fmt.Errorf("检查Deployment失败: %w", err)
	}

	// 更新现有Deployment
	existing.Spec = desired.Spec
	updated, err := client.AppsV1().Deployments(req.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("更新Deployment失败: %w", err)
	}

	return updated, "更新", nil
}

// ensureService 确保Service存在且状态正确
func (am *ApplicationManager) ensureService(ctx context.Context, client *kubernetes.Clientset, req *ApplicationRequest) (*corev1.Service, string, error) {
	// 检查当前Service是否存在
	existing, err := client.CoreV1().Services(req.Namespace).Get(ctx, req.AppName, metav1.GetOptions{})

	// 构造期望的Service
	desired := am.buildServiceSpec(req)

	if err != nil && errors.IsNotFound(err) {
		// 创建新的Service
		created, err := client.CoreV1().Services(req.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return nil, "", fmt.Errorf("创建Service失败: %w", err)
		}
		return created, "创建", nil
	} else if err != nil {
		return nil, "", fmt.Errorf("检查Service失败: %w", err)
	}

	// 更新现有Service
	existing.Spec.Ports = desired.Spec.Ports
	existing.Spec.Selector = desired.Spec.Selector
	updated, err := client.CoreV1().Services(req.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("更新Service失败: %w", err)
	}

	return updated, "更新", nil
}

// buildDeploymentSpec 构建Deployment规范
func (am *ApplicationManager) buildDeploymentSpec(req *ApplicationRequest) *appsv1.Deployment {
	labels := map[string]string{
		"app": req.AppName,
		"env": req.Env,
	}

	// 合并用户自定义标签
	for k, v := range req.Labels {
		labels[k] = v
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.AppName,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &req.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": req.AppName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  req.AppName,
							Image: req.Image,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: req.Port,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: req.EnvVars,
						},
					},
				},
			},
		},
	}

	// 设置资源限制
	if req.LimitsMemory != "" || req.LimitsCPU != "" || req.RequestMemory != "" || req.RequestCPU != "" {
		resources := corev1.ResourceRequirements{}

		if req.LimitsMemory != "" || req.LimitsCPU != "" {
			resources.Limits = make(corev1.ResourceList)
			if req.LimitsMemory != "" {
				resources.Limits[corev1.ResourceMemory] = resource.MustParse(req.LimitsMemory)
			}
			if req.LimitsCPU != "" {
				resources.Limits[corev1.ResourceCPU] = resource.MustParse(req.LimitsCPU)
			}
		}

		if req.RequestMemory != "" || req.RequestCPU != "" {
			resources.Requests = make(corev1.ResourceList)
			if req.RequestMemory != "" {
				resources.Requests[corev1.ResourceMemory] = resource.MustParse(req.RequestMemory)
			}
			if req.RequestCPU != "" {
				resources.Requests[corev1.ResourceCPU] = resource.MustParse(req.RequestCPU)
			}
		}

		deployment.Spec.Template.Spec.Containers[0].Resources = resources
	}

	return deployment
}

// buildServiceSpec 构建Service规范
func (am *ApplicationManager) buildServiceSpec(req *ApplicationRequest) *corev1.Service {
	labels := map[string]string{
		"app": req.AppName,
		"env": req.Env,
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.AppName,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": req.AppName,
			},
			Ports: []corev1.ServicePort{
				{
					Port:       req.Port,
					TargetPort: intstr.FromInt(int(req.Port)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

// validateRequest 验证请求参数
func (am *ApplicationManager) validateRequest(req *ApplicationRequest) error {
	if req.AppName == "" {
		return fmt.Errorf("应用名称不能为空")
	}
	if req.Namespace == "" {
		return fmt.Errorf("命名空间不能为空")
	}
	if req.Env == "" {
		return fmt.Errorf("环境不能为空")
	}
	if req.Image == "" {
		return fmt.Errorf("镜像不能为空")
	}
	if req.Replicas <= 0 {
		return fmt.Errorf("副本数必须大于0")
	}
	if req.Port <= 0 || req.Port > 65535 {
		return fmt.Errorf("端口号必须在1-65535之间")
	}

	// 验证环境是否支持
	if _, ok := envNameMap[req.Env]; !ok {
		return fmt.Errorf("不支持的环境: %s", req.Env)
	}

	return nil
}

// getClientForEnv 获取环境对应的K8s客户端
func (am *ApplicationManager) getClientForEnv(env string) *kubernetes.Clientset {
	standardEnv := envNameMap[env]
	switch standardEnv {
	case "dev":
		return Dev
	case "test":
		return Test
	case "moni":
		return Moni
	default:
		return nil
	}
}

// ApplicationStatus 应用状态
type ApplicationStatus struct {
	AppName           string    `json:"app_name"`
	Namespace         string    `json:"namespace"`
	Env               string    `json:"env"`
	Exists            bool      `json:"exists"`
	Replicas          int32     `json:"replicas"`
	ReadyReplicas     int32     `json:"ready_replicas"`
	AvailableReplicas int32     `json:"available_replicas"`
	Image             string    `json:"image"`
	ServiceExists     bool      `json:"service_exists"`
	ServiceType       string    `json:"service_type,omitempty"`
	ServicePort       int32     `json:"service_port,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	Message           string    `json:"message"`
}
