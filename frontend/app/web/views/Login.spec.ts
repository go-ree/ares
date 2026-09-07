import { mount, flushPromises, type VueWrapper } from '@vue/test-utils';
import ElementPlus from 'element-plus';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import Login from './Login.vue';

const context = vi.hoisted(() => ({
  replace: vi.fn(),
  login: vi.fn(),
  bootstrap: vi.fn(),
  loadOptions: vi.fn(),
  oidcStartURL: vi.fn(() => '/api/v1/auth/oidc/start?return_to=%2F'),
  route: { query: { redirect: '/application/list' } },
  store: {
    options: {
      oidc_enabled: true,
      local_login_enabled: true,
      bootstrap_available: true,
    },
  },
}));

vi.mock('vue-router', () => ({
  useRoute: () => context.route,
  useRouter: () => ({ replace: context.replace }),
}));

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    ...context.store,
    login: context.login,
    bootstrap: context.bootstrap,
    loadOptions: context.loadOptions,
  }),
}));

vi.mock('@/services/auth', () => ({ oidcStartURL: context.oidcStartURL }));

const axiosError = (status: number, data: unknown) =>
  Object.assign(new Error('request failed'), {
    isAxiosError: true,
    response: { status, data },
  });

const mountLogin = async () => {
  const wrapper = mount(Login, { global: { plugins: [ElementPlus] } });
  await flushPromises();
  return wrapper;
};

const bootstrapInput = (wrapper: VueWrapper, placeholder: string) =>
  wrapper.get(`input[placeholder="${placeholder}"]`);

describe('Login', () => {
  beforeEach(() => {
    context.login.mockResolvedValue(true);
    context.bootstrap.mockResolvedValue(true);
    context.loadOptions.mockResolvedValue(context.store.options);
  });

  it('submits only credentials and follows a safe in-app redirect', async () => {
    const wrapper = await mountLogin();
    const username = wrapper.get('input[autocomplete="username"]');
    const password = wrapper.get('input[autocomplete="current-password"]');

    await username.setValue(' alice ');
    await password.setValue('secret');
    await wrapper.find('form.login-form').trigger('submit');
    await flushPromises();

    expect(context.login).toHaveBeenCalledWith({ username: 'alice', password: 'secret' });
    expect(context.replace).toHaveBeenCalledWith('/application/list');
    expect(wrapper.text()).not.toContain('发布人');
    wrapper.unmount();
  });

  it('shows every actionable bootstrap validation error without sending a request', async () => {
    const wrapper = await mountLogin();

    await bootstrapInput(wrapper, 'Bootstrap Token').setValue('token-from-deployment');
    await bootstrapInput(wrapper, '管理员用户名').setValue('ab');
    await bootstrapInput(wrapper, '显示名称').setValue('界'.repeat(86));
    await bootstrapInput(wrapper, '管理员密码').setValue('short');
    await bootstrapInput(wrapper, '再次输入管理员密码').setValue('different');
    await wrapper.findAll('form.login-form')[1].trigger('submit');
    await flushPromises();

    expect(context.bootstrap).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('请按字段提示修正首次管理员信息');
    expect(wrapper.text()).toContain('用户名须为 3–64 个字符');
    expect(wrapper.text()).toContain('显示名称的 UTF-8 编码不能超过 255 字节');
    expect(wrapper.text()).toContain('管理员密码的 UTF-8 编码须为 12–1024 字节');
    expect(wrapper.text()).toContain('两次输入的管理员密码不一致');
    wrapper.unmount();
  });

  it('accepts bootstrap values exactly on UTF-8 byte boundaries', async () => {
    const wrapper = await mountLogin();
    const username = `a${'b'.repeat(63)}`;
    const displayName = '界'.repeat(85);
    const password = '密'.repeat(4);

    await bootstrapInput(wrapper, 'Bootstrap Token').setValue('token-from-deployment');
    await bootstrapInput(wrapper, '管理员用户名').setValue(username);
    await bootstrapInput(wrapper, '显示名称').setValue(displayName);
    await bootstrapInput(wrapper, '管理员密码').setValue(password);
    await bootstrapInput(wrapper, '再次输入管理员密码').setValue(password);
    await wrapper.findAll('form.login-form')[1].trigger('submit');
    await flushPromises();

    expect(context.bootstrap).toHaveBeenCalledWith({
      bootstrap_token: 'token-from-deployment',
      username,
      display_name: displayName,
      password,
    });
    expect(context.replace).toHaveBeenCalledWith('/application/list');
    wrapper.unmount();
  });

  it('shows the public bootstrap validation detail returned in a 400 envelope', async () => {
    context.bootstrap.mockRejectedValue(
      axiosError(400, {
        code: 0,
        message: '首次管理员创建失败',
        error: '密码长度必须在 12 到 1024 字节之间',
      })
    );
    const wrapper = await mountLogin();

    await bootstrapInput(wrapper, 'Bootstrap Token').setValue('token-from-deployment');
    await bootstrapInput(wrapper, '管理员用户名').setValue('local-admin');
    await bootstrapInput(wrapper, '显示名称').setValue('本地管理员');
    await bootstrapInput(wrapper, '管理员密码').setValue('correct horse battery staple');
    await bootstrapInput(wrapper, '再次输入管理员密码').setValue('correct horse battery staple');
    await wrapper.findAll('form.login-form')[1].trigger('submit');
    await flushPromises();

    expect(wrapper.text()).toContain('密码长度必须在 12 到 1024 字节之间');
    expect(wrapper.text()).not.toContain('request failed');
    wrapper.unmount();
  });

  it('does not expose response envelope details for local credential failures', async () => {
    context.login.mockRejectedValue(
      axiosError(401, {
        code: 0,
        message: '登录失败',
        error: 'sensitive credential lookup detail',
      })
    );
    const wrapper = await mountLogin();

    await wrapper.get('input[autocomplete="username"]').setValue('alice');
    await wrapper.get('input[autocomplete="current-password"]').setValue('wrong-password');
    await wrapper.find('form.login-form').trigger('submit');
    await flushPromises();

    expect(wrapper.text()).toContain('用户名或密码错误');
    expect(wrapper.text()).not.toContain('sensitive credential lookup detail');
    wrapper.unmount();
  });
});
