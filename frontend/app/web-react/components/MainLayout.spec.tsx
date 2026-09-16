import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider } from 'react-router';
import { PERMISSIONS } from '@shared/types/auth';
import type { AuthUser, Permission } from '@shared/types/auth';
import MainLayout from './MainLayout';
import { toNavItems, visibleMenuItems } from '../menu/mainMenu';
import { useAuthStore } from '../stores/auth';

vi.mock('@shared/services/auth', () => ({
  getAuthOptions: vi.fn(),
  getSession: vi.fn(),
  login: vi.fn(),
  bootstrap: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
}));

const canAnyWith = (granted: Permission[]) => (required: Permission[]) =>
  required.some(permission => granted.includes(permission));

/** Flattens the surviving tree into item keys so parity is asserted structurally. */
const keysOf = (nodes: ReturnType<typeof visibleMenuItems>): string[] =>
  nodes.flatMap(node => [node.itemKey, ...(node.items ? keysOf(node.items) : [])]);

const identity = (
  permissions: Permission[],
  authSource: AuthUser['auth_source'] = 'bootstrap'
): AuthUser => ({
  id: '1',
  username: 'tester',
  display_name: 'Tester',
  email: 'tester@example.com',
  auth_source: authSource,
  roles: ['developer'],
  permissions,
});

const renderShell = async () => {
  const router = createMemoryRouter(
    [{ path: '/', element: <MainLayout />, children: [{ index: true, element: null }] }],
    { initialEntries: ['/'] }
  );
  return { router, ...render(<RouterProvider router={router} />) };
};

describe('MainLayout permission menu', () => {
  beforeEach(() => {
    useAuthStore.getState().reset();
  });

  it('keeps the full structure for an admin identity', () => {
    const keys = keysOf(visibleMenuItems(canAnyWith(Object.values(PERMISSIONS))));
    expect(keys).toContain('/');
    expect(keys).toContain('/system');
    expect(keys).toContain('/system/users');
    expect(keys).toContain('/publish/merge');
  });

  it('shows read navigation without leaking write-only destinations', () => {
    const granted = [
      PERMISSIONS.APPLICATIONS_READ,
      PERMISSIONS.RELEASES_READ,
      PERMISSIONS.LOGS_READ,
    ];
    const keys = keysOf(visibleMenuItems(canAnyWith(granted)));
    expect(keys).toContain('/application/list');
    expect(keys).toContain('/publish/deploy');
    expect(keys).toContain('/operation/log');
    expect(keys).not.toContain('/application/apply');
    expect(keys).not.toContain('/publish/merge');
    expect(keys).not.toContain('/system/settings');
    // The parent survives because a visible child remains.
    expect(keys).toContain('/application');
  });

  it('drops a parent whose children are all filtered out', () => {
    // Only a write permission survives: 应用申请 stays, 应用列表 goes.
    const keys = keysOf(visibleMenuItems(canAnyWith([PERMISSIONS.APPLICATIONS_WRITE])));
    expect(keys).toContain('/application');
    expect(keys).toContain('/application/apply');
    expect(keys).not.toContain('/application/list');

    // 运维管理 has no visible child for this identity, so it disappears entirely.
    const logsOnly = keysOf(visibleMenuItems(canAnyWith([PERMISSIONS.LOGS_READ])));
    expect(logsOnly).toContain('/operation/log');
    expect(logsOnly).not.toContain('/operation/monitor');

    // 运维管理 matches its own `anyOf` here, but both children need a different
    // permission, so the section is dropped rather than rendered empty. The Vue
    // template would have shown an empty submenu; hiding it is deliberate and
    // unobservable for the shipped roles, which always hold a child permission.
    const releaseCreateOnly = keysOf(visibleMenuItems(canAnyWith([PERMISSIONS.RELEASES_CREATE])));
    expect(releaseCreateOnly).not.toContain('/operation');
    expect(releaseCreateOnly).toContain('/publish');
    expect(releaseCreateOnly).toContain('/publish/merge');
  });

  it('keeps an unconditional section reachable even without its child permissions', () => {
    const keys = keysOf(visibleMenuItems(canAnyWith([])));
    expect(keys).toEqual(['/', '/system', '/system/version']);
  });

  it('shows user management only with users:read', () => {
    const without = keysOf(visibleMenuItems(canAnyWith([PERMISSIONS.SYSTEM_SETTINGS_READ])));
    expect(without).toContain('/system/settings');
    expect(without).not.toContain('/system/users');

    const with_ = keysOf(visibleMenuItems(canAnyWith([PERMISSIONS.USERS_READ])));
    expect(with_).toContain('/system/users');
  });

  it('offers password rotation only to the local bootstrap identity', async () => {
    const openUserMenu = async () => {
      const user = userEvent.setup();
      await user.click(screen.getByLabelText('用户菜单'));
      // The dropdown renders through a portal, so assertions look at the body.
      return within(document.body);
    };

    useAuthStore.setState({
      status: 'authenticated',
      user: identity([PERMISSIONS.APPLICATIONS_READ], 'bootstrap'),
    });
    const local = await renderShell();
    await waitFor(() => expect(local.container.textContent).toContain('应用管理'));
    const localMenu = await openUserMenu();
    await waitFor(() => expect(localMenu.getByText('退出登录')).toBeInTheDocument());
    expect(localMenu.getByText('修改密码')).toBeInTheDocument();
    local.unmount();

    useAuthStore.setState({
      status: 'authenticated',
      user: identity([PERMISSIONS.APPLICATIONS_READ], 'oidc'),
    });
    const federated = await renderShell();
    await waitFor(() => expect(federated.container.textContent).toContain('应用管理'));
    const federatedMenu = await openUserMenu();
    await waitFor(() => expect(federatedMenu.getByText('退出登录')).toBeInTheDocument());
    // A federated identity cannot rotate a local password, so the entry is gone.
    expect(federatedMenu.queryByText('修改密码')).toBeNull();
    federated.unmount();
  });

  it('renders every top-level section for an admin identity', async () => {
    useAuthStore.setState({
      status: 'authenticated',
      user: identity(Object.values(PERMISSIONS) as Permission[]),
    });
    const { container, unmount } = await renderShell();
    await waitFor(() => expect(container.textContent).toContain('首页'));
    // Submenu children mount on expand, so only top-level labels are asserted
    // here; the child structure is covered by the pure-function cases above.
    for (const label of ['首页', '应用管理', '发布工具', '运维管理', '系统设置']) {
      expect(container.textContent).toContain(label);
    }
    expect(container.textContent).toContain('Tester');
    unmount();
  });

  it('marks destinations that this stack has not migrated yet as disabled', () => {
    type NavNode = { itemKey?: unknown; disabled?: unknown; items?: NavNode[] };
    const items = toNavItems(
      visibleMenuItems(canAnyWith(Object.values(PERMISSIONS)))
    ) as unknown as NavNode[];
    const flat = items.flatMap(item => [item, ...(item.items || [])]);
    const byKey = new Map(flat.map(item => [String(item.itemKey ?? item), item]));
    // 首页 is migrated in B1; 应用列表 arrives with B2/B3, so it must not offer
    // a dead link while the Vue stack still owns it.
    expect(byKey.get('/')?.disabled).toBe(false);
    expect(byKey.get('/application/list')?.disabled).toBe(true);
  });
});
