import { computed, ref } from 'vue';
import {
  createBatchRelease,
  getReleaseTargets,
  isAmbiguousReleaseError,
  isPositiveInt64String,
  preflightBatchRelease,
} from '@/services/releases';
import type {
  BatchReleasePreflightResult,
  BatchReleaseRequest,
  ReleaseInputs,
  ReleaseReceipt,
  ReleaseTarget,
  ReleaseTargetPage,
} from '@/models/release';

export interface ReleaseInputRow {
  key: string;
  value: string;
}

export type ReleaseComposerPhase =
  | 'draft'
  | 'preflighting'
  | 'ready'
  | 'submitting'
  | 'uncertain'
  | 'completed';

export interface FrozenReleaseSubmission {
  key: string;
  body: BatchReleaseRequest;
  actorUserId: string;
}

export class FrozenReleaseActorError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'FrozenReleaseActorError';
  }
}

interface ReleaseComposerDependencies {
  getTargets: typeof getReleaseTargets;
  preflight: typeof preflightBatchRelease;
  create: typeof createBatchRelease;
  isAmbiguousError: typeof isAmbiguousReleaseError;
  createKey: () => string;
  getActorID: () => string | null;
}

const defaultDependencies: ReleaseComposerDependencies = {
  getTargets: getReleaseTargets,
  preflight: preflightBatchRelease,
  create: createBatchRelease,
  isAmbiguousError: isAmbiguousReleaseError,
  createKey: () => globalThis.crypto.randomUUID(),
  // Production callers inject the authenticated store identity. Failing
  // closed prevents a future caller from silently dropping actor binding.
  getActorID: () => null,
};

export const MAX_RELEASE_TARGETS = 100;

const emptyTargetPage = (pageNum: number, pageSize: number): ReleaseTargetPage => ({
  total: 0,
  page_num: pageNum,
  page_size: pageSize,
  total_pages: 0,
  targets: [],
});

const cloneRequest = (request: BatchReleaseRequest): BatchReleaseRequest => ({
  items: request.items.map(item => ({
    config_id: item.config_id,
    ref: item.ref,
    inputs: { ...item.inputs },
    ...(item.expected_workflow_version_id === undefined
      ? {}
      : { expected_workflow_version_id: item.expected_workflow_version_id }),
  })),
});

const confirmPreflightIntent = (
  request: BatchReleaseRequest,
  result: BatchReleasePreflightResult
): BatchReleaseRequest => {
  if (
    !result ||
    !Number.isInteger(result.total_count) ||
    !Number.isInteger(result.ready_count) ||
    !Number.isInteger(result.failure_count) ||
    result.total_count !== request.items.length ||
    result.ready_count < 0 ||
    result.failure_count < 0 ||
    result.ready_count + result.failure_count !== result.total_count ||
    !Array.isArray(result.items) ||
    result.items.length !== request.items.length
  ) {
    throw new Error('发布预检响应与请求不匹配');
  }

  const confirmed = cloneRequest(request);
  let readyCount = 0;
  const readyItems = [] as BatchReleaseRequest['items'];
  for (let index = 0; index < result.items.length; index += 1) {
    const responseItem = result.items[index];
    const requestItem = confirmed.items[index];
    if (
      !responseItem ||
      !requestItem ||
      responseItem.request_index !== index ||
      responseItem.config_id !== requestItem.config_id ||
      typeof responseItem.ready !== 'boolean'
    ) {
      throw new Error('发布预检响应与请求不匹配');
    }
    if (!responseItem.ready) continue;
    if (!isPositiveInt64String(responseItem.workflow_version_id)) {
      throw new Error('发布预检未返回有效的工作流版本');
    }
    requestItem.expected_workflow_version_id = responseItem.workflow_version_id;
    readyItems.push(requestItem);
    readyCount += 1;
  }
  if (readyCount !== result.ready_count) {
    throw new Error('发布预检响应计数不一致');
  }
  confirmed.items = readyItems;
  return confirmed;
};

export const releaseReasonLabel = (code?: string): string => {
  const labels: Record<string, string> = {
    environment_disabled: '环境已停用',
    app_config_missing: '尚未配置该环境',
    workflow_not_configured: '尚未配置发布工作流',
    executor_unavailable: '工作流执行器不可用',
    release_target_not_found: '发布目标不存在',
    workflow_version_changed: '工作流已发生变化，请重新预检',
    workflow_invalid: '工作流配置无效',
    invalid_request: '发布参数无效',
    idempotency_key_invalid: '幂等键无效',
    idempotency_key_conflict: '幂等键已用于其他请求',
    idempotency_request_in_progress: '相同发布请求仍在处理中',
    outcome_unknown: '发布结果暂时无法确认',
    internal_error: '服务暂时不可用',
  };
  return (code && labels[code]) || code || '当前不可发布';
};

export function useReleaseComposer(overrides: Partial<ReleaseComposerDependencies> = {}) {
  const dependencies = { ...defaultDependencies, ...overrides };
  const environment = ref('');
  const keyword = ref('');
  const releaseRef = ref('');
  const inputRows = ref<ReleaseInputRow[]>([]);
  const pageNum = ref(1);
  const pageSize = ref(20);
  const targetPage = ref<ReleaseTargetPage>(emptyTargetPage(1, 20));
  const selectedTargets = ref(new Map<number, ReleaseTarget>());
  const targetsLoading = ref(false);
  const phase = ref<ReleaseComposerPhase>('draft');
  const preflightResult = ref<BatchReleasePreflightResult | null>(null);
  const receipt = ref<ReleaseReceipt | null>(null);
  const stagedRequest = ref<BatchReleaseRequest | null>(null);
  const frozenSubmission = ref<FrozenReleaseSubmission | null>(null);
  let targetRequestSequence = 0;

  const targets = computed(() => targetPage.value.targets);
  const total = computed(() => targetPage.value.total);
  const selectedCount = computed(() => selectedTargets.value.size);
  const locked = computed(() =>
    ['preflighting', 'ready', 'submitting', 'uncertain'].includes(phase.value)
  );
  const canPreflight = computed(
    () =>
      phase.value === 'draft' &&
      environment.value !== '' &&
      releaseRef.value.trim() !== '' &&
      selectedTargets.value.size > 0
  );

  const invalidateDraft = () => {
    if (locked.value) return;
    phase.value = 'draft';
    preflightResult.value = null;
    receipt.value = null;
    stagedRequest.value = null;
    frozenSubmission.value = null;
  };

  const setEnvironment = async (nextEnvironment: string) => {
    environment.value = nextEnvironment;
    pageNum.value = 1;
    selectedTargets.value = new Map();
    preflightResult.value = null;
    receipt.value = null;
    stagedRequest.value = null;
    frozenSubmission.value = null;
    phase.value = 'draft';
    await loadTargets();
  };

  const loadTargets = async () => {
    const sequence = ++targetRequestSequence;
    if (!environment.value) {
      targetPage.value = emptyTargetPage(pageNum.value, pageSize.value);
      targetsLoading.value = false;
      return;
    }
    targetsLoading.value = true;
    try {
      const result = await dependencies.getTargets({
        env: environment.value,
        ...(keyword.value.trim() ? { q: keyword.value.trim() } : {}),
        page_num: pageNum.value,
        page_size: pageSize.value,
      });
      if (sequence === targetRequestSequence) targetPage.value = result;
    } catch (error) {
      if (sequence === targetRequestSequence) throw error;
    } finally {
      if (sequence === targetRequestSequence) targetsLoading.value = false;
    }
  };

  const searchTargets = async () => {
    pageNum.value = 1;
    await loadTargets();
  };

  const changePage = async (nextPage: number) => {
    pageNum.value = nextPage;
    await loadTargets();
  };

  const changePageSize = async (nextPageSize: number) => {
    pageSize.value = nextPageSize;
    pageNum.value = 1;
    await loadTargets();
  };

  const replaceCurrentPageSelection = (rows: ReleaseTarget[]) => {
    if (locked.value) return;
    const selection = new Map(selectedTargets.value);
    for (const target of targets.value) {
      if (target.config_id) selection.delete(target.config_id);
    }
    for (const target of rows) {
      if (
        target.available &&
        target.config_id &&
        (selection.has(target.config_id) || selection.size < MAX_RELEASE_TARGETS)
      ) {
        selection.set(target.config_id, target);
      }
    }
    selectedTargets.value = selection;
    invalidateDraft();
  };

  const addInputRow = () => {
    if (locked.value) return;
    inputRows.value.push({ key: '', value: '' });
    invalidateDraft();
  };

  const removeInputRow = (index: number) => {
    if (locked.value) return;
    inputRows.value.splice(index, 1);
    invalidateDraft();
  };

  const buildInputs = (): ReleaseInputs => {
    const inputs: ReleaseInputs = {};
    for (const row of inputRows.value) {
      const key = row.key.trim();
      if (!key) continue;
      if (Object.prototype.hasOwnProperty.call(inputs, key)) {
        throw new Error(`发布参数 key 重复：${key}`);
      }
      inputs[key] = row.value;
    }
    return inputs;
  };

  const buildRequest = (): BatchReleaseRequest => {
    const refValue = releaseRef.value.trim();
    if (!environment.value) throw new Error('请选择发布环境');
    if (!refValue) throw new Error('请填写完整 Git 分支或 ref');
    if (selectedTargets.value.size === 0) throw new Error('请至少选择一个可发布目标');
    if (selectedTargets.value.size > MAX_RELEASE_TARGETS) {
      throw new Error(`一次最多发布 ${MAX_RELEASE_TARGETS} 个目标`);
    }
    const inputs = buildInputs();
    return {
      items: Array.from(selectedTargets.value.entries()).map(([configId, target]) => ({
        config_id: configId,
        ref: refValue,
        inputs: { ...inputs },
        ...(target.workflow_version_id === undefined
          ? {}
          : { expected_workflow_version_id: target.workflow_version_id }),
      })),
    };
  };

  const runPreflight = async (): Promise<BatchReleasePreflightResult> => {
    if (locked.value) throw new Error('当前发布请求已冻结');
    const request = cloneRequest(buildRequest());
    stagedRequest.value = request;
    frozenSubmission.value = null;
    receipt.value = null;
    preflightResult.value = null;
    phase.value = 'preflighting';
    try {
      const result = await dependencies.preflight(request);
      stagedRequest.value = confirmPreflightIntent(request, result);
      preflightResult.value = result;
      phase.value = 'ready';
      return result;
    } catch (error) {
      stagedRequest.value = null;
      phase.value = 'draft';
      throw error;
    }
  };

  const cancelStagedSubmission = () => {
    if (phase.value !== 'ready') return;
    stagedRequest.value = null;
    frozenSubmission.value = null;
    preflightResult.value = null;
    phase.value = 'draft';
  };

  const submitFrozen = async (): Promise<ReleaseReceipt> => {
    let frozen = frozenSubmission.value;
    let currentActorUserId: string | null = null;
    if (
      !frozen &&
      phase.value === 'ready' &&
      stagedRequest.value &&
      stagedRequest.value.items.length > 0
    ) {
      const actorUserId = dependencies.getActorID();
      if (!isPositiveInt64String(actorUserId)) {
        throw new FrozenReleaseActorError('无法确认当前登录主体，已阻止创建发布任务');
      }
      frozen = {
        key: dependencies.createKey(),
        body: cloneRequest(stagedRequest.value),
        actorUserId,
      };
      frozenSubmission.value = frozen;
      stagedRequest.value = null;
      currentActorUserId = actorUserId;
    }
    if (!frozen) throw new Error('预检未发现可发布目标，不能创建发布任务');
    currentActorUserId ??= dependencies.getActorID();
    if (!isPositiveInt64String(currentActorUserId)) {
      phase.value = 'uncertain';
      throw new FrozenReleaseActorError(
        '尚未确认登录会话，已阻止重试；请在新标签页使用原账号登录后再试'
      );
    }
    if (currentActorUserId !== frozen.actorUserId) {
      phase.value = 'uncertain';
      throw new FrozenReleaseActorError(
        '当前登录账号与冻结发布请求不一致，已阻止重试；请恢复原账号或先核对原任务'
      );
    }
    phase.value = 'submitting';
    try {
      const result = await dependencies.create(frozen.body, frozen.key);
      receipt.value = result;
      stagedRequest.value = null;
      frozenSubmission.value = null;
      phase.value = 'completed';
      return result;
    } catch (error) {
      if (dependencies.isAmbiguousError(error)) {
        phase.value = 'uncertain';
      } else {
        stagedRequest.value = null;
        frozenSubmission.value = null;
        phase.value = 'draft';
      }
      throw error;
    }
  };

  const retryFrozenSubmission = async (): Promise<ReleaseReceipt> => {
    if (phase.value !== 'uncertain') throw new Error('当前没有结果不明确的发布请求');
    return submitFrozen();
  };

  const selectFailedReceiptItems = () => {
    if (phase.value !== 'completed' || !receipt.value) return;
    const failedConfigIDs = new Set(
      receipt.value.items.filter(item => !item.success).map(item => item.config_id)
    );
    selectedTargets.value = new Map(
      Array.from(selectedTargets.value.entries()).filter(([configId]) =>
        failedConfigIDs.has(configId)
      )
    );
    invalidateDraft();
  };

  return {
    environment,
    keyword,
    releaseRef,
    inputRows,
    pageNum,
    pageSize,
    targetPage,
    targets,
    total,
    selectedTargets,
    selectedCount,
    targetsLoading,
    phase,
    preflightResult,
    receipt,
    stagedRequest,
    frozenSubmission,
    locked,
    canPreflight,
    invalidateDraft,
    setEnvironment,
    loadTargets,
    searchTargets,
    changePage,
    changePageSize,
    replaceCurrentPageSelection,
    addInputRow,
    removeInputRow,
    buildInputs,
    buildRequest,
    runPreflight,
    cancelStagedSubmission,
    submitFrozen,
    retryFrozenSubmission,
    selectFailedReceiptItems,
  };
}
