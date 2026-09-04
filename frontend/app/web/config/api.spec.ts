import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MockAdapter from 'axios-mock-adapter';
import type { AxiosHeaders } from 'axios';
import api, { configureApiAuth, resetApiAuth } from './api';

describe('shared API client authentication', () => {
  let mock: MockAdapter;

  beforeEach(() => {
    mock = new MockAdapter(api);
  });

  afterEach(() => {
    mock.restore();
    resetApiAuth();
  });

  it('uses cookies and adds CSRF only to unsafe requests', async () => {
    configureApiAuth({
      getCsrfToken: () => 'csrf-value',
      onUnauthorized: vi.fn(),
      onForbidden: vi.fn(),
    });
    const seenCsrfHeaders: unknown[] = [];
    mock.onGet('/read').reply(config => {
      seenCsrfHeaders.push((config.headers as AxiosHeaders).get('X-CSRF-Token'));
      return [200, { code: 1 }];
    });
    mock.onPost('/write').reply(config => {
      seenCsrfHeaders.push((config.headers as AxiosHeaders).get('X-CSRF-Token'));
      return [200, { code: 1 }];
    });

    await api.get('/read');
    await api.post('/write', {});

    expect(api.defaults.withCredentials).toBe(true);
    expect(seenCsrfHeaders).toEqual([undefined, 'csrf-value']);
  });

  it('invalidates through one centralized 401 hook', async () => {
    const onUnauthorized = vi.fn();
    configureApiAuth({ getCsrfToken: () => null, onUnauthorized, onForbidden: vi.fn() });
    mock.onGet('/protected').reply(401);

    await expect(api.get('/protected')).rejects.toMatchObject({ response: { status: 401 } });
    expect(onUnauthorized).toHaveBeenCalledOnce();
  });

  it('can explicitly skip auth handling for the session probe', async () => {
    const onUnauthorized = vi.fn();
    configureApiAuth({ getCsrfToken: () => null, onUnauthorized, onForbidden: vi.fn() });
    mock.onGet('/probe').reply(401);

    await expect(api.get('/probe', { skipAuthHandling: true })).rejects.toBeTruthy();
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it('reports a 403 once without consuming it or changing the current view', async () => {
    const onForbidden = vi.fn();
    configureApiAuth({
      getCsrfToken: () => null,
      onUnauthorized: vi.fn(),
      onForbidden,
    });
    mock.onGet('/forbidden').reply(403);

    await expect(api.get('/forbidden')).rejects.toMatchObject({ response: { status: 403 } });
    expect(onForbidden).toHaveBeenCalledOnce();
  });
});
