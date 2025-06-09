import { monitorApi } from '@/config/api'

// 监控服务相关的 API 接口
export const monitorService = {
  // 获取系统监控数据
  getSystemMetrics: () => {
    return monitorApi.get('/v1/metrics/system')
  },

  // 获取应用监控数据
  getAppMetrics: (appName: string) => {
    return monitorApi.get(`/v1/metrics/app/${appName}`)
  },

  // 获取告警列表
  getAlerts: (params?: any) => {
    return monitorApi.get('/v1/alerts', { params })
  },

  // 获取日志数据
  getLogs: (params: any) => {
    return monitorApi.get('/v1/logs', { params })
  }
} 