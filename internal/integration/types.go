package integration

type Snapshot struct {
	Jenkins    JenkinsView    `json:"jenkins"`
	Kubernetes KubernetesView `json:"kubernetes"`
}

type JenkinsView struct {
	Enabled                   bool   `json:"enabled"`
	Address                   string `json:"address"`
	Username                  string `json:"username"`
	TimeoutSeconds            int    `json:"timeout_seconds"`
	TokenConfigured           bool   `json:"token_configured"`
	CredentialReentryRequired bool   `json:"credential_reentry_required"`
	Connected                 bool   `json:"connected"`
	LastError                 string `json:"last_error"`
}

type KubernetesView struct {
	Enabled        bool                    `json:"enabled"`
	TimeoutSeconds int                     `json:"timeout_seconds"`
	Connected      bool                    `json:"connected"`
	LastError      string                  `json:"last_error"`
	Clusters       []KubernetesClusterView `json:"clusters"`
}

type KubernetesClusterView struct {
	Environment               string `json:"environment"`
	Name                      string `json:"name"`
	Description               string `json:"description"`
	KubeconfigConfigured      bool   `json:"kubeconfig_configured"`
	CredentialReentryRequired bool   `json:"credential_reentry_required"`
}

type UpdateJenkinsRequest struct {
	Enabled        bool    `json:"enabled"`
	Address        string  `json:"address"`
	Username       string  `json:"username"`
	TimeoutSeconds int     `json:"timeout_seconds"`
	Token          *string `json:"token"`
}

type UpdateKubernetesRequest struct {
	Enabled        bool                             `json:"enabled"`
	TimeoutSeconds int                              `json:"timeout_seconds"`
	Clusters       []UpdateKubernetesClusterRequest `json:"clusters"`
}

type UpdateKubernetesClusterRequest struct {
	Environment string  `json:"environment"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Kubeconfig  *string `json:"kubeconfig"`
}

type storedJenkinsConfig struct {
	Enabled         bool   `json:"enabled"`
	Address         string `json:"address"`
	Username        string `json:"username"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	TokenCiphertext string `json:"token_ciphertext,omitempty"`
}

type storedKubernetesConfig struct {
	Enabled        bool                      `json:"enabled"`
	TimeoutSeconds int                       `json:"timeout_seconds"`
	Clusters       []storedKubernetesCluster `json:"clusters"`
}

type storedKubernetesCluster struct {
	Environment          string `json:"environment"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	KubeconfigCiphertext string `json:"kubeconfig_ciphertext,omitempty"`
}
