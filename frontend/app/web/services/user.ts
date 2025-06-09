import { userApi } from '@/config/api'

// 用户服务相关的 API 接口
export const userService = {
  // 用户登录
  login: (data: { username: string; password: string }) => {
    return userApi.post('/v1/auth/login', data)
  },

  // 获取用户信息
  getUserInfo: () => {
    return userApi.get('/v1/user/info')
  },

  // 刷新 token
  refreshToken: () => {
    return userApi.post('/v1/auth/refresh')
  },

  // 用户登出
  logout: () => {
    return userApi.post('/v1/auth/logout')
  }
} 