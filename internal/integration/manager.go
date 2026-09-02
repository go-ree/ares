package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"ares/internal/db"
	"ares/internal/entity"
	"ares/internal/jenkins"
	"ares/internal/k8s"
)

const (
	providerJenkins                  = "jenkins"
	providerKubernetes               = "kubernetes"
	defaultIntegrationTimeoutSeconds = 15
	maxIntegrationTimeoutSeconds     = 120
	maxJenkinsTokenBytes             = 64 * 1024
	maxKubeconfigBytes               = 1024 * 1024
)

var ErrSettingsChanged = errors.New("integration settings changed while the connection was being verified; reload and retry")

var runtimeSettings struct {
	sync.RWMutex
	cipher          *secretCipher
	jenkins         storedJenkinsConfig
	kubernetes      storedKubernetesConfig
	jenkinsError    string
	kubernetesError string
	jenkinsRevision uint64
	k8sRevision     uint64
	jenkinsUpdateMu sync.Mutex
	k8sUpdateMu     sync.Mutex
}

func Initialize(encryptionKey string) error {
	cipher, err := newSecretCipher(encryptionKey)
	if err != nil {
		return err
	}

	jenkinsConfig := storedJenkinsConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds}
	jenkinsLoadErr := loadProvider(providerJenkins, &jenkinsConfig)
	if jenkinsLoadErr != nil {
		jenkinsConfig = storedJenkinsConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds}
		slog.Warn("Jenkins settings could not be loaded; Ares will continue without it", "error", safeError(jenkinsLoadErr))
	}
	kubernetesConfig := storedKubernetesConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds, Clusters: []storedKubernetesCluster{}}
	kubernetesLoadErr := loadProvider(providerKubernetes, &kubernetesConfig)
	if kubernetesLoadErr != nil {
		kubernetesConfig = storedKubernetesConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds, Clusters: []storedKubernetesCluster{}}
		slog.Warn("Kubernetes settings could not be loaded; Ares will continue without it", "error", safeError(kubernetesLoadErr))
	}

	runtimeSettings.Lock()
	runtimeSettings.cipher = cipher
	runtimeSettings.jenkins = jenkinsConfig
	runtimeSettings.kubernetes = kubernetesConfig
	runtimeSettings.jenkinsError = safeError(jenkinsLoadErr)
	runtimeSettings.kubernetesError = safeError(kubernetesLoadErr)
	runtimeSettings.jenkinsRevision++
	jenkinsRevision := runtimeSettings.jenkinsRevision
	runtimeSettings.k8sRevision++
	k8sRevision := runtimeSettings.k8sRevision
	runtimeSettings.Unlock()

	// External systems are activated in the background so a saved-but-offline
	// integration can never delay the core API becoming ready. Revisions prevent
	// a slow boot probe from overwriting a newer Web update.
	if jenkinsLoadErr == nil {
		go applyStoredJenkinsAtBoot(jenkinsConfig, cipher, jenkinsRevision)
	} else {
		jenkins.Disable()
	}
	if kubernetesLoadErr == nil {
		go applyStoredKubernetesAtBoot(kubernetesConfig, cipher, k8sRevision)
	} else {
		k8s.Disable()
	}
	return nil
}

func SnapshotView() Snapshot {
	runtimeSettings.RLock()
	jenkinsConfig := runtimeSettings.jenkins
	kubernetesConfig := runtimeSettings.kubernetes
	jenkinsError := runtimeSettings.jenkinsError
	kubernetesError := runtimeSettings.kubernetesError
	runtimeSettings.RUnlock()

	clusters := make([]KubernetesClusterView, 0, len(kubernetesConfig.Clusters))
	for _, cluster := range kubernetesConfig.Clusters {
		clusters = append(clusters, KubernetesClusterView{
			Environment:          cluster.Environment,
			Name:                 cluster.Name,
			Description:          cluster.Description,
			KubeconfigConfigured: cluster.KubeconfigCiphertext != "",
		})
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Environment < clusters[j].Environment })
	return Snapshot{
		Jenkins: JenkinsView{
			Enabled:         jenkinsConfig.Enabled,
			Address:         jenkinsConfig.Address,
			Username:        jenkinsConfig.Username,
			TimeoutSeconds:  normalizedTimeout(jenkinsConfig.TimeoutSeconds),
			TokenConfigured: jenkinsConfig.TokenCiphertext != "",
			Connected:       jenkins.IsConfigured(),
			LastError:       jenkinsError,
		},
		Kubernetes: KubernetesView{
			Enabled:        kubernetesConfig.Enabled,
			TimeoutSeconds: normalizedTimeout(kubernetesConfig.TimeoutSeconds),
			Connected:      k8s.IsInitialized(),
			LastError:      kubernetesError,
			Clusters:       clusters,
		},
	}
}

func UpdateJenkins(ctx context.Context, req UpdateJenkinsRequest) (JenkinsView, error) {
	timeout, err := validateTimeout(req.TimeoutSeconds)
	if err != nil {
		return JenkinsView{}, err
	}
	runtimeSettings.RLock()
	current := runtimeSettings.jenkins
	cipher := runtimeSettings.cipher
	revision := runtimeSettings.jenkinsRevision
	runtimeSettings.RUnlock()

	next := storedJenkinsConfig{
		Enabled:         req.Enabled,
		Address:         strings.TrimSpace(req.Address),
		Username:        strings.TrimSpace(req.Username),
		TimeoutSeconds:  timeout,
		TokenCiphertext: current.TokenCiphertext,
	}
	if next.Address != "" {
		next.Address, err = jenkins.NormalizeAddress(next.Address)
		if err != nil {
			return JenkinsView{}, err
		}
	}
	identityChanged := normalizedJenkinsAddress(next.Address) != normalizedJenkinsAddress(current.Address) || next.Username != strings.TrimSpace(current.Username)
	if current.TokenCiphertext != "" && req.Token == nil && identityChanged {
		return JenkinsView{}, fmt.Errorf("Jenkins token must be provided when address or username changes")
	}
	if req.Token != nil {
		if len(*req.Token) > maxJenkinsTokenBytes {
			return JenkinsView{}, fmt.Errorf("Jenkins token exceeds %d bytes", maxJenkinsTokenBytes)
		}
		next.TokenCiphertext, err = cipher.encrypt(*req.Token)
		if err != nil {
			return JenkinsView{}, err
		}
	}

	var candidate *jenkins.Runtime
	if next.Enabled {
		token, err := cipher.decrypt(next.TokenCiphertext)
		if err != nil {
			return JenkinsView{}, err
		}
		connectCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		candidate, err = jenkins.BuildRuntime(connectCtx, jenkins.RuntimeConfig{
			Address: next.Address, Username: next.Username, Token: token, Timeout: time.Duration(timeout) * time.Second,
		})
		if err != nil {
			return JenkinsView{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return JenkinsView{}, err
	}

	runtimeSettings.jenkinsUpdateMu.Lock()
	defer runtimeSettings.jenkinsUpdateMu.Unlock()
	runtimeSettings.RLock()
	unchanged := runtimeSettings.jenkinsRevision == revision
	runtimeSettings.RUnlock()
	if !unchanged {
		return JenkinsView{}, ErrSettingsChanged
	}
	if err := ctx.Err(); err != nil {
		return JenkinsView{}, err
	}
	if err := saveProvider(providerJenkins, next); err != nil {
		return JenkinsView{}, err
	}
	if next.Enabled {
		jenkins.Activate(candidate)
	} else {
		jenkins.Disable()
	}
	runtimeSettings.Lock()
	runtimeSettings.jenkins = next
	runtimeSettings.jenkinsError = ""
	runtimeSettings.jenkinsRevision++
	runtimeSettings.Unlock()
	return SnapshotView().Jenkins, nil
}

func UpdateKubernetes(ctx context.Context, req UpdateKubernetesRequest) (KubernetesView, error) {
	timeout, err := validateTimeout(req.TimeoutSeconds)
	if err != nil {
		return KubernetesView{}, err
	}
	runtimeSettings.RLock()
	current := runtimeSettings.kubernetes
	cipher := runtimeSettings.cipher
	revision := runtimeSettings.k8sRevision
	runtimeSettings.RUnlock()
	currentByEnvironment := make(map[string]storedKubernetesCluster, len(current.Clusters))
	for _, cluster := range current.Clusters {
		currentByEnvironment[cluster.Environment] = cluster
	}

	next := storedKubernetesConfig{Enabled: req.Enabled, TimeoutSeconds: timeout, Clusters: make([]storedKubernetesCluster, 0, len(req.Clusters))}
	seenEnvironments := make(map[string]struct{}, len(req.Clusters))
	for _, input := range req.Clusters {
		environment := strings.ToLower(strings.TrimSpace(input.Environment))
		if !validEnvironment(environment) {
			return KubernetesView{}, fmt.Errorf("unsupported Kubernetes environment %q", input.Environment)
		}
		if _, exists := seenEnvironments[environment]; exists {
			return KubernetesView{}, fmt.Errorf("only one Kubernetes cluster can be configured for environment %s", environment)
		}
		seenEnvironments[environment] = struct{}{}
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return KubernetesView{}, fmt.Errorf("Kubernetes cluster name is required for environment %s", environment)
		}
		stored := storedKubernetesCluster{
			Environment: environment,
			Name:        name,
			Description: strings.TrimSpace(input.Description),
		}
		if old, exists := currentByEnvironment[environment]; exists {
			stored.KubeconfigCiphertext = old.KubeconfigCiphertext
		}
		if input.Kubeconfig != nil {
			if len(*input.Kubeconfig) > maxKubeconfigBytes {
				return KubernetesView{}, fmt.Errorf("kubeconfig for %s exceeds %d bytes", environment, maxKubeconfigBytes)
			}
			stored.KubeconfigCiphertext, err = cipher.encrypt(*input.Kubeconfig)
			if err != nil {
				return KubernetesView{}, err
			}
		}
		if stored.KubeconfigCiphertext == "" {
			return KubernetesView{}, fmt.Errorf("kubeconfig is required for environment %s", environment)
		}
		next.Clusters = append(next.Clusters, stored)
	}

	configs, err := runtimeKubernetesConfigs(next, cipher)
	if err != nil {
		return KubernetesView{}, err
	}
	var candidate *k8s.ClientManager
	if next.Enabled {
		if len(next.Clusters) == 0 {
			return KubernetesView{}, fmt.Errorf("at least one Kubernetes cluster is required when the integration is enabled")
		}
		connectCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
		candidate, err = k8s.BuildManager(connectCtx, configs)
		if err != nil {
			return KubernetesView{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return KubernetesView{}, err
	}

	runtimeSettings.k8sUpdateMu.Lock()
	defer runtimeSettings.k8sUpdateMu.Unlock()
	runtimeSettings.RLock()
	unchanged := runtimeSettings.k8sRevision == revision
	runtimeSettings.RUnlock()
	if !unchanged {
		return KubernetesView{}, ErrSettingsChanged
	}
	if err := ctx.Err(); err != nil {
		return KubernetesView{}, err
	}
	if err := saveProvider(providerKubernetes, next); err != nil {
		return KubernetesView{}, err
	}
	if next.Enabled {
		k8s.ActivateManager(candidate)
	} else {
		k8s.Disable()
	}
	runtimeSettings.Lock()
	runtimeSettings.kubernetes = next
	runtimeSettings.kubernetesError = ""
	runtimeSettings.k8sRevision++
	runtimeSettings.Unlock()
	return SnapshotView().Kubernetes, nil
}

func applyStoredJenkinsAtBoot(stored storedJenkinsConfig, cipher *secretCipher, revision uint64) {
	var (
		candidate *jenkins.Runtime
		token     string
		err       error
	)
	if stored.Enabled {
		token, err = cipher.decrypt(stored.TokenCiphertext)
	}
	if stored.Enabled && err == nil {
		timeout := normalizedTimeout(stored.TimeoutSeconds)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()
		candidate, err = jenkins.BuildRuntime(ctx, jenkins.RuntimeConfig{
			Address: stored.Address, Username: stored.Username, Token: token, Timeout: time.Duration(timeout) * time.Second,
		})
	}

	runtimeSettings.jenkinsUpdateMu.Lock()
	defer runtimeSettings.jenkinsUpdateMu.Unlock()
	runtimeSettings.RLock()
	unchanged := runtimeSettings.jenkinsRevision == revision
	runtimeSettings.RUnlock()
	if !unchanged {
		return
	}
	if stored.Enabled && err == nil {
		jenkins.Activate(candidate)
	} else {
		jenkins.Disable()
	}
	runtimeSettings.Lock()
	runtimeSettings.jenkinsError = safeError(err)
	runtimeSettings.Unlock()
	if err != nil {
		slog.Warn("Jenkins configuration could not be activated; Ares will continue without it", "error", safeError(err))
	}
}

func applyStoredKubernetesAtBoot(stored storedKubernetesConfig, cipher *secretCipher, revision uint64) {
	var (
		candidate *k8s.ClientManager
		err       error
	)
	if stored.Enabled {
		var configs []k8s.ClusterConfig
		configs, err = runtimeKubernetesConfigs(stored, cipher)
		if err == nil {
			timeout := time.Duration(normalizedTimeout(stored.TimeoutSeconds)) * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			candidate, err = k8s.BuildManager(ctx, configs)
		}
	}

	runtimeSettings.k8sUpdateMu.Lock()
	defer runtimeSettings.k8sUpdateMu.Unlock()
	runtimeSettings.RLock()
	unchanged := runtimeSettings.k8sRevision == revision
	runtimeSettings.RUnlock()
	if !unchanged {
		return
	}
	if stored.Enabled && err == nil {
		k8s.ActivateManager(candidate)
	} else {
		k8s.Disable()
	}
	runtimeSettings.Lock()
	runtimeSettings.kubernetesError = safeError(err)
	runtimeSettings.Unlock()
	if err != nil {
		slog.Warn("Kubernetes configuration could not be activated; Ares will continue without it", "error", safeError(err))
	}
}

func runtimeKubernetesConfigs(stored storedKubernetesConfig, cipher *secretCipher) ([]k8s.ClusterConfig, error) {
	timeout := time.Duration(normalizedTimeout(stored.TimeoutSeconds)) * time.Second
	configs := make([]k8s.ClusterConfig, 0, len(stored.Clusters))
	for _, cluster := range stored.Clusters {
		content, err := cipher.decrypt(cluster.KubeconfigCiphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt kubeconfig for %s: %w", cluster.Environment, err)
		}
		if content == "" {
			return nil, fmt.Errorf("kubeconfig is required for environment %s", cluster.Environment)
		}
		if err := k8s.ValidateKubeconfig([]byte(content)); err != nil {
			return nil, fmt.Errorf("validate kubeconfig for %s: %w", cluster.Environment, err)
		}
		environment, _ := runtimeEnvironment(cluster.Environment)
		configs = append(configs, k8s.ClusterConfig{
			Name: cluster.Name, Environment: environment, Kubeconfig: []byte(content), Timeout: timeout,
		})
	}
	return configs, nil
}

func runtimeEnvironment(environment string) (k8s.Environment, bool) {
	switch environment {
	case "dev":
		return k8s.EnvDev, true
	case "test":
		return k8s.EnvTest, true
	case "moni":
		return k8s.EnvStage, true
	default:
		return "", false
	}
}

func validEnvironment(environment string) bool {
	_, ok := runtimeEnvironment(environment)
	return ok
}

func normalizedJenkinsAddress(address string) string {
	return strings.TrimRight(strings.TrimSpace(address), "/")
}

func validateTimeout(seconds int) (int, error) {
	if seconds == 0 {
		return defaultIntegrationTimeoutSeconds, nil
	}
	if seconds < 1 || seconds > maxIntegrationTimeoutSeconds {
		return 0, fmt.Errorf("timeout_seconds must be between 1 and %d", maxIntegrationTimeoutSeconds)
	}
	return seconds, nil
}

func normalizedTimeout(seconds int) int {
	if seconds <= 0 {
		return defaultIntegrationTimeoutSeconds
	}
	return seconds
}

func loadProvider(provider string, target any) error {
	row := entity.IntegrationSetting{Provider: provider}
	has, err := db.Engine.ID(provider).Get(&row)
	if err != nil {
		return fmt.Errorf("load %s integration settings: %w", provider, err)
	}
	if !has {
		return nil
	}
	if err := json.Unmarshal([]byte(row.ConfigData), target); err != nil {
		return fmt.Errorf("decode %s integration settings: %w", provider, err)
	}
	return nil
}

func saveProvider(provider string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s integration settings: %w", provider, err)
	}
	row := entity.IntegrationSetting{Provider: provider, ConfigData: string(encoded)}
	has, err := db.Engine.ID(provider).Exist(new(entity.IntegrationSetting))
	if err != nil {
		return fmt.Errorf("check %s integration settings: %w", provider, err)
	}
	if has {
		if _, err := db.Engine.ID(provider).Cols("config_data").Update(&row); err != nil {
			return fmt.Errorf("update %s integration settings: %w", provider, err)
		}
		return nil
	}
	if _, err := db.Engine.Insert(&row); err != nil {
		return fmt.Errorf("create %s integration settings: %w", provider, err)
	}
	return nil
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 500 {
		return message[:500]
	}
	return message
}
