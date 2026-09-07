import { createApp, watch } from 'vue';
import { createPinia } from 'pinia';
import ElementPlus from 'element-plus';
import 'element-plus/dist/index.css';
import '../../src/style.css';
import App from './App.vue';
import zhCn from 'element-plus/es/locale/lang/zh-cn';
import router from './routes';
import { useAuthStore } from './stores/auth';
import { configureApiAuth } from './config/api';
import { normalizeReturnTo } from './utils/return-to';

// Remove identity artifacts written by pre-session versions of the frontend.
try {
  window.localStorage.removeItem('userInfo');
  window.localStorage.removeItem('token');
} catch {
  // Storage can be disabled by browser policy; authentication never depends on it.
}

// 创建应用实例
const app = createApp(App);

// 创建 Pinia 实例
const pinia = createPinia();

app.use(pinia);
app.use(ElementPlus, {
  locale: zhCn,
});

const authStore = useAuthStore(pinia);
let redirectingToLogin = false;

const redirectToLogin = async () => {
  if (redirectingToLogin || router.currentRoute.value.name === 'login') return;
  redirectingToLogin = true;
  try {
    const redirect = normalizeReturnTo(router.currentRoute.value.fullPath);
    await router.replace({ name: 'login', query: { redirect } });
  } finally {
    redirectingToLogin = false;
  }
};

configureApiAuth({
  getCsrfToken: () => authStore.csrfToken,
  onUnauthorized: async () => {
    authStore.invalidate('unauthenticated');
    await redirectToLogin();
  },
  // A 403 is an authorization or request-policy decision, not a lost session.
  // Keep the current page and let the requesting view present contextual feedback.
  onForbidden: () => undefined,
});

watch(
  () => authStore.status,
  (status, previousStatus) => {
    if (status === 'anonymous' && previousStatus === 'authenticated') void redirectToLogin();
  }
);

app.use(router);

void router.isReady().then(() => app.mount('#app'));
