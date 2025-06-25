import { createRouter, createWebHistory } from 'vue-router';
import MainLayout from '../components/layout/MainLayout.vue';
import { useUserStore } from '../stores/user';

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: MainLayout,
      meta: { requiresAuth: true },
      children: [
        {
          path: '',
          name: 'Home',
          component: () => import('../views/Home.vue'),
        },
        {
          path: '/application/list',
          component: () => import('../views/application/AppList.vue'),
        },
        {
          path: '/application/apply',
          component: () => import('../views/application/AppApply.vue'),
        },
        {
          path: '/application/config',
          component: () => import('../views/application/AppConfig.vue'),
        },
        {
          path: '/publish/deploy',
          component: () => import('../views/publish/Deploy.vue'),
        },
        {
          path: '/publish/merge',
          component: () => import('../views/publish/Merge.vue'),
        },
        {
          path: '/operation/log',
          component: () => import('../views/operation/Log.vue'),
        },
        {
          path: '/operation/monitor',
          component: () => import('../views/operation/Monitor.vue'),
        },
        {
          path: '/system/settings',
          component: () => import('../views/system/Settings.vue'),
        },
        {
          path: '/system/version',
          component: () => import('../views/system/Version.vue'),
        },
      ],
    },
    {
      path: '/login',
      component: () => import('../views/Login.vue'),
      meta: { requiresAuth: false },
    },
  ],
});

router.beforeEach((to, from, next) => {
  const userStore = useUserStore();

  if (to.meta.requiresAuth !== false) {
    if (!userStore.isLoggedIn) {
      console.log('用户未登录，跳转到登录页');
      next('/login');
      return;
    }
  }

  if (to.path === '/login' && userStore.isLoggedIn) {
    console.log('用户已登录，跳转到首页');
    next('/');
    return;
  }

  next();
});

export default router;
