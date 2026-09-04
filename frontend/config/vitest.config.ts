import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('../app/web', import.meta.url)),
    },
  },
  test: {
    environment: 'happy-dom',
    setupFiles: [fileURLToPath(new URL('./vitest.setup.ts', import.meta.url))],
    clearMocks: true,
    restoreMocks: true,
  },
});
