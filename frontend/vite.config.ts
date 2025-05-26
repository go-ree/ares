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
      '/api': {
        target: 'http://ares.ttpai.top',
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
