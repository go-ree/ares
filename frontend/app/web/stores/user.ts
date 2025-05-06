import { defineStore } from 'pinia'

export interface UserInfo {
  id: number
  username: string
  nameCn: string
  email: string
  roles: string[]
}

interface UserState {
  userInfo: UserInfo | null
}

export const useUserStore = defineStore('user', {
  state: (): UserState => ({
    userInfo: null
  }),

  getters: {
    isLoggedIn: (state) => !!state.userInfo,
    hasRole: (state) => (role: string) => state.userInfo?.roles.includes(role)
  },

  actions: {
    setUserInfo(userInfo: UserInfo) {
      this.userInfo = userInfo
    },

    clearUserInfo() {
      this.userInfo = null
    }
  }
}) 