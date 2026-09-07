import { mount } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { ref } from 'vue';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PERMISSIONS, type Permission } from '@/types/auth';
import MainLayout from './MainLayout.vue';

const context = vi.hoisted(() => ({
  permissions: new Set<string>(),
  replace: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
  authSource: 'bootstrap',
}));

vi.mock('vue-router', () => ({
  useRouter: () => ({
    currentRoute: ref({ path: '/' }),
    replace: context.replace,
  }),
}));

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { username: 'viewer', display_name: 'Viewer', auth_source: context.authSource },
    can: (permission: Permission) => context.permissions.has(permission),
    canAny: (permissions: Permission[]) =>
      permissions.some(permission => context.permissions.has(permission)),
    logout: context.logout,
    changePassword: context.changePassword,
  }),
}));

describe('MainLayout permissions', () => {
  beforeEach(() => {
    context.authSource = 'bootstrap';
    context.permissions.clear();
    context.permissions.add(PERMISSIONS.APPLICATIONS_READ);
    context.permissions.add(PERMISSIONS.RELEASES_READ);
    context.permissions.add(PERMISSIONS.LOGS_READ);
  });

  it('offers password rotation only to the local bootstrap identity', () => {
    const local = mount(MainLayout, {
      global: { plugins: [ElementPlus], stubs: { RouterView: true, Teleport: true } },
    });
    expect(local.text()).toContain('修改密码');
    local.unmount();

    context.authSource = 'oidc';
    const oidc = mount(MainLayout, {
      global: { plugins: [ElementPlus], stubs: { RouterView: true, Teleport: true } },
    });
    expect(oidc.text()).not.toContain('修改密码');
    oidc.unmount();
  });

  it('shows read navigation without leaking write/admin actions', () => {
    const wrapper = mount(MainLayout, {
      global: {
        plugins: [ElementPlus],
        stubs: { RouterView: true },
      },
    });

    expect(wrapper.text()).toContain('应用列表');
    expect(wrapper.text()).toContain('服务发布');
    expect(wrapper.text()).toContain('日志查询');
    expect(wrapper.text()).not.toContain('应用申请');
    expect(wrapper.text()).not.toContain('代码合并');
    expect(wrapper.text()).not.toContain('一键批量发布');
    expect(wrapper.text()).not.toContain('系统配置');
    wrapper.unmount();
  });

  it('requires every permission used by the batch deploy route', () => {
    context.permissions.clear();
    context.permissions.add(PERMISSIONS.RELEASES_CREATE);
    const withoutApplicationRead = mount(MainLayout, {
      global: { plugins: [ElementPlus], stubs: { RouterView: true } },
    });
    expect(withoutApplicationRead.text()).not.toContain('一键批量发布');
    withoutApplicationRead.unmount();

    context.permissions.add(PERMISSIONS.APPLICATIONS_READ);
    const withBothPermissions = mount(MainLayout, {
      global: { plugins: [ElementPlus], stubs: { RouterView: true } },
    });
    expect(withBothPermissions.text()).toContain('一键批量发布');
    withBothPermissions.unmount();
  });

  it('shows user management only with users:read', () => {
    const withoutUserRead = mount(MainLayout, {
      global: { plugins: [ElementPlus], stubs: { RouterView: true } },
    });
    expect(withoutUserRead.text()).not.toContain('用户与角色');
    withoutUserRead.unmount();

    context.permissions.add(PERMISSIONS.USERS_READ);
    const withUserRead = mount(MainLayout, {
      global: { plugins: [ElementPlus], stubs: { RouterView: true } },
    });
    expect(withUserRead.text()).toContain('用户与角色');
    withUserRead.unmount();
  });
});
