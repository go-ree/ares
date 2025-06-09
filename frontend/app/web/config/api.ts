import axios from 'axios'
import type { AxiosInstance } from 'axios'

// API 服务配置
const API_CONFIG = {
  ares: {
    baseURL: '/api/ares',
    timeout: 10000
  },
  user: {
    baseURL: '/api/user',
    timeout: 10000
  },
  monitor: {
    baseURL: '/api/monitor',
    timeout: 10000
  }
}

// 创建不同服务的 axios 实例
const createApiInstance = (service: keyof typeof API_CONFIG): AxiosInstance => {
  const config = API_CONFIG[service]
  const instance = axios.create({
    baseURL: config.baseURL,
    timeout: config.timeout,
    headers: {
      'Content-Type': 'application/json'
    }
  })

  // 请求拦截器
  instance.interceptors.request.use(
    (config) => {
      // 添加认证 token
      const token = localStorage.getItem('token')
      if (token) {
        config.headers.Authorization = `Bearer ${token}`
      }
      return config
    },
    (error) => {
      return Promise.reject(error)
    }
  )

  // 响应拦截器
  instance.interceptors.response.use(
    (response) => {
      return response
    },
    (error) => {
      if (error.response) {
        switch (error.response.status) {
          case 401:
            // 未授权，跳转到登录页
            window.location.href = '/login'
            break
          case 403:
            console.error('没有权限访问该资源')
            break
          case 500:
            console.error('服务器错误')
            break
          default:
            console.error('请求失败:', error.response.data)
        }
      }
      return Promise.reject(error)
    }
  )

  return instance
}

// 导出不同服务的 API 实例
export const aresApi = createApiInstance('ares')
export const userApi = createApiInstance('user')
export const monitorApi = createApiInstance('monitor')

// 保持向后兼容的默认实例
export default aresApi 