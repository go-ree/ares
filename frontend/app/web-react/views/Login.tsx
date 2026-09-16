import { useState } from 'react';
import { Button, Card, Input, Typography } from '@douyinfe/semi-ui-19';
import { useNavigate, useSearchParams } from 'react-router';
import { getPasswordChangeErrorMessage } from '@shared/services/auth';
import { normalizeReturnTo } from '@shared/utils/return-to';
import { useAuthStore } from '../stores/auth';

const { Title, Text } = Typography;

/**
 * B0 minimal login. It exists so the unauthenticated guard has a real target;
 * B1 completes it with the full Semi form, bootstrap flow, OIDC entry and the
 * shared error presentation used by the Vue page.
 */
export default function Login() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const login = useAuthStore(state => state.login);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending) return;
    setPending(true);
    setError(null);
    try {
      const authenticated = await login({ username, password });
      setPending(false);
      if (authenticated) {
        navigate(normalizeReturnTo(searchParams.get('redirect')), { replace: true });
      } else {
        setError('登录失败，请检查用户名或密码。');
      }
    } catch (loginError) {
      setPending(false);
      setError(getPasswordChangeErrorMessage(loginError) || '登录失败，请检查用户名或密码。');
    }
  };

  return (
    <div style={{ display: 'flex', justifyContent: 'center', paddingTop: 96 }}>
      <Card style={{ width: 360 }}>
        <Title heading={5}>登录 Ares</Title>
        <form onSubmit={submit} style={{ marginTop: 16 }}>
          <Input
            aria-label='用户名'
            placeholder='用户名'
            value={username}
            onChange={setUsername}
            autoComplete='username'
          />
          <Input
            aria-label='密码'
            mode='password'
            placeholder='密码'
            value={password}
            onChange={setPassword}
            autoComplete='current-password'
            style={{ marginTop: 12 }}
          />
          {error ? (
            <Text type='danger' style={{ display: 'block', marginTop: 12 }}>
              {error}
            </Text>
          ) : null}
          <Button
            theme='solid'
            type='primary'
            htmlType='submit'
            loading={pending}
            block
            style={{ marginTop: 16 }}
          >
            登录
          </Button>
        </form>
      </Card>
    </div>
  );
}
