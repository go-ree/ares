import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Divider,
  Empty,
  Input,
  Spin,
  Typography,
} from '@douyinfe/semi-ui-19';
import axios from 'axios';
import { useNavigate, useSearchParams } from 'react-router';
import { oidcStartURL } from '@shared/services/auth';
import type { ApiEnvelope } from '@shared/types/auth';
import { normalizeReturnTo } from '@shared/utils/return-to';
import { useAuthStore } from '../stores/auth';

const { Title, Text } = Typography;

const BOOTSTRAP_USERNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$/;
const utf8ByteLength = (value: string) => new TextEncoder().encode(value).length;

type BootstrapField =
  | 'bootstrap_token'
  | 'username'
  | 'display_name'
  | 'password'
  | 'password_confirmation';

/** Mirrors the Vue page's status-to-message mapping for local sign-in. */
const authErrorMessage = (error: unknown, fallback: string) => {
  if (!axios.isAxiosError(error)) return fallback;
  switch (error.response?.status) {
    case 401:
      return '用户名或密码错误';
    case 409:
      return '首次管理员已经创建，请使用现有账号登录';
    case 429:
      return '尝试次数过多，请稍后再试';
    case 503:
      return '认证服务暂时不可用，请稍后再试';
    default:
      return fallback;
  }
};

/** A rejected bootstrap carries the server's field message; surface it instead. */
const bootstrapErrorMessage = (error: unknown, fallback: string) => {
  if (axios.isAxiosError<ApiEnvelope<unknown>>(error) && error.response?.status === 400) {
    const response = error.response.data;
    const details = typeof response?.error === 'string' ? response.error.trim() : '';
    const message = typeof response?.message === 'string' ? response.message.trim() : '';
    return details || message || fallback;
  }
  return authErrorMessage(error, fallback);
};

export default function Login() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const options = useAuthStore(state => state.options);
  const loadOptions = useAuthStore(state => state.loadOptions);
  const login = useAuthStore(state => state.login);
  const bootstrap = useAuthStore(state => state.bootstrap);

  const [optionsLoading, setOptionsLoading] = useState(false);
  const [loginLoading, setLoginLoading] = useState(false);
  const [bootstrapLoading, setBootstrapLoading] = useState(false);
  const [pageError, setPageError] = useState('');
  const [loginForm, setLoginForm] = useState({ username: '', password: '' });
  const [bootstrapForm, setBootstrapForm] = useState({
    bootstrap_token: '',
    username: '',
    display_name: '',
    password: '',
  });
  const [passwordConfirmation, setPasswordConfirmation] = useState('');
  const [validationVisible, setValidationVisible] = useState(false);

  const returnTo = normalizeReturnTo(searchParams.get('redirect'));

  const bootstrapErrors = useMemo(() => {
    const errors: Partial<Record<BootstrapField, string>> = {};
    const username = bootstrapForm.username.trim();
    const displayName = bootstrapForm.display_name.trim();

    if (!bootstrapForm.bootstrap_token) {
      errors.bootstrap_token = '请输入部署环境中配置的 Bootstrap Token';
    }
    if (!BOOTSTRAP_USERNAME_PATTERN.test(username)) {
      errors.username =
        '用户名须为 3–64 个字符，以字母或数字开头，且仅含字母、数字、点、下划线或连字符';
    }
    if (!displayName) {
      errors.display_name = '显示名称不能为空';
    } else if (utf8ByteLength(displayName) > 255) {
      errors.display_name = '显示名称的 UTF-8 编码不能超过 255 字节';
    }

    const passwordBytes = utf8ByteLength(bootstrapForm.password);
    if (Array.from(bootstrapForm.password).length < 8 || passwordBytes > 1024) {
      errors.password = '管理员密码至少 8 个字符，UTF-8 编码不能超过 1024 字节';
    } else if (/^\p{Nd}+$/u.test(bootstrapForm.password)) {
      errors.password = '密码不能是纯数字';
    }
    if (!passwordConfirmation) {
      errors.password_confirmation = '请再次输入管理员密码';
    } else if (bootstrapForm.password !== passwordConfirmation) {
      errors.password_confirmation = '两次输入的管理员密码不一致';
    }
    return errors;
  }, [bootstrapForm, passwordConfirmation]);

  const fieldError = (field: BootstrapField) =>
    validationVisible ? bootstrapErrors[field] || '' : '';

  const canSubmitLogin = Boolean(loginForm.username.trim()) && Boolean(loginForm.password);
  const hasLoginMethod = Boolean(
    options?.oidc_enabled || options?.local_login_enabled || options?.bootstrap_available
  );

  const loadAuthOptions = useCallback(async () => {
    setOptionsLoading(true);
    try {
      await loadOptions();
    } catch {
      setPageError('无法加载登录配置，请稍后刷新页面');
    } finally {
      setOptionsLoading(false);
    }
  }, [loadOptions]);

  useEffect(() => {
    void loadAuthOptions();
  }, [loadAuthOptions]);

  const handleLocalLogin = async () => {
    if (!canSubmitLogin || loginLoading) return;
    setPageError('');
    setLoginLoading(true);
    try {
      const authenticated = await login({
        username: loginForm.username.trim(),
        password: loginForm.password,
      });
      setLoginForm(previous => ({ ...previous, password: '' }));
      if (!authenticated) throw new Error('登录后未建立有效会话');
      navigate(returnTo, { replace: true });
    } catch (error) {
      setLoginForm(previous => ({ ...previous, password: '' }));
      setPageError(authErrorMessage(error, '登录失败，请稍后再试'));
    } finally {
      setLoginLoading(false);
    }
  };

  const handleBootstrap = async () => {
    if (bootstrapLoading) return;
    setPageError('');
    setValidationVisible(true);
    if (Object.keys(bootstrapErrors).length > 0) {
      setPageError('请按字段提示修正首次管理员信息');
      return;
    }
    setBootstrapLoading(true);
    const clearSecrets = () => {
      setBootstrapForm(previous => ({ ...previous, bootstrap_token: '', password: '' }));
      setPasswordConfirmation('');
    };
    try {
      const authenticated = await bootstrap({
        bootstrap_token: bootstrapForm.bootstrap_token,
        username: bootstrapForm.username.trim(),
        display_name: bootstrapForm.display_name.trim(),
        password: bootstrapForm.password,
      });
      setValidationVisible(false);
      clearSecrets();
      if (!authenticated) throw new Error('初始化后未建立有效会话');
      navigate(returnTo, { replace: true });
    } catch (error) {
      setValidationVisible(false);
      clearSecrets();
      setPageError(bootstrapErrorMessage(error, '首次管理员创建失败，请稍后再试'));
      if (axios.isAxiosError(error) && error.response?.status === 409) {
        await loadOptions().catch(() => undefined);
      }
    } finally {
      setBootstrapLoading(false);
    }
  };

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'center',
        padding: 24,
        backgroundColor: 'var(--semi-color-fill-0)',
      }}
    >
      <Card style={{ width: 'min(440px, 100%)' }}>
        <div style={{ textAlign: 'center' }}>
          <Title heading={4} style={{ margin: 0 }}>
            Ares 登录
          </Title>
          <Text type='tertiary'>身份与权限由服务端会话确认。</Text>
        </div>

        <Spin spinning={optionsLoading}>
          {pageError ? (
            <Banner type='danger' description={pageError} style={{ marginTop: 16 }} />
          ) : null}

          {options?.oidc_enabled ? (
            <Button
              theme='solid'
              type='primary'
              size='large'
              block
              style={{ marginTop: 16 }}
              onClick={() => window.location.assign(oidcStartURL(returnTo))}
            >
              使用组织账号登录
            </Button>
          ) : null}

          {options?.local_login_enabled ? (
            <>
              {options.oidc_enabled ? <Divider margin='16px'>本地恢复管理员</Divider> : null}
              <form
                onSubmit={event => {
                  event.preventDefault();
                  void handleLocalLogin();
                }}
              >
                <Input
                  aria-label='用户名'
                  autoComplete='username'
                  maxLength={128}
                  placeholder='用户名'
                  value={loginForm.username}
                  onChange={(value: string) =>
                    setLoginForm(previous => ({ ...previous, username: value }))
                  }
                />
                <Input
                  aria-label='密码'
                  mode='password'
                  autoComplete='current-password'
                  placeholder='密码'
                  style={{ marginTop: 12 }}
                  value={loginForm.password}
                  onChange={(value: string) =>
                    setLoginForm(previous => ({ ...previous, password: value }))
                  }
                />
                <Button
                  htmlType='submit'
                  theme='solid'
                  block
                  style={{ marginTop: 16 }}
                  loading={loginLoading}
                  disabled={!canSubmitLogin || bootstrapLoading}
                >
                  本地登录
                </Button>
              </form>
            </>
          ) : null}

          {options?.bootstrap_available ? (
            <>
              <Divider margin='16px'>首次部署管理员</Divider>
              <Banner
                type='warning'
                description='Bootstrap 只能成功一次；完成后请从部署环境中删除 Bootstrap Token。'
              />
              <form
                style={{ marginTop: 16 }}
                onSubmit={event => {
                  event.preventDefault();
                  void handleBootstrap();
                }}
              >
                <div style={{ marginBottom: 12 }}>
                  <Input
                    aria-label='Bootstrap Token'
                    mode='password'
                    autoComplete='off'
                    placeholder='Bootstrap Token'
                    value={bootstrapForm.bootstrap_token}
                    onChange={(value: string) =>
                      setBootstrapForm(previous => ({ ...previous, bootstrap_token: value }))
                    }
                  />
                  {fieldError('bootstrap_token') ? (
                    <Text type='danger' role='alert' style={{ display: 'block', marginTop: 4 }}>
                      {fieldError('bootstrap_token')}
                    </Text>
                  ) : null}
                </div>
                <div style={{ marginBottom: 12 }}>
                  <Input
                    aria-label='管理员用户名'
                    autoComplete='username'
                    maxLength={64}
                    placeholder='管理员用户名'
                    value={bootstrapForm.username}
                    onChange={(value: string) =>
                      setBootstrapForm(previous => ({ ...previous, username: value }))
                    }
                  />
                  <Text type='tertiary' size='small' style={{ display: 'block', marginTop: 4 }}>
                    3–64 个字符，以字母或数字开头，仅可包含字母、数字、点、下划线和连字符。
                  </Text>
                  {fieldError('username') ? (
                    <Text type='danger' role='alert' style={{ display: 'block', marginTop: 4 }}>
                      {fieldError('username')}
                    </Text>
                  ) : null}
                </div>
                <div style={{ marginBottom: 12 }}>
                  <Input
                    aria-label='显示名称'
                    autoComplete='name'
                    maxLength={255}
                    placeholder='显示名称'
                    value={bootstrapForm.display_name}
                    onChange={(value: string) =>
                      setBootstrapForm(previous => ({ ...previous, display_name: value }))
                    }
                  />
                  <Text type='tertiary' size='small' style={{ display: 'block', marginTop: 4 }}>
                    不能为空，UTF-8 编码后不能超过 255 字节。
                  </Text>
                  {fieldError('display_name') ? (
                    <Text type='danger' role='alert' style={{ display: 'block', marginTop: 4 }}>
                      {fieldError('display_name')}
                    </Text>
                  ) : null}
                </div>
                <div style={{ marginBottom: 12 }}>
                  <Input
                    aria-label='管理员密码'
                    mode='password'
                    autoComplete='new-password'
                    placeholder='管理员密码'
                    value={bootstrapForm.password}
                    onChange={(value: string) =>
                      setBootstrapForm(previous => ({ ...previous, password: value }))
                    }
                  />
                  <Text type='tertiary' size='small' style={{ display: 'block', marginTop: 4 }}>
                    至少 8 个字符，不能是纯数字；UTF-8 编码不能超过 1024 字节。
                  </Text>
                  {fieldError('password') ? (
                    <Text type='danger' role='alert' style={{ display: 'block', marginTop: 4 }}>
                      {fieldError('password')}
                    </Text>
                  ) : null}
                </div>
                <div style={{ marginBottom: 12 }}>
                  <Input
                    aria-label='再次输入管理员密码'
                    mode='password'
                    autoComplete='new-password'
                    placeholder='再次输入管理员密码'
                    value={passwordConfirmation}
                    onChange={setPasswordConfirmation}
                  />
                  {fieldError('password_confirmation') ? (
                    <Text type='danger' role='alert' style={{ display: 'block', marginTop: 4 }}>
                      {fieldError('password_confirmation')}
                    </Text>
                  ) : null}
                </div>
                <Button
                  htmlType='submit'
                  theme='solid'
                  type='primary'
                  block
                  loading={bootstrapLoading}
                  disabled={bootstrapLoading || loginLoading}
                >
                  创建首次管理员并登录
                </Button>
              </form>
            </>
          ) : null}

          {options && !hasLoginMethod ? (
            <Empty
              style={{ padding: 24 }}
              description='服务端尚未启用可用的登录方式'
              imageStyle={{ width: 72, height: 72 }}
            />
          ) : null}
        </Spin>
      </Card>
    </div>
  );
}
