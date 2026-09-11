import api from '@/config/api';
import type {
  ApiResponse,
  TaskRecord,
  TaskAttempt,
  PublishLogQueryParams,
  PublishLogQueryResponse,
} from '../models/deploy';

const BASE_URL = '/api/v1/deploy';

// 获取任务详情
export const getTaskDetail = (taskId: number) => {
  return api.get<ApiResponse<TaskRecord>>(`${BASE_URL}/publish/query/${taskId}`);
};

// 查询发布日志
export const queryPublishLogs = async (params: PublishLogQueryParams) => {
  return api.post<ApiResponse<PublishLogQueryResponse>>(`${BASE_URL}/publish/query`, params);
};

export type LegacyTaskLogType = 'ci' | 'cd';

// 日志传输不复用 Axios 实例；统一在 service 中构造同源、只读的日志 URL。
export const taskStepLogStreamUrl = (
  taskId: number,
  stepKey: string,
  cursor?: string,
  attempt?: number
) => {
  const path = `/api/v1/tasks/${taskId}/steps/${encodeURIComponent(stepKey)}/logs/stream`;
  const params = new URLSearchParams();
  if (cursor) params.set('cursor', cursor);
  if (attempt !== undefined) params.set('attempt', String(attempt));
  return params.size ? `${path}?${params.toString()}` : path;
};

export const getTaskAttempts = (taskId: number, stepKey: string) =>
  api.get<ApiResponse<TaskAttempt[]>>(
    `/api/v1/tasks/${taskId}/steps/${encodeURIComponent(stepKey)}/attempts`
  );

export const retryTaskStep = (taskId: number, stepKey: string, expectedAttempt: number) =>
  api.post(`/api/v1/tasks/${taskId}/steps/${encodeURIComponent(stepKey)}/retry`, {
    expected_attempt: expectedAttempt,
  });

/** @deprecated 仅供 engine_version < 2 的历史任务读取旧 Jenkins 日志。 */
export const legacyTaskLogStreamUrl = (
  taskId: number,
  logType: LegacyTaskLogType,
  cursor?: string
) => {
  const params = new URLSearchParams({
    task_id: String(taskId),
    log_type: logType,
  });
  if (cursor && /^\d+$/.test(cursor)) params.set('start', cursor);
  return `/api/v1/job/stream/log?${params.toString()}`;
};
