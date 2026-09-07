import { createRouter, createWebHistory } from 'vue-router';
import type { RouteLocationNormalized, RouteRecordRaw } from 'vue-router';
import MainLayout from '../components/layout/MainLayout.vue';
import { useAuthStore } from '@/stores/auth';
import { PERMISSIONS, type Permission } from '@/types/auth';
import { normalizeReturnTo } from '@/utils/return-to';

const routes: RouteRecordRaw[] = [
  {
    path: '/',
    component: MainLayout,
    meta: { requiresAuth: true },
    children: [
      {
        path: '',
        name: 'home',
        component: () => import('../views/Home.vue'),
      },
      {
        path: 'application/list',
        name: 'application-list',
        component: () => import('../views/application/AppList.vue'),
        meta: { requiredPermissions: [PERMISSIONS.APPLICATIONS_READ] },
      },
      {
        path: 'application/apply',
        name: 'application-apply',
        component: () => import('../views/application/AppApply.vue'),
        meta: { requiredPermissions: [PERMISSIONS.APPLICATIONS_WRITE] },
      },
      {
        path: 'application/config',
        redirect: '/application/list',
      },
      {
        path: 'application/:appId',
        name: 'application-detail',
        component: () => import('../views/application/AppDetail.vue'),
        props: true,
        meta: { requiredPermissions: [PERMISSIONS.APPLICATIONS_READ] },
        children: [
          {
            path: '',
            redirect: to => `/application/${to.params.appId}/info`,
          },
          {
            path: 'info',
            name: 'application-info',
            component: () => import('../views/application/detail/AppInfo.vue'),
          },
          {
            path: 'config',
            name: 'application-config',
            component: () => import('../views/application/detail/AppConfigDetail.vue'),
            meta: { requiredPermissions: [PERMISSIONS.APP_CONFIGS_READ] },
          },
          {
            path: 'domains',
            name: 'application-domains',
            component: () => import('../views/application/detail/AppDomains.vue'),
            meta: { requiredPermissions: [PERMISSIONS.DOMAINS_READ] },
          },
          {
            path: 'pods',
            name: 'application-pods',
            component: () => import('../views/application/detail/AppPods.vue'),
            meta: { requiredPermissions: [PERMISSIONS.KUBERNETES_READ] },
          },
        ],
      },
      {
        path: 'publish/deploy',
        name: 'publish-deploy',
        component: () => import('../views/publish/Deploy.vue'),
        meta: { requiredPermissions: [PERMISSIONS.RELEASES_READ] },
      },
      {
        path: 'publish/merge',
        name: 'publish-merge',
        component: () => import('../views/publish/Merge.vue'),
        meta: { requiredPermissions: [PERMISSIONS.RELEASES_CREATE] },
      },
      {
        path: 'operation/log',
        name: 'operation-log',
        component: () => import('../views/operation/Log.vue'),
        meta: { requiredPermissions: [PERMISSIONS.LOGS_READ] },
      },
      {
        path: 'operation/batch-deploy',
        name: 'operation-batch-deploy',
        component: () => import('../views/operation/BatchDeploy.vue'),
        meta: {
          requiredPermissions: [PERMISSIONS.APPLICATIONS_READ, PERMISSIONS.RELEASES_CREATE],
        },
      },
      {
        path: 'operation/monitor',
        name: 'operation-monitor',
        component: () => import('../views/operation/Monitor.vue'),
        meta: { requiredPermissions: [PERMISSIONS.KUBERNETES_READ] },
      },
      {
        path: 'system/settings',
        name: 'system-settings',
        component: () => import('../views/system/Settings.vue'),
        meta: { requiredPermissions: [PERMISSIONS.SYSTEM_SETTINGS_READ] },
      },
      {
        path: 'system/users',
        name: 'system-users',
        component: () => import('../views/system/Users.vue'),
        meta: { requiredPermissions: [PERMISSIONS.USERS_READ] },
      },
      {
        path: 'system/version',
        name: 'system-version',
        component: () => import('../views/system/Version.vue'),
      },
      {
        path: 'forbidden',
        name: 'forbidden',
        component: () => import('../views/Forbidden.vue'),
      },
    ],
  },
  {
    path: '/login',
    name: 'login',
    component: () => import('../views/Login.vue'),
    meta: { requiresAuth: false, publicOnly: true },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('../views/NotFound.vue'),
    meta: { requiresAuth: false },
  },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

const requiredPermissions = (to: RouteLocationNormalized): Permission[] =>
  Array.from(new Set(to.matched.flatMap(record => record.meta.requiredPermissions || [])));

router.beforeEach(async to => {
  const authStore = useAuthStore();
  const requiresAuth = to.matched.some(record => record.meta.requiresAuth === true);

  // A concurrent bootstrap probe may already have put the store into loading.
  // Await the shared flight before deciding that the user is anonymous.
  if (authStore.status === 'unknown' || authStore.status === 'loading') {
    await authStore.ensureSession();
  }

  if (requiresAuth && !authStore.isAuthenticated) {
    return {
      name: 'login',
      query: { redirect: normalizeReturnTo(to.fullPath) },
      replace: true,
    };
  }

  if (to.meta.publicOnly && authStore.isAuthenticated) {
    return normalizeReturnTo(to.query.redirect);
  }

  if (authStore.isAuthenticated) {
    const missingPermission = requiredPermissions(to).some(
      permission => !authStore.can(permission)
    );
    if (missingPermission && to.name !== 'forbidden') {
      return { name: 'forbidden', query: { from: normalizeReturnTo(to.fullPath) }, replace: true };
    }
  }

  return true;
});

export default router;
