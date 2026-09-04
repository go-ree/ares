import axios from 'axios';
import api from '@/config/api';
import type { ApiResponse, EnvironmentCatalogItem } from '@/models/application';

const BASE_URL = '/api/v1/environments';
const SYSTEM_BASE_URL = '/api/v1/system/environments';
const DEFAULT_REQUEST_TIMEOUT_MS = 15000;

export interface CreateEnvironmentRequest {
  code: string;
  name: string;
  enabled?: boolean;
  sort_order: number;
}

export interface UpdateEnvironmentRequest {
  name?: string;
  enabled?: boolean;
  sort_order?: number;
}

const unwrap = <T>(response: ApiResponse<T>): T => {
  if (response.code !== 1) {
    throw new Error(response.error || response.message || '请求失败');
  }
  return response.result;
};

export const getEnvironments = () => api.get<ApiResponse<EnvironmentCatalogItem[]>>(BASE_URL);

export const getSystemEnvironments = async () => {
  const response = await api.get<ApiResponse<EnvironmentCatalogItem[]>>(SYSTEM_BASE_URL, {
    timeout: DEFAULT_REQUEST_TIMEOUT_MS,
  });
  return unwrap(response.data);
};

export const createSystemEnvironment = async (payload: CreateEnvironmentRequest) => {
  const response = await api.post<ApiResponse<EnvironmentCatalogItem>>(SYSTEM_BASE_URL, payload, {
    timeout: DEFAULT_REQUEST_TIMEOUT_MS,
  });
  return unwrap(response.data);
};

export const updateSystemEnvironment = async (code: string, payload: UpdateEnvironmentRequest) => {
  const response = await api.patch<ApiResponse<EnvironmentCatalogItem>>(
    `${SYSTEM_BASE_URL}/${encodeURIComponent(code)}`,
    payload,
    { timeout: DEFAULT_REQUEST_TIMEOUT_MS }
  );
  return unwrap(response.data);
};

export const getEnvironmentApiErrorMessage = (error: unknown): string => {
  if (axios.isAxiosError<ApiResponse<unknown>>(error)) {
    const response = error.response?.data;
    return response?.error || response?.message || error.message || '请求失败';
  }
  return error instanceof Error ? error.message : '请求失败';
};
