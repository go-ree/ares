package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var registry struct {
	sync.RWMutex
	manager *ClientManager
}

// BuildManager constructs and verifies an immutable client registry without
// changing the active runtime. Call ActivateManager only after persistence has
// succeeded so a failed Web update cannot disrupt working clients.
func BuildManager(ctx context.Context, configs []ClusterConfig) (*ClientManager, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	next := &ClientManager{clients: make(map[Environment]map[string]*kubernetes.Clientset)}
	seen := make(map[Environment]map[string]struct{})
	normalizedConfigs := make([]ClusterConfig, 0, len(configs))
	for _, cfg := range configs {
		environment, err := ParseEnvironment(string(cfg.Environment))
		if err != nil {
			return nil, err
		}
		cfg.Environment = environment
		if _, ok := seen[environment]; !ok {
			seen[environment] = make(map[string]struct{})
			next.clients[environment] = make(map[string]*kubernetes.Clientset)
		}
		if cfg.Name == "" {
			return nil, fmt.Errorf("Kubernetes cluster name is required for %s", cfg.Environment)
		}
		if _, exists := seen[cfg.Environment][cfg.Name]; exists {
			return nil, fmt.Errorf("duplicate Kubernetes cluster %s/%s", cfg.Environment, cfg.Name)
		}
		seen[cfg.Environment][cfg.Name] = struct{}{}
		normalizedConfigs = append(normalizedConfigs, cfg)
	}

	type buildResult struct {
		config ClusterConfig
		client *kubernetes.Clientset
		err    error
	}
	results := make(chan buildResult, len(normalizedConfigs))
	for _, cfg := range normalizedConfigs {
		cfg := cfg
		go func() {
			client, err := createClient(ctx, cfg)
			results <- buildResult{config: cfg, client: client, err: err}
		}()
	}
	for range normalizedConfigs {
		result := <-results
		if result.err != nil {
			return nil, fmt.Errorf("initialize Kubernetes cluster %s/%s: %w", result.config.Environment, result.config.Name, result.err)
		}
		next.clients[result.config.Environment][result.config.Name] = result.client
	}
	return next, nil
}

func createClient(ctx context.Context, cfg ClusterConfig) (*kubernetes.Clientset, error) {
	var (
		restConfig *rest.Config
		err        error
	)
	if cfg.InCluster {
		restConfig, err = rest.InClusterConfig()
	} else {
		if len(cfg.Kubeconfig) == 0 {
			return nil, fmt.Errorf("kubeconfig is required")
		}
		if err := ValidateKubeconfig(cfg.Kubeconfig); err != nil {
			return nil, err
		}
		restConfig, err = clientcmd.RESTConfigFromKubeConfig(cfg.Kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("load Kubernetes configuration: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	restConfig.Timeout = cfg.Timeout

	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	body, err := client.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return nil, fmt.Errorf("verify Kubernetes cluster: %w", err)
	}
	var serverVersion version.Info
	if err := json.Unmarshal(body, &serverVersion); err != nil {
		return nil, fmt.Errorf("parse Kubernetes server version: %w", err)
	}
	return client, nil
}

// ValidateKubeconfig keeps Web-provided configuration self-contained. In
// particular, exec credential plugins would otherwise run arbitrary commands
// in the Ares container, and file references could read host/container paths.
func ValidateKubeconfig(content []byte) error {
	raw, err := clientcmd.Load(content)
	if err != nil {
		return fmt.Errorf("parse kubeconfig: %w", err)
	}
	for name, cluster := range raw.Clusters {
		if cluster.CertificateAuthority != "" {
			return fmt.Errorf("kubeconfig cluster %q must embed certificate-authority-data instead of referencing a file", name)
		}
	}
	for name, authInfo := range raw.AuthInfos {
		if authInfo.Exec != nil {
			return fmt.Errorf("kubeconfig user %q uses a forbidden exec credential plugin", name)
		}
		if authInfo.AuthProvider != nil {
			return fmt.Errorf("kubeconfig user %q uses a forbidden auth-provider plugin", name)
		}
		if authInfo.ClientCertificate != "" || authInfo.ClientKey != "" || authInfo.TokenFile != "" {
			return fmt.Errorf("kubeconfig user %q must embed credentials instead of referencing files", name)
		}
	}
	return nil
}

func ActivateManager(next *ClientManager) {
	registry.Lock()
	registry.manager = next
	registry.Unlock()
}

func Disable() {
	ActivateManager(nil)
}

func IsInitialized() bool {
	registry.RLock()
	defer registry.RUnlock()
	return registry.manager != nil
}

// GetClient gets a named client from the current immutable registry.
func GetClient(env Environment, clusterName string) (*kubernetes.Clientset, error) {
	registry.RLock()
	defer registry.RUnlock()
	if registry.manager == nil {
		return nil, fmt.Errorf("Kubernetes client registry is not initialized")
	}
	envClients, exists := registry.manager.clients[env]
	if !exists {
		return nil, fmt.Errorf("environment %q does not exist", env)
	}
	client, exists := envClients[clusterName]
	if !exists {
		return nil, fmt.Errorf("cluster %q does not exist in environment %q", clusterName, env)
	}
	return client, nil
}

func ListClusters(env Environment) []string {
	registry.RLock()
	defer registry.RUnlock()
	if registry.manager == nil {
		return nil
	}
	envClients, exists := registry.manager.clients[env]
	if !exists {
		return nil
	}
	names := make([]string, 0, len(envClients))
	for name := range envClients {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListEnvironments returns every environment currently configured in the
// immutable runtime registry. An empty list is valid when Kubernetes is disabled.
func ListEnvironments() []Environment {
	registry.RLock()
	defer registry.RUnlock()
	if registry.manager == nil {
		return nil
	}
	environments := make([]Environment, 0, len(registry.manager.clients))
	for environment := range registry.manager.clients {
		environments = append(environments, environment)
	}
	sort.Slice(environments, func(i, j int) bool { return environments[i] < environments[j] })
	return environments
}

func DefaultClient(env Environment) *kubernetes.Clientset {
	registry.RLock()
	defer registry.RUnlock()
	if registry.manager == nil {
		return nil
	}
	envClients, exists := registry.manager.clients[env]
	if !exists || len(envClients) == 0 {
		return nil
	}
	var firstName string
	for name := range envClients {
		if firstName == "" || name < firstName {
			firstName = name
		}
	}
	return envClients[firstName]
}
