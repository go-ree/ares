package k8s

import (
	"ares/internal/config"
	"context"
	"fmt"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// 全局K8s客户端变量 - 直接访问
var (
	Dev  *kubernetes.Clientset // 开发环境客户端
	Test *kubernetes.Clientset // 测试环境客户端
	Moni *kubernetes.Clientset // 监控环境客户端
)

// Init 初始化K8s客户端管理器和全局变量
func Init() error {
	slog.Info("开始初始化K8s客户端管理器")

	// 从配置文件构建集群配置
	configs, err := buildClusterConfigs()
	if err != nil {
		return fmt.Errorf("构建集群配置失败: %w", err)
	}

	// 初始化客户端管理器
	if err := InitManager(configs); err != nil {
		return fmt.Errorf("初始化客户端管理器失败: %w", err)
	}

	// 设置全局变量
	setupGlobalClients()

	// 更新环境客户端映射表
	updateEnvClientMap()

	slog.Info("K8s客户端管理器初始化完成")
	return nil
}

// buildClusterConfigs 从配置文件构建集群配置列表
func buildClusterConfigs() ([]ClusterConfig, error) {
	var configs []ClusterConfig

	for envName, cluster := range config.Main.K8s.Clusters {
		env, err := parseEnvironment(envName)
		if err != nil {
			slog.Warn("跳过未识别的环境", "env", envName)
			continue
		}

		cfg := ClusterConfig{
			Name:        cluster.Name,
			Environment: env,
			ConfigPath:  cluster.ConfigPath,
		}
		configs = append(configs, cfg)

		slog.Info("添加集群配置",
			"env", envName,
			"cluster", cluster.Name,
			"config_path", cluster.ConfigPath)
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("未找到有效的集群配置")
	}

	return configs, nil
}

// parseEnvironment 解析环境字符串为环境类型
func parseEnvironment(envStr string) (Environment, error) {
	switch envStr {
	case "dev":
		return EnvDev, nil
	case "test":
		return EnvTest, nil
	case "moni":
		return EnvStage, nil
	default:
		return "", fmt.Errorf("未知环境: %s", envStr)
	}
}

// setupGlobalClients 设置全局客户端变量
func setupGlobalClients() {
	// 环境配置映射
	envConfigs := []struct {
		env    Environment
		client **kubernetes.Clientset
		name   string
	}{
		{EnvDev, &Dev, "Dev"},
		{EnvTest, &Test, "Test"},
		{EnvStage, &Moni, "Moni"},
	}

	// 统一设置客户端
	for _, cfg := range envConfigs {
		if client, err := getClientForEnv(cfg.env); err == nil {
			*cfg.client = client
			slog.Info(fmt.Sprintf("%s环境客户端初始化成功", cfg.name))

			// 获取并输出集群详细信息
			logClusterInfo(client, cfg.name, string(cfg.env))
		} else {
			slog.Warn(fmt.Sprintf("%s环境客户端初始化失败", cfg.name), "error", err)
		}
	}
}

// getClientForEnv 获取指定环境的默认客户端（第一个可用集群）
func getClientForEnv(env Environment) (*kubernetes.Clientset, error) {
	clusters := ListClusters(env)
	if len(clusters) == 0 {
		return nil, fmt.Errorf("环境 [%s] 无可用集群", env)
	}

	// 返回第一个集群的客户端作为默认客户端
	return GetClient(env, clusters[0])
}

// IsInitialized 检查是否已初始化
func IsInitialized() bool {
	return manager != nil
}

// logClusterInfo 获取并输出集群详细信息
func logClusterInfo(client *kubernetes.Clientset, envName, envKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slog.Info("=== 开始获取集群信息 ===", "env", envName)

	// 1. 获取Kubernetes版本信息
	if serverVersion, err := client.Discovery().ServerVersion(); err == nil {
		slog.Info("✅ Kubernetes集群版本信息",
			"env", envName,
			"kubernetes_version", serverVersion.String(),
			"git_version", serverVersion.GitVersion,
			"platform", serverVersion.Platform,
			"go_version", serverVersion.GoVersion)
	} else {
		slog.Error("❌ 获取Kubernetes版本失败", "env", envName, "error", err)
		return // 如果无法获取版本，说明连接有问题，不继续获取其他信息
	}

	// 2. 获取节点信息
	if nodeList, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		readyNodes := 0
		notReadyNodes := 0

		for _, node := range nodeList.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" {
					if condition.Status == "True" {
						readyNodes++
					} else {
						notReadyNodes++
					}
					break
				}
			}
		}

		slog.Info("✅ 集群节点信息",
			"env", envName,
			"total_nodes", len(nodeList.Items),
			"ready_nodes", readyNodes,
			"not_ready_nodes", notReadyNodes)

		// 详细输出节点信息
		for _, node := range nodeList.Items {
			nodeInfo := map[string]interface{}{
				"name":    node.Name,
				"roles":   getNodeRoles(node.Labels),
				"version": node.Status.NodeInfo.KubeletVersion,
				"os":      node.Status.NodeInfo.OperatingSystem,
				"arch":    node.Status.NodeInfo.Architecture,
			}

			// 获取节点状态
			for _, condition := range node.Status.Conditions {
				if condition.Type == "Ready" {
					nodeInfo["ready"] = condition.Status == "True"
					if condition.Status != "True" {
						nodeInfo["reason"] = condition.Reason
						nodeInfo["message"] = condition.Message
					}
					break
				}
			}
			// 节点数量过多，暂不打印输出
			//slog.Info("📋 节点详情", "env", envName, "node", nodeInfo)
		}
	} else {
		slog.Warn("⚠️ 获取节点信息失败", "env", envName, "error", err)
	}

	// 3. 获取命名空间信息
	if nsList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{}); err == nil {
		namespaces := make([]string, 0, len(nsList.Items))
		for _, ns := range nsList.Items {
			namespaces = append(namespaces, ns.Name)
		}

		slog.Info("✅ 集群命名空间信息",
			"env", envName,
			"total_namespaces", len(nsList.Items),
			"namespaces", namespaces)
	} else {
		slog.Warn("⚠️ 获取命名空间信息失败", "env", envName, "error", err)
	}

	// 4. 获取Pod总数统计
	if podList, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{}); err == nil {
		runningPods := 0
		pendingPods := 0
		failedPods := 0
		succeededPods := 0

		for _, pod := range podList.Items {
			switch pod.Status.Phase {
			case "Running":
				runningPods++
			case "Pending":
				pendingPods++
			case "Failed":
				failedPods++
			case "Succeeded":
				succeededPods++
			}
		}

		slog.Info("✅ 集群Pod统计信息",
			"env", envName,
			"total_pods", len(podList.Items),
			"running_pods", runningPods,
			"pending_pods", pendingPods,
			"failed_pods", failedPods,
			"succeeded_pods", succeededPods)
	} else {
		slog.Warn("⚠️ 获取Pod统计信息失败", "env", envName, "error", err)
	}

	// 5. 获取系统级别的服务信息
	if serviceList, err := client.CoreV1().Services("kube-system").List(ctx, metav1.ListOptions{}); err == nil {
		services := make([]string, 0, len(serviceList.Items))
		for _, svc := range serviceList.Items {
			services = append(services, svc.Name)
		}

		slog.Info("✅ 系统服务信息",
			"env", envName,
			"kube_system_services", len(serviceList.Items),
			"services", services)
	} else {
		slog.Warn("⚠️ 获取系统服务信息失败", "env", envName, "error", err)
	}

	slog.Info("=== 集群信息获取完成 ===",
		"env", envName,
		"connection_status", "✅ 成功连接到K8s集群",
		"summary", "集群连接正常，所有基础信息已获取")
}

// getNodeRoles 从节点标签中提取角色信息
func getNodeRoles(labels map[string]string) []string {
	var roles []string

	// 检查常见的角色标签
	roleLabels := []string{
		"node-role.kubernetes.io/master",
		"node-role.kubernetes.io/control-plane",
		"node-role.kubernetes.io/worker",
		"kubernetes.io/role",
	}

	for _, roleLabel := range roleLabels {
		if _, exists := labels[roleLabel]; exists {
			if roleLabel == "node-role.kubernetes.io/master" {
				roles = append(roles, "master")
			} else if roleLabel == "node-role.kubernetes.io/control-plane" {
				roles = append(roles, "control-plane")
			} else if roleLabel == "node-role.kubernetes.io/worker" {
				roles = append(roles, "worker")
			} else if value, ok := labels[roleLabel]; ok && value != "" {
				roles = append(roles, value)
			}
		}
	}

	// 如果没有找到角色，标记为worker
	if len(roles) == 0 {
		roles = append(roles, "worker")
	}

	return roles
}
