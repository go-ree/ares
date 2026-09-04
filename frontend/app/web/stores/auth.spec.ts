import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';
import * as authService from '@/services/auth';
import { useAuthStore } from './auth';
import { PERMISSIONS, type SessionSnapshot } from '@/types/auth';

vi.mock('@/services/auth', () => ({
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

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it('deduplicates concurrent session probes and keeps identity in memory', async () => {
    let resolveSession!: (value: Awaited<ReturnType<typeof authService.getSession>>) => void;
    vi.mocked(authService.getSession).mockReturnValue(
      new Promise(resolve => {
        resolveSession = resolve;
      })
    );
    const store = useAuthStore();

    const first = store.ensureSession();
    const second = store.ensureSession();
    expect(authService.getSession).toHaveBeenCalledOnce();
    resolveSession((await response(session)) as never);

    await expect(Promise.all([first, second])).resolves.toEqual([true, true]);
    expect(store.user?.id).toBe('42');
    expect(store.csrfToken).toBe('csrf-token');
    expect(store.can(PERMISSIONS.RELEASES_CREATE)).toBe(true);
    expect(localStorage.length).toBe(0);
  });

  it('keeps an authenticated identity usable during background revalidation', async () => {
    vi.mocked(authService.getSession).mockReturnValueOnce(response(session));
    const store = useAuthStore();
    await store.ensureSession();

    let resolveRefresh!: (value: Awaited<ReturnType<typeof authService.getSession>>) => void;
    vi.mocked(authService.getSession).mockReturnValueOnce(
      new Promise(resolve => {
        resolveRefresh = resolve;
      })
    );

    const refresh = store.refreshSession();
    expect(store.status).toBe('authenticated');
    expect(store.isAuthenticated).toBe(true);

    resolveRefresh((await response(session)) as never);
    await expect(refresh).resolves.toBe(true);
    expect(store.status).toBe('authenticated');
  });

  it('discards a late refresh result after the session is invalidated', async () => {
    vi.mocked(authService.getSession).mockReturnValueOnce(response(session));
    const store = useAuthStore();
    await store.ensureSession();

    let resolveRefresh!: (value: Awaited<ReturnType<typeof authService.getSession>>) => void;
    vi.mocked(authService.getSession).mockReturnValueOnce(
      new Promise(resolve => {
        resolveRefresh = resolve;
      })
    );
    const refresh = store.refreshSession();

    store.invalidate('logout');
    resolveRefresh((await response(session)) as never);

    await expect(refresh).resolves.toBe(false);
    expect(store.status).toBe('anonymous');
    expect(store.user).toBeNull();
    expect(store.invalidationReason).toBe('logout');
  });

  it.each([401, 403])('clears the session when a probe returns %s', async status => {
    vi.mocked(authService.getSession).mockRejectedValue(authFailure(status));
    const store = useAuthStore();

    await expect(store.ensureSession()).resolves.toBe(false);
    expect(store.status).toBe('anonymous');
    expect(store.user).toBeNull();
    expect(store.csrfToken).toBeNull();
  });

  it('logs in with credentials then trusts the session endpoint for identity', async () => {
    vi.mocked(authService.login).mockReturnValue(response(session));
    vi.mocked(authService.getSession).mockReturnValue(response(session));
    const store = useAuthStore();

    await expect(store.login({ username: 'alice', password: 'secret' })).resolves.toBe(true);

    expect(authService.login).toHaveBeenCalledWith({ username: 'alice', password: 'secret' });
    expect(authService.getSession).toHaveBeenCalledOnce();
    expect(store.user).toEqual(session.user);
  });

  it('does not pretend logout succeeded when revocation fails', async () => {
    vi.mocked(authService.getSession).mockReturnValue(response(session));
    vi.mocked(authService.logout).mockRejectedValue(new Error('network down'));
    const store = useAuthStore();
    await store.ensureSession();

    await expect(store.logout()).rejects.toThrow('network down');
    expect(store.isAuthenticated).toBe(true);
  });

  it('clears the local session only after password rotation succeeds', async () => {
    vi.mocked(authService.getSession).mockReturnValue(response(session));
    vi.mocked(authService.changePassword).mockReturnValue(response(null));
    const store = useAuthStore();
    await store.ensureSession();

    await store.changePassword({
      current_password: 'correct horse battery staple',
      new_password: 'new correct horse battery staple',
    });

    expect(authService.changePassword).toHaveBeenCalledOnce();
    expect(store.status).toBe('anonymous');
    expect(store.user).toBeNull();
    expect(store.invalidationReason).toBe('password_changed');
  });
});
