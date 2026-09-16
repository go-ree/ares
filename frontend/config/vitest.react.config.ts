import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// React project of the dual-stack transition (ADR-0008). The Vue project keeps
// config/vitest.config.ts; both stay green until B5 deletes the Vue stack.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@shared': fileURLToPath(new URL('../app/shared', import.meta.url)),
      '@react': fileURLToPath(new URL('../app/web-react', import.meta.url)),
    },
  },
  test: {
    include: ['app/web-react/**/*.spec.{ts,tsx}'],
    environment: 'jsdom',
    setupFiles: [fileURLToPath(new URL('./vitest.react.setup.ts', import.meta.url))],
    clearMocks: true,
    restoreMocks: true,
  },
});
