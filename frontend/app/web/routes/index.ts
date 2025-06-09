import { createRouter, createWebHistory } from 'vue-router'
import MainLayout from '../components/layout/MainLayout.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: MainLayout,
      children: [
        {
          path: '',
          name: 'Home',
          component: () => import('../views/Home.vue')
        },
        {
          path: '/publish/deploy',
          component: () => import('../views/publish/Deploy.vue')
        },
        {
          path: '/publish/merge',
          component: () => import('../views/publish/Merge.vue')
        },
        {
          path: '/operation/log',
          component: () => import('../views/operation/Log.vue')
        },
        {
          path: '/operation/monitor',
          component: () => import('../views/operation/Monitor.vue')
        },
        {
          path: '/system/settings',
          component: () => import('../views/system/Settings.vue')
        },
        {
          path: '/system/version',
          component: () => import('../views/system/Version.vue')
        }
      ]
    },
    {
      path: '/login',
      component: () => import('../views/Login.vue')
    }
  ]
})

export default router 