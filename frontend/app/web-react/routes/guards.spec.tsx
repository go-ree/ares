import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import { createMemoryRouter, RouterProvider } from 'react-router';
import type { LoaderFunctionArgs, RouteObject } from 'react-router';
import * as authService from '@shared/services/auth';
import { PERMISSIONS, type SessionSnapshot } from '@shared/types/auth';
import { appRoutes } from './index';
import { requirePermissions } from './guards';
import { useAuthStore } from '../stores/auth';

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
    permissions: [PERMISSIONS.APPLICATIONS_READ],
  },
  expires_at: '2099-01-01T00:00:00Z',
  csrf_token: 'csrf-token',
};

function response<T>(result: T) {
  return Promise.resolve({ data: { code: 1, message: 'ok', result } } as never);
}

const signIn = async () => {
  vi.mocked(authService.getSession).mockReturnValue(response(session) as never);
  await useAuthStore.getState().ensureSession();
};

/**
 * A data router only applies a loader redirect while it has a subscriber, so the
 * route is rendered and then the settled location is read back.
 */
const renderAt = (entry: string, routes: RouteObject[] = appRoutes) => {
  const router = createMemoryRouter(routes, { initialEntries: [entry] });
  render(<RouterProvider router={router} />);
  return router;
};

const locationFor = async (entry: string) => {
  const router = renderAt(entry);
  await waitFor(() => expect(router.state.initialized).toBe(true));
  return `${router.state.location.pathname}${router.state.location.search}`;
};

describe('route guards (React)', () => {
  beforeEach(() => {
    useAuthStore.getState().reset();
    vi.mocked(authService.getSession).mockReset();
    vi.mocked(authService.getSession).mockRejectedValue(
      Object.assign(new Error('anonymous'), { isAxiosError: true, response: { status: 401 } })
    );
  });

  it('sends an anonymous visitor to login with the intended path preserved', async () => {
    const router = renderAt('/');
    await waitFor(() => expect(router.state.initialized).toBe(true));
    expect(`${router.state.location.pathname}${router.state.location.search}`).toBe(
      '/login?redirect=%2F'
    );
    expect(router.state.historyAction).toBe('REPLACE');
  });

  it('treats the forbidden page as protected too', async () => {
    await expect(locationFor('/forbidden')).resolves.toBe('/login?redirect=%2Fforbidden');
  });

  it('allows an authenticated visitor through and keeps the path', async () => {
    await signIn();
    await expect(locationFor('/')).resolves.toBe('/');
  });

  it('redirects an authenticated visitor away from login using a normalized target', async () => {
    await signIn();
    await expect(locationFor('/login?redirect=%2Fapplication%2Flist')).resolves.toBe(
      '/application/list'
    );
  });

  it('refuses an off-origin redirect target and falls back to the default path', async () => {
    await signIn();
    await expect(locationFor('/login?redirect=%2F%2Fevil.example%2Fsteal')).resolves.toBe('/');
  });

  it('falls back to the default path when no redirect target is present', async () => {
    await signIn();
    await expect(locationFor('/login')).resolves.toBe('/');
  });

  it('renders the not-found route for an unknown path', async () => {
    await signIn();
    const router = renderAt('/does-not-exist');
    await waitFor(() => expect(router.state.initialized).toBe(true));
    const matches = router.state.matches;
    expect(matches[matches.length - 1].route.path).toBe('*');
  });

  it('redirects a missing permission to the forbidden page with the source path', async () => {
    await signIn();
    // The redirect target must exist in this minimal table, otherwise React
    // Router logs a route-miss that would mask real failures.
    const guarded: RouteObject[] = [
      {
        path: '/guarded',
        loader: requirePermissions([PERMISSIONS.USERS_WRITE]),
        element: null,
      },
      { path: '/forbidden', element: null },
    ];
    const router = renderAt('/guarded', guarded);
    await waitFor(() =>
      expect(`${router.state.location.pathname}${router.state.location.search}`).toBe(
        '/forbidden?from=%2Fguarded'
      )
    );
    expect(router.state.historyAction).toBe('REPLACE');
  });

  it('does not start a protected data request before its own permission check passes', async () => {
    await signIn();
    const load = vi.fn();
    const guarded: RouteObject[] = [
      {
        path: '/guarded',
        loader: requirePermissions([PERMISSIONS.USERS_WRITE], load),
        element: null,
      },
      { path: '/forbidden', element: null },
    ];
    const router = renderAt('/guarded', guarded);
    await waitFor(() => expect(router.state.location.pathname).toBe('/forbidden'));
    expect(load).not.toHaveBeenCalled();
  });

  it('runs a composed data request after permission succeeds and preserves loader arguments', async () => {
    vi.mocked(authService.getSession).mockReturnValue(
      response({
        ...session,
        user: { ...session.user, permissions: [PERMISSIONS.USERS_WRITE] },
      } as never) as never
    );
    const load = vi.fn((_args: LoaderFunctionArgs) => ({ ok: true }));
    const guarded: RouteObject[] = [
      {
        path: '/guarded/:id',
        loader: requirePermissions([PERMISSIONS.USERS_WRITE], load),
        element: null,
      },
    ];
    const router = renderAt('/guarded/42', guarded);
    await waitFor(() => expect(router.state.initialized).toBe(true));
    expect(load).toHaveBeenCalledOnce();
    expect(load.mock.calls[0][0].params).toEqual({ id: '42' });
    expect(router.state.loaderData['0']).toEqual({ ok: true });
  });

  it('lets a granted permission reach the guarded route', async () => {
    vi.mocked(authService.getSession).mockReturnValue(
      response({
        ...session,
        user: { ...session.user, permissions: [PERMISSIONS.USERS_WRITE] },
      } as never) as never
    );
    await useAuthStore.getState().ensureSession();
    // The redirect target must exist in this minimal table, otherwise React
    // Router logs a route-miss that would mask real failures.
    const guarded: RouteObject[] = [
      {
        path: '/guarded',
        loader: requirePermissions([PERMISSIONS.USERS_WRITE]),
        element: null,
      },
      { path: '/forbidden', element: null },
    ];
    const router = renderAt('/guarded', guarded);
    await waitFor(() => expect(router.state.initialized).toBe(true));
    expect(router.state.location.pathname).toBe('/guarded');
  });

  it('does not re-probe a confirmed anonymous session on every navigation', async () => {
    await signIn();
    vi.mocked(authService.getSession).mockClear();
    const router = renderAt('/');
    await waitFor(() => expect(router.state.initialized).toBe(true));
    await router.navigate('/forbidden');
    // Identity is in memory, so no additional session request is issued.
    expect(authService.getSession).not.toHaveBeenCalled();
  });
});
