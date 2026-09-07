import axios from 'axios';
import api from '@/config/api';
import type {
  IntegrationSettings,
  JenkinsIntegrationSettings,
  KubernetesIntegrationSettings,
  SystemApiResponse,
  UpdateJenkinsIntegrationRequest,
  UpdateKubernetesIntegrationRequest,
} from '@/types/system';

const BASE_URL = '/api/v1/system/integrations';
const DEFAULT_REQUEST_TIMEOUT_MS = 15000;
const CONNECTION_TIMEOUT_BUFFER_MS = 5000;

const unwrap = <T>(response: SystemApiResponse<T>): T => {
  if (response.code !== 1) {
    const details = typeof response.error === 'string' ? response.error : '';
    throw new Error(details || response.message || '请求失败');
  }

  return response.result;
};

const updateRequestTimeout = (timeoutSeconds: number) =>
  Math.max(DEFAULT_REQUEST_TIMEOUT_MS, timeoutSeconds * 1000 + CONNECTION_TIMEOUT_BUFFER_MS);

export const getIntegrationSettings = async () => {
  const response = await api.get<SystemApiResponse<IntegrationSettings>>(BASE_URL, {
    timeout: DEFAULT_REQUEST_TIMEOUT_MS,
  });

  return unwrap(response.data);
};

export const updateJenkinsIntegration = async (payload: UpdateJenkinsIntegrationRequest) => {
  const response = await api.put<SystemApiResponse<JenkinsIntegrationSettings>>(
    `${BASE_URL}/jenkins`,
    payload,
    {
      timeout: updateRequestTimeout(payload.timeout_seconds),
    }
  );

  return unwrap(response.data);
};

export const updateKubernetesIntegration = async (payload: UpdateKubernetesIntegrationRequest) => {
  const response = await api.put<SystemApiResponse<KubernetesIntegrationSettings>>(
    `${BASE_URL}/kubernetes`,
    payload,
    {
      timeout: updateRequestTimeout(payload.timeout_seconds),
    }
  );

  return unwrap(response.data);
};

export const getSystemApiErrorMessage = (error: unknown): string => {
  if (axios.isAxiosError<SystemApiResponse<unknown>>(error)) {
    const response = error.response?.data;
    const details = typeof response?.error === 'string' ? response.error : '';
    return details || response?.message || error.message || '请求失败';
  }

  return error instanceof Error ? error.message : '请求失败';
};
