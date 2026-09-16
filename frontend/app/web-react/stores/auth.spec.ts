import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as authService from '@shared/services/auth';
import { PERMISSIONS, type SessionSnapshot } from '@shared/types/auth';
import { can, canAny, isAuthenticated, useAuthStore } from './auth';

vi.mock('@shared/services/auth', () => ({
  getAuthOptions: vi.fn(),
  getSession: vi.fn(),
  login: vi.fn(),
  bootstrap: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
}));

const session: SessionSnapshot = {
  user: {
    id: '42',
    username: 'alice',
    display_name: 'Alice',
    email: 'alice@example.com',
    auth_source: 'oidc',
    roles: ['developer'],
    permissions: [PERMISSIONS.APPLICATIONS_READ, PERMISSIONS.RELEASES_CREATE],
  },
  expires_at: '2099-01-01T00:00:00Z',
  csrf_token: 'csrf-token',
};

const response = <T>(result: T) =>
  Promise.resolve({ data: { code: 1, message: 'ok', result } } as never);

const authFailure = (status: number) =>
  Object.assign(new Error('auth failed'), { isAxiosError: true, response: { status } });

describe('auth store (React)', () => {
  beforeEach(() => {
    useAuthStore.getState().reset();
    vi.mocked(authService.getSession).mockReset();
  });

  it('deduplicates concurrent session probes and exposes the identity outside React', async () => {
    let resolveSession!: (value: never) => void;
    vi.mocked(authService.getSession).mockReturnValue(
      new Promise(resolve => {
        resolveSession = resolve as never;
      }) as never
    );

    const store = useAuthStore.getState();
    const first = store.ensureSession();
    const second = store.ensureSession();
    expect(authService.getSession).toHaveBeenCalledOnce();
    resolveSession((await response(session)) as never);

    await expect(Promise.all([first, second])).resolves.toEqual([true, true]);
    expect(useAuthStore.getState().csrfToken).toBe('csrf-token');
    expect(isAuthenticated()).toBe(true);
    expect(can(PERMISSIONS.RELEASES_CREATE)).toBe(true);
    expect(canAny([PERMISSIONS.USERS_WRITE, PERMISSIONS.RELEASES_CREATE])).toBe(true);
    expect(localStorage.length).toBe(0);
  });

  it('turns a 401 into an anonymous session with an explicit invalidation reason', async () => {
    vi.mocked(authService.getSession).mockReturnValueOnce(response(session));
    await useAuthStore.getState().ensureSession();
    expect(isAuthenticated()).toBe(true);

    vi.mocked(authService.getSession).mockRejectedValueOnce(authFailure(401));
    await expect(useAuthStore.getState().refreshSession()).resolves.toBe(false);
    expect(isAuthenticated()).toBe(false);
    expect(useAuthStore.getState().status).toBe('anonymous');
    expect(useAuthStore.getState().invalidationReason).toBe('unauthenticated');
    expect(useAuthStore.getState().user).toBeNull();
  });

  it('keeps a confirmed identity when a background probe is forbidden', async () => {
    vi.mocked(authService.getSession).mockReturnValueOnce(response(session));
    await useAuthStore.getState().ensureSession();

    // A 403 is an authorization decision, not proof that the session died.
    vi.mocked(authService.getSession).mockRejectedValueOnce(authFailure(403));
    await expect(useAuthStore.getState().refreshSession()).resolves.toBe(false);
    expect(isAuthenticated()).toBe(true);
    expect(useAuthStore.getState().status).toBe('authenticated');

    // Without a confirmed identity the same 403 leaves the caller anonymous.
    useAuthStore.getState().reset();
    vi.mocked(authService.getSession).mockRejectedValueOnce(authFailure(403));
    await expect(useAuthStore.getState().ensureSession()).resolves.toBe(false);
    expect(useAuthStore.getState().status).toBe('anonymous');
    expect(useAuthStore.getState().invalidationReason).toBe('unauthenticated');
  });

  it('never lets a stale probe restore an identity that was invalidated meanwhile', async () => {
    let resolveSession!: (value: never) => void;
    vi.mocked(authService.getSession).mockReturnValue(
      new Promise(resolve => {
        resolveSession = resolve as never;
      }) as never
    );

    const inFlight = useAuthStore.getState().ensureSession();
    // A 401 from any other request, or an explicit logout, bumps the generation.
    useAuthStore.getState().invalidate('unauthenticated');
    resolveSession((await response(session)) as never);

    await expect(inFlight).resolves.toBe(false);
    expect(isAuthenticated()).toBe(false);
    expect(useAuthStore.getState().user).toBeNull();
  });

  it('keeps the previous status and records a message when the probe fails unexpectedly', async () => {
    vi.mocked(authService.getSession).mockRejectedValueOnce(new Error('网络不可用'));
    await expect(useAuthStore.getState().ensureSession()).resolves.toBe(false);
    expect(useAuthStore.getState().status).toBe('anonymous');
    expect(useAuthStore.getState().initializationError).toBe('网络不可用');

    vi.mocked(authService.getSession).mockReturnValueOnce(response(session));
    await useAuthStore.getState().ensureSession();
    vi.mocked(authService.getSession).mockRejectedValueOnce(new Error('网络不可用'));
    // A transport failure is not proof that the session died: the previously
    // confirmed identity stays usable and the probe reports it as authenticated.
    await expect(useAuthStore.getState().refreshSession()).resolves.toBe(true);
    expect(useAuthStore.getState().status).toBe('authenticated');
    expect(useAuthStore.getState().initializationError).toBe('网络不可用');
    expect(isAuthenticated()).toBe(true);
  });

  it('expires a session whose server deadline has already passed', async () => {
    vi.mocked(authService.getSession).mockReturnValueOnce(
      response({ ...session, expires_at: '2000-01-01T00:00:00Z' } as never) as never
    );
    await useAuthStore.getState().ensureSession();
    expect(useAuthStore.getState().status).toBe('anonymous');
    expect(useAuthStore.getState().invalidationReason).toBe('session_expired');
  });

  it('clears the identity on password rotation and tolerates an expired logout', async () => {
    vi.mocked(authService.getSession).mockReturnValueOnce(response(session));
    await useAuthStore.getState().ensureSession();
    vi.mocked(authService.changePassword).mockResolvedValueOnce(undefined as never);
    await useAuthStore.getState().changePassword({
      current_password: 'old-password',
      new_password: 'new-password',
    });
    expect(useAuthStore.getState().invalidationReason).toBe('password_changed');
    expect(isAuthenticated()).toBe(false);

    vi.mocked(authService.getSession).mockReturnValueOnce(response(session));
    await useAuthStore.getState().ensureSession();
    vi.mocked(authService.logout).mockRejectedValueOnce(authFailure(401));
    await expect(useAuthStore.getState().logout()).resolves.toBeUndefined();
    expect(useAuthStore.getState().invalidationReason).toBe('logout');
  });
});
