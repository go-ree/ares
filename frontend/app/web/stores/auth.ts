import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import axios from 'axios';
import {
  bootstrap as bootstrapRequest,
  changePassword as changePasswordRequest,
  getAuthOptions,
  getSession,
  login as loginRequest,
  logout as logoutRequest,
} from '@/services/auth';
import type {
  AuthOptions,
  AuthStatus,
  AuthUser,
  BootstrapRequest,
  ChangePasswordRequest,
  LoginRequest,
  Permission,
  SessionSnapshot,
} from '@/types/auth';

const isSessionSnapshot = (value: unknown): value is SessionSnapshot => {
  if (!value || typeof value !== 'object') return false;
  const snapshot = value as Partial<SessionSnapshot>;
  const user = snapshot.user as Partial<AuthUser> | undefined;
  return Boolean(
    user &&
      typeof user.id === 'string' &&
      typeof user.username === 'string' &&
      typeof user.display_name === 'string' &&
      Array.isArray(user.roles) &&
      Array.isArray(user.permissions) &&
      typeof snapshot.expires_at === 'string' &&
      typeof snapshot.csrf_token === 'string' &&
      snapshot.csrf_token
  );
};

export const useAuthStore = defineStore('auth', () => {
  const status = ref<AuthStatus>('unknown');
  const user = ref<AuthUser | null>(null);
  const expiresAt = ref<string | null>(null);
  const csrfToken = ref<string | null>(null);
  const options = ref<AuthOptions | null>(null);
  const initializationError = ref<string | null>(null);
  const invalidationReason = ref<string | null>(null);

  let sessionFlight: Promise<boolean> | null = null;
  let sessionGeneration = 0;
  let expirationTimer: ReturnType<typeof setTimeout> | null = null;

  const isAuthenticated = computed(() => status.value === 'authenticated' && Boolean(user.value));
  const permissions = computed<Permission[]>(() => user.value?.permissions || []);
  const can = (permission: Permission) => permissions.value.includes(permission);
  const canAny = (required: Permission[]) => required.some(can);

  const clearExpirationTimer = () => {
    if (expirationTimer) clearTimeout(expirationTimer);
    expirationTimer = null;
  };

  const clearSession = (reason: string | null = null) => {
    sessionGeneration += 1;
    // A request that started before logout, password rotation, or a 401 must
    // never be allowed to restore the old in-memory identity when it finishes.
    // Detach it here; its generation check below will discard the late result.
    sessionFlight = null;
    clearExpirationTimer();
    user.value = null;
    expiresAt.value = null;
    csrfToken.value = null;
    status.value = 'anonymous';
    invalidationReason.value = reason;
  };

  const scheduleExpiration = (expiresAtValue: string) => {
    clearExpirationTimer();
    const expirationTime = new Date(expiresAtValue).getTime();
    if (!Number.isFinite(expirationTime)) {
      clearSession('session_expired');
      return;
    }
    const armTimer = () => {
      const remaining = expirationTime - Date.now();
      if (remaining <= 0) {
        clearSession('session_expired');
        return;
      }
      expirationTimer = setTimeout(armTimer, Math.min(remaining, 2_147_483_647));
    };
    armTimer();
  };

  const applySession = (snapshot: unknown) => {
    if (!isSessionSnapshot(snapshot)) throw new Error('服务端返回了无效的会话信息');
    user.value = snapshot.user;
    expiresAt.value = snapshot.expires_at;
    csrfToken.value = snapshot.csrf_token;
    status.value = 'authenticated';
    initializationError.value = null;
    invalidationReason.value = null;
    scheduleExpiration(snapshot.expires_at);
  };

  const fetchSession = async (force: boolean) => {
    if (sessionFlight) return sessionFlight;
    if (!force && isAuthenticated.value) return true;

    const previousStatus = status.value;
    const requestGeneration = sessionGeneration;
    // Keep a confirmed identity usable while a background revalidation is in
    // flight. `loading` is reserved for probes that do not yet have an identity.
    if (previousStatus !== 'authenticated') status.value = 'loading';

    const flight = (async () => {
      try {
        const response = await getSession();
        if (requestGeneration !== sessionGeneration) return isAuthenticated.value;
        applySession(response.data.result);
        return true;
      } catch (error) {
        if (requestGeneration !== sessionGeneration) return isAuthenticated.value;
        if (
          axios.isAxiosError(error) &&
          (error.response?.status === 401 || error.response?.status === 403)
        ) {
          clearSession('unauthenticated');
          initializationError.value = null;
          return false;
        }

        status.value = previousStatus === 'authenticated' ? 'authenticated' : 'anonymous';
        initializationError.value = error instanceof Error ? error.message : '无法确认登录状态';
        return isAuthenticated.value;
      } finally {
        // Generation changes are the only path that detaches/replaces a flight.
        // A stale request must not clear the newer generation's shared promise.
        if (requestGeneration === sessionGeneration) sessionFlight = null;
      }
    })();
    sessionFlight = flight;
    return flight;
  };

  const ensureSession = () => fetchSession(false);
  const refreshSession = () => fetchSession(true);

  const loadOptions = async () => {
    const response = await getAuthOptions();
    options.value = response.data.result;
    return options.value;
  };

  const login = async (request: LoginRequest) => {
    await loginRequest(request);
    clearSession();
    return refreshSession();
  };

  const bootstrap = async (request: BootstrapRequest) => {
    await bootstrapRequest(request);
    clearSession();
    const authenticated = await refreshSession();
    if (options.value) options.value.bootstrap_available = false;
    return authenticated;
  };

  const logout = async () => {
    try {
      await logoutRequest();
      clearSession('logout');
    } catch (error) {
      if (axios.isAxiosError(error) && error.response?.status === 401) {
        clearSession('logout');
        return;
      }
      throw error;
    }
  };

  const changePassword = async (request: ChangePasswordRequest) => {
    await changePasswordRequest(request);
    clearSession('password_changed');
  };

  const invalidate = (reason = 'unauthenticated') => {
    clearSession(reason);
  };

  const reset = () => {
    sessionGeneration += 1;
    clearExpirationTimer();
    status.value = 'unknown';
    user.value = null;
    expiresAt.value = null;
    csrfToken.value = null;
    options.value = null;
    initializationError.value = null;
    invalidationReason.value = null;
    sessionFlight = null;
  };

  return {
    status,
    user,
    expiresAt,
    csrfToken,
    options,
    initializationError,
    invalidationReason,
    isAuthenticated,
    permissions,
    can,
    canAny,
    ensureSession,
    refreshSession,
    loadOptions,
    login,
    bootstrap,
    logout,
    changePassword,
    invalidate,
    reset,
  };
});
