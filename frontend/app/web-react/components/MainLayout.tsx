import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Avatar,
  Banner,
  Dropdown,
  Form,
  Input,
  Layout,
  Modal,
  Nav,
  Toast,
} from '@douyinfe/semi-ui-19';
import { IconChevronDown, IconExit, IconKey } from '@douyinfe/semi-icons';
import { Outlet, useLocation, useNavigate } from 'react-router';
import { getPasswordChangeErrorMessage } from '@shared/services/auth';
import type { Permission } from '@shared/types/auth';
import { useAuthStore } from '../stores/auth';
import { isMigrated } from '../routes/migration';
import { toNavItems, visibleMenuItems } from '../menu/mainMenu';

const { Header, Sider, Content } = Layout;

/** Sider auto-collapse threshold, matching the Vue shell. */
const COLLAPSE_THRESHOLD = 1200;

export default function MainLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const user = useAuthStore(state => state.user);
  const permissions = useAuthStore(state => state.user?.permissions);
  const logout = useAuthStore(state => state.logout);
  const changePassword = useAuthStore(state => state.changePassword);

  const [collapsed, setCollapsed] = useState(false);
  const [logoutLoading, setLogoutLoading] = useState(false);
  const [passwordVisible, setPasswordVisible] = useState(false);
  const [passwordSubmitting, setPasswordSubmitting] = useState(false);
  const [passwordForm, setPasswordForm] = useState({ current: '', next: '', confirmation: '' });

  useEffect(() => {
    const handleResize = () => setCollapsed(window.innerWidth < COLLAPSE_THRESHOLD);
    handleResize();
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  const canAny = useCallback(
    (required: Permission[]) =>
      required.some(permission => (permissions || []).includes(permission)),
    [permissions]
  );
  const navItems = useMemo(() => toNavItems(visibleMenuItems(canAny)), [canAny]);
  const userDisplayName = user?.display_name || user?.username || '未登录';
  const userInitials = userDisplayName.trim().slice(0, 2).toUpperCase();
  const canChangePassword = user?.auth_source === 'bootstrap';

  const handleLogout = async () => {
    if (logoutLoading) return;
    setLogoutLoading(true);
    try {
      await logout();
      navigate('/login', { replace: true });
    } catch {
      Toast.error('退出登录失败，会话尚未确认撤销，请重试');
    } finally {
      setLogoutLoading(false);
    }
  };

  const submitPasswordChange = async () => {
    if (passwordSubmitting) return;
    const { current, next, confirmation } = passwordForm;
    if (!current || !next || !confirmation) {
      Toast.warning('请完整填写当前密码、新密码和确认密码');
      return;
    }
    const passwordBytes = new TextEncoder().encode(next).byteLength;
    if (Array.from(next).length < 8 || passwordBytes > 1024) {
      Toast.warning('新密码至少 8 个字符，UTF-8 编码不能超过 1024 字节');
      return;
    }
    if (/^\p{Nd}+$/u.test(next)) {
      Toast.warning('密码不能是纯数字');
      return;
    }
    if (next !== confirmation) {
      Toast.warning('两次输入的新密码不一致');
      return;
    }
    if (current === next) {
      Toast.warning('新密码不能与当前密码相同');
      return;
    }

    setPasswordSubmitting(true);
    try {
      await changePassword({ current_password: current, new_password: next });
      setPasswordVisible(false);
      Toast.success('密码已更新，请重新登录');
      navigate('/login', { replace: true });
    } catch (error) {
      Toast.error(getPasswordChangeErrorMessage(error));
    } finally {
      setPasswordSubmitting(false);
    }
  };

  return (
    <Layout style={{ height: '100vh' }}>
      <Header
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          height: 60,
          padding: '0 20px',
          backgroundColor: 'var(--semi-color-bg-1)',
          boxShadow: '0 1px 4px rgba(0, 21, 41, 0.08)',
          position: 'fixed',
          top: 0,
          left: 0,
          right: 0,
          zIndex: 1000,
        }}
      >
        <span style={{ fontSize: 18, fontWeight: 600 }}>Ares</span>
        <Dropdown
          trigger='click'
          position='bottomRight'
          render={
            <Dropdown.Menu>
              {canChangePassword ? (
                <Dropdown.Item icon={<IconKey />} onClick={() => setPasswordVisible(true)}>
                  修改密码
                </Dropdown.Item>
              ) : null}
              <Dropdown.Item disabled={logoutLoading} icon={<IconExit />} onClick={handleLogout}>
                {logoutLoading ? '正在退出…' : '退出登录'}
              </Dropdown.Item>
            </Dropdown.Menu>
          }
        >
          <span
            style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}
            aria-label='用户菜单'
          >
            <Avatar size='small'>{userInitials}</Avatar>
            <span style={{ fontSize: 14 }}>{userDisplayName}</span>
            <IconChevronDown />
          </span>
        </Dropdown>
      </Header>
      <Layout hasSider style={{ marginTop: 60, height: 'calc(100vh - 60px)' }}>
        <Sider
          style={{
            width: collapsed ? 64 : 200,
            flex: '0 0 auto',
            overflow: 'auto',
            backgroundColor: 'var(--semi-color-bg-1)',
            transition: 'width 0.3s',
          }}
        >
          <Nav
            style={{ height: '100%' }}
            isCollapsed={collapsed}
            onCollapseChange={setCollapsed}
            selectedKeys={[location.pathname]}
            items={navItems}
            footer={{ collapseButton: true }}
            onSelect={({ itemKey }) => {
              const target = String(itemKey);
              if (isMigrated(target) && target !== location.pathname) navigate(target);
            }}
          />
        </Sider>
        <Content style={{ padding: 20, overflow: 'auto' }}>
          <Outlet />
        </Content>
      </Layout>

      <Modal
        title='修改密码'
        visible={passwordVisible}
        confirmLoading={passwordSubmitting}
        maskClosable={!passwordSubmitting}
        closeOnEsc={!passwordSubmitting}
        closable={!passwordSubmitting}
        okText='确认修改'
        onOk={submitPasswordChange}
        onCancel={() => {
          if (!passwordSubmitting) setPasswordVisible(false);
        }}
        afterClose={() => {
          // Semi has no destroyOnClose; reset once the close animation ended.
          setPasswordForm({ current: '', next: '', confirmation: '' });
        }}
      >
        <Banner
          type='warning'
          bordered
          description='修改成功后会撤销该账号的全部会话，需要使用新密码重新登录。'
        />
        <Form labelPosition='top' style={{ marginTop: 16 }}>
          <Form.Slot label='当前密码'>
            <Input
              mode='password'
              autoComplete='current-password'
              disabled={passwordSubmitting}
              value={passwordForm.current}
              onChange={(value: string) =>
                setPasswordForm(previous => ({ ...previous, current: value }))
              }
            />
          </Form.Slot>
          <Form.Slot label='新密码'>
            <Input
              mode='password'
              autoComplete='new-password'
              disabled={passwordSubmitting}
              value={passwordForm.next}
              onChange={(value: string) =>
                setPasswordForm(previous => ({ ...previous, next: value }))
              }
            />
          </Form.Slot>
          <Form.Slot label='确认新密码'>
            <Input
              mode='password'
              autoComplete='new-password'
              disabled={passwordSubmitting}
              value={passwordForm.confirmation}
              onChange={(value: string) =>
                setPasswordForm(previous => ({ ...previous, confirmation: value }))
              }
            />
          </Form.Slot>
        </Form>
      </Modal>
    </Layout>
  );
}
