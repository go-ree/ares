import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import MockAdapter from 'axios-mock-adapter';
import api from '@/config/api';
import { batchDeploy } from './deploy';

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
