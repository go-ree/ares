package k8s

import (
	"ares/internal/config"
	"fmt"
	"log/slog"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	manager *ClientManager
	once    sync.Once
)

// InitManager 初始化客户端管理器
func InitManager(configs []ClusterConfig) error {
	var initErr error
	once.Do(func() {
		manager = &ClientManager{
			clients: make(map[Environment]map[string]*kubernetes.Clientset),
		}

		// 初始化每个环境的map
		for _, env := range []Environment{EnvDev, EnvTest, EnvStage} {
			manager.clients[env] = make(map[string]*kubernetes.Clientset)
		}

		// 初始化每个集群的客户端
		for _, cfg := range configs {
			client, err := createClient(cfg)
			if err != nil {
				initErr = fmt.Errorf("初始化集群 [%s-%s] 失败: %w",
					cfg.Environment, cfg.Name, err)
				return
			}

			manager.clients[cfg.Environment][cfg.Name] = client
			slog.Info("成功初始化集群连接",
				"environment", cfg.Environment,
				"cluster", cfg.Name)
		}
	})
	return initErr
}

// createClient 创建单个集群的客户端
func createClient(cfg ClusterConfig) (*kubernetes.Clientset, error) {
	var k8sConfig *rest.Config
	var err error

	if cfg.ConfigPath == "" {
		// 尝试使用集群内配置
		k8sConfig, err = rest.InClusterConfig()
	} else {
		// 使用指定的kubeconfig
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", cfg.ConfigPath)
	}

	if err != nil {
		return nil, fmt.Errorf("加载集群配置失败: %w", err)
	}
	k8sConfig.Timeout = config.K8sTimeout()

	clientset, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("创建客户端失败: %w", err)
	}

	// 验证连接
	if _, err := clientset.Discovery().ServerVersion(); err != nil {
		return nil, fmt.Errorf("验证集群连接失败: %w", err)
	}

	return clientset, nil
}

// GetClient 获取指定环境和集群的客户端
func GetClient(env Environment, clusterName string) (*kubernetes.Clientset, error) {
	if manager == nil {
		return nil, fmt.Errorf("客户端管理器未初始化")
	}

	envClients, exists := manager.clients[env]
	if !exists {
		return nil, fmt.Errorf("环境 [%s] 不存在", env)
	}

	client, exists := envClients[clusterName]
	if !exists {
		return nil, fmt.Errorf("集群 [%s] 在环境 [%s] 中不存在", clusterName, env)
	}

	return client, nil
}

// ListClusters 列出指定环境的所有集群
func ListClusters(env Environment) []string {
	if manager == nil {
		return nil
	}

	envClients, exists := manager.clients[env]
	if !exists {
		return nil
	}

	clusters := make([]string, 0, len(envClients))
	for name := range envClients {
		clusters = append(clusters, name)
	}
	return clusters
}
