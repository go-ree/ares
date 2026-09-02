import axios from 'axios';
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

const systemApi = axios.create({
  timeout: DEFAULT_REQUEST_TIMEOUT_MS,
  headers: {
    'Content-Type': 'application/json',
  },
});

const adminHeaders = (adminToken: string) => {
  const token = adminToken.trim();
  if (!token) {
    throw new Error('请输入管理员令牌');
  }

  return {
    'X-Ares-Admin-Token': token,
  };
};

const unwrap = <T>(response: SystemApiResponse<T>): T => {
  if (response.code !== 1) {
    const details = typeof response.error === 'string' ? response.error : '';
    throw new Error(details || response.message || '请求失败');
  }

  return response.result;
};

const updateRequestTimeout = (timeoutSeconds: number) =>
  Math.max(DEFAULT_REQUEST_TIMEOUT_MS, timeoutSeconds * 1000 + CONNECTION_TIMEOUT_BUFFER_MS);

export const getIntegrationSettings = async (adminToken: string) => {
  const response = await systemApi.get<SystemApiResponse<IntegrationSettings>>(BASE_URL, {
    headers: adminHeaders(adminToken),
  });

  return unwrap(response.data);
};

export const updateJenkinsIntegration = async (
  adminToken: string,
  payload: UpdateJenkinsIntegrationRequest
) => {
  const response = await systemApi.put<SystemApiResponse<JenkinsIntegrationSettings>>(
    `${BASE_URL}/jenkins`,
    payload,
    {
      headers: adminHeaders(adminToken),
      timeout: updateRequestTimeout(payload.timeout_seconds),
    }
  );

  return unwrap(response.data);
};

export const updateKubernetesIntegration = async (
  adminToken: string,
  payload: UpdateKubernetesIntegrationRequest
) => {
  const response = await systemApi.put<SystemApiResponse<KubernetesIntegrationSettings>>(
    `${BASE_URL}/kubernetes`,
    payload,
    {
      headers: adminHeaders(adminToken),
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
