import { describe, expect, it } from 'vitest';
import { normalizeReturnTo } from './return-to';

describe('normalizeReturnTo', () => {
  it('keeps same-origin application paths', () => {
    expect(normalizeReturnTo('/application/42/config?env=prod#workflow')).toBe(
      '/application/42/config?env=prod#workflow'
    );
  });

  it.each([
    'https://evil.example/path',
    '//evil.example/path',
    '/%2f%2fevil.example/path',
    '/%252f%252fevil.example/path',
    '/%25252525252f%25252525252fevil.example/path',
    '/%2e%2e//evil.example/path',
    '/safe/%2e%2e/%2e%2e//evil.example/path',
    '/%2e%2e/%2f%2fevil.example/path',
    '/%2e%2e/%252f%252fevil.example/path',
    '/\\evil.example/path',
    '/%5cevil.example/path',
    '/bad%encoding',
    '/login?redirect=/application/list',
    '/api/v1/auth/logout',
  ])('rejects unsafe redirect value %s', value => {
    expect(normalizeReturnTo(value)).toBe('/');
  });
});
