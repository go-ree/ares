<template>
  <el-result icon="warning" title="没有访问权限" sub-title="当前账号不能访问该页面。">
    <template #extra>
      <el-button type="primary" @click="router.replace('/')">返回首页</el-button>
    </template>
  </el-result>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router';

const router = useRouter();
</script>
