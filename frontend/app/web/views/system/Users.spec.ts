import { flushPromises, mount } from '@vue/test-utils';
import ElementPlus, { ElMessage, ElMessageBox } from 'element-plus';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PERMISSIONS, type ManagedUser, type Permission } from '@/types/auth';
import Users from './Users.vue';

const context = vi.hoisted(() => ({
  permissions: new Set<string>(),
  currentUserID: '99',
  listUsers: vi.fn(),
  updateUser: vi.fn(),
  invalidate: vi.fn(),
  replace: vi.fn(),
}));

vi.mock('vue-router', () => ({
  useRouter: () => ({ replace: context.replace }),
}));

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: context.currentUserID, username: 'operator' },
    can: (permission: Permission) => context.permissions.has(permission),
    invalidate: context.invalidate,
  }),
}));

vi.mock('@/services/users', () => ({
  listUsers: context.listUsers,
  updateUser: context.updateUser,
  getUserApiError: (error: { status?: number; message?: string }) => ({
    status: error.status,
    message: error.message || '请求失败',
  }),
}));

const user = (overrides: Partial<ManagedUser> = {}): ManagedUser => ({
  id: '7',
  username: 'alice',
  display_name: 'Alice Zhang',
  email: 'alice@example.com',
  auth_source: 'oidc',
  role: 'viewer',
  enabled: true,
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
  ...overrides,
});

const mountPage = async () => {
  const wrapper = mount(Users, { global: { plugins: [ElementPlus] }, attachTo: document.body });
  await flushPromises();
  return wrapper;
};

describe('Users', () => {
  beforeEach(() => {
    context.permissions.clear();
    context.permissions.add(PERMISSIONS.USERS_READ);
    context.currentUserID = '99';
    context.listUsers.mockReset().mockResolvedValue({ items: [user()], next_offset: 1 });
    context.updateUser.mockReset();
    context.invalidate.mockReset();
    context.replace.mockReset();
    vi.spyOn(ElMessageBox, 'confirm').mockImplementation(
      () => Promise.resolve('confirm') as ReturnType<typeof ElMessageBox.confirm>
    );
    vi.spyOn(ElMessage, 'success').mockImplementation(() => undefined as never);
    vi.spyOn(ElMessage, 'error').mockImplementation(() => undefined as never);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('shows user identity, source, role and status without write controls to users:read', async () => {
    const wrapper = await mountPage();

    expect(wrapper.text()).toContain('Alice Zhang');
    expect(wrapper.text()).toContain('alice');
    expect(wrapper.text()).toContain('alice@example.com');
    expect(wrapper.text()).toContain('OIDC');
    expect(wrapper.text()).toContain('查看者');
    expect(wrapper.text()).toContain('已启用');
    expect(wrapper.text()).toContain('只读权限');
    expect(wrapper.find('[aria-label="修改 alice 的角色"]').exists()).toBe(false);
    expect(wrapper.find('[aria-label="切换 alice 的启用状态"]').exists()).toBe(false);
    wrapper.unmount();
  });

  it('allows users:write to change a role after confirmation', async () => {
    context.permissions.add(PERMISSIONS.USERS_WRITE);
    context.updateUser.mockResolvedValue(user({ role: 'developer' }));
    const wrapper = await mountPage();

    wrapper.findComponent({ name: 'ElSelect' }).vm.$emit('change', 'developer');
    await flushPromises();

    expect(ElMessageBox.confirm).toHaveBeenCalledOnce();
    expect(context.updateUser).toHaveBeenCalledWith('7', { role: 'developer' });
    expect(ElMessage.success).toHaveBeenCalledWith('用户 alice 已更新');
    wrapper.unmount();
  });

  it('surfaces the protected last-admin conflict and leaves state unchanged', async () => {
    context.permissions.add(PERMISSIONS.USERS_WRITE);
    context.updateUser.mockRejectedValue({ status: 409, message: 'last admin' });
    const wrapper = await mountPage();

    wrapper.findComponent({ name: 'ElSwitch' }).vm.$emit('change', false);
    await flushPromises();

    expect(context.updateUser).toHaveBeenCalledWith('7', { enabled: false });
    expect(ElMessage.error).toHaveBeenCalledWith('至少需要保留一个已启用的管理员，本次修改未生效');
    expect(wrapper.text()).toContain('启用');
    wrapper.unmount();
  });

  it('immediately invalidates the current session after a self role change', async () => {
    context.permissions.add(PERMISSIONS.USERS_WRITE);
    context.currentUserID = '7';
    context.updateUser.mockResolvedValue(user({ role: 'developer' }));
    const wrapper = await mountPage();

    wrapper.findComponent({ name: 'ElSelect' }).vm.$emit('change', 'developer');
    await flushPromises();

    expect(context.invalidate).toHaveBeenCalledWith('permissions_changed');
    expect(context.replace).toHaveBeenCalledWith({ name: 'login' });
    wrapper.unmount();
  });
});
