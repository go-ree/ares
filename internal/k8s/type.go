package k8s

import (
	"k8s.io/client-go/kubernetes"
)

// Environment 环境类型
type Environment string

const (
	EnvDev   Environment = "dev"
	EnvTest  Environment = "test"
	EnvStage Environment = "moni"
)

// ClusterConfig 集群配置
type ClusterConfig struct {
	Name        string      // 集群名称
	Environment Environment // 环境
	ConfigPath  string      // kubeconfig 路径
}

// ClientManager K8s客户端管理器
type ClientManager struct {
	clients map[Environment]map[string]*kubernetes.Clientset
}
