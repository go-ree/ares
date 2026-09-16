import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { RouterProvider } from 'react-router';
// The React 19 adapter must be imported before any Semi component: it wires
// createRoot into Semi's global config so imperative portals (Toast, Modal,
// Notification) can mount under React 19.
import '@douyinfe/semi-ui-19/react19-adapter';
import { ConfigProvider } from '@douyinfe/semi-ui-19';
import zhCN from '@douyinfe/semi-ui-19/lib/es/locale/source/zh_CN';
import '@douyinfe/semi-ui-19/dist/css/semi.min.css';
import { configureApiAuth } from '@shared/config/api';
import { normalizeReturnTo } from '@shared/utils/return-to';
import { createAppRouter } from './routes';
import { useAuthStore } from './stores/auth';

const router = createAppRouter();
let redirectingToLogin = false;

// Same policy as the Vue entry: a 401 invalidates the identity and returns to
// login, while a 403 is an authorization decision that must not log the user out.
const redirectToLogin = () => {
  if (redirectingToLogin) return;
  const { pathname, search } = router.state.location;
  if (pathname === '/login') return;
  redirectingToLogin = true;
  const redirect = normalizeReturnTo(`${pathname}${search}`);
  void router
    .navigate(`/login?redirect=${encodeURIComponent(redirect)}`, { replace: true })
    .finally(() => {
      redirectingToLogin = false;
    });
};

configureApiAuth({
  getCsrfToken: () => useAuthStore.getState().csrfToken,
  onUnauthorized: async () => {
    useAuthStore.getState().invalidate('unauthenticated');
    redirectToLogin();
  },
  onForbidden: () => undefined,
});

// A background invalidation (expiration timer, revocation elsewhere) must leave
// the protected page instead of rendering it with an empty identity.
useAuthStore.subscribe((state, previous) => {
  if (state.status === 'anonymous' && previous.status === 'authenticated') redirectToLogin();
});

// Remove identity artifacts written by pre-session versions of the frontend.
try {
  window.localStorage.removeItem('userInfo');
  window.localStorage.removeItem('token');
} catch {
  // Storage can be disabled by browser policy; authentication never depends on it.
}

const container = document.getElementById('root');
if (!container) throw new Error('缺少根节点 #root');

createRoot(container).render(
  <StrictMode>
    <ConfigProvider locale={zhCN}>
      <RouterProvider router={router} />
    </ConfigProvider>
  </StrictMode>
);
