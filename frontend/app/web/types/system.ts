export type KubernetesEnvironment = string;

export interface SystemApiResponse<T> {
  code: number;
  message: string;
  result: T;
  error: unknown;
  help: string;
}

export interface JenkinsIntegrationSettings {
  enabled: boolean;
  address: string;
  username: string;
  timeout_seconds: number;
  token_configured: boolean;
  connected: boolean;
  last_error: string;
}

export interface KubernetesClusterSettings {
  environment: KubernetesEnvironment;
  name: string;
  description: string;
  kubeconfig_configured: boolean;
}

export interface KubernetesIntegrationSettings {
  enabled: boolean;
  timeout_seconds: number;
  connected: boolean;
  last_error: string;
  clusters: KubernetesClusterSettings[];
}

export interface IntegrationSettings {
  jenkins: JenkinsIntegrationSettings;
  kubernetes: KubernetesIntegrationSettings;
}

export interface UpdateJenkinsIntegrationRequest {
  enabled: boolean;
  address: string;
  username: string;
  timeout_seconds: number;
  token?: string;
}

export interface UpdateKubernetesClusterRequest {
  environment: KubernetesEnvironment;
  name: string;
  description: string;
  kubeconfig?: string;
}

export interface UpdateKubernetesIntegrationRequest {
  enabled: boolean;
  timeout_seconds: number;
  clusters: UpdateKubernetesClusterRequest[];
}
