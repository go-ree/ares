import axios from 'axios';
import api from '@/config/api';
import type {
  BatchReleasePreflightResult,
  BatchReleaseRequest,
  ReleaseApiEnvelope,
  ReleasePreflightItem,
  ReleaseReceipt,
  ReleaseRequest,
  ReleaseTargetPage,
  ReleaseTargetQuery,
} from '@/models/release';

const RELEASES_URL = '/api/v1/releases';
const APP_CONFIGS_URL = '/api/v1/app-configs';
const MAX_INT64_DECIMAL = '9223372036854775807';
const MAX_SIGNED_INT = 2147483647;
const RECEIPT_FAILURE_CODES = new Set([
  'release_target_not_found',
  'environment_disabled',
  'workflow_not_configured',
  'workflow_version_changed',
  'workflow_invalid',
  'executor_unavailable',
]);
const DEFINITIVE_RELEASE_ERRORS = new Map<string, ReadonlySet<number>>([
  ['invalid_request', new Set([400, 415])],
  ['idempotency_key_invalid', new Set([400])],
  ['idempotency_key_conflict', new Set([409])],
  ['release_target_not_found', new Set([404])],
  ['environment_disabled', new Set([409])],
  ['workflow_version_changed', new Set([409])],
  ['request_too_large', new Set([413])],
  ['workflow_not_configured', new Set([422])],
  ['workflow_invalid', new Set([422])],
  ['executor_unavailable', new Set([422])],
]);

export class AmbiguousReleaseResponseError extends Error {
  constructor(message = '发布服务返回了无法确认的创建结果') {
    super(message);
    this.name = 'AmbiguousReleaseResponseError';
  }
}

const unwrap = <T>(envelope: ReleaseApiEnvelope<T>, fallback: string): T => {
  if (envelope.code !== 1) {
    throw new Error(envelope.error || envelope.message || envelope.msg || fallback);
  }
  return envelope.result;
};

const unwrapCreate = (envelope: unknown): unknown => {
  if (!envelope || typeof envelope !== 'object') throw new AmbiguousReleaseResponseError();
  const candidate = envelope as { code?: unknown; result?: unknown };
  if (candidate.code !== 1 || candidate.result === null || candidate.result === undefined) {
    throw new AmbiguousReleaseResponseError();
  }
  return candidate.result;
};

export const isPositiveInt64String = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[1-9][0-9]{0,18}$/.test(value) &&
  (value.length < MAX_INT64_DECIMAL.length || value <= MAX_INT64_DECIMAL);

const isPositiveSignedInt = (value: unknown): value is number =>
  Number.isInteger(value) && Number(value) > 0 && Number(value) <= MAX_SIGNED_INT;

export const validateReleaseReceipt = (
  request: BatchReleaseRequest,
  value: unknown
): ReleaseReceipt => {
  if (!value || typeof value !== 'object') throw new AmbiguousReleaseResponseError();
  const receipt = value as Partial<ReleaseReceipt>;
  if (
    !isPositiveInt64String(receipt.record_id) ||
    !Number.isInteger(receipt.total_count) ||
    !Number.isInteger(receipt.success_count) ||
    !Number.isInteger(receipt.failure_count) ||
    receipt.total_count !== request.items.length ||
    Number(receipt.success_count) < 0 ||
    Number(receipt.failure_count) < 0 ||
    Number(receipt.success_count) + Number(receipt.failure_count) !== receipt.total_count ||
    !Array.isArray(receipt.items) ||
    receipt.items.length !== request.items.length
  ) {
    throw new AmbiguousReleaseResponseError();
  }

  let successCount = 0;
  for (let index = 0; index < request.items.length; index += 1) {
    const requestItem = request.items[index];
    const item = receipt.items[index];
    if (
      !requestItem ||
      !item ||
      item.request_index !== index ||
      item.config_id !== requestItem.config_id ||
      typeof item.success !== 'boolean'
    ) {
      throw new AmbiguousReleaseResponseError();
    }
    if (item.success) {
      if (
        !isPositiveSignedInt(item.task_id) ||
        !isPositiveInt64String(item.workflow_version_id) ||
        (requestItem.expected_workflow_version_id !== undefined &&
          item.workflow_version_id !== requestItem.expected_workflow_version_id) ||
        item.error_code !== undefined
      ) {
        throw new AmbiguousReleaseResponseError();
      }
      successCount += 1;
      continue;
    }
    if (
      item.task_id !== undefined ||
      item.workflow_version_id !== undefined ||
      typeof item.error_code !== 'string' ||
      !RECEIPT_FAILURE_CODES.has(item.error_code)
    ) {
      throw new AmbiguousReleaseResponseError();
    }
  }
  if (
    successCount !== receipt.success_count ||
    request.items.length - successCount !== receipt.failure_count
  ) {
    throw new AmbiguousReleaseResponseError();
  }
  return receipt as ReleaseReceipt;
};

export const getReleaseTargets = async (params: ReleaseTargetQuery): Promise<ReleaseTargetPage> => {
  const response = await api.get<ReleaseApiEnvelope<ReleaseTargetPage>>(`${RELEASES_URL}/targets`, {
    params,
  });
  return unwrap(response.data, '获取发布目标失败');
};

export const preflightRelease = async (
  configId: number,
  request: ReleaseRequest
): Promise<ReleasePreflightItem> => {
  const response = await api.post<ReleaseApiEnvelope<ReleasePreflightItem>>(
    `${APP_CONFIGS_URL}/${configId}/releases/preflight`,
    request
  );
  return unwrap(response.data, '发布预检失败');
};

export const preflightBatchRelease = async (
  request: BatchReleaseRequest
): Promise<BatchReleasePreflightResult> => {
  const response = await api.post<ReleaseApiEnvelope<BatchReleasePreflightResult>>(
    `${RELEASES_URL}/batch/preflight`,
    request
  );
  return unwrap(response.data, '批量发布预检失败');
};

export const createRelease = async (
  configId: number,
  request: ReleaseRequest,
  idempotencyKey: string
): Promise<ReleaseReceipt> => {
  const response = await api.post<ReleaseApiEnvelope<unknown>>(
    `${APP_CONFIGS_URL}/${configId}/releases`,
    request,
    {
      headers: { 'Idempotency-Key': idempotencyKey },
      // This view owns an in-memory frozen pair. A 401 must not navigate away
      // and destroy it before the user can restore the same actor's session.
      skipAuthHandling: true,
    }
  );
  return validateReleaseReceipt(
    { items: [{ config_id: configId, ...request }] },
    unwrapCreate(response.data)
  );
};

export const createBatchRelease = async (
  request: BatchReleaseRequest,
  idempotencyKey: string
): Promise<ReleaseReceipt> => {
  const response = await api.post<ReleaseApiEnvelope<unknown>>(`${RELEASES_URL}/batch`, request, {
    headers: { 'Idempotency-Key': idempotencyKey },
    skipAuthHandling: true,
  });
  return validateReleaseReceipt(request, unwrapCreate(response.data));
};

export const isAmbiguousReleaseError = (error: unknown): boolean => {
  if (error instanceof AmbiguousReleaseResponseError) return true;
  if (!axios.isAxiosError(error)) return false;
  if (!error.response) return true;
  // Once a canonical create has been dispatched, authentication, authorization,
  // and admission failures do not prove that an earlier attempt using the same
  // frozen pair did not commit. Keep that pair available for a safe replay.
  if ([401, 403, 408, 429].includes(error.response.status) || error.response.status >= 500) {
    return true;
  }
  const data = error.response.data;
  if (!data || typeof data !== 'object' || !('error' in data)) return true;
  const code = (data as { error?: unknown }).error;
  if (code === 'outcome_unknown' || code === 'idempotency_request_in_progress') return true;
  if (typeof code !== 'string') return true;
  return !DEFINITIVE_RELEASE_ERRORS.get(code)?.has(error.response.status);
};

export const getReleaseRetryAfterSeconds = (error: unknown): number => {
  if (!axios.isAxiosError(error) || !error.response) return 0;
  const headers = error.response.headers;
  let raw: unknown;
  if ('get' in headers && typeof headers.get === 'function') raw = headers.get('retry-after');
  else raw = headers['retry-after'];
  if (typeof raw !== 'string' || !/^\d+$/.test(raw)) return 0;
  return Math.min(30, Math.max(0, Number(raw)));
};

export const getReleaseApiErrorMessage = (error: unknown, fallback = '请求失败'): string => {
  if (axios.isAxiosError<ReleaseApiEnvelope<unknown>>(error)) {
    const response = error.response?.data;
    return response?.error || response?.message || response?.msg || error.message || fallback;
  }
  return error instanceof Error ? error.message : fallback;
};
