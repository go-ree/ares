import api from '@/config/api'
import type { AppInfo, AppQueryParams, PageResponse, ApiResponse } from '../models/application'

const BASE_URL = '/api/v1'

// 查询应用列表
export const queryApps = async (params: AppQueryParams) => {
  return api.post<ApiResponse<PageResponse<AppInfo>>>(`${BASE_URL}/apps/query`, params)
}

// 获取应用列表
export const getAppList = (params: AppQueryParams) => {
  return api.get<ApiResponse<PageResponse<AppInfo>>>(`${BASE_URL}/list`, { params })
}

// 获取应用详情
export const getAppDetail = (id: number) => {
  return api.get<ApiResponse<AppInfo>>(`${BASE_URL}/${id}`)
}

// 创建应用
export const createApp = (data: Partial<AppInfo>) => {
  return api.post<ApiResponse<AppInfo>>(`${BASE_URL}/create`, data)
}

// 更新应用
export const updateApp = (id: number, data: Partial<AppInfo>) => {
  return api.put<ApiResponse<AppInfo>>(`${BASE_URL}/${id}`, data)
}

// 删除应用
export const deleteApp = (id: number) => {
  return api.delete<ApiResponse<void>>(`${BASE_URL}/${id}`)
}

// 审核应用
export const reviewApp = (id: number, approved: boolean, comment?: string) => {
  return api.post<ApiResponse<void>>(`${BASE_URL}/${id}/review`, { approved, comment })
} 