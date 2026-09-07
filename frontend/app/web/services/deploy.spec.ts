import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import MockAdapter from 'axios-mock-adapter';
import api from '@/config/api';
import { batchDeploy, legacyTaskLogStreamUrl, taskStepLogStreamUrl } from './deploy';

describe('deploy service identity boundary', () => {
  let mock: MockAdapter;

  beforeEach(() => {
    mock = new MockAdapter(api);
  });

  afterEach(() => {
    mock.restore();
  });

  it('never submits a client-controlled publisher', async () => {
    let submitted: unknown;
    mock.onPost('/api/v1/deploy/publish/batch').reply(config => {
      submitted = JSON.parse(String(config.data));
      return [200, { code: 1, message: 'ok', result: { task_records: [] } }];
    });

    await batchDeploy([{ app_name: 'api', env: 'prod', branch: 'main' }]);

    expect(submitted).toEqual({
      batch_publish: [{ app_name: 'api', env: 'prod', branch: 'main' }],
    });
    expect(JSON.stringify(submitted)).not.toContain('publisher');
  });
});

describe('deploy log stream URLs', () => {
  it('encodes the canonical task step and opaque cursor without executor-controlled fields', () => {
    const raw = taskStepLogStreamUrl(12, 'folder.build_1-log', 'opaque /+=雪');
    const url = new URL(raw, 'http://ares.test');

    expect(url.pathname).toBe('/api/v1/tasks/12/steps/folder.build_1-log/logs/stream');
    expect(url.searchParams.get('cursor')).toBe('opaque /+=雪');
    expect([...url.searchParams.keys()]).toEqual(['cursor']);
    expect(raw).not.toContain('job');
    expect(raw).not.toContain('build_id');
    expect(raw).not.toContain('address');
  });

  it('keeps the deprecated v1 request task-scoped and only forwards numeric offsets', () => {
    expect(legacyTaskLogStreamUrl(12, 'ci', '42')).toBe(
      '/api/v1/job/stream/log?task_id=12&log_type=ci&start=42'
    );
    expect(legacyTaskLogStreamUrl(12, 'cd', 'opaque')).toBe(
      '/api/v1/job/stream/log?task_id=12&log_type=cd'
    );
  });
});
