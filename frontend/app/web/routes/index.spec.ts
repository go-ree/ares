import { createPinia, setActivePinia } from 'pinia';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import router from './index';
import { useAuthStore } from '@/stores/auth';
import * as authService from '@/services/auth';
import { PERMISSIONS, type Permission, type SessionSnapshot } from '@/types/auth';

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
    id: '1',
    username: 'alice',
    display_name: 'Alice',
    auth_source: 'oidc',
    roles: ['viewer'],
    permissions: [PERMISSIONS.APPLICATIONS_READ],
  },
  csrf_token: 'csrf',
  expires_at: '2099-01-01T00:00:00Z',
};

const authenticate = (permissions: Permission[]) => {
  const auth = useAuthStore();
  auth.$patch({
    status: 'authenticated',
    user: {
      id: '1',
      username: 'alice',
      display_name: 'Alice',
      auth_source: 'oidc',
      roles: ['viewer'],
      permissions,
    },
    csrfToken: 'csrf',
    expiresAt: '2099-01-01T00:00:00Z',
  });
};

describe('router session and permission guards', () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    setActivePinia(createPinia());
    useAuthStore().$patch({ status: 'anonymous' });
    await router.replace('/login');
  });

  it('preserves the in-app return path for anonymous users', async () => {
    await router.push('/application/list?owner=alice');

    expect(router.currentRoute.value.name).toBe('login');
    expect(router.currentRoute.value.query.redirect).toBe('/application/list?owner=alice');
  });

  it('awaits an existing loading session probe before redirecting', async () => {
    let resolveSession!: (value: Awaited<ReturnType<typeof authService.getSession>>) => void;
    vi.mocked(authService.getSession).mockReturnValue(
      new Promise(resolve => {
        resolveSession = resolve;
      })
    );
    const auth = useAuthStore();
    auth.$patch({ status: 'unknown' });
    const probe = auth.ensureSession();
    expect(auth.status).toBe('loading');

    const navigation = router.push('/application/list');
    resolveSession({ data: { code: 1, message: 'ok', result: session } } as never);
    await probe;
    await navigation;

    expect(router.currentRoute.value.name).toBe('application-list');
  });

  it('denies a route unless its exact server-expanded permission is present', async () => {
    authenticate([PERMISSIONS.APPLICATIONS_READ]);
    await router.push('/system/settings');
    expect(router.currentRoute.value.name).toBe('forbidden');

    authenticate([PERMISSIONS.SYSTEM_SETTINGS_READ]);
    await router.push('/system/settings');
    expect(router.currentRoute.value.name).toBe('system-settings');
  });

  it('guards user management with users:read rather than another system permission', async () => {
    authenticate([PERMISSIONS.SYSTEM_SETTINGS_READ]);
    await router.push('/system/users');
    expect(router.currentRoute.value.name).toBe('forbidden');

    authenticate([PERMISSIONS.USERS_READ]);
    await router.push('/system/users');
    expect(router.currentRoute.value.name).toBe('system-users');
  });
});
