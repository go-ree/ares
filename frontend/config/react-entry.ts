import type { Plugin } from 'vite';

/**
 * Vite's SPA fallback always chooses the root index.html. During the staged
 * migration that file still boots Vue, so the standalone React dev server must
 * explicitly serve react.html for browser navigations and refreshes.
 */
export const reactEntryTarget = (
  method: string | undefined,
  requestURL: string | undefined,
  accept: string | undefined
): string | null => {
  if (method !== 'GET' || !accept?.toLowerCase().includes('text/html')) return null;
  const url = new URL(requestURL || '/', 'http://localhost');
  if (url.pathname === '/api' || url.pathname.startsWith('/api/')) return null;
  return `/react.html${url.search}`;
};

export const reactEntryFallback = (): Plugin => ({
  name: 'ares-react-entry-fallback',
  configureServer(server) {
    server.middlewares.use((request, _response, next) => {
      const target = reactEntryTarget(
        request.method,
        request.url,
        typeof request.headers.accept === 'string' ? request.headers.accept : undefined
      );
      if (target) request.url = target;
      next();
    });
  },
});
