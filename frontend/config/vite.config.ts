import { defineConfig, loadEnv } from 'vite';
import vue from '@vitejs/plugin-vue';
import path from 'path';
import fs from 'fs';
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

      // 健康检测中间件 - 放在最前面，优先级最高
      server.middlewares.use((req: any, res: any, next: any) => {
        // 只处理健康检测路径
        if (req.url !== '/ttpai/inside/checkup') {
          return next();
        }

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
        } catch (error: any) {
          console.error('Error in health check middleware:', error);
          res.writeHead(500, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Internal server error', details: error.message }));
        }
      });

      // 静态资源服务中间件 - 处理 /assets/ 路径（必须在 SPA 回退之前）
      server.middlewares.use((req: any, res: any, next: any) => {
        const url = req.url;

        if (url.startsWith('/assets/')) {
          console.log(`[${new Date().toISOString()}] Serving static asset: ${req.method} ${url}`);

          // 移除 /assets/ 前缀，直接映射到 dist/assets/
          const filePath = path.join(process.cwd(), 'dist', url);

          if (fs.existsSync(filePath)) {
            const ext = path.extname(filePath);
            let contentType = 'application/octet-stream';

            // 设置正确的 Content-Type
            if (ext === '.js') {
              contentType = 'application/javascript';
            } else if (ext === '.css') {
              contentType = 'text/css';
            } else if (ext === '.svg') {
              contentType = 'image/svg+xml';
            }

            const content = fs.readFileSync(filePath);
            res.writeHead(200, {
              'Content-Type': contentType,
              'Cache-Control': 'public, max-age=31536000',
            });
            res.end(content);
          } else {
            console.error('Static asset not found:', filePath);
            res.writeHead(404, { 'Content-Type': 'text/plain' });
            res.end('Asset not found');
          }
          return;
        }

        next();
      });

      // 开发模式的 SPA 回退中间件 - 只在 vite preview 不可用时使用
      server.middlewares.use((req: any, res: any, next: any) => {
        const url = req.url;

        // 跳过健康检测接口
        if (url === '/ttpai/inside/checkup') {
          return next();
        }

        // 跳过API代理
        if (url.startsWith('/api/')) {
          return next();
        }

        // 跳过静态资源文件
        if (url.startsWith('/assets/')) {
          return next();
        }

        // 跳过 Vite 内部路径和资源
        if (
          url.startsWith('/@') ||
          url.includes('/@') ||
          url.includes('/node_modules/') ||
          url.includes('__vite') ||
          url.includes('?') ||
          (url.includes('.') && !url.includes('?'))
        ) {
          return next();
        }

        // 对于所有其他路由，返回index.html让Vue Router处理
        console.log(`[${new Date().toISOString()}] Dev SPA fallback for: ${req.method} ${url}`);

        // 读取并返回index.html
        let indexPath = path.resolve(process.cwd(), 'index.html');
        let html = '';

        // 如果根目录没有 index.html，尝试使用 dist/index.html
        if (!fs.existsSync(indexPath)) {
          indexPath = path.resolve(process.cwd(), 'dist', 'index.html');
          console.log('Root index.html not found, trying dist/index.html at:', indexPath);
        }

        if (fs.existsSync(indexPath)) {
          html = fs.readFileSync(indexPath, 'utf-8');
          res.writeHead(200, { 'Content-Type': 'text/html' });
          res.end(html);
        } else {
          console.error('index.html not found at:', indexPath);
          res.writeHead(404, { 'Content-Type': 'text/plain' });
          res.end('index.html not found');
        }
      });

      console.log('=== Health check middleware configured ===');
    },
    configurePreviewServer(server: any) {
      console.log('=== Health check plugin loaded for preview ===');

      // 为preview模式添加健康检测 - 放在最前面
      server.middlewares.use((req: any, res: any, next: any) => {
        // 只处理健康检测路径
        if (req.url !== '/ttpai/inside/checkup') {
          return next();
        }

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
        } catch (error: any) {
          console.error('Error in preview health check middleware:', error);
          res.writeHead(500, { 'Content-Type': 'application/json' });
          res.end(JSON.stringify({ error: 'Internal server error', details: error.message }));
        }
      });

      // Preview模式的SPA回退中间件 - 必须保留，因为 vite preview 不会自动处理 SPA 路由
      server.middlewares.use((req: any, res: any, next: any) => {
        const url = req.url;

        // 跳过健康检测接口
        if (url === '/ttpai/inside/checkup') {
          return next();
        }

        // 跳过API代理
        if (url.startsWith('/api/')) {
          return next();
        }

        // 跳过静态资源文件
        if (url.startsWith('/assets/')) {
          return next();
        }

        // 对于所有其他路由，返回index.html让Vue Router处理
        console.log(`[${new Date().toISOString()}] Preview SPA fallback for: ${req.method} ${url}`);

        // 在生产环境中使用 server.transformIndexHtml 来处理 index.html
        try {
          const htmlPath = path.join(process.cwd(), 'dist', 'index.html');
          if (fs.existsSync(htmlPath)) {
            let html = fs.readFileSync(htmlPath, 'utf-8');
            // 使用 Vite 的 transformIndexHtml 方法来处理 HTML
            if (server.transformIndexHtml) {
              server
                .transformIndexHtml(url, html)
                .then((transformedHtml: string) => {
                  res.writeHead(200, { 'Content-Type': 'text/html' });
                  res.end(transformedHtml);
                })
                .catch((err: any) => {
                  console.error('Error transforming index.html:', err);
                  res.writeHead(200, { 'Content-Type': 'text/html' });
                  res.end(html);
                });
            } else {
              res.writeHead(200, { 'Content-Type': 'text/html' });
              res.end(html);
            }
          } else {
            console.error('dist/index.html not found at:', htmlPath);
            res.writeHead(404, { 'Content-Type': 'text/plain' });
            res.end('index.html not found');
          }
        } catch (error: any) {
          console.error('Error in preview SPA fallback:', error);
          res.writeHead(500, { 'Content-Type': 'text/plain' });
          res.end('Internal server error');
        }
      });
    },
  };

  const plugins = [vue(), healthCheckPlugin];

  // 构建分析插件 - 只在需要分析时导入
  if (shouldAnalyze) {
    import('rollup-plugin-visualizer').then(({ visualizer }) => {
      plugins.push(
        visualizer({
          filename: 'dist/stats.html',
          gzipSize: true,
          brotliSize: true,
        }) as any
      );
    });
  }

  return {
    plugins,
    // 设置根目录
    root: process.cwd(),
    server: {
      port: 8080,
      hmr: {
        overlay: true,
      },
      watch: {
        usePolling: true,
      },
      // 添加调试信息
      strictPort: true,
      cors: true,
      // 允许的主机名
      allowedHosts: [
        'localhost',
        '127.0.0.1',
        'chaoscanvas.ttpai.top',
        '.ttpai.top', // 允许所有ttpai.top子域名
        '.ttpai.fun',
        '.ttpai.xyz',
        'all', // 允许所有主机（生产环境推荐）
      ],
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
        '@': path.resolve(__dirname, '../app/web'),
        '@components': path.resolve(__dirname, '../app/web/components'),
        '@assets': path.resolve(__dirname, '../app/web/assets'),
        '@services': path.resolve(__dirname, '../app/web/services'),
        '@utils': path.resolve(__dirname, '../app/web/utils'),
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

      // Terser 压缩配置 - 只在生产环境使用
      ...(isProduction && {
        terserOptions: {
          compress: {
            drop_console: true,
            drop_debugger: true,
          },
        },
      }),
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
