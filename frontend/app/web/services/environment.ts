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

const systemEnvironmentApi = axios.create({
  timeout: DEFAULT_REQUEST_TIMEOUT_MS,
  headers: {
    'Content-Type': 'application/json',
  },
});

const adminHeaders = (adminToken: string) => {
  const token = adminToken.trim();
  if (!token) throw new Error('请输入管理员令牌');
  return { 'X-Ares-Admin-Token': token };
};

const unwrap = <T>(response: ApiResponse<T>): T => {
  if (response.code !== 1) {
    throw new Error(response.error || response.message || '请求失败');
  }
  return response.result;
};

export const getEnvironments = () => api.get<ApiResponse<EnvironmentCatalogItem[]>>(BASE_URL);

export const getSystemEnvironments = async (adminToken: string) => {
  const response = await systemEnvironmentApi.get<ApiResponse<EnvironmentCatalogItem[]>>(
    SYSTEM_BASE_URL,
    { headers: adminHeaders(adminToken) }
  );
  return unwrap(response.data);
};

export const createSystemEnvironment = async (
  adminToken: string,
  payload: CreateEnvironmentRequest
) => {
  const response = await systemEnvironmentApi.post<ApiResponse<EnvironmentCatalogItem>>(
    SYSTEM_BASE_URL,
    payload,
    { headers: adminHeaders(adminToken) }
  );
  return unwrap(response.data);
};

export const updateSystemEnvironment = async (
  adminToken: string,
  code: string,
  payload: UpdateEnvironmentRequest
) => {
  const response = await systemEnvironmentApi.patch<ApiResponse<EnvironmentCatalogItem>>(
    `${SYSTEM_BASE_URL}/${encodeURIComponent(code)}`,
    payload,
    { headers: adminHeaders(adminToken) }
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
