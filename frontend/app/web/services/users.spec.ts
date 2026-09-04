import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import MockAdapter from 'axios-mock-adapter';
import type { AxiosHeaders } from 'axios';
import api, { configureApiAuth } from '@/config/api';
import { getUserApiError, listUsers, updateUser } from './users';

describe('users service', () => {
  let mock: MockAdapter;

  beforeEach(() => {
    mock = new MockAdapter(api);
  });

  afterEach(() => {
    mock.restore();
  });

  it('requests a bounded page and unwraps the user list', async () => {
    mock.onGet('/api/v1/system/users', { params: { offset: 100, limit: 100 } }).reply(200, {
      code: 1,
      message: '查询成功',
      result: { items: [{ id: '7', username: 'alice' }], next_offset: 101 },
    });

    await expect(listUsers(100, 100)).resolves.toMatchObject({
      items: [{ id: '7', username: 'alice' }],
      next_offset: 101,
    });
  });

  it('uses the shared cookie and CSRF client for user updates', async () => {
    configureApiAuth({
      getCsrfToken: () => 'csrf-token',
      onUnauthorized: () => undefined,
      onForbidden: () => undefined,
    });
    mock.onPatch('/api/v1/system/users/7').reply(config => {
      expect((config.headers as AxiosHeaders).get('X-CSRF-Token')).toBe('csrf-token');
      expect(JSON.parse(config.data)).toEqual({ role: 'developer' });
      return [
        200,
        {
          code: 1,
          message: '用户已更新',
          result: { id: '7', username: 'alice', role: 'developer', enabled: true },
        },
      ];
    });

    await expect(updateUser('7', { role: 'developer' })).resolves.toMatchObject({
      id: '7',
      role: 'developer',
    });
  });

  it('keeps the conflict status and backend detail for last-admin feedback', async () => {
    mock.onPatch('/api/v1/system/users/1').reply(409, {
      code: 0,
      message: '更新用户失败',
      result: null,
      error: '不能禁用或降级最后一个可用管理员',
    });

    let failure: unknown;
    try {
      await updateUser('1', { enabled: false });
    } catch (error) {
      failure = error;
    }

    expect(getUserApiError(failure)).toEqual({
      status: 409,
      message: '不能禁用或降级最后一个可用管理员',
    });
  });
});
