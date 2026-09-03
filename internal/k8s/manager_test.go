package k8s

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestBuildManagerHonorsCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	config := testKubeconfig()
	config.Clusters["cluster"].Server = server.URL
	content, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatalf("encode kubeconfig: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err = BuildManager(ctx, []ClusterConfig{{Name: "cluster", Environment: Environment("dev"), Kubeconfig: content, Timeout: time.Minute}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("BuildManager() error = %v, want context cancellation", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("BuildManager() returned after %s, want prompt cancellation", elapsed)
	}
}

func TestBuildManagerRejectsInvalidVersionResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	t.Cleanup(server.Close)

	config := testKubeconfig()
	config.Clusters["cluster"].Server = server.URL
	content, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatalf("encode kubeconfig: %v", err)
	}

	_, err = BuildManager(context.Background(), []ClusterConfig{{
		Name:        "cluster",
		Environment: Environment("dev"),
		Kubeconfig:  content,
		Timeout:     time.Second,
	}})
	if err == nil {
		t.Fatal("BuildManager() accepted an invalid Kubernetes version response")
	}
	if !strings.Contains(err.Error(), "parse Kubernetes server version") {
		t.Fatalf("BuildManager() error = %q, want a version parsing error", err)
	}
}

func TestBuildManagerAcceptsDynamicEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","minor":"30","gitVersion":"v1.30.0"}`))
	}))
	t.Cleanup(server.Close)

	config := testKubeconfig()
	config.Clusters["cluster"].Server = server.URL
	content, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatalf("encode kubeconfig: %v", err)
	}

	manager, err := BuildManager(context.Background(), []ClusterConfig{{
		Name:        "qa-cluster",
		Environment: Environment("QA-CN"),
		Kubeconfig:  content,
		Timeout:     time.Second,
	}})
	if err != nil {
		t.Fatalf("BuildManager() error = %v", err)
	}
	if _, ok := manager.clients[Environment("qa-cn")]["qa-cluster"]; !ok {
		t.Fatalf("dynamic environment was not normalized and registered: %#v", manager.clients)
	}
	if _, exists := manager.clients[Environment("dev")]; exists {
		t.Fatalf("BuildManager() preallocated legacy environment: %#v", manager.clients)
	}
}

func TestParseEnvironment(t *testing.T) {
	got, err := ParseEnvironment(" Prod-Blue ")
	if err != nil {
		t.Fatalf("ParseEnvironment() error = %v", err)
	}
	if got != Environment("prod-blue") {
		t.Fatalf("ParseEnvironment() = %q, want prod-blue", got)
	}
	if _, err := ParseEnvironment("bad env"); err == nil {
		t.Fatal("ParseEnvironment() accepted whitespace")
	}
}

func TestValidateKubeconfigRejectsUnsafeCredentialSources(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*clientcmdapi.Cluster, *clientcmdapi.AuthInfo)
		want   string
	}{
		{
			name: "exec credential plugin",
			mutate: func(_ *clientcmdapi.Cluster, authInfo *clientcmdapi.AuthInfo) {
				authInfo.Exec = &clientcmdapi.ExecConfig{
					APIVersion: "client.authentication.k8s.io/v1",
					Command:    "/bin/sh",
				}
			},
			want: "forbidden exec credential plugin",
		},
		{
			name: "auth provider plugin",
			mutate: func(_ *clientcmdapi.Cluster, authInfo *clientcmdapi.AuthInfo) {
				authInfo.AuthProvider = &clientcmdapi.AuthProviderConfig{Name: "gcp"}
			},
			want: "forbidden auth-provider plugin",
		},
		{
			name: "certificate authority file",
			mutate: func(cluster *clientcmdapi.Cluster, _ *clientcmdapi.AuthInfo) {
				cluster.CertificateAuthority = "/etc/ares/cluster-ca.pem"
			},
			want: "embed certificate-authority-data",
		},
		{
			name: "client certificate file",
			mutate: func(_ *clientcmdapi.Cluster, authInfo *clientcmdapi.AuthInfo) {
				authInfo.ClientCertificate = "/etc/ares/client.pem"
			},
			want: "embed credentials instead of referencing files",
		},
		{
			name: "client key file",
			mutate: func(_ *clientcmdapi.Cluster, authInfo *clientcmdapi.AuthInfo) {
				authInfo.ClientKey = "/etc/ares/client-key.pem"
			},
			want: "embed credentials instead of referencing files",
		},
		{
			name: "token file",
			mutate: func(_ *clientcmdapi.Cluster, authInfo *clientcmdapi.AuthInfo) {
				authInfo.TokenFile = "/var/run/secrets/service-account-token"
			},
			want: "embed credentials instead of referencing files",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := testKubeconfig()
			test.mutate(config.Clusters["cluster"], config.AuthInfos["user"])
			content, err := clientcmd.Write(*config)
			if err != nil {
				t.Fatalf("encode kubeconfig: %v", err)
			}

			err = ValidateKubeconfig(content)
			if err == nil {
				t.Fatal("validateKubeconfig() accepted an unsafe kubeconfig")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateKubeconfig() error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestValidateKubeconfigAcceptsEmbeddedCredentials(t *testing.T) {
	config := testKubeconfig()
	config.Clusters["cluster"].CertificateAuthorityData = []byte("embedded-ca-data")
	config.AuthInfos["user"].ClientCertificateData = []byte("embedded-client-certificate")
	config.AuthInfos["user"].ClientKeyData = []byte("embedded-client-key")
	config.AuthInfos["user"].Token = "embedded-token"

	content, err := clientcmd.Write(*config)
	if err != nil {
		t.Fatalf("encode kubeconfig: %v", err)
	}
	if err := ValidateKubeconfig(content); err != nil {
		t.Fatalf("validateKubeconfig() rejected embedded credentials: %v", err)
	}
}

func TestRegistryConcurrentActivationAndReads(t *testing.T) {
	registry.RLock()
	original := registry.manager
	registry.RUnlock()
	t.Cleanup(func() { ActivateManager(original) })

	managers := []*ClientManager{
		testClientManager("cluster-a"),
		testClientManager("cluster-b"),
	}
	ActivateManager(managers[0])

	const iterations = 5000
	errors := make(chan string, 1)
	var waitGroup sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		worker := worker
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				if worker%2 == 0 {
					ActivateManager(managers[iteration%len(managers)])
					continue
				}
				_ = IsInitialized()
				_ = ListClusters(Environment("dev"))
				if DefaultClient(Environment("dev")) == nil {
					select {
					case errors <- "DefaultClient returned nil while a manager was always active":
					default:
					}
					return
				}
				_, _ = GetClient(Environment("dev"), fmt.Sprintf("cluster-%c", 'a'+rune(iteration%2)))
			}
		}()
	}
	waitGroup.Wait()
	select {
	case message := <-errors:
		t.Fatal(message)
	default:
	}

	if !IsInitialized() {
		t.Fatal("registry unexpectedly became uninitialized")
	}
}

func TestRegistryDisabledState(t *testing.T) {
	registry.RLock()
	original := registry.manager
	registry.RUnlock()
	t.Cleanup(func() { ActivateManager(original) })

	Disable()
	if IsInitialized() {
		t.Fatal("IsInitialized() = true after Disable()")
	}
	if clusters := ListClusters(Environment("dev")); clusters != nil {
		t.Fatalf("ListClusters() = %v after Disable(), want nil", clusters)
	}
	if client := DefaultClient(Environment("dev")); client != nil {
		t.Fatalf("DefaultClient() = %#v after Disable(), want nil", client)
	}
	if _, err := GetClient(Environment("dev"), "cluster-a"); err == nil {
		t.Fatal("GetClient() unexpectedly succeeded after Disable()")
	}
}

func TestListEnvironmentsUsesRuntimeRegistry(t *testing.T) {
	registry.RLock()
	original := registry.manager
	registry.RUnlock()
	t.Cleanup(func() { ActivateManager(original) })

	ActivateManager(&ClientManager{clients: map[Environment]map[string]*kubernetes.Clientset{
		Environment("prod-blue"): {},
		Environment("qa-cn"):     {},
	}})

	got := ListEnvironments()
	want := []Environment{"prod-blue", "qa-cn"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ListEnvironments() = %v, want %v", got, want)
	}
}

func testClientManager(name string) *ClientManager {
	return &ClientManager{clients: map[Environment]map[string]*kubernetes.Clientset{
		Environment("dev"):  {name: {}},
		Environment("test"): {},
		Environment("moni"): {},
	}}
}

func testKubeconfig() *clientcmdapi.Config {
	return &clientcmdapi.Config{
		APIVersion: "v1",
		Kind:       "Config",
		Clusters: map[string]*clientcmdapi.Cluster{
			"cluster": {Server: "https://kubernetes.example.test"},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{
			"user": {},
		},
		Contexts: map[string]*clientcmdapi.Context{
			"context": {Cluster: "cluster", AuthInfo: "user"},
		},
		CurrentContext: "context",
	}
}
