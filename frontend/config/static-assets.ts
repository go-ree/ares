import fs from 'node:fs';
import path from 'node:path';
import type { IncomingMessage, ServerResponse } from 'node:http';
import type { Connect } from 'vite';

const assetContentTypes: Record<string, string> = {
  '.css': 'text/css',
  '.js': 'application/javascript',
  '.svg': 'image/svg+xml',
};

const notFound = (response: ServerResponse) => {
  response.writeHead(404, { 'Content-Type': 'text/plain; charset=utf-8' });
  response.end('Asset not found');
};

const isContainedPath = (root: string, candidate: string) =>
  candidate.startsWith(root.endsWith(path.sep) ? root : root + path.sep);

// Vite normally owns source assets. This middleware exists only for legacy
// pages that refer to an already-built dist asset while the development server
// is running. Treat the URL as untrusted: decoded dot segments, platform path
// separators, and symlinks must never escape dist/assets.
export const createDistAssetMiddleware = (
  assetRoot = path.resolve(process.cwd(), 'dist', 'assets')
) => {
  const resolvedRoot = path.resolve(assetRoot);

  return (request: IncomingMessage, response: ServerResponse, next: Connect.NextFunction) => {
    const rawURL = request.url || '';
    const rawPath = rawURL.split(/[?#]/, 1)[0];
    if (!rawPath.startsWith('/assets/')) {
      next();
      return;
    }
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      response.setHeader('Allow', 'GET, HEAD');
      response.writeHead(405, { 'Content-Type': 'text/plain; charset=utf-8' });
      response.end('Method not allowed');
      return;
    }

    let decodedPath: string;
    try {
      decodedPath = decodeURIComponent(rawPath);
    } catch {
      notFound(response);
      return;
    }
    const relativePath = decodedPath.slice('/assets/'.length);
    const segments = relativePath.split('/');
    if (
      relativePath === '' ||
      relativePath.includes('\0') ||
      relativePath.includes('\\') ||
      path.isAbsolute(relativePath) ||
      segments.some(segment => segment === '' || segment === '.' || segment === '..')
    ) {
      notFound(response);
      return;
    }

    const candidate = path.resolve(resolvedRoot, ...segments);
    if (!isContainedPath(resolvedRoot, candidate)) {
      notFound(response);
      return;
    }

    try {
      const realRoot = fs.realpathSync.native(resolvedRoot);
      const realCandidate = fs.realpathSync.native(candidate);
      if (!isContainedPath(realRoot, realCandidate) || !fs.statSync(realCandidate).isFile()) {
        notFound(response);
        return;
      }
      const content = fs.readFileSync(realCandidate);
      const contentType =
        assetContentTypes[path.extname(realCandidate)] || 'application/octet-stream';
      response.writeHead(200, {
        'Cache-Control': 'public, max-age=31536000',
        'Content-Length': String(content.length),
        'Content-Type': contentType,
        'X-Content-Type-Options': 'nosniff',
      });
      response.end(request.method === 'HEAD' ? undefined : content);
    } catch {
      notFound(response);
    }
  };
};
