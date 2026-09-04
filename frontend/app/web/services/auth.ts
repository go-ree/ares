import axios from 'axios';
import api from '@/config/api';
import type {
  ApiEnvelope,
  AuthOptions,
  BootstrapRequest,
  ChangePasswordRequest,
  LoginRequest,
  SessionSnapshot,
} from '@/types/auth';
import { normalizeReturnTo } from '@/utils/return-to';

const AUTH_BASE_URL = '/api/v1/auth';

export const getAuthOptions = () =>
  api.get<ApiEnvelope<AuthOptions>>(`${AUTH_BASE_URL}/options`, {
    skipAuthHandling: true,
  });

export const getSession = () =>
  api.get<ApiEnvelope<SessionSnapshot>>(`${AUTH_BASE_URL}/session`, {
    skipAuthHandling: true,
  });

export const login = (request: LoginRequest) =>
  api.post<ApiEnvelope<SessionSnapshot>>(`${AUTH_BASE_URL}/login`, request, {
    skipAuthHandling: true,
    skipCsrf: true,
  });

export const bootstrap = (request: BootstrapRequest) =>
  api.post<ApiEnvelope<SessionSnapshot>>(`${AUTH_BASE_URL}/bootstrap`, request, {
    skipAuthHandling: true,
    skipCsrf: true,
  });

export const logout = () => api.post<ApiEnvelope<null>>(`${AUTH_BASE_URL}/logout`);

export const changePassword = (request: ChangePasswordRequest) =>
  api.post<ApiEnvelope<null>>(`${AUTH_BASE_URL}/password`, request);

export const getPasswordChangeErrorMessage = (error: unknown) => {
  if (axios.isAxiosError<ApiEnvelope<unknown>>(error)) {
    if (error.response?.status === 401) return '会话已失效，请重新登录';
    if (error.response?.status === 400 || error.response?.status === 422) {
      const detail = error.response.data?.error;
      if (typeof detail === 'string' && detail) return detail;
    }
  }
  return '密码修改失败，请稍后重试';
};

export const oidcStartURL = (returnTo: unknown) =>
  `${AUTH_BASE_URL}/oidc/start?return_to=${encodeURIComponent(normalizeReturnTo(returnTo))}`;
