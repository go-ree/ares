package k8s

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodManager Pod管理器
type PodManager struct {
	logger *slog.Logger
}

// NewPodManager 创建新的Pod管理器
func NewPodManager() *PodManager {
	return &PodManager{
		logger: slog.Default(),
	}
}

// PodInfo Pod信息
type PodInfo struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Status       string            `json:"status"`
	Phase        string            `json:"phase"` // Pod阶段：Pending、Running、Succeeded、Failed、Unknown
	RestartCount int32             `json:"restart_count"`
	NodeName     string            `json:"node_name"`
	PodIP        string            `json:"pod_ip"`
	HostIP       string            `json:"host_ip"`
	Labels       map[string]string `json:"labels"`
	CreatedAt    time.Time         `json:"created_at"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	Message      string            `json:"message,omitempty"`
}

// GetAppPodsRequest 触发发布动作所需的请求参数
type GetAppPodsRequest struct {
	AppName string `json:"app_name"`
	Env     string `json:"env"`
}

// PodLogRequest Pod日志请求
type PodLogRequest struct {
	PodName      string `json:"pod_name" validate:"required"`
	Namespace    string `json:"namespace" validate:"required"`
	Env          string `json:"env" validate:"required"`
	Container    string `json:"container,omitempty"`
	TailLines    *int64 `json:"tail_lines,omitempty"`
	SinceSeconds *int64 `json:"since_seconds,omitempty"`
	Follow       bool   `json:"follow,omitempty"`
	Previous     bool   `json:"previous,omitempty"`
}

// PodExecRequest Pod命令执行请求
type PodExecRequest struct {
	PodName   string   `json:"pod_name" validate:"required"`
	Namespace string   `json:"namespace" validate:"required"`
	Env       string   `json:"env" validate:"required"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command" validate:"required"`
}

// PodMetrics Pod资源使用情况
type PodMetrics struct {
	PodName     string            `json:"pod_name"`
	Namespace   string            `json:"namespace"`
	Containers  []ContainerMetric `json:"containers"`
	CollectedAt time.Time         `json:"collected_at"`
}

// ContainerMetric 容器资源指标
type ContainerMetric struct {
	Name   string `json:"name"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// GetPodsInNamespace 获取命名空间中的所有Pod
func (pm *PodManager) GetPodsInNamespace(ctx context.Context, namespace, env string, labelSelector string) ([]PodInfo, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := pm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	pm.logger.Info("开始获取Pod列表",
		"namespace", namespace,
		"env", env,
		"selector", labelSelector)

	options := metav1.ListOptions{}
	if labelSelector != "" {
		options.LabelSelector = labelSelector
	}

	podList, err := client.CoreV1().Pods(namespace).List(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("获取Pod列表失败: %w", err)
	}

	pods := make([]PodInfo, 0, len(podList.Items))
	for _, pod := range podList.Items {
		podInfo := pm.convertPodToPodInfo(&pod)
		pods = append(pods, *podInfo)
	}

	pm.logger.Info("Pod列表获取完成",
		"namespace", namespace,
		"env", env,
		"count", len(pods))

	return pods, nil
}

// GetPodStatus 获取单个Pod状态
func (pm *PodManager) GetPodStatus(ctx context.Context, podName, namespace, env string) (*PodInfo, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := pm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Pod状态失败: %w", err)
	}

	return pm.convertPodToPodInfo(pod), nil
}

// RestartPod 重启Pod（通过删除Pod让Deployment重新创建）
func (pm *PodManager) RestartPod(ctx context.Context, podName, namespace, env string) error {
	if !IsEnvAvailable(env) {
		return fmt.Errorf("环境 %s 不可用", env)
	}

	client := pm.getClientForEnv(env)
	if client == nil {
		return fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	pm.logger.Info("开始重启Pod",
		"pod", podName,
		"namespace", namespace,
		"env", env)

	// 删除Pod，让控制器重新创建
	err := client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("删除Pod失败: %w", err)
	}

	pm.logger.Info("Pod删除成功，等待重新创建",
		"pod", podName,
		"namespace", namespace,
		"env", env)

	return nil
}

// RestartPodsBySelector 通过标签选择器批量重启Pod
func (pm *PodManager) RestartPodsBySelector(ctx context.Context, namespace, env, labelSelector string) ([]string, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := pm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	pm.logger.Info("开始批量重启Pod",
		"namespace", namespace,
		"env", env,
		"selector", labelSelector)

	// 获取符合条件的Pod列表
	podList, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("获取Pod列表失败: %w", err)
	}

	var restartedPods []string
	for _, pod := range podList.Items {
		err := client.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			pm.logger.Error("删除Pod失败",
				"pod", pod.Name,
				"error", err)
			continue
		}
		restartedPods = append(restartedPods, pod.Name)
	}

	pm.logger.Info("批量重启Pod完成",
		"namespace", namespace,
		"env", env,
		"count", len(restartedPods))

	return restartedPods, nil
}

// GetPodLogs 获取Pod日志
func (pm *PodManager) GetPodLogs(ctx context.Context, req *PodLogRequest) (string, error) {
	if err := pm.validateLogRequest(req); err != nil {
		return "", fmt.Errorf("参数验证失败: %w", err)
	}

	if !IsEnvAvailable(req.Env) {
		return "", fmt.Errorf("环境 %s 不可用", req.Env)
	}

	client := pm.getClientForEnv(req.Env)
	if client == nil {
		return "", fmt.Errorf("无法获取环境 %s 的K8s客户端", req.Env)
	}

	pm.logger.Info("开始获取Pod日志",
		"pod", req.PodName,
		"namespace", req.Namespace,
		"env", req.Env,
		"container", req.Container)

	// 构建日志选项
	logOptions := &corev1.PodLogOptions{
		Container: req.Container,
		Follow:    req.Follow,
		Previous:  req.Previous,
	}

	if req.TailLines != nil {
		logOptions.TailLines = req.TailLines
	}

	if req.SinceSeconds != nil {
		logOptions.SinceSeconds = req.SinceSeconds
	}

	// 获取日志流
	request := client.CoreV1().Pods(req.Namespace).GetLogs(req.PodName, logOptions)
	logs, err := request.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("获取Pod日志失败: %w", err)
	}
	defer logs.Close()

	// 读取日志内容
	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, logs)
	if err != nil {
		return "", fmt.Errorf("读取日志内容失败: %w", err)
	}

	pm.logger.Info("Pod日志获取完成",
		"pod", req.PodName,
		"env", req.Env,
		"size", buf.Len())

	return buf.String(), nil
}

// StreamPodLogs 流式获取Pod日志
func (pm *PodManager) StreamPodLogs(ctx context.Context, req *PodLogRequest, output chan<- string) error {
	if err := pm.validateLogRequest(req); err != nil {
		return fmt.Errorf("参数验证失败: %w", err)
	}

	if !IsEnvAvailable(req.Env) {
		return fmt.Errorf("环境 %s 不可用", req.Env)
	}

	client := pm.getClientForEnv(req.Env)
	if client == nil {
		return fmt.Errorf("无法获取环境 %s 的K8s客户端", req.Env)
	}

	defer close(output)

	pm.logger.Info("开始流式获取Pod日志",
		"pod", req.PodName,
		"namespace", req.Namespace,
		"env", req.Env)

	// 强制开启Follow模式进行流式读取
	logOptions := &corev1.PodLogOptions{
		Container: req.Container,
		Follow:    true,
		Previous:  req.Previous,
	}

	if req.TailLines != nil {
		logOptions.TailLines = req.TailLines
	}

	// 获取日志流
	request := client.CoreV1().Pods(req.Namespace).GetLogs(req.PodName, logOptions)
	logs, err := request.Stream(ctx)
	if err != nil {
		return fmt.Errorf("获取Pod日志流失败: %w", err)
	}
	defer logs.Close()

	// 逐行读取并发送
	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case output <- scanner.Text():
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("读取日志流失败: %w", err)
	}

	pm.logger.Info("Pod日志流式读取完成",
		"pod", req.PodName,
		"env", req.Env)

	return nil
}

// GetPodEvents 获取Pod相关事件
func (pm *PodManager) GetPodEvents(ctx context.Context, podName, namespace, env string) ([]corev1.Event, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := pm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	// 获取Pod信息
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Pod信息失败: %w", err)
	}

	// 获取与Pod相关的事件
	events, err := client.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.uid=%s", podName, pod.UID),
	})
	if err != nil {
		return nil, fmt.Errorf("获取Pod事件失败: %w", err)
	}

	return events.Items, nil
}

// WaitForPodReady 等待Pod准备就绪
func (pm *PodManager) WaitForPodReady(ctx context.Context, podName, namespace, env string, timeout time.Duration) error {
	if !IsEnvAvailable(env) {
		return fmt.Errorf("环境 %s 不可用", env)
	}

	client := pm.getClientForEnv(env)
	if client == nil {
		return fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	pm.logger.Info("开始等待Pod就绪",
		"pod", podName,
		"namespace", namespace,
		"env", env,
		"timeout", timeout)

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待Pod就绪超时: %w", ctx.Err())
		default:
			pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("获取Pod状态失败: %w", err)
			}

			// 检查Pod是否就绪
			if pm.isPodReady(pod) {
				pm.logger.Info("Pod已就绪",
					"pod", podName,
					"env", env)
				return nil
			}

			// 如果Pod失败，直接返回错误
			if pod.Status.Phase == corev1.PodFailed {
				return fmt.Errorf("Pod状态为失败: %s", pod.Status.Message)
			}

			time.Sleep(2 * time.Second)
		}
	}
}

// GetPodsByNamePrefix 通过Pod名称前缀获取Pod列表
func (pm *PodManager) GetPodsByNamePrefix(ctx context.Context, namespace, env, namePrefix string) ([]PodInfo, error) {
	if !IsEnvAvailable(env) {
		return nil, fmt.Errorf("环境 %s 不可用", env)
	}

	client := pm.getClientForEnv(env)
	if client == nil {
		return nil, fmt.Errorf("无法获取环境 %s 的K8s客户端", env)
	}

	pm.logger.Info("开始通过名称前缀获取Pod列表",
		"namespace", namespace,
		"env", env,
		"name_prefix", namePrefix)

	// 获取所有Pod
	podList, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("获取Pod列表失败: %w", err)
	}

	// 过滤匹配前缀的Pod
	var pods []PodInfo
	for _, pod := range podList.Items {
		if strings.HasPrefix(pod.Name, namePrefix) {
			podInfo := pm.convertPodToPodInfo(&pod)
			pods = append(pods, *podInfo)
		}
	}

	pm.logger.Info("通过名称前缀获取Pod列表完成",
		"namespace", namespace,
		"env", env,
		"name_prefix", namePrefix,
		"count", len(pods))

	return pods, nil
}

// convertPodToPodInfo 将K8s Pod转换为PodInfo
func (pm *PodManager) convertPodToPodInfo(pod *corev1.Pod) *PodInfo {
	info := &PodInfo{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Phase:     string(pod.Status.Phase),
		NodeName:  pod.Spec.NodeName,
		PodIP:     pod.Status.PodIP,
		HostIP:    pod.Status.HostIP,
		Labels:    pod.Labels,
		CreatedAt: pod.CreationTimestamp.Time,
	}

	// 设置状态信息
	info.Status = string(pod.Status.Phase)
	if len(pod.Status.Conditions) > 0 {
		// 优先显示PodReady状态
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				if condition.Status == corev1.ConditionTrue {
					info.Status = "Ready"
				} else {
					info.Status = "NotReady"
				}
				break
			}
		}

		// 如果没有PodReady条件，则显示Phase
		if info.Status == string(pod.Status.Phase) {
			// 对于Running状态的Pod，可以显示更详细的状态
			if pod.Status.Phase == corev1.PodRunning {
				// 检查是否有容器未就绪
				allContainersReady := true
				for _, containerStatus := range pod.Status.ContainerStatuses {
					if !containerStatus.Ready {
						allContainersReady = false
						break
					}
				}
				if allContainersReady {
					info.Status = "Running"
				} else {
					info.Status = "Running(NotReady)"
				}
			}
		}
	}

	// 计算重启次数
	for _, containerStatus := range pod.Status.ContainerStatuses {
		info.RestartCount += containerStatus.RestartCount
	}

	// 设置启动时间
	if pod.Status.StartTime != nil {
		info.StartedAt = &pod.Status.StartTime.Time
	}

	// 设置错误信息
	if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodPending {
		for _, condition := range pod.Status.Conditions {
			if condition.Status == corev1.ConditionFalse && condition.Message != "" {
				info.Message = condition.Message
				break
			}
		}
		if info.Message == "" && pod.Status.Message != "" {
			info.Message = pod.Status.Message
		}
	}

	return info
}

// isPodReady 检查Pod是否就绪
func (pm *PodManager) isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}

	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

// validateLogRequest 验证日志请求参数
func (pm *PodManager) validateLogRequest(req *PodLogRequest) error {
	if req.PodName == "" {
		return fmt.Errorf("Pod名称不能为空")
	}
	if req.Namespace == "" {
		return fmt.Errorf("命名空间不能为空")
	}
	if req.Env == "" {
		return fmt.Errorf("环境不能为空")
	}

	if _, err := ParseEnvironment(req.Env); err != nil {
		return err
	}

	return nil
}

// getClientForEnv 获取环境对应的K8s客户端
func (pm *PodManager) getClientForEnv(env string) *kubernetes.Clientset {
	return getClientByEnv(env)
}
