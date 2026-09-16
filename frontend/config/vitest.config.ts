import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';
import vue from '@vitejs/plugin-vue';

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('../app/web', import.meta.url)),
      '@shared': fileURLToPath(new URL('../app/shared', import.meta.url)),
    },
  },
  test: {
    // The Vue project must not pick up the React stack's specs; each stack has
    // its own config and gate until B5 removes the Vue one.
    include: ['app/web/**/*.spec.ts', 'app/shared/**/*.spec.ts', 'config/**/*.spec.ts'],
    environment: 'happy-dom',
    setupFiles: [fileURLToPath(new URL('./vitest.setup.ts', import.meta.url))],
    clearMocks: true,
    restoreMocks: true,
  },
});
