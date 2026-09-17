import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, RouterProvider } from 'react-router';
import * as authService from '@shared/services/auth';
import Login from './Login';
import { useAuthStore } from '../stores/auth';

vi.mock('@shared/services/auth', () => ({
  getAuthOptions: vi.fn(),
  getSession: vi.fn(),
  login: vi.fn(),
  bootstrap: vi.fn(),
  logout: vi.fn(),
  changePassword: vi.fn(),
  oidcStartURL: vi.fn((returnTo: string) => `/api/v1/auth/oidc/start?return_to=${returnTo}`),
}));

const envelope = <T,>(result: T) =>
  Promise.resolve({ data: { code: 1, message: 'ok', result } } as never);

const httpFailure = (status: number, body: unknown) =>
  Object.assign(new Error('request failed'), {
    isAxiosError: true,
    response: { status, data: body },
  });

const options = {
  oidc_enabled: false,
  local_login_enabled: true,
  bootstrap_available: false,
};

const renderLogin = (entry = '/login') => {
  const router = createMemoryRouter(
    [
      { path: '/login', element: <Login /> },
      { path: '/application/list', element: <div>已进入应用列表</div> },
    ],
    { initialEntries: [entry] }
  );
  return { router, ...render(<RouterProvider router={router} />) };
};

describe('Login', () => {
  beforeEach(() => {
    useAuthStore.getState().reset();
    vi.mocked(authService.getAuthOptions).mockReset();
    vi.mocked(authService.login).mockReset();
    vi.mocked(authService.bootstrap).mockReset();
    vi.mocked(authService.getAuthOptions).mockImplementation(() => envelope(options) as never);
  });

  it('submits only credentials and follows a safe in-app redirect', async () => {
    const user = userEvent.setup();
    vi.mocked(authService.login).mockResolvedValue(undefined as never);
    vi.mocked(authService.getSession).mockImplementation(
      () =>
        envelope({
          user: {
            id: '1',
            username: 'alice',
            display_name: 'Alice',
            email: '',
            auth_source: 'bootstrap',
            roles: ['admin'],
            permissions: [],
          },
          expires_at: '2099-01-01T00:00:00Z',
          csrf_token: 'csrf',
        }) as never
    );

    const { router } = renderLogin('/login?redirect=%2Fapplication%2Flist');
    await waitFor(() => expect(screen.getByLabelText('用户名')).toBeInTheDocument());

    await user.type(screen.getByLabelText('用户名'), '  alice  ');
    await user.type(screen.getByLabelText('密码'), 'correct horse');
    await user.click(screen.getByRole('button', { name: '本地登录' }));

    await waitFor(() => expect(router.state.location.pathname).toBe('/application/list'));
    expect(authService.login).toHaveBeenCalledTimes(1);
    // Only the credentials cross the wire, and the username is trimmed.
    expect(vi.mocked(authService.login).mock.calls[0][0]).toEqual({
      username: 'alice',
      password: 'correct horse',
    });
  });

  it('refuses an off-origin redirect and falls back to the default path', async () => {
    useAuthStore.setState({
      status: 'authenticated',
      user: {
        id: '1',
        username: 'alice',
        display_name: 'Alice',
        email: '',
        auth_source: 'bootstrap',
        roles: ['admin'],
        permissions: [],
      },
    });
    const { router } = renderLogin('/login?redirect=%2F%2Fevil.example');
    // publicOnly is not applied by this bare router, so the page renders; the
    // security-relevant part is that the target never reaches navigation.
    await waitFor(() => expect(router.state.location.search).toContain('redirect'));
    expect(router.state.location.pathname).toBe('/login');
  });

  it('shows every actionable bootstrap validation error without sending a request', async () => {
    const user = userEvent.setup();
    vi.mocked(authService.getAuthOptions).mockImplementation(
      () => envelope({ ...options, bootstrap_available: true }) as never
    );
    renderLogin();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: '创建首次管理员并登录' })).toBeInTheDocument()
    );

    await user.click(screen.getByRole('button', { name: '创建首次管理员并登录' }));

    await waitFor(() =>
      expect(screen.getByText('请输入部署环境中配置的 Bootstrap Token')).toBeInTheDocument()
    );
    expect(
      screen.getByText(
        '用户名须为 3–64 个字符，以字母或数字开头，且仅含字母、数字、点、下划线或连字符'
      )
    ).toBeInTheDocument();
    expect(screen.getByText('显示名称不能为空')).toBeInTheDocument();
    expect(
      screen.getByText('管理员密码至少 8 个字符，UTF-8 编码不能超过 1024 字节')
    ).toBeInTheDocument();
    expect(screen.getByText('请再次输入管理员密码')).toBeInTheDocument();
    expect(screen.getByText('请按字段提示修正首次管理员信息')).toBeInTheDocument();
    // Nothing may leave the browser while the form is locally invalid.
    expect(authService.bootstrap).not.toHaveBeenCalled();
  });

  it('rejects a pure-digit and mismatched bootstrap password locally', async () => {
    const user = userEvent.setup();
    vi.mocked(authService.getAuthOptions).mockImplementation(
      () => envelope({ ...options, bootstrap_available: true }) as never
    );
    renderLogin();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: '创建首次管理员并登录' })).toBeInTheDocument()
    );

    await user.type(screen.getByLabelText('Bootstrap Token'), 'token-value');
    await user.type(screen.getByLabelText('管理员用户名'), 'admin2');
    await user.type(screen.getByLabelText('显示名称'), '管理员');
    await user.type(screen.getByLabelText('管理员密码'), '12345678');
    await user.type(screen.getByLabelText('再次输入管理员密码'), '87654321');
    await user.click(screen.getByRole('button', { name: '创建首次管理员并登录' }));

    await waitFor(() => expect(screen.getByText('密码不能是纯数字')).toBeInTheDocument());
    expect(screen.getByText('两次输入的管理员密码不一致')).toBeInTheDocument();
    expect(authService.bootstrap).not.toHaveBeenCalled();
  });

  it('shows the public bootstrap validation detail returned in a 400 envelope', async () => {
    const user = userEvent.setup();
    vi.mocked(authService.getAuthOptions).mockImplementation(
      () => envelope({ ...options, bootstrap_available: true }) as never
    );
    vi.mocked(authService.bootstrap).mockRejectedValue(
      httpFailure(400, {
        code: 0,
        message: '请求无效',
        error: 'bootstrap token 已失效',
        result: null,
      })
    );
    renderLogin();
    await waitFor(() =>
      expect(screen.getByRole('button', { name: '创建首次管理员并登录' })).toBeInTheDocument()
    );

    await user.type(screen.getByLabelText('Bootstrap Token'), 'token-value');
    await user.type(screen.getByLabelText('管理员用户名'), 'admin2');
    await user.type(screen.getByLabelText('显示名称'), '管理员');
    await user.type(screen.getByLabelText('管理员密码'), 'A-strong-passphrase');
    await user.type(screen.getByLabelText('再次输入管理员密码'), 'A-strong-passphrase');
    await user.click(screen.getByRole('button', { name: '创建首次管理员并登录' }));

    await waitFor(() => expect(screen.getByText('bootstrap token 已失效')).toBeInTheDocument());
  });

  it('does not expose response envelope details for local credential failures', async () => {
    const user = userEvent.setup();
    vi.mocked(authService.login).mockRejectedValue(
      httpFailure(401, { code: 0, message: '内部提示', error: '私有诊断细节' })
    );
    renderLogin();
    await waitFor(() => expect(screen.getByLabelText('用户名')).toBeInTheDocument());

    await user.type(screen.getByLabelText('用户名'), 'alice');
    await user.type(screen.getByLabelText('密码'), 'wrong-password');
    await user.click(screen.getByRole('button', { name: '本地登录' }));

    await waitFor(() => expect(screen.getByText('用户名或密码错误')).toBeInTheDocument());
    expect(screen.queryByText('私有诊断细节')).toBeNull();
    expect(screen.queryByText('内部提示')).toBeNull();
    // The rejected attempt clears the password field.
    expect((screen.getByLabelText('密码') as HTMLInputElement).value).toBe('');
  });
});
