import { describe, expect, it, vi } from 'vitest';
import { AxiosError, AxiosHeaders } from 'axios';
import {
  FrozenReleaseActorError,
  useReleaseComposer as createReleaseComposer,
} from './useReleaseComposer';
import { AmbiguousReleaseResponseError } from '@/services/releases';
import type {
  BatchReleaseRequest,
  ReleaseReceipt,
  ReleaseTarget,
  ReleaseTargetPage,
} from '@/models/release';

const target = (configId = 7, env = 'staging'): ReleaseTarget => ({
  config_id: configId,
  app_id: 1,
  app_name: 'app-' + configId,
  app_name_cn: '应用 ' + configId,
  env,
  workflow_version_id: '12',
  available: true,
  steps: [{ key: 'build', name: '构建', uses: 'builtin.build' }],
});

const page = (targets: ReleaseTarget[]): ReleaseTargetPage => ({
  total: targets.length,
  page_num: 1,
  page_size: 20,
  total_pages: 1,
  targets,
});

const receipt = (): ReleaseReceipt => ({
  record_id: '88',
  total_count: 1,
  success_count: 1,
  failure_count: 0,
  items: [
    {
      request_index: 0,
      config_id: 7,
      success: true,
      task_id: 99,
      workflow_version_id: '12',
    },
  ],
});

const readyPreflight = (request: BatchReleaseRequest, workflowVersionID = '12') => ({
  total_count: request.items.length,
  ready_count: request.items.length,
  failure_count: 0,
  items: request.items.map((item, requestIndex) => ({
    request_index: requestIndex,
    config_id: item.config_id,
    ready: true,
    workflow_version_id: workflowVersionID,
    steps: [],
  })),
});

const responseError = (status: number, code: string, retryAfter?: string) =>
  new AxiosError('request failed', 'ERR_BAD_RESPONSE', undefined, undefined, {
    status,
    statusText: 'error',
    headers: retryAfter ? { 'retry-after': retryAfter } : {},
    config: { headers: new AxiosHeaders() },
    data: { code: 0, message: 'failed', result: null, error: code },
  });

const useReleaseComposer = (overrides: Parameters<typeof createReleaseComposer>[0] = {}) =>
  createReleaseComposer({ getActorID: () => '42', ...overrides });

describe('release composer', () => {
  it('clears selected AppConfigs when the environment changes', async () => {
    const getTargets = vi.fn(async ({ env }: { env: string }) =>
      page([target(env === 'prod' ? 9 : 7, env)])
    );
    const composer = useReleaseComposer({ getTargets });

    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    expect(composer.selectedCount.value).toBe(1);

    await composer.setEnvironment('prod');

    expect(composer.selectedCount.value).toBe(0);
    expect(composer.targets.value[0]?.env).toBe('prod');
  });

  it('freezes the canonical body and idempotency key across an ambiguous retry', async () => {
    const sent: Array<{ body: BatchReleaseRequest; key: string }> = [];
    const createKey = vi.fn(() => '08db8664-f05b-47a1-bc5e-3f4299827457');
    const create = vi
      .fn()
      .mockImplementationOnce(async (body: BatchReleaseRequest, key: string) => {
        sent.push({ body, key });
        throw new Error('connection reset');
      })
      .mockImplementationOnce(async (body: BatchReleaseRequest, key: string) => {
        sent.push({ body, key });
        return receipt();
      });
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target()])),
      preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
      create,
      isAmbiguousError: () => true,
      createKey,
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'release/2026-09';
    composer.inputRows.value.push({ key: 'region', value: 'cn' });

    await composer.runPreflight();
    expect(createKey).not.toHaveBeenCalled();
    composer.releaseRef.value = 'mutated-behind-disabled-ui';
    composer.inputRows.value[0]!.value = 'mutated';
    await expect(composer.submitFrozen()).rejects.toThrow('connection reset');
    expect(composer.phase.value).toBe('uncertain');

    await composer.retryFrozenSubmission();

    expect(createKey).toHaveBeenCalledTimes(1);
    expect(sent).toHaveLength(2);
    expect(sent[0]).toEqual(sent[1]);
    expect(sent[0]).toEqual({
      key: '08db8664-f05b-47a1-bc5e-3f4299827457',
      body: {
        items: [
          {
            config_id: 7,
            ref: 'release/2026-09',
            inputs: { region: 'cn' },
            expected_workflow_version_id: '12',
          },
        ],
      },
    });
    expect(composer.receipt.value?.record_id).toBe('88');
  });

  it('keeps the frozen pair when a successful HTTP response cannot be verified', async () => {
    const createKey = vi.fn(() => '08db8664-f05b-47a1-bc5e-3f4299827457');
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target()])),
      preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
      create: vi.fn(async () => {
        throw new AmbiguousReleaseResponseError();
      }),
      createKey,
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'main';
    await composer.runPreflight();

    await expect(composer.submitFrozen()).rejects.toBeInstanceOf(AmbiguousReleaseResponseError);

    expect(composer.phase.value).toBe('uncertain');
    expect(composer.frozenSubmission.value).toEqual({
      key: '08db8664-f05b-47a1-bc5e-3f4299827457',
      actorUserId: '42',
      body: {
        items: [
          {
            config_id: 7,
            ref: 'main',
            inputs: {},
            expected_workflow_version_id: '12',
          },
        ],
      },
    });
    expect(createKey).toHaveBeenCalledTimes(1);
  });

  it.each([
    { status: 401, code: 'unauthenticated' },
    { status: 403, code: 'forbidden' },
    { status: 429, code: 'too_many_requests', retryAfter: '7' },
  ])(
    'keeps the frozen pair when the first canonical create response is $status',
    async ({ status, code, retryAfter }) => {
      const key = '08db8664-f05b-47a1-bc5e-3f4299827457';
      const createKey = vi.fn(() => key);
      const create = vi.fn(async () => {
        throw responseError(status, code, retryAfter);
      });
      const composer = useReleaseComposer({
        getTargets: vi.fn(async () => page([target()])),
        preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
        create,
        createKey,
      });
      await composer.setEnvironment('staging');
      composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
      composer.releaseRef.value = 'main';
      await composer.runPreflight();

      await expect(composer.submitFrozen()).rejects.toMatchObject({ response: { status } });

      expect(composer.phase.value).toBe('uncertain');
      expect(composer.frozenSubmission.value).toEqual({
        key,
        actorUserId: '42',
        body: {
          items: [
            {
              config_id: 7,
              ref: 'main',
              inputs: {},
              expected_workflow_version_id: '12',
            },
          ],
        },
      });
      expect(createKey).toHaveBeenCalledTimes(1);
      expect(create).toHaveBeenCalledTimes(1);
    }
  );

  it.each([
    { status: 401, code: 'unauthenticated' },
    { status: 403, code: 'forbidden' },
    { status: 429, code: 'too_many_requests', retryAfter: '7' },
  ])(
    'keeps the exact frozen pair when an uncertain retry receives $status',
    async ({ status, code, retryAfter }) => {
      const key = '08db8664-f05b-47a1-bc5e-3f4299827457';
      const sent: Array<{ body: BatchReleaseRequest; key: string }> = [];
      const createKey = vi.fn(() => key);
      const create = vi
        .fn()
        .mockImplementationOnce(async (body: BatchReleaseRequest, requestKey: string) => {
          sent.push({ body, key: requestKey });
          throw new AxiosError('network error');
        })
        .mockImplementationOnce(async (body: BatchReleaseRequest, requestKey: string) => {
          sent.push({ body, key: requestKey });
          throw responseError(status, code, retryAfter);
        });
      const composer = useReleaseComposer({
        getTargets: vi.fn(async () => page([target()])),
        preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
        create,
        createKey,
      });
      await composer.setEnvironment('staging');
      composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
      composer.releaseRef.value = 'main';
      await composer.runPreflight();

      await expect(composer.submitFrozen()).rejects.toBeInstanceOf(AxiosError);
      expect(composer.phase.value).toBe('uncertain');
      const frozen = composer.frozenSubmission.value;

      await expect(composer.retryFrozenSubmission()).rejects.toMatchObject({
        response: { status },
      });

      expect(composer.phase.value).toBe('uncertain');
      expect(composer.frozenSubmission.value).toEqual(frozen);
      expect(sent).toHaveLength(2);
      expect(sent[1]).toEqual(sent[0]);
      expect(createKey).toHaveBeenCalledTimes(1);
    }
  );

  it.each([
    { name: 'an unauthenticated session', nextActorID: null },
    { name: 'a different authenticated actor', nextActorID: '43' },
  ])(
    'blocks an uncertain retry under $name without losing the frozen pair',
    async ({ nextActorID }) => {
      let actorID: string | null = '42';
      const key = '08db8664-f05b-47a1-bc5e-3f4299827457';
      const create = vi.fn(async () => {
        throw new AxiosError('network error');
      });
      const composer = useReleaseComposer({
        getTargets: vi.fn(async () => page([target()])),
        preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
        create,
        createKey: () => key,
        getActorID: () => actorID,
      });
      await composer.setEnvironment('staging');
      composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
      composer.releaseRef.value = 'main';
      await composer.runPreflight();
      await expect(composer.submitFrozen()).rejects.toBeInstanceOf(AxiosError);
      const frozen = composer.frozenSubmission.value;
      actorID = nextActorID;

      await expect(composer.retryFrozenSubmission()).rejects.toBeInstanceOf(
        FrozenReleaseActorError
      );

      expect(composer.phase.value).toBe('uncertain');
      expect(composer.frozenSubmission.value).toEqual(frozen);
      expect(create).toHaveBeenCalledTimes(1);
    }
  );

  it('refuses to mint a key when the initial authenticated actor is unavailable', async () => {
    const create = vi.fn();
    const createKey = vi.fn(() => '08db8664-f05b-47a1-bc5e-3f4299827457');
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target()])),
      preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
      create,
      createKey,
      getActorID: () => null,
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'main';
    await composer.runPreflight();

    await expect(composer.submitFrozen()).rejects.toBeInstanceOf(FrozenReleaseActorError);

    expect(composer.phase.value).toBe('ready');
    expect(composer.frozenSubmission.value).toBeNull();
    expect(createKey).not.toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
  });

  it('uses a new idempotency key after a completed request is edited', async () => {
    const keys = ['08db8664-f05b-47a1-bc5e-3f4299827457', 'eed54b39-42a2-4939-bf59-9b88d3722830'];
    const create = vi.fn(async (_body: BatchReleaseRequest, _key: string) => receipt());
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target()])),
      preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
      create,
      createKey: () => keys.shift()!,
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'main';
    await composer.runPreflight();
    await composer.submitFrozen();
    expect(composer.canPreflight.value).toBe(false);

    composer.releaseRef.value = 'release/next';
    composer.invalidateDraft();
    expect(composer.canPreflight.value).toBe(true);
    await composer.runPreflight();
    await composer.submitFrozen();

    expect(create.mock.calls.map(call => call[1])).toEqual([
      '08db8664-f05b-47a1-bc5e-3f4299827457',
      'eed54b39-42a2-4939-bf59-9b88d3722830',
    ]);
  });

  it('rejects duplicate input keys before preflight', async () => {
    const preflight = vi.fn();
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target()])),
      preflight,
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'main';
    composer.inputRows.value.push({ key: ' region ', value: 'cn' }, { key: 'region', value: 'us' });

    await expect(composer.runPreflight()).rejects.toThrow('发布参数 key 重复：region');
    expect(preflight).not.toHaveBeenCalled();
    expect(composer.phase.value).toBe('draft');
  });

  it('caps cross-page selection at the batch protocol limit', async () => {
    const targets = Array.from({ length: 101 }, (_, index) => target(index + 1));
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page(targets)),
    });

    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection(targets);
    composer.releaseRef.value = 'main';

    expect(composer.selectedCount.value).toBe(100);
    expect(composer.buildRequest().items).toHaveLength(100);
  });

  it('starts a new draft with only failed receipt targets', async () => {
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target(7), target(9)])),
      preflight: vi.fn(async (request: BatchReleaseRequest) => readyPreflight(request)),
      create: vi.fn(async () => ({
        record_id: '89',
        total_count: 2,
        success_count: 1,
        failure_count: 1,
        items: [
          {
            request_index: 0,
            config_id: 7,
            success: true,
            task_id: 100,
            workflow_version_id: '12',
          },
          { request_index: 1, config_id: 9, success: false, error_code: 'executor_unavailable' },
        ],
      })),
      createKey: () => '08db8664-f05b-47a1-bc5e-3f4299827457',
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection(composer.targets.value);
    composer.releaseRef.value = 'main';
    await composer.runPreflight();
    await composer.submitFrozen();

    composer.selectFailedReceiptItems();

    expect(composer.phase.value).toBe('draft');
    expect([...composer.selectedTargets.value.keys()]).toEqual([9]);
    expect(composer.receipt.value).toBeNull();
  });

  it('submits only targets that passed this exact preflight', async () => {
    const create = vi.fn(async (_body: BatchReleaseRequest, _key: string) => receipt());
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target(7), target(9)])),
      preflight: vi.fn(async () => ({
        total_count: 2,
        ready_count: 1,
        failure_count: 1,
        items: [
          {
            request_index: 0,
            config_id: 7,
            ready: true,
            workflow_version_id: '9007199254740993',
            steps: [],
          },
          {
            request_index: 1,
            config_id: 9,
            ready: false,
            error_code: 'executor_unavailable',
            steps: [],
          },
        ],
      })),
      create,
      createKey: () => '08db8664-f05b-47a1-bc5e-3f4299827457',
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection(composer.targets.value);
    composer.releaseRef.value = 'main';

    const result = await composer.runPreflight();
    expect(result.items).toHaveLength(2);
    expect(composer.stagedRequest.value).toEqual({
      items: [
        {
          config_id: 7,
          ref: 'main',
          inputs: {},
          expected_workflow_version_id: '9007199254740993',
        },
      ],
    });

    await composer.submitFrozen();
    expect(create.mock.calls[0]?.[0].items.map(item => item.config_id)).toEqual([7]);
  });

  it('keeps zero-ready preflight visible but refuses to mint a key or submit', async () => {
    const create = vi.fn();
    const createKey = vi.fn(() => '08db8664-f05b-47a1-bc5e-3f4299827457');
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target()])),
      preflight: vi.fn(async () => ({
        total_count: 1,
        ready_count: 0,
        failure_count: 1,
        items: [
          {
            request_index: 0,
            config_id: 7,
            ready: false,
            error_code: 'executor_unavailable',
            steps: [],
          },
        ],
      })),
      create,
      createKey,
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'main';

    await composer.runPreflight();

    expect(composer.phase.value).toBe('ready');
    expect(composer.preflightResult.value?.failure_count).toBe(1);
    expect(composer.stagedRequest.value).toEqual({ items: [] });
    await expect(composer.submitFrozen()).rejects.toThrow('预检未发现可发布目标');
    expect(createKey).not.toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
  });

  it('pins the exact BIGINT workflow version returned by preflight', async () => {
    const selectedTarget = target();
    delete selectedTarget.workflow_version_id;
    const create = vi.fn(async (_body: BatchReleaseRequest, _key: string) => receipt());
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([selectedTarget])),
      preflight: vi.fn(async (request: BatchReleaseRequest) =>
        readyPreflight(request, '9007199254740993')
      ),
      create,
      createKey: () => '08db8664-f05b-47a1-bc5e-3f4299827457',
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'main';

    await composer.runPreflight();

    expect(composer.stagedRequest.value?.items[0]?.expected_workflow_version_id).toBe(
      '9007199254740993'
    );
    await composer.submitFrozen();
    expect(create.mock.calls[0]?.[0].items[0]?.expected_workflow_version_id).toBe(
      '9007199254740993'
    );
  });

  it.each([
    {
      name: 'request index',
      mutate: (result: ReturnType<typeof readyPreflight>) => {
        result.items[0]!.request_index = 1;
      },
    },
    {
      name: 'config id',
      mutate: (result: ReturnType<typeof readyPreflight>) => {
        result.items[0]!.config_id = 9;
      },
    },
    {
      name: 'item count',
      mutate: (result: ReturnType<typeof readyPreflight>) => {
        result.total_count = 2;
      },
    },
    {
      name: 'workflow version',
      mutate: (result: ReturnType<typeof readyPreflight>) => {
        result.items[0]!.workflow_version_id = '9223372036854775808';
      },
    },
  ])('fails closed when preflight returns a mismatched $name', async ({ mutate }) => {
    const create = vi.fn();
    const composer = useReleaseComposer({
      getTargets: vi.fn(async () => page([target()])),
      preflight: vi.fn(async (request: BatchReleaseRequest) => {
        const result = readyPreflight(request);
        mutate(result);
        return result;
      }),
      create,
    });
    await composer.setEnvironment('staging');
    composer.replaceCurrentPageSelection([composer.targets.value[0]!]);
    composer.releaseRef.value = 'main';

    await expect(composer.runPreflight()).rejects.toThrow(/发布预检/);

    expect(composer.phase.value).toBe('draft');
    expect(composer.stagedRequest.value).toBeNull();
    expect(composer.frozenSubmission.value).toBeNull();
    expect(create).not.toHaveBeenCalled();
  });
});
