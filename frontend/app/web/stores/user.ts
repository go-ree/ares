import { defineStore } from 'pinia';

export interface UserInfo {
  id: number;
  username: string;
  nameCn: string;
  email: string;
  roles: string[];
}

interface UserState {
  userInfo: UserInfo | null;
}

export const useUserStore = defineStore('user', {
  state: (): UserState => ({
    userInfo: null,
  }),

  getters: {
    isLoggedIn: state => !!state.userInfo,
    hasRole: state => (role: string) => state.userInfo?.roles.includes(role),
  },

  actions: {
    setUserInfo(userInfo: UserInfo) {
      this.userInfo = userInfo;
      // 保存到本地存储
      localStorage.setItem('userInfo', JSON.stringify(userInfo));
    },

    clearUserInfo() {
      this.userInfo = null;
      // 清除本地存储
      localStorage.removeItem('userInfo');
    },

    // 退出登录
    logout() {
      this.clearUserInfo();
    },

    // 从本地存储恢复用户信息
    restoreUserInfo() {
      const stored = localStorage.getItem('userInfo');
      if (stored) {
        try {
          this.userInfo = JSON.parse(stored);
        } catch (error) {
          console.error('解析用户信息失败:', error);
          localStorage.removeItem('userInfo');
        }
      }
    },
  },
});
