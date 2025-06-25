import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import path from 'path';
import { visualizer } from 'rollup-plugin-visualizer';
import type { Connect } from 'vite';

// https://vite.dev/config/
export default defineConfig(({ command, mode }) => {
  // 加载环境变量
  const env = loadEnv(mode, process.cwd(), '');
  const isProduction = mode === 'production';
  const shouldAnalyze = env.ANALYZE === 'true';

  // 设置 NODE_ENV
  process.env.NODE_ENV = isProduction ? 'production' : 'development';

  // 健康检测插件
  const healthCheckPlugin = {
    name: 'health-check',
    configureServer(server: any) {
      console.log('=== Health check plugin loaded ===');
      console.log('Server config:', {
        port: server.config?.server?.port,
        host: server.config?.server?.host,
        mode: server.config?.mode,
      });

      // 健康检测中间件 - 放在最前面
      server.middlewares.use('/ttpai/inside/checkup', (req: any, res: any, next: any) => {
        console.log('=== Health check endpoint hit ===');
        console.log(`[${new Date().toISOString()}] ${req.method} ${req.url}`);

        try {
          // 设置 CORS 头
          res.setHeader('Access-Control-Allow-Origin', '*');
          res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
          res.setHeader('Access-Control-Allow-Headers', 'Content-Type');

          // 处理 OPTIONS 请求
          if (req.method === 'OPTIONS') {
            console.log('Handling OPTIONS request');
            res.writeHead(200);
            res.end();
            return;
          }

          // 返回健康检测响应
          const response = {
            status: 'ok',
            timestamp: new Date().toISOString(),
            service: 'chaoscanvas',
            version: process.env.npm_package_version || '0.0.0',
            environment: process.env.NODE_ENV || 'development',
            hostname: process.env.HOSTNAME || 'unknown',
            nodeEnv: process.env.NODE_ENV,
            port: server.config?.server?.port,
            mode: server.config?.mode,
          };

          console.log('Health check response:', response);
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify(response));
        } catch (error) {
          console.error('Error in health check middleware:', error);
          res.writeHead(500, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Internal server error', details: error.message }));
        }
      });

      console.log('=== Health check middleware configured ===');
    },
    configurePreviewServer(server: any) {
      console.log('=== Health check plugin loaded for preview ===');

      // 为preview模式添加健康检测
      server.middlewares.use('/ttpai/inside/checkup', (req: any, res: any, next: any) => {
        console.log('=== Preview Health check endpoint hit ===');

        try {
          // 设置 CORS 头
          res.setHeader('Access-Control-Allow-Origin', '*');
          res.setHeader('Access-Control-Allow-Methods', 'GET, POST, OPTIONS');
          res.setHeader('Access-Control-Allow-Headers', 'Content-Type');

          // 处理 OPTIONS 请求
          if (req.method === 'OPTIONS') {
            res.writeHead(200);
            res.end();
            return;
          }

          // 返回健康检测响应
          const response = {
            status: 'ok',
            timestamp: new Date().toISOString(),
            service: 'chaoscanvas',
            version: process.env.npm_package_version || '0.0.0',
            environment: 'production',
            hostname: process.env.HOSTNAME || 'unknown',
            nodeEnv: 'production',
            port: 8080,
            mode: 'production',
          };

          console.log('Preview Health check response:', response);
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify(response));
        } catch (error) {
          console.error('Error in preview health check middleware:', error);
          res.writeHead(500, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Internal server error', details: error.message }));
        }
      });
    },
  };

  const plugins = [vue(), healthCheckPlugin];

  // 构建分析插件
  if (shouldAnalyze) {
    plugins.push(
      visualizer({
        filename: 'dist/stats.html',
        open: true,
        gzipSize: true,
        brotliSize: true,
      }) as any
    );
  }

  return {
    plugins,
    // 设置根目录
    root: process.cwd(),
    server: {
      port: 8080,
      open: true,
      hmr: {
        overlay: true,
      },
      watch: {
        usePolling: true,
      },
      // 添加调试信息
      strictPort: true,
      cors: true,
      proxy: {
        '/api': {
          target: env.VITE_API_BASE_URL || 'http://ares.ttpai.top',
          changeOrigin: true,
          secure: false,
          rewrite: path => path,
        },
      },
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './app/web'),
        '@components': path.resolve(__dirname, './app/web/components'),
        '@assets': path.resolve(__dirname, './app/web/assets'),
        '@services': path.resolve(__dirname, './app/web/services'),
        '@utils': path.resolve(__dirname, './app/web/utils'),
      },
    },
    build: {
      // 构建优化配置
      target: 'es2015',
      outDir: 'dist',
      assetsDir: 'assets',
      minify: isProduction ? 'terser' : false,
      sourcemap: !isProduction,
      chunkSizeWarningLimit: 1000,

      // Rollup 配置
      rollupOptions: {
        output: {
          // 手动分包
          manualChunks: {
            vendor: ['vue', 'vue-router', 'pinia'],
            elementPlus: ['element-plus', '@element-plus/icons-vue'],
            utils: ['axios'],
          },
          // 静态资源命名规则
          chunkFileNames: isProduction ? 'assets/js/[name]-[hash].js' : 'assets/js/[name].js',
          entryFileNames: isProduction ? 'assets/js/[name]-[hash].js' : 'assets/js/[name].js',
          assetFileNames: isProduction
            ? 'assets/[ext]/[name]-[hash].[ext]'
            : 'assets/[ext]/[name].[ext]',
        },
      },

      // Terser 压缩配置
      terserOptions: isProduction
        ? {
            compress: {
              drop_console: true,
              drop_debugger: true,
            },
          }
        : {},
    },

    // 环境变量配置
    define: {
      __APP_VERSION__: JSON.stringify(process.env.npm_package_version),
      __BUILD_TIME__: JSON.stringify(new Date().toISOString()),
    },

    // CSS 预处理器配置
    css: {
      preprocessorOptions: {
        scss: {
          additionalData: `@import "@/assets/styles/variables.scss";`,
        },
      },
    },
  };
});
