import { create } from 'zustand';
import axios from 'axios';
import {
  bootstrap as bootstrapRequest,
  changePassword as changePasswordRequest,
  getAuthOptions,
  getSession,
  login as loginRequest,
  logout as logoutRequest,
} from '@shared/services/auth';
import type {
  AuthOptions,
  AuthStatus,
  AuthUser,
  BootstrapRequest,
  ChangePasswordRequest,
  LoginRequest,
  Permission,
  SessionSnapshot,
} from '@shared/types/auth';

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

interface AuthState {
  status: AuthStatus;
  user: AuthUser | null;
  expiresAt: string | null;
  csrfToken: string | null;
  options: AuthOptions | null;
  initializationError: string | null;
  invalidationReason: string | null;
  ensureSession: () => Promise<boolean>;
  refreshSession: () => Promise<boolean>;
  loadOptions: () => Promise<AuthOptions>;
  login: (request: LoginRequest) => Promise<boolean>;
  bootstrap: (request: BootstrapRequest) => Promise<boolean>;
  logout: () => Promise<void>;
  changePassword: (request: ChangePasswordRequest) => Promise<void>;
  invalidate: (reason?: string) => void;
  reset: () => void;
}

// Session generation, the shared in-flight promise and the expiration timer stay
// outside the store on purpose: they are coordination state, so updating them
// must never notify React subscribers. This mirrors the Vue implementation,
// where the same values were plain closure variables.
let sessionFlight: Promise<boolean> | null = null;
let sessionGeneration = 0;
let expirationTimer: ReturnType<typeof setTimeout> | null = null;

const initialSnapshot = {
  status: 'unknown' as AuthStatus,
  user: null,
  expiresAt: null,
  csrfToken: null,
  options: null,
  initializationError: null,
  invalidationReason: null,
};

export const useAuthStore = create<AuthState>()((set, get) => {
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
    set({
      user: null,
      expiresAt: null,
      csrfToken: null,
      status: 'anonymous',
      invalidationReason: reason,
    });
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
    set({
      user: snapshot.user,
      expiresAt: snapshot.expires_at,
      csrfToken: snapshot.csrf_token,
      status: 'authenticated',
      initializationError: null,
      invalidationReason: null,
    });
    scheduleExpiration(snapshot.expires_at);
  };

  const fetchSession = async (force: boolean): Promise<boolean> => {
    if (sessionFlight) return sessionFlight;
    if (!force && isAuthenticated()) return true;

    const previousStatus = get().status;
    const requestGeneration = sessionGeneration;
    // Keep a confirmed identity usable while a background revalidation is in
    // flight. `loading` is reserved for probes that do not yet have an identity.
    if (previousStatus !== 'authenticated') set({ status: 'loading' });

    const flight = (async () => {
      try {
        const response = await getSession();
        if (requestGeneration !== sessionGeneration) return isAuthenticated();
        applySession(response.data.result);
        return true;
      } catch (error) {
        if (requestGeneration !== sessionGeneration) return isAuthenticated();
        if (axios.isAxiosError(error) && error.response?.status === 401) {
          clearSession('unauthenticated');
          set({ initializationError: null });
          return false;
        }
        if (axios.isAxiosError(error) && error.response?.status === 403) {
          // A forbidden background probe does not prove that the existing
          // session is invalid. Keep a previously confirmed identity intact so
          // callers can withdraw only the affected capability (for example an
          // SSE log stream) without turning a permission failure into logout.
          if (previousStatus === 'authenticated') {
            set({ status: 'authenticated', initializationError: null });
            return false;
          }
          clearSession('unauthenticated');
          set({ initializationError: null });
          return false;
        }

        set({
          status: previousStatus === 'authenticated' ? 'authenticated' : 'anonymous',
          initializationError: error instanceof Error ? error.message : '无法确认登录状态',
        });
        return isAuthenticated();
      } finally {
        // Generation changes are the only path that detaches/replaces a flight.
        // A stale request must not clear the newer generation's shared promise.
        if (requestGeneration === sessionGeneration) sessionFlight = null;
      }
    })();
    sessionFlight = flight;
    return flight;
  };

  return {
    ...initialSnapshot,
    ensureSession: () => fetchSession(false),
    refreshSession: () => fetchSession(true),
    loadOptions: async () => {
      const response = await getAuthOptions();
      set({ options: response.data.result });
      return response.data.result;
    },
    login: async (request: LoginRequest) => {
      await loginRequest(request);
      clearSession();
      return fetchSession(true);
    },
    bootstrap: async (request: BootstrapRequest) => {
      await bootstrapRequest(request);
      clearSession();
      const authenticated = await fetchSession(true);
      const currentOptions = get().options;
      if (currentOptions) set({ options: { ...currentOptions, bootstrap_available: false } });
      return authenticated;
    },
    logout: async () => {
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
    },
    changePassword: async (request: ChangePasswordRequest) => {
      await changePasswordRequest(request);
      clearSession('password_changed');
    },
    invalidate: (reason = 'unauthenticated') => clearSession(reason),
    reset: () => {
      sessionGeneration += 1;
      clearExpirationTimer();
      sessionFlight = null;
      set({ ...initialSnapshot });
    },
  };
});

/**
 * Reads outside React (route loaders, the API 401 hook) must go through these
 * helpers so they observe the same snapshot the components render.
 */
export const isAuthenticated = (): boolean => {
  const { status, user } = useAuthStore.getState();
  return status === 'authenticated' && Boolean(user);
};

export const permissions = (): Permission[] => useAuthStore.getState().user?.permissions || [];

export const can = (permission: Permission): boolean => permissions().includes(permission);

export const canAny = (required: Permission[]): boolean => required.some(can);
