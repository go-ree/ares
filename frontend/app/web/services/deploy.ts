import api from '@/config/api'
import type { 
  DeployInfo, 
  DeployRequest, 
  BatchDeployRequest,
  BatchDeployResponse,
  DeployQueryParams, 
  PageResponse, 
  ApiResponse,
  TaskRecord
} from '../models/deploy'

const BASE_URL = '/api/v1/deploy'

// 用户信息接口
interface UserInfo {
  username: string
  nameCn: string
}

// 批量发布服务，需要传入用户信息
export const batchDeploy = async (deployRequests: Omit<DeployRequest, 'publisher'>[], userInfo: UserInfo) => {
  if (!userInfo) {
    throw new Error('用户信息不能为空')
  }

  // 构建批量发布请求，包含发布人信息
  const batchRequest: BatchDeployRequest = {
    batch_publish: deployRequests.map(request => ({
      ...request,
      publisher: userInfo.nameCn  // 使用中文名作为发布人
    }))
  }

  return api.post<ApiResponse<BatchDeployResponse>>(`${BASE_URL}/publish/batch`, batchRequest)
}

// 单个应用发布（内部使用批量发布接口）
export const createDeploy = async (request: Omit<DeployRequest, 'publisher'>, userInfo: UserInfo) => {
  const response = await batchDeploy([request], userInfo)
  return {
    ...response,
    data: {
      ...response.data,
      result: response.data.result.task_records[0] // 返回第一个任务记录
    }
  }
}

// 查询发布列表
export const queryDeploys = async (params: DeployQueryParams) => {
  return api.post<ApiResponse<PageResponse<DeployInfo>>>(`${BASE_URL}/query`, params)
}

// 获取发布列表
export const getDeployList = (params: DeployQueryParams) => {
  return api.get<ApiResponse<PageResponse<DeployInfo>>>(`${BASE_URL}/list`, { params })
}

// 获取发布详情
export const getDeployDetail = (deployId: number) => {
  return api.get<ApiResponse<DeployInfo>>(`${BASE_URL}/${deployId}`)
}

// 获取任务详情
export const getTaskDetail = (taskId: number) => {
  return api.get<ApiResponse<TaskRecord>>(`${BASE_URL}/publish/query/${taskId}`)
}

// 取消发布
export const cancelDeploy = (deployId: number, userInfo: UserInfo, comment?: string) => {
  if (!userInfo) {
    throw new Error('用户信息不能为空')
  }

  return api.post<ApiResponse<void>>(`${BASE_URL}/${deployId}/cancel`, {
    publisher: userInfo.username,
    publisher_cn: userInfo.nameCn,
    comment
  })
}

// 获取发布日志
export const getDeployLogs = (deployId: number) => {
  return api.get<ApiResponse<string>>(`${BASE_URL}/${deployId}/logs`)
}

// 重新发布
export const redeploy = async (deployId: number, userInfo: UserInfo, comment?: string) => {
  if (!userInfo) {
    throw new Error('用户信息不能为空')
  }

  return api.post<ApiResponse<DeployInfo>>(`${BASE_URL}/${deployId}/redeploy`, {
    publisher: userInfo.username,
    publisher_cn: userInfo.nameCn,
    comment
  })
} 