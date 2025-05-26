import axios from 'axios'
import type { AppInfo, AppQueryParams, PageResponse } from '../models/application'

const BASE_URL = '/api/v1'

// 查询应用列表
export const queryApps = async (params: AppQueryParams) => {
  return axios.post<PageResponse<AppInfo>>(`${BASE_URL}/apps/query`, params)
}

// 获取应用列表
export const getAppList = (params: AppQueryParams) => {
  return axios.get<PageResponse<AppInfo>>(`${BASE_URL}/list`, { params })
}

// 获取应用详情
export const getAppDetail = (id: number) => {
  return axios.get<AppInfo>(`${BASE_URL}/${id}`)
}

// 创建应用
export const createApp = (data: Partial<AppInfo>) => {
  return axios.post<AppInfo>(`${BASE_URL}/create`, data)
}

// 更新应用
export const updateApp = (id: number, data: Partial<AppInfo>) => {
  return axios.put<AppInfo>(`${BASE_URL}/${id}`, data)
}

// 删除应用
export const deleteApp = (id: number) => {
  return axios.delete(`${BASE_URL}/${id}`)
}

// 审核应用
export const reviewApp = (id: number, approved: boolean, comment?: string) => {
  return axios.post(`${BASE_URL}/${id}/review`, { approved, comment })
} 