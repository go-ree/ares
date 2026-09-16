import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

// React + Semi stack (ADR-0008). It builds to its own output directory and entry
// so the Vue build stays byte-identical until the B5 entry switch.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');

  return {
    root: path.resolve(import.meta.dirname, '..'),
    plugins: [react()],
    server: {
      port: 8081,
      host: '127.0.0.1',
      proxy: {
        '/api': {
          target: env.VITE_API_BASE_URL || 'http://127.0.0.1:8080',
          changeOrigin: true,
          secure: false,
          rewrite: requestPath => requestPath,
        },
      },
    },
    resolve: {
      alias: {
        '@shared': path.resolve(import.meta.dirname, '../app/shared'),
        '@react': path.resolve(import.meta.dirname, '../app/web-react'),
        // Semi ships its aggregate stylesheet at dist/css/semi.min.css but does
        // not expose ./dist/* in its exports map, so the documented import path
        // fails to resolve. Alias the specifier to the real file instead of
        // importing out of node_modules by a fragile relative path.
        '@douyinfe/semi-ui-19/dist/css/semi.min.css': path.resolve(
          import.meta.dirname,
          '../node_modules/@douyinfe/semi-ui-19/dist/css/semi.min.css'
        ),
      },
    },
    build: {
      target: 'es2020',
      outDir: 'dist-react',
      emptyOutDir: true,
      sourcemap: mode !== 'production',
      rollupOptions: {
        input: path.resolve(import.meta.dirname, '../react.html'),
        output: {
          // This Vite release rejects the object form of manualChunks.
          manualChunks(id: string) {
            if (
              id.includes('node_modules/react-router') ||
              /node_modules\/react(-dom)?\//.test(id)
            ) {
              return 'react';
            }
            if (id.includes('@douyinfe')) return 'semi';
            return undefined;
          },
        },
      },
    },
  };
});
