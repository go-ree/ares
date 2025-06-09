<template>
  <div class="home-wrapper">
    <el-row :gutter="20">
      <!-- 欢迎卡片 -->
      <el-col :span="8">
        <el-card class="home-card">
          <template #header>
            <span>欢迎使用</span>
          </template>
          <div class="welcome-content card-content">
            <el-icon class="welcome-icon"><Promotion /></el-icon>
            <div class="welcome-title">ChaosCanvas</div>
            <div class="welcome-desc">一站式应用发布管理平台</div>
          </div>
        </el-card>
      </el-col>
      <!-- 应用统计卡片 -->
      <el-col :span="8">
        <el-card class="home-card">
          <template #header>
            <span>应用统计</span>
          </template>
          <div class="stats-list card-content">
            <div class="stats-item">
              <el-icon class="stats-icon"><Files /></el-icon>
              <span>应用总数</span>
              <span class="stats-value">0</span>
            </div>
            <div class="stats-item">
              <el-icon class="stats-icon"><Clock /></el-icon>
              <span>待发布</span>
              <span class="stats-value">0</span>
            </div>
            <div class="stats-item">
              <el-icon class="stats-icon"><Finished /></el-icon>
              <span>已发布</span>
              <span class="stats-value">0</span>
            </div>
          </div>
        </el-card>
      </el-col>
      <!-- 快捷操作卡片 -->
      <el-col :span="8">
        <el-card class="home-card">
          <template #header>
            <span>快捷操作</span>
          </template>
          <div class="quick-actions card-content">
            <el-button type="primary" size="large" class="quick-btn" @click="goApply" plain>
              <el-icon><Plus /></el-icon><span>申请应用</span>
            </el-button>
            <el-button type="success" size="large" class="quick-btn" @click="goMerge" plain>
              <el-icon><Link /></el-icon><span>代码合并</span>
            </el-button>
            <el-button type="warning" size="large" class="quick-btn" @click="goDeploy" plain>
              <el-icon><UploadFilled /></el-icon><span>服务发布</span>
            </el-button>
          </div>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { useRouter } from 'vue-router'
import { Promotion, Files, Clock, Finished, Plus, Link, UploadFilled } from '@element-plus/icons-vue'

const router = useRouter()
const goApply = () => router.push('/application/apply')
const goMerge = () => router.push('/publish/merge')
const goDeploy = () => router.push('/publish/deploy')
</script>

<style scoped>
.home-wrapper {
  padding: 24px;
  background: #f5f7fa;
  min-height: 100vh;
}
.el-card {
  border-radius: 8px;
}
.home-card {
  height: 280px;
  display: flex;
  flex-direction: column;
}
.card-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
}
.welcome-content {
  text-align: center;
}
.welcome-title {
  font-size: 2rem;
  font-weight: bold;
  margin-bottom: 12px;
}
.welcome-desc {
  color: #909399;
}
.welcome-icon {
  font-size: 40px;
  color: #409EFF;
  margin-bottom: 12px;
}
.stats-list {
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 16px 0;
}
.stats-item {
  display: flex;
  align-items: center;
  font-size: 16px;
  color: #606266;
}
.stats-icon {
  font-size: 22px;
  margin-right: 8px;
  color: #409EFF;
}
.stats-value {
  margin-left: auto;
  font-weight: bold;
  color: #409EFF;
  font-size: 18px;
}
.quick-actions {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px 0;
  align-items: stretch;
}
.quick-btn {
  margin: 0;
  font-size: 16px;
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  justify-content: center;
  box-sizing: border-box;
}
.quick-btn :deep(.el-button__icon) {
  margin-left: 0 !important;
  margin-right: 8px !important;
}
</style> 