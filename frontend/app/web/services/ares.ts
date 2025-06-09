import { aresApi } from '@/config/api'

// Ares 服务相关的 API 接口
export const aresService = {
  // 查询应用列表
  queryApps: (params: any) => {
    return aresApi.get('/v1/apps/query/appname', { params })
  },

  // 获取发布状态
  getPublishStatus: () => {
    return aresApi.get('/v1/deploy/publish/status')
  },

  // 获取 CI 日志
  getCiLog: (taskId: number) => {
    return aresApi.get(`/v1/deploy/log/ci?task_id=${taskId}`)
  },

  // 获取 CD 日志
  getCdLog: (taskId: number) => {
    return aresApi.get(`/v1/deploy/log/cd?task_id=${taskId}`)
  }
} 