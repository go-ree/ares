import api from '@/config/api';
import type {
  DeployInfo,
  DeployRequest,
  BatchDeployRequest,
  BatchDeployResponse,
  DeployQueryParams,
  PageResponse,
  ApiResponse,
  TaskRecord,
  TaskRecordResult,
  PublishLogQueryParams,
  PublishLogQueryResponse,
} from '../models/deploy';

const BASE_URL = '/api/v1/deploy';

// 发布人由服务端从会话中确定，客户端不可代填身份。
export const batchDeploy = async (deployRequests: DeployRequest[]) => {
  const batchRequest: BatchDeployRequest = {
    batch_publish: deployRequests,
  };

  return api.post<ApiResponse<BatchDeployResponse>>(`${BASE_URL}/publish/batch`, batchRequest);
};

// 单个应用发布（内部使用批量发布接口）
export const createDeploy = async (
  request: DeployRequest
): Promise<{
  data: ApiResponse<TaskRecordResult>;
  status: number;
  statusText: string;
}> => {
  const response = await batchDeploy([request]);
  // batchDeploy 返回的是 BatchDeployResponse，但后端响应在不同场景下可能出现：
  // - code=0，result=null，error 内包含 task_records
  // - code=1，但 task_records[0].success=false 且 task_record=null（任务实际创建失败）
  // 这里统一做兜底解析，避免前端出现 undefined/null 读取错误，并把后端错误信息透出。
  const data = response.data as any;
  const baseMessage: string = data?.msg || data?.message || '发布失败';

  const payload = data?.result ?? data?.error; // 兼容 result=null 时把 error 当作承载体
  const firstTaskRecord: TaskRecordResult | undefined = payload?.task_records?.[0];

  if (!firstTaskRecord) {
    throw new Error(`${baseMessage}：接口未返回 task_records`);
  }

  // code!=1 或单条任务 success=false / task_record 为空，都视为失败
  if (data?.code !== 1 || !firstTaskRecord.success || !firstTaskRecord.task_record) {
    throw new Error(firstTaskRecord.error || baseMessage);
  }

  return {
    data: {
      ...response.data,
      result: firstTaskRecord, // 返回第一个任务记录结果
    },
    status: response.status,
    statusText: response.statusText,
  };
};

// 查询发布列表
export const queryDeploys = async (params: DeployQueryParams) => {
  return api.post<ApiResponse<PageResponse<DeployInfo>>>(`${BASE_URL}/query`, params);
};

// 获取发布列表
export const getDeployList = (params: DeployQueryParams) => {
  return api.get<ApiResponse<PageResponse<DeployInfo>>>(`${BASE_URL}/list`, { params });
};

// 获取发布详情
export const getDeployDetail = (deployId: number) => {
  return api.get<ApiResponse<DeployInfo>>(`${BASE_URL}/${deployId}`);
};

// 获取任务详情
export const getTaskDetail = (taskId: number) => {
  return api.get<ApiResponse<TaskRecord>>(`${BASE_URL}/publish/query/${taskId}`);
};

// 取消发布
export const cancelDeploy = (deployId: number, comment?: string) =>
  api.post<ApiResponse<void>>(`${BASE_URL}/${deployId}/cancel`, { comment });

// 获取发布日志
export const getDeployLogs = (deployId: number) => {
  return api.get<ApiResponse<string>>(`${BASE_URL}/${deployId}/logs`);
};

// 重新发布
export const redeploy = async (deployId: number, comment?: string) =>
  api.post<ApiResponse<DeployInfo>>(`${BASE_URL}/${deployId}/redeploy`, { comment });

// 查询发布日志
export const queryPublishLogs = async (params: PublishLogQueryParams) => {
  return api.post<ApiResponse<PublishLogQueryResponse>>(`${BASE_URL}/publish/query`, params);
};

// 查询单个任务的日志
export const queryTaskLogs = async (taskId: number, logType: 'ci' | 'cd' = 'ci') => {
  return api.get<ApiResponse<string>>(`${BASE_URL}/task/${taskId}/logs/${logType}`);
};
