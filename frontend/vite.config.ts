import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'path'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 3000,
    open: true,
    hmr: {
      overlay: true,
    },
    watch: {
      usePolling: true,
    },
    proxy: {
      // Ares 服务代理
      '/api/ares': {
        target: 'https://ares.ttpai.top',
        changeOrigin: true,
        secure: false,
        rewrite: (path) => path.replace(/^\/api\/ares/, '/api')
      },
      // 用户服务代理
      '/api/user': {
        target: 'https://user.ttpai.top',
        changeOrigin: true,
        secure: false,
        rewrite: (path) => path.replace(/^\/api\/user/, '/api')
      },
      // 监控服务代理
      '/api/monitor': {
        target: 'https://monitor.ttpai.top',
        changeOrigin: true,
        secure: false,
        rewrite: (path) => path.replace(/^\/api\/monitor/, '/api')
      },
      // 通用 API 代理（保持向后兼容）
      '/api': {
        target: 'https://ares.ttpai.top',
        changeOrigin: true,
        secure: false,
        rewrite: (path) => path
      }
    }
  },
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './app/web'),
      '@components': path.resolve(__dirname, './app/web/components'),
      '@assets': path.resolve(__dirname, './app/web/assets'),
      '@services': path.resolve(__dirname, './app/web/services'),
      '@utils': path.resolve(__dirname, './app/web/utils'),
    }
  }
})
