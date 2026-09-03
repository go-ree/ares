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

// 用户信息接口
interface UserInfo {
  username: string;
  nameCn: string;
}

// 批量发布服务，需要传入用户信息
export const batchDeploy = async (
  deployRequests: Omit<DeployRequest, 'publisher'>[],
  userInfo: UserInfo
) => {
  if (!userInfo) {
    throw new Error('用户信息不能为空');
  }

  // 构建批量发布请求，包含发布人信息
  const batchRequest: BatchDeployRequest = {
    batch_publish: deployRequests.map(request => ({
      ...request,
      publisher: userInfo.nameCn, // 使用中文名作为发布人
    })),
  };

  return api.post<ApiResponse<BatchDeployResponse>>(`${BASE_URL}/publish/batch`, batchRequest);
};

// 单个应用发布（内部使用批量发布接口）
export const createDeploy = async (
  request: Omit<DeployRequest, 'publisher'>,
  userInfo: UserInfo
): Promise<{
  data: ApiResponse<TaskRecordResult>;
  status: number;
  statusText: string;
}> => {
  const response = await batchDeploy([request], userInfo);
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
export const cancelDeploy = (deployId: number, userInfo: UserInfo, comment?: string) => {
  if (!userInfo) {
    throw new Error('用户信息不能为空');
  }

  return api.post<ApiResponse<void>>(`${BASE_URL}/${deployId}/cancel`, {
    publisher: userInfo.username,
    publisher_cn: userInfo.nameCn,
    comment,
  });
};

// 获取发布日志
export const getDeployLogs = (deployId: number) => {
  return api.get<ApiResponse<string>>(`${BASE_URL}/${deployId}/logs`);
};

// 重新发布
export const redeploy = async (deployId: number, userInfo: UserInfo, comment?: string) => {
  if (!userInfo) {
    throw new Error('用户信息不能为空');
  }

  return api.post<ApiResponse<DeployInfo>>(`${BASE_URL}/${deployId}/redeploy`, {
    publisher: userInfo.username,
    publisher_cn: userInfo.nameCn,
    comment,
  });
};

// 查询发布日志
export const queryPublishLogs = async (params: PublishLogQueryParams) => {
  return api.post<ApiResponse<PublishLogQueryResponse>>(`${BASE_URL}/publish/query`, params);
};

// 查询单个任务的日志
export const queryTaskLogs = async (taskId: number, logType: 'ci' | 'cd' = 'ci') => {
  return api.get<ApiResponse<string>>(`${BASE_URL}/task/${taskId}/logs/${logType}`);
};

// SSE流式日志查询接口
export const streamJobLogs = (taskId: number, logType: 'ci' | 'cd') => {
  const url = `/api/v1/job/stream/log?task_id=${taskId}&log_type=${logType}`;

  return new Promise<string>((resolve, reject) => {
    const eventSource = new EventSource(url);
    let logContent = '';

    eventSource.onmessage = (event: MessageEvent) => {
      try {
        // 解析JSON数据
        const data = JSON.parse(event.data);
        if (data.code === 1 && data.result && Array.isArray(data.result)) {
          // 将每一行日志添加到内容中
          logContent += data.result.join('\n') + '\n';
        } else if (data.code === 0) {
          // 处理错误
          reject(new Error(data.msg || data.error || '获取日志失败'));
          eventSource.close();
        }
      } catch (error) {
        console.error('解析SSE数据失败:', error);
        reject(error);
        eventSource.close();
      }
    };

    // 监听错误事件
    eventSource.addEventListener('error', (event: MessageEvent) => {
      try {
        const data = JSON.parse(event.data);
        reject(new Error(data.msg || data.error || '获取日志失败'));
      } catch (error) {
        reject(new Error('获取日志失败'));
      }
      eventSource.close();
    });

    // 监听结束事件
    eventSource.addEventListener('end', () => {
      console.log('SSE流结束');
      eventSource.close();
      resolve(logContent);
    });

    eventSource.onerror = error => {
      console.error('SSE连接错误:', error);
      eventSource.close();
      reject(new Error('SSE连接失败'));
    };

    // 监听连接打开
    eventSource.onopen = () => {
      console.log('SSE连接已建立');
    };

    // 设置超时处理
    const timeout = setTimeout(() => {
      eventSource.close();
      resolve(logContent); // 超时后返回已获取的日志内容
    }, 30000); // 30秒超时

    // 监听连接关闭
    eventSource.addEventListener('close', () => {
      clearTimeout(timeout);
      resolve(logContent);
    });
  });
};
