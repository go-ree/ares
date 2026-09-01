<template>
  <div class="login-container">
    <el-card class="login-card">
      <template #header>
        <h2>系统登录</h2>
      </template>
      <el-form @submit.prevent>
        <el-form-item>
          <el-input
            v-model="nameCn"
            placeholder="请输入您的姓名"
            @keyup.enter.prevent="handleLogin"
          />
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            @click="handleLogin"
            style="width: 100%"
            :disabled="!nameCn.trim()"
          >
            登录
          </el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import { useUserStore } from '../stores/user';

const router = useRouter();
const userStore = useUserStore();
const nameCn = ref('');

const handleLogin = () => {
  if (!nameCn.value.trim()) {
    return;
  }

  // 简化登录：只需要中文名称
  userStore.setUserInfo({
    id: Date.now(), // 使用时间戳作为临时ID
    username: nameCn.value,
    nameCn: nameCn.value,
    email: `${nameCn.value}@example.com`,
    roles: ['user'], // 默认用户角色
  });

  router.push('/');
};
</script>

<style scoped>
.login-container {
  height: 100vh;
  display: flex;
  justify-content: center;
  align-items: center;
  background-color: #f5f7fa;
}

.login-card {
  width: 400px;
}

.login-card :deep(.el-card__header) {
  text-align: center;
}

.login-card h2 {
  margin: 0;
  color: #303133;
}
</style>
