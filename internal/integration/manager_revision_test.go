package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-ree/ares/internal/jenkins"
	"github.com/go-ree/ares/internal/k8s"
)

type memorySettingsStore struct {
	mu              sync.Mutex
	rows            map[string]providerRecord
	barrierProvider string
	barrierTarget   int
	barrierCount    int
	barrier         chan struct{}
}

type guardedMemorySettingsStore struct {
	*memorySettingsStore
	jenkinsError        error
	lastRequireDrain    bool
	jenkinsCompareCalls int
}

func (store *guardedMemorySettingsStore) compareAndSwapJenkins(
	ctx context.Context,
	configData string,
	expected providerRecord,
	requireDrain bool,
) (uint64, error) {
	store.mu.Lock()
	store.lastRequireDrain = requireDrain
	store.jenkinsCompareCalls++
	guardErr := store.jenkinsError
	store.mu.Unlock()
	if requireDrain && guardErr != nil {
		return 0, guardErr
	}
	return store.compareAndSwap(ctx, providerJenkins, configData, expected)
}

func (store *guardedMemorySettingsStore) drainDecision() (bool, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.lastRequireDrain, store.jenkinsCompareCalls
}

func newMemorySettingsStore() *memorySettingsStore {
	return &memorySettingsStore{rows: make(map[string]providerRecord)}
}

func (store *memorySettingsStore) load(ctx context.Context, provider string) (providerRecord, error) {
	store.mu.Lock()
	record := store.rows[provider]
	var barrier chan struct{}
	if provider == store.barrierProvider && store.barrierTarget > 0 {
		store.barrierCount++
		barrier = store.barrier
		if store.barrierCount == store.barrierTarget {
			close(store.barrier)
			store.barrierTarget = 0
		}
	}
	store.mu.Unlock()
	if barrier != nil {
		select {
		case <-barrier:
		case <-ctx.Done():
			return providerRecord{}, ctx.Err()
		}
	}
	return record, nil
}

func (store *memorySettingsStore) compareAndSwap(_ context.Context, provider, configData string, expected providerRecord) (uint64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, exists := store.rows[provider]
	if exists != expected.exists || (exists && current.revision != expected.revision) {
		return 0, ErrSettingsChanged
	}
	revision := uint64(1)
	if exists {
		revision = current.revision + 1
	}
	store.rows[provider] = providerRecord{exists: true, revision: revision, configData: configData}
	return revision, nil
}

func (store *memorySettingsStore) set(provider string, revision uint64, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	store.mu.Lock()
	store.rows[provider] = providerRecord{exists: true, revision: revision, configData: string(encoded)}
	store.mu.Unlock()
}

func (store *memorySettingsStore) row(provider string) providerRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.rows[provider]
}

func installMemorySettingsStore(t *testing.T, store *memorySettingsStore) *secretCipher {
	t.Helper()
	cipher, err := newSecretCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("newSecretCipher() error = %v", err)
	}
	previousStore := providerSettingsStore
	previousJenkins := jenkins.Current()
	runtimeSettings.Lock()
	previousCipher := runtimeSettings.cipher
	previousJenkinsConfig := runtimeSettings.jenkins
	previousKubernetesConfig := runtimeSettings.kubernetes
	previousJenkinsError := runtimeSettings.jenkinsError
	previousKubernetesError := runtimeSettings.kubernetesError
	previousJenkinsRevision := runtimeSettings.jenkinsRevision
	previousKubernetesRevision := runtimeSettings.k8sRevision
	previousJenkinsAppliedRevision := runtimeSettings.jenkinsAppliedRevision
	previousKubernetesAppliedRevision := runtimeSettings.k8sAppliedRevision
	runtimeSettings.cipher = cipher
	runtimeSettings.jenkins = storedJenkinsConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds}
	runtimeSettings.kubernetes = storedKubernetesConfig{TimeoutSeconds: defaultIntegrationTimeoutSeconds, Clusters: []storedKubernetesCluster{}}
	runtimeSettings.jenkinsError = ""
	runtimeSettings.kubernetesError = ""
	runtimeSettings.jenkinsRevision = 0
	runtimeSettings.k8sRevision = 0
	runtimeSettings.jenkinsAppliedRevision = 0
	runtimeSettings.k8sAppliedRevision = 0
	runtimeSettings.Unlock()
	providerSettingsStore = store
	jenkins.Disable()
	k8s.Disable()
	t.Cleanup(func() {
		providerSettingsStore = previousStore
		if previousJenkins == nil {
			jenkins.Disable()
		} else {
			jenkins.Activate(previousJenkins)
		}
		k8s.Disable()
		runtimeSettings.Lock()
		runtimeSettings.cipher = previousCipher
		runtimeSettings.jenkins = previousJenkinsConfig
		runtimeSettings.kubernetes = previousKubernetesConfig
		runtimeSettings.jenkinsError = previousJenkinsError
		runtimeSettings.kubernetesError = previousKubernetesError
		runtimeSettings.jenkinsRevision = previousJenkinsRevision
		runtimeSettings.k8sRevision = previousKubernetesRevision
		runtimeSettings.jenkinsAppliedRevision = previousJenkinsAppliedRevision
		runtimeSettings.k8sAppliedRevision = previousKubernetesAppliedRevision
		runtimeSettings.Unlock()
	})
	return cipher
}

func TestConcurrentUpdatesHaveOneCASWinner(t *testing.T) {
	for _, test := range []struct {
		name            string
		provider        string
		initialRevision uint64
		initialValue    any
		wantRevision    uint64
		update          func(context.Context) error
	}{
		{
			name: "jenkins create", provider: providerJenkins, wantRevision: 1,
			update: func(ctx context.Context) error {
				_, err := UpdateJenkins(ctx, UpdateJenkinsRequest{TimeoutSeconds: 1})
				return err
			},
		},
		{
			name: "kubernetes update", provider: providerKubernetes,
			initialRevision: 4,
			initialValue:    storedKubernetesConfig{TimeoutSeconds: 1, Clusters: []storedKubernetesCluster{}},
			wantRevision:    5,
			update: func(ctx context.Context) error {
				_, err := UpdateKubernetes(ctx, UpdateKubernetesRequest{TimeoutSeconds: 1, Clusters: []UpdateKubernetesClusterRequest{}})
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newMemorySettingsStore()
			if test.initialRevision > 0 {
				store.set(test.provider, test.initialRevision, test.initialValue)
			}
			store.barrierProvider = test.provider
			store.barrierTarget = 2
			store.barrier = make(chan struct{})
			installMemorySettingsStore(t, store)

			start := make(chan struct{})
			results := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func() {
					<-start
					results <- test.update(context.Background())
				}()
			}
			close(start)
			var successes, conflicts int
			for i := 0; i < 2; i++ {
				err := <-results
				switch {
				case err == nil:
					successes++
				case errors.Is(err, ErrSettingsChanged):
					conflicts++
				default:
					t.Fatalf("update error = %v", err)
				}
			}
			if successes != 1 || conflicts != 1 {
				t.Fatalf("successes/conflicts = %d/%d, want 1/1", successes, conflicts)
			}
			if got := store.row(test.provider).revision; got != test.wantRevision {
				t.Fatalf("database revision = %d, want %d", got, test.wantRevision)
			}
		})
	}
}

func TestEnsureJenkinsCurrentDiscardsSlowOldProbe(t *testing.T) {
	store := newMemorySettingsStore()
	cipher := installMemorySettingsStore(t, store)

	oldProbeStarted := make(chan struct{})
	releaseOldProbe := make(chan struct{})
	var signalOnce sync.Once
	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		signalOnce.Do(func() { close(oldProbeStarted) })
		<-releaseOldProbe
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"NORMAL"}`))
	}))
	defer oldServer.Close()
	newServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"NORMAL"}`))
	}))
	defer newServer.Close()

	oldConfig := encryptedJenkinsConfig(t, cipher, oldServer.URL, "old-token")
	newConfig := encryptedJenkinsConfig(t, cipher, newServer.URL, "new-token")
	store.set(providerJenkins, 1, oldConfig)

	done := make(chan error, 1)
	go func() { done <- EnsureJenkinsCurrent(context.Background()) }()
	select {
	case <-oldProbeStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("old Jenkins probe did not start")
	}
	store.set(providerJenkins, 2, newConfig)
	close(releaseOldProbe)
	if err := <-done; err != nil {
		t.Fatalf("EnsureJenkinsCurrent() error = %v", err)
	}

	current := jenkins.Current()
	if current == nil || current.Config.Address != newServer.URL {
		t.Fatalf("active Jenkins address = %#v, want %q", current, newServer.URL)
	}
	runtimeSettings.RLock()
	desired, applied := runtimeSettings.jenkinsRevision, runtimeSettings.jenkinsAppliedRevision
	runtimeSettings.RUnlock()
	if desired != 2 || applied != 2 {
		t.Fatalf("desired/applied revisions = %d/%d, want 2/2", desired, applied)
	}
}

func TestInitializeLoadsDatabaseRevisions(t *testing.T) {
	store := newMemorySettingsStore()
	installMemorySettingsStore(t, store)
	store.set(providerJenkins, 11, storedJenkinsConfig{TimeoutSeconds: 1})
	store.set(providerKubernetes, 12, storedKubernetesConfig{TimeoutSeconds: 1, Clusters: []storedKubernetesCluster{}})

	if err := Initialize(testEncryptionKey); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	runtimeSettings.RLock()
	jenkinsDesired, jenkinsApplied := runtimeSettings.jenkinsRevision, runtimeSettings.jenkinsAppliedRevision
	kubernetesDesired, kubernetesApplied := runtimeSettings.k8sRevision, runtimeSettings.k8sAppliedRevision
	runtimeSettings.RUnlock()
	if jenkinsDesired != 11 || jenkinsApplied != 0 || kubernetesDesired != 12 || kubernetesApplied != 0 {
		t.Fatalf("initialized desired/applied revisions = Jenkins %d/%d, Kubernetes %d/%d", jenkinsDesired, jenkinsApplied, kubernetesDesired, kubernetesApplied)
	}
	if err := EnsureJenkinsCurrent(context.Background()); err != nil {
		t.Fatalf("EnsureJenkinsCurrent() error = %v", err)
	}
	if err := EnsureKubernetesCurrent(context.Background()); err != nil {
		t.Fatalf("EnsureKubernetesCurrent() error = %v", err)
	}
	runtimeSettings.RLock()
	jenkinsApplied = runtimeSettings.jenkinsAppliedRevision
	kubernetesApplied = runtimeSettings.k8sAppliedRevision
	runtimeSettings.RUnlock()
	if jenkinsApplied != 11 || kubernetesApplied != 12 {
		t.Fatalf("applied revisions after first managed sync = Jenkins %d, Kubernetes %d", jenkinsApplied, kubernetesApplied)
	}
}

func TestRunSynchronizerStopsDuringProviderProbe(t *testing.T) {
	store := newMemorySettingsStore()
	cipher := installMemorySettingsStore(t, store)
	requestStarted := make(chan struct{})
	var signalOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		signalOnce.Do(func() { close(requestStarted) })
		<-request.Context().Done()
	}))
	defer server.Close()
	store.set(providerJenkins, 1, encryptedJenkinsConfig(t, cipher, server.URL, "cancel-token"))
	store.set(providerKubernetes, 1, storedKubernetesConfig{TimeoutSeconds: 1, Clusters: []storedKubernetesCluster{}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunSynchronizer(ctx, time.Hour)
		close(done)
	}()
	select {
	case <-requestStarted:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("Jenkins synchronization probe did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunSynchronizer did not stop after context cancellation")
	}
}

func TestDisabledSynchronizerSettingsDoNotValidateOrDial(t *testing.T) {
	store := newMemorySettingsStore()
	installMemorySettingsStore(t, store)
	// A disabled setting with deliberately invalid encrypted material proves the
	// synchronizer did not decrypt, validate, or contact an external endpoint.
	store.set(providerJenkins, 8, storedJenkinsConfig{
		Enabled: false, Address: "https://must-not-be-contacted.invalid", Username: "user", TimeoutSeconds: 1, TokenCiphertext: "not-ciphertext",
	})
	store.set(providerKubernetes, 9, storedKubernetesConfig{
		Enabled: false, TimeoutSeconds: 1, Clusters: []storedKubernetesCluster{{
			Environment: "production", Name: "invalid", KubeconfigCiphertext: "not-ciphertext",
		}},
	})

	if err := EnsureJenkinsCurrent(context.Background()); err != nil {
		t.Fatalf("EnsureJenkinsCurrent() error = %v", err)
	}
	if err := EnsureKubernetesCurrent(context.Background()); err != nil {
		t.Fatalf("EnsureKubernetesCurrent() error = %v", err)
	}
	if jenkins.IsConfigured() || k8s.IsInitialized() {
		t.Fatal("disabled settings unexpectedly activated an external integration")
	}
}

func TestSettingsUnavailableDoesNotExposeStoreError(t *testing.T) {
	err := settingsUnavailable("load Jenkins settings", errors.New("secret-token-value"))
	if !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("error = %v, want ErrSettingsUnavailable", err)
	}
	if strings.Contains(err.Error(), "secret-token-value") {
		t.Fatalf("error exposed the store payload: %v", err)
	}
}

func TestUpdateJenkinsDrainPolicyAllowsDisabledRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"mode":"NORMAL"}`))
	}))
	defer server.Close()

	t.Run("disabled generation may be enabled", func(t *testing.T) {
		memoryStore := newMemorySettingsStore()
		cipher := installMemorySettingsStore(t, memoryStore)
		current := encryptedJenkinsConfig(t, cipher, server.URL, "recovery-token")
		current.Enabled = false
		memoryStore.set(providerJenkins, 4, current)
		guardedStore := &guardedMemorySettingsStore{
			memorySettingsStore: memoryStore,
			jenkinsError:        fmt.Errorf("%w: stranded work", ErrActiveJenkinsRuns),
		}
		providerSettingsStore = guardedStore

		view, err := UpdateJenkins(context.Background(), UpdateJenkinsRequest{
			Enabled: true, Address: server.URL, Username: current.Username, TimeoutSeconds: 2,
		})
		if err != nil {
			t.Fatalf("enable disabled Jenkins generation: %v", err)
		}
		if !view.Enabled || !view.Connected {
			t.Fatalf("enabled Jenkins view = %#v", view)
		}
		if requireDrain, calls := guardedStore.drainDecision(); requireDrain || calls != 1 {
			t.Fatalf("disabled -> enabled drain/calls = %t/%d, want false/1", requireDrain, calls)
		}
		if revision := memoryStore.row(providerJenkins).revision; revision != 5 {
			t.Fatalf("recovered Jenkins revision = %d, want 5", revision)
		}
	})

	t.Run("enabled generation must drain before shutdown", func(t *testing.T) {
		memoryStore := newMemorySettingsStore()
		cipher := installMemorySettingsStore(t, memoryStore)
		current := encryptedJenkinsConfig(t, cipher, server.URL, "active-token")
		memoryStore.set(providerJenkins, 7, current)
		guardedStore := &guardedMemorySettingsStore{
			memorySettingsStore: memoryStore,
			jenkinsError:        fmt.Errorf("%w: active work", ErrActiveJenkinsRuns),
		}
		providerSettingsStore = guardedStore

		_, err := UpdateJenkins(context.Background(), UpdateJenkinsRequest{
			Enabled: false, Address: server.URL, Username: current.Username, TimeoutSeconds: 2,
		})
		if !errors.Is(err, ErrActiveJenkinsRuns) {
			t.Fatalf("disable enabled Jenkins error = %v, want ErrActiveJenkinsRuns", err)
		}
		if requireDrain, calls := guardedStore.drainDecision(); !requireDrain || calls != 1 {
			t.Fatalf("enabled -> disabled drain/calls = %t/%d, want true/1", requireDrain, calls)
		}
		if revision := memoryStore.row(providerJenkins).revision; revision != 7 {
			t.Fatalf("rejected Jenkins revision = %d, want 7", revision)
		}
	})
}

func TestSaveJenkinsProviderCASPreservesActiveRunSentinel(t *testing.T) {
	store := &guardedMemorySettingsStore{
		memorySettingsStore: newMemorySettingsStore(),
		jenkinsError:        fmt.Errorf("%w: legacy_tasks=1", ErrActiveJenkinsRuns),
	}
	previousStore := providerSettingsStore
	providerSettingsStore = store
	t.Cleanup(func() { providerSettingsStore = previousStore })

	_, err := saveJenkinsProviderCAS(context.Background(), storedJenkinsConfig{
		Enabled: true, Address: "https://jenkins.example.test", Username: "builder", TimeoutSeconds: 1,
	}, providerRecord{}, true)
	if !errors.Is(err, ErrActiveJenkinsRuns) {
		t.Fatalf("error = %v, want ErrActiveJenkinsRuns", err)
	}
	if errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("active-run rejection was sanitized as settings unavailable: %v", err)
	}
}

func encryptedJenkinsConfig(t *testing.T, cipher *secretCipher, address, token string) storedJenkinsConfig {
	t.Helper()
	const username = "build-user"
	ciphertext, err := cipher.encrypt(token, jenkinsCredentialContext(address, username))
	if err != nil {
		t.Fatalf("encrypt Jenkins token: %v", err)
	}
	return storedJenkinsConfig{
		Enabled: true, Address: address, Username: username, TimeoutSeconds: 2, TokenCiphertext: ciphertext,
	}
}
