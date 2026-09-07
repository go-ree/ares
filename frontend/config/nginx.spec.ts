import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const nginx = readFileSync(resolve(process.cwd(), 'nginx.conf'), 'utf8');

describe('nginx task-step SSE proxy', () => {
  it('routes the canonical dynamic stream before the generic API prefix', () => {
    const streamLocation = nginx.indexOf(
      'location ~ "^/api/v1/tasks/[1-9][0-9]*/steps/[^/]+/logs/stream$"'
    );
    const genericApiLocation = nginx.indexOf('location /api/');

    expect(streamLocation).toBeGreaterThan(-1);
    expect(genericApiLocation).toBeGreaterThan(streamLocation);
    expect(nginx).not.toContain('location ^~ /api/');
  });

  it('preserves queries and disables buffering and compression for the canonical stream', () => {
    const start = nginx.indexOf('location ~ "^/api/v1/tasks/[1-9][0-9]*/steps/[^/]+/logs/stream$"');
    const end = nginx.indexOf('\n    }', start);
    const block = nginx.slice(start, end);

    expect(block).toContain('proxy_pass $ares_upstream$request_uri;');
    // Do not rewrite this header through $http_last_event_id: Nginx joins
    // duplicate values into one string and would bypass the backend's
    // single-value validation. Default proxy forwarding preserves both the
    // normal single value and duplicate header shape.
    expect(block).not.toContain('proxy_set_header Last-Event-ID');
    expect(block).toContain('proxy_buffering off;');
    expect(block).toContain('proxy_request_buffering off;');
    expect(block).toContain('proxy_cache off;');
    expect(block).toContain('gzip off;');
    expect(block).toContain('add_header X-Accel-Buffering "no" always;');
  });
});
