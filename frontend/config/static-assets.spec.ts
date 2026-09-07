import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import type { AddressInfo } from 'node:net';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { createDistAssetMiddleware } from './static-assets.ts';

describe('dist asset development middleware', () => {
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), 'ares-static-assets-'));
  const assetRoot = path.join(workspace, 'dist', 'assets');
  const secretPath = path.join(workspace, 'secret.txt');
  const appContent = 'console.log("safe asset");';
  const server = http.createServer((request, response) => {
    createDistAssetMiddleware(assetRoot)(request, response, () => {
      response.writeHead(404);
      response.end('next');
    });
  });
  let port = 0;

  beforeAll(async () => {
    fs.mkdirSync(path.join(assetRoot, 'js'), { recursive: true });
    fs.writeFileSync(path.join(assetRoot, 'js', 'app.js'), appContent);
    fs.writeFileSync(secretPath, 'must never be served');
    fs.symlinkSync(secretPath, path.join(assetRoot, 'linked-secret.txt'));
    await new Promise<void>((resolve, reject) => {
      server.once('error', reject);
      server.listen(0, '127.0.0.1', () => resolve());
    });
    port = (server.address() as AddressInfo).port;
  });

  afterAll(async () => {
    await new Promise<void>((resolve, reject) =>
      server.close(error => (error ? reject(error) : resolve()))
    );
    fs.rmSync(workspace, { recursive: true, force: true });
  });

  const request = (requestPath: string, method = 'GET') =>
    new Promise<{ status: number; body: string; headers: http.IncomingHttpHeaders }>(
      (resolve, reject) => {
        const outgoing = http.request(
          { host: '127.0.0.1', port, method, path: requestPath },
          response => {
            const chunks: Buffer[] = [];
            response.on('data', chunk => chunks.push(Buffer.from(chunk)));
            response.on('end', () =>
              resolve({
                status: response.statusCode || 0,
                body: Buffer.concat(chunks).toString(),
                headers: response.headers,
              })
            );
          }
        );
        outgoing.once('error', reject);
        outgoing.end();
      }
    );

  it('serves regular files for GET and HEAD while ignoring the query string', async () => {
    const get = await request('/assets/js/app.js?cache=1');
    expect(get.status).toBe(200);
    expect(get.body).toBe(appContent);
    expect(get.headers['x-content-type-options']).toBe('nosniff');

    const head = await request('/assets/js/app.js', 'HEAD');
    expect(head.status).toBe(200);
    expect(head.body).toBe('');
    expect(head.headers['content-length']).toBe(String(Buffer.byteLength(appContent)));
  });

  it.each([
    '/assets/../../secret.txt',
    '/assets/%2e%2e/%2e%2e/secret.txt',
    '/assets/%2e%2e%2fsecret.txt',
    '/assets/%5c..%5csecret.txt',
    '/assets/linked-secret.txt',
  ])('does not serve traversal or an escaping symlink: %s', async requestPath => {
    const response = await request(requestPath);
    expect(response.status).toBe(404);
    expect(response.body).not.toContain('must never be served');
  });

  it('rejects methods other than GET and HEAD', async () => {
    const response = await request('/assets/js/app.js', 'POST');
    expect(response.status).toBe(405);
    expect(response.headers.allow).toBe('GET, HEAD');
  });
});
