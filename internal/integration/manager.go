package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-ree/ares/internal/db"
	"github.com/go-ree/ares/internal/entity"
	environmentcatalog "github.com/go-ree/ares/internal/environment"
	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/k8s"
	"github.com/go-ree/ares/internal/workflow"
	mysqlDriver "github.com/go-sql-driver/mysql"
)

const (
	providerJenkins                  = "jenkins"
	providerKubernetes               = "kubernetes"
	defaultIntegrationTimeoutSeconds = 15
	maxIntegrationTimeoutSeconds     = 120
	maxJenkinsTokenBytes             = 64 * 1024
	maxKubeconfigBytes               = 1024 * 1024
	maxSynchronizerAttempts          = 2
)

var (
	ErrSettingsChanged = errors.New("integration settings changed while the connection was being verified; reload and retry")
	// ErrActiveJenkinsRuns is returned when changing an enabled Jenkins
	// generation would orphan work that is already in flight. Callers should
	// use errors.Is instead of matching the localized detail text.
	ErrActiveJenkinsRuns = errors.New("仍有 Jenkins 在途任务")
	// ErrSettingsUnavailable deliberately carries no provider response or
	// persisted payload. Callers may log it without exposing credentials.
	ErrSettingsUnavailable = errors.New("integration settings are temporarily unavailable")
)

type providerRecord struct {
	exists     bool
	revision   uint64
	configData string
}

type settingsStore interface {
	load(context.Context, string) (providerRecord, error)
	compareAndSwap(context.Context, string, string, providerRecord) (uint64, error)
}

type databaseSettingsStore struct{}

type jenkinsSettingsCASStore interface {
	compareAndSwapJenkins(context.Context, string, providerRecord, bool) (uint64, error)
}

// providerSettingsStore is replaceable only by package tests. Production code
// installs the database implementation once and treats it as immutable.
var providerSettingsStore settingsStore = databaseSettingsStore{}

var runtimeSettings struct {
	sync.RWMutex
	cipher          *secretCipher
	jenkins         storedJenkinsConfig
	kubernetes      storedKubernetesConfig
	jenkinsError    string
	kubernetesError string
	// *Revision is the newest database revision observed by this replica;
	// *AppliedRevision is the revision of the currently active runtime. Keeping
	// them separate makes failed probes retryable without retaining stale access.
	jenkinsRevision        uint64
	k8sRevision            uint64
	jenkinsAppliedRevision uint64
	k8sAppliedRevision     uint64
	jenkinsUpdateMu        sync.Mutex
	k8sUpdateMu            sync.Mutex
}

func Initialize(encryptionKey string) error {
	return InitializeContext(context.Background(), encryptionKey)
}

func InitializeContext(parent context.Context, encryptionKey string) error {
	cipher, err := newSecretCipher(encryptionKey)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(nonNilContext(parent), time.Duration(defaultIntegrationTimeoutSeconds)*time.Second)
	defer cancel()
	jenkinsConfig, jenkinsRecord, jenkinsLoadErr := loadJenkinsSettings(ctx)
	if jenkinsLoadErr != nil {
		jenkinsConfig = storedJenkinsConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds}
		slog.Warn("Jenkins settings could not be loaded; Ares will continue without it", "error", safeError(jenkinsLoadErr))
	}
	kubernetesConfig, kubernetesRecord, kubernetesLoadErr := loadKubernetesSettings(ctx)
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
	runtimeSettings.jenkinsRevision = jenkinsRecord.revision
	runtimeSettings.k8sRevision = kubernetesRecord.revision
	runtimeSettings.jenkinsAppliedRevision = 0
	runtimeSettings.k8sAppliedRevision = 0
	runtimeSettings.Unlock()
	jenkins.Disable()
	k8s.Disable()

	// The lifecycle-owned synchronizer performs the first apply immediately
	// after startup. Initialize deliberately creates no context.Background
	// probe: every network operation must remain cancellable by service shutdown.
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
	kubernetesReentryRequired := false
	for _, cluster := range kubernetesConfig.Clusters {
		reentryRequired := credentialReentryRequired(cluster.KubeconfigCiphertext)
		kubernetesReentryRequired = kubernetesReentryRequired || reentryRequired
		clusters = append(clusters, KubernetesClusterView{
			Environment:               cluster.Environment,
			Name:                      cluster.Name,
			Description:               cluster.Description,
			KubeconfigConfigured:      cluster.KubeconfigCiphertext != "",
			CredentialReentryRequired: reentryRequired,
		})
	}
	jenkinsReentryRequired := credentialReentryRequired(jenkinsConfig.TokenCiphertext)
	if jenkinsReentryRequired {
		jenkinsError = safeError(ErrCredentialReentryRequired)
	}
	if kubernetesReentryRequired {
		kubernetesError = safeError(ErrCredentialReentryRequired)
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Environment < clusters[j].Environment })
	return Snapshot{
		Jenkins: JenkinsView{
			Enabled:                   jenkinsConfig.Enabled,
			Address:                   jenkinsConfig.Address,
			Username:                  jenkinsConfig.Username,
			TimeoutSeconds:            normalizedTimeout(jenkinsConfig.TimeoutSeconds),
			TokenConfigured:           jenkinsConfig.TokenCiphertext != "",
			CredentialReentryRequired: jenkinsReentryRequired,
			Connected:                 jenkins.IsConfigured(),
			LastError:                 jenkinsError,
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
	ctx = nonNilContext(ctx)
	timeout, err := validateTimeout(req.TimeoutSeconds)
	if err != nil {
		return JenkinsView{}, err
	}
	current, currentRecord, err := loadJenkinsSettings(ctx)
	if err != nil {
		return JenkinsView{}, settingsUnavailable("load Jenkins settings", err)
	}
	runtimeSettings.RLock()
	cipher := runtimeSettings.cipher
	runtimeSettings.RUnlock()
	if cipher == nil {
		return JenkinsView{}, ErrSettingsUnavailable
	}

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
	// Only replacement or shutdown of the currently enabled generation requires
	// a drain. Requiring an already disabled generation to be empty before it can
	// be enabled would deadlock recovery of Jenkins work stranded by an outage or
	// an earlier fail-closed disable. The settings transaction still takes the
	// exclusive provider fence: pending claims wait for the new generation, and
	// already-running v2 references remain pinned to their original address.
	requiresDrain := current.Enabled
	if current.TokenCiphertext != "" && req.Token == nil && identityChanged {
		return JenkinsView{}, fmt.Errorf("Jenkins token must be provided when address or username changes")
	}
	if req.Token != nil {
		if len(*req.Token) > maxJenkinsTokenBytes {
			return JenkinsView{}, fmt.Errorf("Jenkins token exceeds %d bytes", maxJenkinsTokenBytes)
		}
		next.TokenCiphertext, err = cipher.encrypt(*req.Token, jenkinsCredentialContext(next.Address, next.Username))
		if err != nil {
			return JenkinsView{}, err
		}
	}

	var candidate *jenkins.Runtime
	if next.Enabled {
		token, err := cipher.decrypt(next.TokenCiphertext, jenkinsCredentialContext(next.Address, next.Username))
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
	if err := ctx.Err(); err != nil {
		return JenkinsView{}, err
	}
	var committedRevision uint64
	if err := jenkins.CommitRuntime(func() (*jenkins.Runtime, error) {
		var saveErr error
		committedRevision, saveErr = saveJenkinsProviderCAS(ctx, next, currentRecord, requiresDrain)
		if saveErr != nil {
			return nil, saveErr
		}
		if !next.Enabled {
			return nil, nil
		}
		return candidate, nil
	}); err != nil {
		return JenkinsView{}, err
	}
	runtimeSettings.Lock()
	runtimeSettings.jenkins = next
	runtimeSettings.jenkinsError = ""
	runtimeSettings.jenkinsRevision = committedRevision
	runtimeSettings.jenkinsAppliedRevision = committedRevision
	runtimeSettings.Unlock()
	return SnapshotView().Jenkins, nil
}

func ensureNoActiveJenkinsRuns(ctx context.Context) error {
	if db.Engine == nil || db.Engine.DB() == nil || db.Engine.DB().DB == nil {
		return errors.New("数据库尚未初始化")
	}
	return ensureNoActiveJenkinsRunsWith(ctx, db.Engine.DB().DB)
}

type queryRowContexter interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func ensureNoActiveJenkinsRunsWith(ctx context.Context, querier queryRowContexter) error {
	activeLegacyTasks, activeWorkflowSteps, err := countActiveJenkinsRunsWith(ctx, querier)
	if err != nil {
		return fmt.Errorf("检查 Jenkins 在途任务失败: %w", err)
	}
	if activeLegacyTasks == 0 && activeWorkflowSteps == 0 {
		return nil
	}
	return fmt.Errorf(
		"%w（旧版任务 %d 个、工作流步骤 %d 个），完成或处置后才能修改当前已启用的 Jenkins 配置",
		ErrActiveJenkinsRuns,
		activeLegacyTasks, activeWorkflowSteps,
	)
}

func countActiveJenkinsRuns(ctx context.Context) (legacyTasks, workflowSteps int64, err error) {
	if db.Engine == nil || db.Engine.DB() == nil || db.Engine.DB().DB == nil {
		return 0, 0, errors.New("数据库尚未初始化")
	}
	return countActiveJenkinsRunsWith(ctx, db.Engine.DB().DB)
}

func countActiveJenkinsRunsWith(ctx context.Context, querier queryRowContexter) (legacyTasks, workflowSteps int64, err error) {
	err = querier.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_record
		WHERE engine_version < ? AND deleted_at IS NULL AND (
			status IN (?, ?) OR (status = ? AND auto_deploy = 1)
		)`, 2, entity.StatusPackaging, entity.StatusDeploying, entity.StatusPackaged).Scan(&legacyTasks)
	if err != nil {
		return 0, 0, err
	}
	err = querier.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM task_step_records step
		JOIN task_record task ON task.task_id = step.task_id
		WHERE step.status = ? AND step.uses = ?
			AND task.engine_version >= ? AND task.status IN (?, ?)
			AND task.deleted_at IS NULL`,
		workflow.StepRunning, "jenkins.job@v1", 2, workflow.TaskQueued, workflow.TaskRunning,
	).Scan(&workflowSteps)
	if err != nil {
		return 0, 0, err
	}
	return legacyTasks, workflowSteps, nil
}

func UpdateKubernetes(ctx context.Context, req UpdateKubernetesRequest) (KubernetesView, error) {
	ctx = nonNilContext(ctx)
	timeout, err := validateTimeout(req.TimeoutSeconds)
	if err != nil {
		return KubernetesView{}, err
	}
	current, currentRecord, err := loadKubernetesSettings(ctx)
	if err != nil {
		return KubernetesView{}, settingsUnavailable("load Kubernetes settings", err)
	}
	runtimeSettings.RLock()
	cipher := runtimeSettings.cipher
	runtimeSettings.RUnlock()
	if cipher == nil {
		return KubernetesView{}, ErrSettingsUnavailable
	}
	currentByEnvironment := make(map[string]storedKubernetesCluster, len(current.Clusters))
	for _, cluster := range current.Clusters {
		currentByEnvironment[cluster.Environment] = cluster
	}

	next := storedKubernetesConfig{Enabled: req.Enabled, TimeoutSeconds: timeout, Clusters: make([]storedKubernetesCluster, 0, len(req.Clusters))}
	seenEnvironments := make(map[string]struct{}, len(req.Clusters))
	environmentService := environmentcatalog.NewService()
	for _, input := range req.Clusters {
		runtimeEnv, err := k8s.ParseEnvironment(input.Environment)
		if err != nil {
			return KubernetesView{}, err
		}
		environment := string(runtimeEnv)
		if _, err := environmentService.Get(ctx, environment); err != nil {
			return KubernetesView{}, fmt.Errorf("Kubernetes 环境 %s 不在环境目录中: %w", environment, err)
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
			if stored.KubeconfigCiphertext != "" && input.Kubeconfig == nil && name != strings.TrimSpace(old.Name) {
				return KubernetesView{}, fmt.Errorf("kubeconfig must be provided when the cluster name changes for environment %s", environment)
			}
		}
		if input.Kubeconfig != nil {
			if len(*input.Kubeconfig) > maxKubeconfigBytes {
				return KubernetesView{}, fmt.Errorf("kubeconfig for %s exceeds %d bytes", environment, maxKubeconfigBytes)
			}
			stored.KubeconfigCiphertext, err = cipher.encrypt(*input.Kubeconfig, kubernetesCredentialContext(environment, name))
			if err != nil {
				return KubernetesView{}, err
			}
		}
		if stored.KubeconfigCiphertext == "" {
			return KubernetesView{}, fmt.Errorf("kubeconfig is required for environment %s", environment)
		}
		next.Clusters = append(next.Clusters, stored)
	}

	var candidate *k8s.ClientManager
	if next.Enabled {
		if len(next.Clusters) == 0 {
			return KubernetesView{}, fmt.Errorf("at least one Kubernetes cluster is required when the integration is enabled")
		}
		configs, err := runtimeKubernetesConfigs(next, cipher)
		if err != nil {
			return KubernetesView{}, err
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
	if err := ctx.Err(); err != nil {
		return KubernetesView{}, err
	}
	committedRevision, err := saveProviderCAS(ctx, providerKubernetes, next, currentRecord)
	if err != nil {
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
	runtimeSettings.k8sRevision = committedRevision
	runtimeSettings.k8sAppliedRevision = committedRevision
	runtimeSettings.Unlock()
	return SnapshotView().Kubernetes, nil
}

// RunSynchronizer blocks until ctx is cancelled. Each pass reads the database
// revision and only probes a provider whose desired runtime is not applied.
// Provider probes run concurrently, but each provider permits at most two
// attempts per pass so continuous writes cannot create an unbounded retry loop.
func RunSynchronizer(ctx context.Context, interval time.Duration) {
	ctx = nonNilContext(ctx)
	if interval <= 0 {
		interval = time.Second
	}
	synchronizeIntegrations(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			synchronizeIntegrations(ctx)
		}
	}
}

func synchronizeIntegrations(ctx context.Context) {
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		if err := EnsureJenkinsCurrent(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Jenkins settings synchronization failed", "error", safeError(err))
		}
	}()
	go func() {
		defer wait.Done()
		if err := EnsureKubernetesCurrent(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("Kubernetes settings synchronization failed", "error", safeError(err))
		}
	}()
	wait.Wait()
}

// EnsureJenkinsCurrent is a cheap database revision check when this replica is
// current and a bounded synchronous refresh otherwise. Executors can call it
// immediately before acquiring a Jenkins runtime to shorten the stale window.
func EnsureJenkinsCurrent(ctx context.Context) error {
	ctx = nonNilContext(ctx)
	for attempt := 0; attempt < maxSynchronizerAttempts; attempt++ {
		stored, record, err := loadJenkinsSettings(ctx)
		if err != nil {
			failJenkinsRevision(record.revision, err)
			return settingsUnavailable("load Jenkins settings", err)
		}
		cipher, needsApply, stale := stageJenkinsRevision(stored, record.revision)
		if stale {
			continue
		}
		if !needsApply {
			return nil
		}
		err = applyJenkinsRevision(ctx, stored, cipher, record.revision, true)
		if errors.Is(err, ErrSettingsChanged) {
			continue
		}
		return err
	}
	return ErrSettingsChanged
}

// EnsureKubernetesCurrent is the Kubernetes counterpart of
// EnsureJenkinsCurrent. Disabled settings are applied without building a
// client or issuing any network request.
func EnsureKubernetesCurrent(ctx context.Context) error {
	ctx = nonNilContext(ctx)
	for attempt := 0; attempt < maxSynchronizerAttempts; attempt++ {
		stored, record, err := loadKubernetesSettings(ctx)
		if err != nil {
			failKubernetesRevision(record.revision, err)
			return settingsUnavailable("load Kubernetes settings", err)
		}
		cipher, needsApply, stale := stageKubernetesRevision(stored, record.revision)
		if stale {
			continue
		}
		if !needsApply {
			return nil
		}
		err = applyKubernetesRevision(ctx, stored, cipher, record.revision, true)
		if errors.Is(err, ErrSettingsChanged) {
			continue
		}
		return err
	}
	return ErrSettingsChanged
}

func stageJenkinsRevision(stored storedJenkinsConfig, revision uint64) (*secretCipher, bool, bool) {
	runtimeSettings.jenkinsUpdateMu.Lock()
	defer runtimeSettings.jenkinsUpdateMu.Unlock()
	runtimeSettings.Lock()
	if revision < runtimeSettings.jenkinsRevision {
		cipher := runtimeSettings.cipher
		runtimeSettings.jenkinsAppliedRevision = 0
		runtimeSettings.jenkinsError = safeError(ErrSettingsChanged)
		runtimeSettings.Unlock()
		jenkins.Disable()
		return cipher, false, true
	}
	changed := revision > runtimeSettings.jenkinsRevision
	if changed {
		runtimeSettings.jenkins = stored
		runtimeSettings.jenkinsRevision = revision
		runtimeSettings.jenkinsError = ""
	}
	cipher := runtimeSettings.cipher
	needsApply := runtimeSettings.jenkinsAppliedRevision != revision
	runtimeSettings.Unlock()
	if changed {
		// Never retain the old endpoint while credentials for the new revision
		// are being verified.
		jenkins.Disable()
	}
	return cipher, needsApply, false
}

func stageKubernetesRevision(stored storedKubernetesConfig, revision uint64) (*secretCipher, bool, bool) {
	runtimeSettings.k8sUpdateMu.Lock()
	defer runtimeSettings.k8sUpdateMu.Unlock()
	runtimeSettings.Lock()
	if revision < runtimeSettings.k8sRevision {
		cipher := runtimeSettings.cipher
		runtimeSettings.k8sAppliedRevision = 0
		runtimeSettings.kubernetesError = safeError(ErrSettingsChanged)
		runtimeSettings.Unlock()
		k8s.Disable()
		return cipher, false, true
	}
	changed := revision > runtimeSettings.k8sRevision
	if changed {
		runtimeSettings.kubernetes = stored
		runtimeSettings.k8sRevision = revision
		runtimeSettings.kubernetesError = ""
	}
	cipher := runtimeSettings.cipher
	needsApply := runtimeSettings.k8sAppliedRevision != revision
	runtimeSettings.Unlock()
	if changed {
		k8s.Disable()
	}
	return cipher, needsApply, false
}

func failJenkinsRevision(revision uint64, cause error) {
	runtimeSettings.jenkinsUpdateMu.Lock()
	defer runtimeSettings.jenkinsUpdateMu.Unlock()
	runtimeSettings.Lock()
	runtimeSettings.jenkinsAppliedRevision = 0
	runtimeSettings.jenkinsError = safeError(cause)
	if revision >= runtimeSettings.jenkinsRevision {
		if revision > runtimeSettings.jenkinsRevision {
			runtimeSettings.jenkins = storedJenkinsConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds}
		}
		runtimeSettings.jenkinsRevision = revision
	}
	runtimeSettings.Unlock()
	jenkins.Disable()
}

func failKubernetesRevision(revision uint64, cause error) {
	runtimeSettings.k8sUpdateMu.Lock()
	defer runtimeSettings.k8sUpdateMu.Unlock()
	runtimeSettings.Lock()
	runtimeSettings.k8sAppliedRevision = 0
	runtimeSettings.kubernetesError = safeError(cause)
	if revision >= runtimeSettings.k8sRevision {
		if revision > runtimeSettings.k8sRevision {
			runtimeSettings.kubernetes = storedKubernetesConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds, Clusters: []storedKubernetesCluster{}}
		}
		runtimeSettings.k8sRevision = revision
	}
	runtimeSettings.Unlock()
	k8s.Disable()
}

func applyJenkinsRevision(ctx context.Context, stored storedJenkinsConfig, cipher *secretCipher, revision uint64, verifyDatabase bool) error {
	ctx = nonNilContext(ctx)
	if cipher == nil {
		return ErrSettingsUnavailable
	}
	runtimeSettings.RLock()
	desiredRevision := runtimeSettings.jenkinsRevision
	runtimeSettings.RUnlock()
	if desiredRevision != revision {
		return ErrSettingsChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var candidate *jenkins.Runtime
	var probeErr error
	if stored.Enabled {
		var token string
		token, probeErr = cipher.decrypt(stored.TokenCiphertext, jenkinsCredentialContext(stored.Address, stored.Username))
		if probeErr == nil {
			timeout := normalizedTimeout(stored.TimeoutSeconds)
			probeCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			candidate, probeErr = jenkins.BuildRuntime(probeCtx, jenkins.RuntimeConfig{
				Address: stored.Address, Username: stored.Username, Token: token, Timeout: time.Duration(timeout) * time.Second,
			})
			cancel()
		}
	}

	runtimeSettings.jenkinsUpdateMu.Lock()
	defer runtimeSettings.jenkinsUpdateMu.Unlock()
	runtimeSettings.RLock()
	unchanged := runtimeSettings.jenkinsRevision == revision
	runtimeSettings.RUnlock()
	if !unchanged {
		return ErrSettingsChanged
	}
	if verifyDatabase {
		latest, record, err := loadJenkinsSettings(ctx)
		if err != nil {
			runtimeSettings.Lock()
			runtimeSettings.jenkinsError = safeError(err)
			runtimeSettings.Unlock()
			jenkins.Disable()
			return settingsUnavailable("verify Jenkins settings revision", err)
		}
		if record.revision != revision {
			adoptNewerJenkinsRevision(latest, record.revision, revision)
			return ErrSettingsChanged
		}
	}
	if probeErr != nil {
		jenkins.Disable()
		runtimeSettings.Lock()
		runtimeSettings.jenkinsError = safeError(probeErr)
		runtimeSettings.Unlock()
		return settingsUnavailable("activate Jenkins settings", probeErr)
	}
	if stored.Enabled {
		jenkins.Activate(candidate)
	} else {
		jenkins.Disable()
	}
	runtimeSettings.Lock()
	runtimeSettings.jenkins = stored
	runtimeSettings.jenkinsError = ""
	runtimeSettings.jenkinsAppliedRevision = revision
	runtimeSettings.Unlock()
	return nil
}

func applyKubernetesRevision(ctx context.Context, stored storedKubernetesConfig, cipher *secretCipher, revision uint64, verifyDatabase bool) error {
	ctx = nonNilContext(ctx)
	if cipher == nil {
		return ErrSettingsUnavailable
	}
	runtimeSettings.RLock()
	desiredRevision := runtimeSettings.k8sRevision
	runtimeSettings.RUnlock()
	if desiredRevision != revision {
		return ErrSettingsChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var candidate *k8s.ClientManager
	var probeErr error
	if stored.Enabled {
		var configs []k8s.ClusterConfig
		configs, probeErr = runtimeKubernetesConfigs(stored, cipher)
		if probeErr == nil {
			timeout := time.Duration(normalizedTimeout(stored.TimeoutSeconds)) * time.Second
			probeCtx, cancel := context.WithTimeout(ctx, timeout)
			candidate, probeErr = k8s.BuildManager(probeCtx, configs)
			cancel()
		}
	}

	runtimeSettings.k8sUpdateMu.Lock()
	defer runtimeSettings.k8sUpdateMu.Unlock()
	runtimeSettings.RLock()
	unchanged := runtimeSettings.k8sRevision == revision
	runtimeSettings.RUnlock()
	if !unchanged {
		return ErrSettingsChanged
	}
	if verifyDatabase {
		latest, record, err := loadKubernetesSettings(ctx)
		if err != nil {
			runtimeSettings.Lock()
			runtimeSettings.kubernetesError = safeError(err)
			runtimeSettings.Unlock()
			k8s.Disable()
			return settingsUnavailable("verify Kubernetes settings revision", err)
		}
		if record.revision != revision {
			adoptNewerKubernetesRevision(latest, record.revision, revision)
			return ErrSettingsChanged
		}
	}
	if probeErr != nil {
		k8s.Disable()
		runtimeSettings.Lock()
		runtimeSettings.kubernetesError = safeError(probeErr)
		runtimeSettings.Unlock()
		return settingsUnavailable("activate Kubernetes settings", probeErr)
	}
	if stored.Enabled {
		k8s.ActivateManager(candidate)
	} else {
		k8s.Disable()
	}
	runtimeSettings.Lock()
	runtimeSettings.kubernetes = stored
	runtimeSettings.kubernetesError = ""
	runtimeSettings.k8sAppliedRevision = revision
	runtimeSettings.Unlock()
	return nil
}

// These wrappers retain the existing package-level boot-test seam. Production
// startup uses the database-verifying variants above.
func applyStoredJenkinsAtBoot(stored storedJenkinsConfig, cipher *secretCipher, revision uint64) {
	_ = applyJenkinsRevision(context.Background(), stored, cipher, revision, false)
}

func applyStoredKubernetesAtBoot(stored storedKubernetesConfig, cipher *secretCipher, revision uint64) {
	_ = applyKubernetesRevision(context.Background(), stored, cipher, revision, false)
}

func adoptNewerJenkinsRevision(stored storedJenkinsConfig, revision, previous uint64) {
	if revision <= previous {
		return
	}
	runtimeSettings.Lock()
	if revision > runtimeSettings.jenkinsRevision {
		runtimeSettings.jenkins = stored
		runtimeSettings.jenkinsRevision = revision
		runtimeSettings.jenkinsError = ""
	}
	runtimeSettings.Unlock()
	jenkins.Disable()
}

func adoptNewerKubernetesRevision(stored storedKubernetesConfig, revision, previous uint64) {
	if revision <= previous {
		return
	}
	runtimeSettings.Lock()
	if revision > runtimeSettings.k8sRevision {
		runtimeSettings.kubernetes = stored
		runtimeSettings.k8sRevision = revision
		runtimeSettings.kubernetesError = ""
	}
	runtimeSettings.Unlock()
	k8s.Disable()
}

func runtimeKubernetesConfigs(stored storedKubernetesConfig, cipher *secretCipher) ([]k8s.ClusterConfig, error) {
	timeout := time.Duration(normalizedTimeout(stored.TimeoutSeconds)) * time.Second
	configs := make([]k8s.ClusterConfig, 0, len(stored.Clusters))
	for _, cluster := range stored.Clusters {
		content, err := cipher.decrypt(cluster.KubeconfigCiphertext, kubernetesCredentialContext(cluster.Environment, cluster.Name))
		if err != nil {
			return nil, fmt.Errorf("decrypt kubeconfig for %s: %w", cluster.Environment, err)
		}
		if content == "" {
			return nil, fmt.Errorf("kubeconfig is required for environment %s", cluster.Environment)
		}
		if err := k8s.ValidateKubeconfig([]byte(content)); err != nil {
			return nil, fmt.Errorf("validate kubeconfig for %s: %w", cluster.Environment, err)
		}
		environment, err := k8s.ParseEnvironment(cluster.Environment)
		if err != nil {
			return nil, err
		}
		configs = append(configs, k8s.ClusterConfig{
			Name: cluster.Name, Environment: environment, Kubeconfig: []byte(content), Timeout: timeout,
		})
	}
	return configs, nil
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

func loadJenkinsSettings(ctx context.Context) (storedJenkinsConfig, providerRecord, error) {
	config := storedJenkinsConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds}
	record, err := providerSettingsStore.load(nonNilContext(ctx), providerJenkins)
	if err != nil {
		return config, record, err
	}
	if !record.exists {
		return config, record, nil
	}
	if err := json.Unmarshal([]byte(record.configData), &config); err != nil {
		return storedJenkinsConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds}, record,
			fmt.Errorf("decode Jenkins integration settings: %w", err)
	}
	return config, record, nil
}

func loadKubernetesSettings(ctx context.Context) (storedKubernetesConfig, providerRecord, error) {
	config := storedKubernetesConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds, Clusters: []storedKubernetesCluster{}}
	record, err := providerSettingsStore.load(nonNilContext(ctx), providerKubernetes)
	if err != nil {
		return config, record, err
	}
	if !record.exists {
		return config, record, nil
	}
	if err := json.Unmarshal([]byte(record.configData), &config); err != nil {
		return storedKubernetesConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds, Clusters: []storedKubernetesCluster{}}, record,
			fmt.Errorf("decode Kubernetes integration settings: %w", err)
	}
	if config.Clusters == nil {
		config.Clusters = []storedKubernetesCluster{}
	}
	return config, record, nil
}

func saveProviderCAS(ctx context.Context, provider string, value any, expected providerRecord) (uint64, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("encode %s integration settings: %w", provider, err)
	}
	revision, err := providerSettingsStore.compareAndSwap(nonNilContext(ctx), provider, string(encoded), expected)
	if err != nil {
		if errors.Is(err, ErrSettingsChanged) {
			return 0, ErrSettingsChanged
		}
		return 0, settingsUnavailable("persist "+provider+" settings", err)
	}
	return revision, nil
}

func saveJenkinsProviderCAS(
	ctx context.Context,
	value storedJenkinsConfig,
	expected providerRecord,
	requireDrain bool,
) (uint64, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("encode Jenkins integration settings: %w", err)
	}
	guarded, ok := providerSettingsStore.(jenkinsSettingsCASStore)
	if !ok {
		// In-memory package tests do not own SQL task rows. Production always uses
		// databaseSettingsStore and therefore always takes the database fence.
		return providerSettingsStore.compareAndSwap(nonNilContext(ctx), providerJenkins, string(encoded), expected)
	}
	revision, err := guarded.compareAndSwapJenkins(nonNilContext(ctx), string(encoded), expected, requireDrain)
	if err != nil {
		if errors.Is(err, ErrSettingsChanged) || errors.Is(err, ErrActiveJenkinsRuns) {
			return 0, err
		}
		return 0, settingsUnavailable("persist Jenkins settings", err)
	}
	return revision, nil
}

func (databaseSettingsStore) load(ctx context.Context, provider string) (providerRecord, error) {
	if db.Engine == nil {
		return providerRecord{}, errors.New("database is not initialized")
	}
	var record providerRecord
	err := db.Engine.DB().DB.QueryRowContext(ctx,
		`SELECT config_data, revision FROM integration_settings WHERE provider = ?`, provider,
	).Scan(&record.configData, &record.revision)
	if errors.Is(err, sql.ErrNoRows) {
		return providerRecord{}, nil
	}
	if err != nil {
		return providerRecord{}, fmt.Errorf("load %s integration settings: %w", provider, err)
	}
	record.exists = true
	return record, nil
}

func (databaseSettingsStore) compareAndSwap(ctx context.Context, provider, configData string, expected providerRecord) (uint64, error) {
	if db.Engine == nil {
		return 0, errors.New("database is not initialized")
	}
	if expected.revision == math.MaxUint64 {
		return 0, errors.New("integration settings revision exhausted")
	}
	if !expected.exists {
		_, err := db.Engine.DB().DB.ExecContext(ctx,
			`INSERT INTO integration_settings (provider, config_data, revision) VALUES (?, ?, 1)`,
			provider, configData,
		)
		if err != nil {
			var mysqlErr *mysqlDriver.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return 0, ErrSettingsChanged
			}
			return 0, fmt.Errorf("create %s integration settings: %w", provider, err)
		}
		return 1, nil
	}
	result, err := db.Engine.DB().DB.ExecContext(ctx,
		`UPDATE integration_settings
		 SET config_data = ?, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		 WHERE provider = ? AND revision = ?`,
		configData, provider, expected.revision,
	)
	if err != nil {
		return 0, fmt.Errorf("update %s integration settings: %w", provider, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("inspect %s integration settings update: %w", provider, err)
	}
	if affected != 1 {
		return 0, ErrSettingsChanged
	}
	return expected.revision + 1, nil
}

func (databaseSettingsStore) compareAndSwapJenkins(
	ctx context.Context,
	configData string,
	expected providerRecord,
	requireDrain bool,
) (uint64, error) {
	if db.Engine == nil || db.Engine.DB() == nil || db.Engine.DB().DB == nil {
		return 0, errors.New("database is not initialized")
	}
	if expected.revision == math.MaxUint64 {
		return 0, errors.New("integration settings revision exhausted")
	}
	tx, err := db.Engine.DB().DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// ClaimStep locks its task row first and then takes provider rows in sorted
	// order with FOR SHARE. This transaction takes only the Jenkins provider row
	// exclusively; the activity queries below must remain non-locking consistent
	// reads. Locking task rows here would reverse ClaimStep's order and introduce
	// a task -> provider / provider -> task deadlock. READ COMMITTED gives each
	// count a fresh view after a preceding ClaimStep releases this row lock.
	var currentRevision uint64
	err = tx.QueryRowContext(ctx, `SELECT revision FROM integration_settings
		WHERE provider = ? FOR UPDATE`, providerJenkins).Scan(&currentRevision)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if expected.exists {
			return 0, ErrSettingsChanged
		}
		if requireDrain {
			if err := ensureNoActiveJenkinsRunsWith(ctx, tx); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO integration_settings
			(provider, config_data, revision) VALUES (?, ?, 1)`, providerJenkins, configData); err != nil {
			var mysqlErr *mysqlDriver.MySQLError
			if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
				return 0, ErrSettingsChanged
			}
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return 1, nil
	case err != nil:
		return 0, err
	case !expected.exists || currentRevision != expected.revision:
		return 0, ErrSettingsChanged
	}

	if requireDrain {
		if err := ensureNoActiveJenkinsRunsWith(ctx, tx); err != nil {
			return 0, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE integration_settings
		SET config_data = ?, revision = revision + 1, updated_at = CURRENT_TIMESTAMP
		WHERE provider = ? AND revision = ?`, configData, providerJenkins, expected.revision)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected != 1 {
		return 0, ErrSettingsChanged
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return expected.revision + 1, nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func settingsUnavailable(operation string, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("%w: %s", ErrSettingsUnavailable, operation)
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "外部集成连接超时"
	case errors.Is(err, context.Canceled):
		return "外部集成连接已取消"
	case errors.Is(err, ErrCredentialReentryRequired):
		return "已保存的旧版凭据无法安全迁移，请重新录入"
	default:
		// Provider errors and Kubernetes Status.message are untrusted and may
		// reflect Authorization headers. Runtime snapshots are Web-visible, so
		// retain only a stable operator-facing class.
		return "外部集成暂不可用"
	}
}
