package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

// ServiceManager Service管理器
type ServiceManager struct {
	logger *slog.Logger
}

// NewServiceManager 创建新的Service管理器
func NewServiceManager() *ServiceManager {
	return &ServiceManager{
		logger: slog.Default(),
	}
}

// ServiceRequest Service创建/更新请求
type ServiceRequest struct {
	ServiceName              string             `json:"service_name" validate:"required"`
	Namespace                string             `json:"namespace" validate:"required"`
	Env                      string             `json:"env" validate:"required,oneof=dev test moni"`
	Selector                 map[string]string  `json:"selector" validate:"required"`
	Ports                    []ServicePort      `json:"ports" validate:"required,min=1"`
	ServiceType              corev1.ServiceType `json:"service_type,omitempty"`
	Labels                   map[string]string  `json:"labels,omitempty"`
	Annotations              map[string]string  `json:"annotations,omitempty"`
	ExternalIPs              []string           `json:"external_ips,omitempty"`
	LoadBalancerSourceRanges []string           `json:"load_balancer_source_ranges,omitempty"`
}

// ServicePort Service端口配置
type ServicePort struct {
	Name       string              `json:"name,omitempty"`
	Port       int32               `json:"port" validate:"required,min=1,max=65535"`
	TargetPort *intstr.IntOrString `json:"target_port,omitempty"`
	Protocol   corev1.Protocol     `json:"protocol,omitempty"`
	NodePort   int32               `json:"node_port,omitempty"`
}

// ServiceResult Service操作结果
type ServiceResult struct {
	Action  string          `json:"action"` // 创建/更新/删除
	Service *corev1.Service `json:"service"`
	Message string          `json:"message"`
}

// ServiceInfo Service信息
type ServiceInfo struct {
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	Type           string            `json:"type"`
	ClusterIP      string            `json:"cluster_ip"`
	ExternalIPs    []string          `json:"external_ips,omitempty"`
	LoadBalancerIP string            `json:"load_balancer_ip,omitempty"`
	Ports          []ServicePortInfo `json:"ports"`
	Selector       map[string]string `json:"selector"`
	Labels         map[string]string `json:"labels"`
	CreatedAt      time.Time         `json:"created_at"`
	Message        string            `json:"message,omitempty"`
}

// ServicePortInfo Service端口信息
type ServicePortInfo struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	TargetPort string `json:"target_port"`
	Protocol   string `json:"protocol"`
	NodePort   int32  `json:"node_port,omitempty"`
}

// ServiceEndpoint Service端点信息
type ServiceEndpoint struct {
	IP       string `json:"ip"`
	Port     int32  `json:"port"`
	Ready    bool   `json:"ready"`
	NodeName string `json:"node_name,omitempty"`
}

// CreateOrUpdateService 创建或更新Service（声明式）
func (sm *ServiceManager) CreateOrUpdateService(ctx context.Context, req *ServiceRequest) (*ServiceResult, error) {
	// 1. 参数验证
	if err := sm.validateServiceRequest(req); err != nil {
		return nil, fmt.Errorf("参数验证失败: %w", err)
	}

	// 2. 环境检查
	if !IsEnvAvailable(req.Env) {
		return nil, fmt.Errorf("环境 %s 不可用", req.Env)
	}

	// 3. 获取对应客户端
	client := sm.getClientForEnv(req.Env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", req.Env)
	}

	sm.logger.Info("开始创建/更新Service",
		"service", req.ServiceName,
		"namespace", req.Namespace,
		"env", req.Env,
		"type", req.ServiceType)

	// 4. 检查当前Service是否存在
	existing, err := client.CoreV1().Services(req.Namespace).Get(ctx, req.ServiceName, metav1.GetOptions{})

	// 5. 构造期望的Service
	desired := sm.buildServiceSpec(req)

	var result *ServiceResult
	if err != nil && errors.IsNotFound(err) {
		// 创建新的Service
		created, err := client.CoreV1().Services(req.Namespace).Create(ctx, desired, metav1.CreateOptions{})
		if err != nil {
			return nil, fmt.Errorf("创建Service失败: %w", err)
		}
		result = &ServiceResult{
			Action:  "创建",
			Service: created,
			Message: fmt.Sprintf("Service %s 创建成功", req.ServiceName),
		}
	} else if err != nil {
		return nil, fmt.Errorf("检查Service失败: %w", err)
	} else {
		// 更新现有Service
		existing.Spec.Ports = desired.Spec.Ports
		existing.Spec.Selector = desired.Spec.Selector
		existing.Spec.Type = desired.Spec.Type
		existing.Spec.ExternalIPs = desired.Spec.ExternalIPs
		existing.Spec.LoadBalancerSourceRanges = desired.Spec.LoadBalancerSourceRanges

		// 合并标签和注解
		if existing.Labels == nil {
			existing.Labels = make(map[string]string)
		}
		for k, v := range desired.Labels {
			existing.Labels[k] = v
		}

		if existing.Annotations == nil {
			existing.Annotations = make(map[string]string)
		}
		for k, v := range desired.Annotations {
			existing.Annotations[k] = v
		}

		updated, err := client.CoreV1().Services(req.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("更新Service失败: %w", err)
		}
		result = &ServiceResult{
			Action:  "更新",
			Service: updated,
			Message: fmt.Sprintf("Service %s 更新成功", req.ServiceName),
		}
	}

	sm.logger.Info("Service操作完成",
		"service", req.ServiceName,
		"env", req.Env,
		"action", result.Action)

	return result, nil
}

// DeleteService 删除Service
func (sm *ServiceManager) DeleteService(ctx context.Context, serviceName, namespace, env string) (*ServiceResult, error) {
	// 环境检查
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := sm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	sm.logger.Info("开始删除Service",
		"service", serviceName,
		"namespace", namespace,
		"env", env)

	// 先获取Service信息用于返回
	service, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &ServiceResult{
				Action:  "删除",
				Message: fmt.Sprintf("Service %s 不存在，无需删除", serviceName),
			}, nil
		}
		return nil, fmt.Errorf("获取Service信息失败: %w", err)
	}

	// 删除Service
	err = client.CoreV1().Services(namespace).Delete(ctx, serviceName, metav1.DeleteOptions{})
	if err != nil {
		return nil, fmt.Errorf("删除Service失败: %w", err)
	}

	result := &ServiceResult{
		Action:  "删除",
		Service: service,
		Message: fmt.Sprintf("Service %s 删除成功", serviceName),
	}

	sm.logger.Info("Service删除完成",
		"service", serviceName,
		"env", env)

	return result, nil
}

// GetService 获取Service信息
func (sm *ServiceManager) GetService(ctx context.Context, serviceName, namespace, env string) (*ServiceInfo, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := sm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	service, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Service失败: %w", err)
	}

	return sm.convertServiceToServiceInfo(service), nil
}

// ListServicesInNamespace 获取命名空间中的所有Service
func (sm *ServiceManager) ListServicesInNamespace(ctx context.Context, namespace, env string, labelSelector string) ([]ServiceInfo, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := sm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	sm.logger.Info("开始获取Service列表",
		"namespace", namespace,
		"env", env,
		"selector", labelSelector)

	options := metav1.ListOptions{}
	if labelSelector != "" {
		options.LabelSelector = labelSelector
	}

	serviceList, err := client.CoreV1().Services(namespace).List(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("获取Service列表失败: %w", err)
	}

	services := make([]ServiceInfo, 0, len(serviceList.Items))
	for _, service := range serviceList.Items {
		serviceInfo := sm.convertServiceToServiceInfo(&service)
		services = append(services, *serviceInfo)
	}

	sm.logger.Info("Service列表获取完成",
		"namespace", namespace,
		"env", env,
		"count", len(services))

	return services, nil
}

// GetServiceEndpoints 获取Service的端点信息
func (sm *ServiceManager) GetServiceEndpoints(ctx context.Context, serviceName, namespace, env string) ([]ServiceEndpoint, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := sm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	// 获取Endpoints
	endpoints, err := client.CoreV1().Endpoints(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Service端点失败: %w", err)
	}

	var serviceEndpoints []ServiceEndpoint
	for _, subset := range endpoints.Subsets {
		// 处理就绪的端点
		for _, address := range subset.Addresses {
			for _, port := range subset.Ports {
				endpoint := ServiceEndpoint{
					IP:    address.IP,
					Port:  port.Port,
					Ready: true,
				}
				if address.NodeName != nil {
					endpoint.NodeName = *address.NodeName
				}
				serviceEndpoints = append(serviceEndpoints, endpoint)
			}
		}

		// 处理未就绪的端点
		for _, address := range subset.NotReadyAddresses {
			for _, port := range subset.Ports {
				endpoint := ServiceEndpoint{
					IP:    address.IP,
					Port:  port.Port,
					Ready: false,
				}
				if address.NodeName != nil {
					endpoint.NodeName = *address.NodeName
				}
				serviceEndpoints = append(serviceEndpoints, endpoint)
			}
		}
	}

	return serviceEndpoints, nil
}

// ExposeService 将Service暴露为不同类型
func (sm *ServiceManager) ExposeService(ctx context.Context, serviceName, namespace, env string, serviceType corev1.ServiceType) (*ServiceResult, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := sm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	sm.logger.Info("开始暴露Service",
		"service", serviceName,
		"namespace", namespace,
		"env", env,
		"type", serviceType)

	// 获取现有Service
	service, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Service失败: %w", err)
	}

	// 更新Service类型
	service.Spec.Type = serviceType

	// 如果是NodePort或LoadBalancer类型，需要处理端口分配
	if serviceType == corev1.ServiceTypeNodePort || serviceType == corev1.ServiceTypeLoadBalancer {
		for i := range service.Spec.Ports {
			// 如果没有指定NodePort，让K8s自动分配
			if service.Spec.Ports[i].NodePort == 0 && serviceType == corev1.ServiceTypeNodePort {
				// K8s会自动分配NodePort
			}
		}
	}

	// 更新Service
	updated, err := client.CoreV1().Services(namespace).Update(ctx, service, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("更新Service类型失败: %w", err)
	}

	result := &ServiceResult{
		Action:  "暴露",
		Service: updated,
		Message: fmt.Sprintf("Service %s 已暴露为 %s 类型", serviceName, serviceType),
	}

	sm.logger.Info("Service暴露完成",
		"service", serviceName,
		"env", env,
		"type", serviceType)

	return result, nil
}

// buildServiceSpec 构建Service规范
func (sm *ServiceManager) buildServiceSpec(req *ServiceRequest) *corev1.Service {
	labels := map[string]string{
		"service": req.ServiceName,
		"env":     req.Env,
	}

	// 合并用户自定义标签
	for k, v := range req.Labels {
		labels[k] = v
	}

	// 构造端口配置
	var ports []corev1.ServicePort
	for _, port := range req.Ports {
		servicePort := corev1.ServicePort{
			Name:     port.Name,
			Port:     port.Port,
			Protocol: port.Protocol,
		}

		// 设置目标端口
		if port.TargetPort != nil {
			servicePort.TargetPort = *port.TargetPort
		} else {
			servicePort.TargetPort = intstr.FromInt(int(port.Port))
		}

		// 设置协议默认值
		if servicePort.Protocol == "" {
			servicePort.Protocol = corev1.ProtocolTCP
		}

		// 设置NodePort（如果指定）
		if port.NodePort > 0 {
			servicePort.NodePort = port.NodePort
		}

		ports = append(ports, servicePort)
	}

	// 设置Service类型默认值
	serviceType := req.ServiceType
	if serviceType == "" {
		serviceType = corev1.ServiceTypeClusterIP
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.ServiceName,
			Namespace:   req.Namespace,
			Labels:      labels,
			Annotations: req.Annotations,
		},
		Spec: corev1.ServiceSpec{
			Selector:                 req.Selector,
			Ports:                    ports,
			Type:                     serviceType,
			ExternalIPs:              req.ExternalIPs,
			LoadBalancerSourceRanges: req.LoadBalancerSourceRanges,
		},
	}

	return service
}

// convertServiceToServiceInfo 将K8s Service转换为ServiceInfo
func (sm *ServiceManager) convertServiceToServiceInfo(service *corev1.Service) *ServiceInfo {
	info := &ServiceInfo{
		Name:        service.Name,
		Namespace:   service.Namespace,
		Type:        string(service.Spec.Type),
		ClusterIP:   service.Spec.ClusterIP,
		ExternalIPs: service.Spec.ExternalIPs,
		Selector:    service.Spec.Selector,
		Labels:      service.Labels,
		CreatedAt:   service.CreationTimestamp.Time,
	}

	// 设置LoadBalancer IP
	if len(service.Status.LoadBalancer.Ingress) > 0 {
		if service.Status.LoadBalancer.Ingress[0].IP != "" {
			info.LoadBalancerIP = service.Status.LoadBalancer.Ingress[0].IP
		} else if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
			info.LoadBalancerIP = service.Status.LoadBalancer.Ingress[0].Hostname
		}
	}

	// 转换端口信息
	for _, port := range service.Spec.Ports {
		portInfo := ServicePortInfo{
			Name:       port.Name,
			Port:       port.Port,
			TargetPort: port.TargetPort.String(),
			Protocol:   string(port.Protocol),
		}
		if port.NodePort > 0 {
			portInfo.NodePort = port.NodePort
		}
		info.Ports = append(info.Ports, portInfo)
	}

	return info
}

// validateServiceRequest 验证Service请求参数
func (sm *ServiceManager) validateServiceRequest(req *ServiceRequest) error {
	if req.ServiceName == "" {
		return fmt.Errorf("Service名称不能为空")
	}
	if req.Namespace == "" {
		return fmt.Errorf("命名空间不能为空")
	}
	if req.Env == "" {
		return fmt.Errorf("环境不能为空")
	}
	if len(req.Selector) == 0 {
		return fmt.Errorf("选择器不能为空")
	}
	if len(req.Ports) == 0 {
		return fmt.Errorf("端口配置不能为空")
	}

	// 验证端口配置
	for i, port := range req.Ports {
		if port.Port <= 0 || port.Port > 65535 {
			return fmt.Errorf("端口 %d 配置无效，端口号必须在1-65535之间", i)
		}
	}

	// 验证环境是否支持
	if _, ok := envNameMap[req.Env]; !ok {
		return fmt.Errorf("不支持的环境: %s", req.Env)
	}

	return nil
}

// getClientForEnv 获取环境对应的K8s客户端
func (sm *ServiceManager) getClientForEnv(env string) *kubernetes.Clientset {
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
