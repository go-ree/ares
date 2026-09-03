package k8s

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
)

// Environment is a catalog-backed environment code. It deliberately has no
// fixed constants: every valid catalog entry can be configured at runtime.
type Environment string

var environmentPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,62}$`)

// ParseEnvironment normalizes and validates a data-driven environment code.
func ParseEnvironment(value string) (Environment, error) {
	code := strings.ToLower(strings.TrimSpace(value))
	if !environmentPattern.MatchString(code) {
		return "", fmt.Errorf("invalid Kubernetes environment %q", value)
	}
	return Environment(code), nil
}

// ClusterConfig 集群配置
type ClusterConfig struct {
	Name        string      // 集群名称
	Environment Environment // 动态环境代码
	Kubeconfig  []byte      // Web 上传的 kubeconfig 内容
	InCluster   bool        // 使用 Pod 的 service account
	Timeout     time.Duration
}

// ClientManager K8s客户端管理器
type ClientManager struct {
	clients map[Environment]map[string]*kubernetes.Clientset
}
