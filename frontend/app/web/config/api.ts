import axios from 'axios';
import type { AxiosError, AxiosInstance } from 'axios';

const SAFE_METHODS = new Set(['get', 'head', 'options']);

interface ApiAuthHooks {
  getCsrfToken: () => string | null;
  onUnauthorized: (error: AxiosError) => void | Promise<void>;
  onForbidden: (error: AxiosError) => void | Promise<void>;
}

const noAuthHooks: ApiAuthHooks = {
  getCsrfToken: () => null,
  onUnauthorized: () => undefined,
  onForbidden: () => undefined,
};

let authHooks: ApiAuthHooks = noAuthHooks;

export const configureApiAuth = (hooks: ApiAuthHooks) => {
  authHooks = hooks;
};

export const resetApiAuth = () => {
  authHooks = noAuthHooks;
};

const api: AxiosInstance = axios.create({
  timeout: 10000,
  withCredentials: true,
  headers: {
    'Content-Type': 'application/json',
  },
});

api.interceptors.request.use(config => {
  const method = (config.method || 'get').toLowerCase();
  if (!config.skipCsrf && !SAFE_METHODS.has(method)) {
    const csrfToken = authHooks.getCsrfToken();
    if (csrfToken) config.headers.set('X-CSRF-Token', csrfToken);
  }
  return config;
});

api.interceptors.response.use(
  response => response,
  async (error: AxiosError) => {
    if (!error.config?.skipAuthHandling) {
      if (error.response?.status === 401) await authHooks.onUnauthorized(error);
      if (error.response?.status === 403) await authHooks.onForbidden(error);
    }
    return Promise.reject(error);
  }
);

export default api;
