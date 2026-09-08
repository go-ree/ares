import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AxiosError, AxiosHeaders } from 'axios';
import MockAdapter from 'axios-mock-adapter';
import api, { configureApiAuth, resetApiAuth } from '@/config/api';
import {
  AmbiguousReleaseResponseError,
  createBatchRelease,
  createRelease,
  getReleaseRetryAfterSeconds,
  getReleaseTargets,
  isAmbiguousReleaseError,
  preflightBatchRelease,
  preflightRelease,
} from './releases';
import type { BatchReleaseRequest } from '@/models/release';

const success = <T>(result: T) => ({ code: 1, message: 'ok', result, error: null });

describe('release API contract', () => {
  let mock: MockAdapter;

  beforeEach(() => {
    mock = new MockAdapter(api);
  });

  afterEach(() => {
    mock.restore();
    resetApiAuth();
  });

  it('queries AppConfig targets with the selected environment and pagination', async () => {
    mock.onGet('/api/v1/releases/targets').reply(config => {
      expect(config.params).toEqual({ env: 'staging', q: 'api', page_num: 2, page_size: 20 });
      return [
        200,
        success({
          total: 0,
          page_num: 2,
          page_size: 20,
          total_pages: 0,
          targets: [],
        }),
      ];
    });

    const result = await getReleaseTargets({
      env: 'staging',
      q: 'api',
      page_num: 2,
      page_size: 20,
    });

    expect(result.targets).toEqual([]);
  });

  it('uses canonical single-target preflight and create routes', async () => {
    const body = {
      ref: 'release/2026-09',
      inputs: { region: 'cn' },
      expected_workflow_version_id: '12',
    };
    mock.onPost('/api/v1/app-configs/7/releases/preflight', body).reply(
      200,
      success({
        config_id: 7,
        ready: true,
        workflow_version_id: '12',
        steps: [],
      })
    );
    mock.onPost('/api/v1/app-configs/7/releases', body).reply(config => {
      const headers = config.headers as AxiosHeaders;
      expect(headers.get('Idempotency-Key')).toBe('08db8664-f05b-47a1-bc5e-3f4299827457');
      expect(headers.get('Idempotency-Key')).not.toContain('"');
      expect(config.skipAuthHandling).toBe(true);
      return [
        200,
        success({
          record_id: '9007199254740993',
          total_count: 1,
          success_count: 1,
          failure_count: 0,
          items: [
            {
              request_index: 0,
              config_id: 7,
              success: true,
              task_id: 31,
              workflow_version_id: '12',
            },
          ],
        }),
      ];
    });

    expect((await preflightRelease(7, body)).ready).toBe(true);
    expect(
      (await createRelease(7, body, '08db8664-f05b-47a1-bc5e-3f4299827457')).items[0]?.task_id
    ).toBe(31);
  });

  it('sends the identical canonical batch body to preflight and create with one key', async () => {
    const body = {
      items: [
        {
          config_id: 7,
          ref: 'main',
          inputs: { color: 'blue' },
          expected_workflow_version_id: '12',
        },
        { config_id: 9, ref: 'main', inputs: { color: 'blue' } },
      ],
    };
    const seenBodies: unknown[] = [];
    mock.onPost('/api/v1/releases/batch/preflight').reply(config => {
      seenBodies.push(JSON.parse(String(config.data)));
      return [
        200,
        success({
          total_count: 2,
          ready_count: 2,
          failure_count: 0,
          items: [],
        }),
      ];
    });
    mock.onPost('/api/v1/releases/batch').reply(config => {
      seenBodies.push(JSON.parse(String(config.data)));
      expect((config.headers as AxiosHeaders).get('Idempotency-Key')).toBe('release-key-123456');
      expect(config.skipAuthHandling).toBe(true);
      return [
        200,
        success({
          record_id: '2',
          total_count: 2,
          success_count: 1,
          failure_count: 1,
          items: [
            {
              request_index: 0,
              config_id: 7,
              success: true,
              task_id: 31,
              workflow_version_id: '12',
            },
            {
              request_index: 1,
              config_id: 9,
              success: false,
              error_code: 'executor_unavailable',
            },
          ],
        }),
      ];
    });

    await preflightBatchRelease(body);
    await createBatchRelease(body, 'release-key-123456');

    expect(seenBodies).toEqual([body, body]);
    expect(JSON.stringify(seenBodies)).not.toContain('publisher');
    expect(JSON.stringify(seenBodies)).not.toContain('is_rundeck');
  });

  it('keeps canonical create 401 handling in the release view and still sends CSRF', async () => {
    const onUnauthorized = vi.fn();
    configureApiAuth({
      getCsrfToken: () => 'csrf-value',
      onUnauthorized,
      onForbidden: vi.fn(),
    });
    mock.onPost('/api/v1/releases/batch').reply(config => {
      expect((config.headers as AxiosHeaders).get('X-CSRF-Token')).toBe('csrf-value');
      expect(config.skipAuthHandling).toBe(true);
      return [401, { code: 0, error: 'unauthenticated' }];
    });

    await expect(
      createBatchRelease(
        { items: [{ config_id: 7, ref: 'main', inputs: {} }] },
        'release-key-123456'
      )
    ).rejects.toMatchObject({ response: { status: 401 } });

    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it.each([
    { name: 'null response', response: null },
    { name: 'null result', response: success(null) },
    { name: 'malformed success envelope', response: { code: 0, result: { record_id: '1' } } },
    {
      name: 'invalid record id',
      response: success({
        record_id: '9223372036854775808',
        total_count: 2,
        success_count: 1,
        failure_count: 1,
        items: [
          {
            request_index: 0,
            config_id: 7,
            success: true,
            task_id: 31,
            workflow_version_id: '12',
          },
          {
            request_index: 1,
            config_id: 9,
            success: false,
            error_code: 'executor_unavailable',
          },
        ],
      }),
    },
    {
      name: 'out-of-order items',
      response: success({
        record_id: '1',
        total_count: 2,
        success_count: 2,
        failure_count: 0,
        items: [
          {
            request_index: 0,
            config_id: 9,
            success: true,
            task_id: 32,
            workflow_version_id: '13',
          },
          {
            request_index: 1,
            config_id: 7,
            success: true,
            task_id: 31,
            workflow_version_id: '12',
          },
        ],
      }),
    },
    {
      name: 'workflow version differs from frozen intent',
      response: success({
        record_id: '1',
        total_count: 2,
        success_count: 2,
        failure_count: 0,
        items: [
          {
            request_index: 0,
            config_id: 7,
            success: true,
            task_id: 31,
            workflow_version_id: '999',
          },
          {
            request_index: 1,
            config_id: 9,
            success: true,
            task_id: 32,
            workflow_version_id: '13',
          },
        ],
      }),
    },
    {
      name: 'truncated items',
      response: success({
        record_id: '1',
        total_count: 2,
        success_count: 1,
        failure_count: 1,
        items: [
          {
            request_index: 0,
            config_id: 7,
            success: true,
            task_id: 31,
            workflow_version_id: '12',
          },
        ],
      }),
    },
    {
      name: 'invalid receipt counts',
      response: success({
        record_id: '1',
        total_count: 2,
        success_count: 1,
        failure_count: 0,
        items: [
          {
            request_index: 0,
            config_id: 7,
            success: true,
            task_id: 31,
            workflow_version_id: '12',
          },
          {
            request_index: 1,
            config_id: 9,
            success: false,
            error_code: 'executor_unavailable',
          },
        ],
      }),
    },
    {
      name: 'incomplete successful item',
      response: success({
        record_id: '1',
        total_count: 2,
        success_count: 1,
        failure_count: 1,
        items: [
          { request_index: 0, config_id: 7, success: true, task_id: 31 },
          {
            request_index: 1,
            config_id: 9,
            success: false,
            error_code: 'executor_unavailable',
          },
        ],
      }),
    },
    {
      name: 'workflow version not bound to the frozen intent',
      response: success({
        record_id: '1',
        total_count: 2,
        success_count: 1,
        failure_count: 1,
        items: [
          {
            request_index: 0,
            config_id: 7,
            success: true,
            task_id: 31,
            workflow_version_id: '99',
          },
          {
            request_index: 1,
            config_id: 9,
            success: false,
            error_code: 'executor_unavailable',
          },
        ],
      }),
    },
    {
      name: 'unstable failure item',
      response: success({
        record_id: '1',
        total_count: 2,
        success_count: 1,
        failure_count: 1,
        items: [
          {
            request_index: 0,
            config_id: 7,
            success: true,
            task_id: 31,
            workflow_version_id: '12',
          },
          {
            request_index: 1,
            config_id: 9,
            success: false,
            task_id: 32,
            error_code: 'private_database_error',
          },
        ],
      }),
    },
  ])('treats a 200 $name as an ambiguous release result', async ({ response }) => {
    const body: BatchReleaseRequest = {
      items: [
        { config_id: 7, ref: 'main', inputs: {}, expected_workflow_version_id: '12' },
        { config_id: 9, ref: 'main', inputs: {}, expected_workflow_version_id: '13' },
      ],
    };
    mock.onPost('/api/v1/releases/batch').reply(200, response);

    const error = await createBatchRelease(body, 'release-key-123456').catch(value => value);

    expect(error).toBeInstanceOf(AmbiguousReleaseResponseError);
    expect(isAmbiguousReleaseError(error)).toBe(true);
  });
});

describe('release retry safety', () => {
  const responseError = (status: number, code?: string, retryAfter?: string) =>
    new AxiosError('request failed', 'ERR_BAD_RESPONSE', undefined, undefined, {
      status,
      statusText: 'error',
      headers: retryAfter ? { 'retry-after': retryAfter } : {},
      config: { headers: new AxiosHeaders() },
      data: code ? { code: 0, message: 'failed', result: null, error: code } : null,
    });

  it('allows the same frozen pair to retry only for explicitly ambiguous outcomes', () => {
    expect(isAmbiguousReleaseError(new AxiosError('network error'))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(401, 'unauthenticated'))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(403, 'forbidden'))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(408))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(429, 'too_many_requests'))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(409, 'idempotency_request_in_progress'))).toBe(
      true
    );
    expect(isAmbiguousReleaseError(responseError(503, 'outcome_unknown'))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(502))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(504))).toBe(true);
  });

  it('retains the frozen pair for every server failure after submission', () => {
    expect(isAmbiguousReleaseError(responseError(500, 'internal_error'))).toBe(true);
    expect(isAmbiguousReleaseError(responseError(409, 'idempotency_key_conflict'))).toBe(false);
    expect(isAmbiguousReleaseError(responseError(422, 'workflow_invalid'))).toBe(false);
    expect(isAmbiguousReleaseError(new Error('local validation'))).toBe(false);
  });

  it.each([
    { status: 409, code: undefined },
    { status: 422, code: undefined },
    { status: 409, code: 'unknown_gateway_error' },
    { status: 422, code: 'unknown_gateway_error' },
    { status: 409, code: 'workflow_invalid' },
    { status: 422, code: 'idempotency_key_conflict' },
  ])('retains the frozen pair for an unverified $status/$code response', ({ status, code }) => {
    expect(isAmbiguousReleaseError(responseError(status, code))).toBe(true);
  });

  it.each([
    { status: 400, code: 'invalid_request' },
    { status: 400, code: 'idempotency_key_invalid' },
    { status: 404, code: 'release_target_not_found' },
    { status: 409, code: 'environment_disabled' },
    { status: 409, code: 'workflow_version_changed' },
    { status: 413, code: 'request_too_large' },
    { status: 415, code: 'invalid_request' },
    { status: 422, code: 'workflow_not_configured' },
    { status: 422, code: 'workflow_invalid' },
    { status: 422, code: 'executor_unavailable' },
  ])('clears the pair only for a verified $status/$code rejection', ({ status, code }) => {
    expect(isAmbiguousReleaseError(responseError(status, code))).toBe(false);
  });

  it('parses and bounds Retry-After for a manual same-pair retry', () => {
    expect(getReleaseRetryAfterSeconds(responseError(429, 'too_many_requests', '7'))).toBe(7);
    expect(
      getReleaseRetryAfterSeconds(responseError(409, 'idempotency_request_in_progress', '2'))
    ).toBe(2);
    expect(getReleaseRetryAfterSeconds(responseError(409, 'outcome_unknown', '120'))).toBe(30);
    expect(getReleaseRetryAfterSeconds(responseError(409, 'outcome_unknown', 'tomorrow'))).toBe(0);
  });
});
