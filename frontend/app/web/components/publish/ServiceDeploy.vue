<template>
  <div class="service-deploy">
    <el-card class="deploy-card">
      <el-tabs v-model="activeTab" class="deploy-tabs">
        <el-tab-pane label="工具" name="tool">
          <!-- 发布工具组件 -->
          <DeployTool />
          
          <!-- 正在发布的服务列表组件 -->
          <DeployingList @view-log="handleViewLog" />
        </el-tab-pane>
        
        <el-tab-pane label="日志" name="log">
          <!-- 日志查询组件 -->
          <LogQuery @view-log-detail="handleViewLogDetail" />
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- 日志详情对话框组件 -->
    <LogDetail 
      v-model:visible="logDetailVisible"
      :log-data="currentLogData"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import DeployTool from './DeployTool.vue'
import DeployingList from './DeployingList.vue'
import LogQuery from './LogQuery.vue'
import LogDetail from './LogDetail.vue'
import type { DeployingService } from '@/types/deploy'

// 当前激活的标签页
const activeTab = ref('tool')

// 日志详情相关
const logDetailVisible = ref(false)
const currentLogData = ref<DeployingService | undefined>()

// 查看日志（从发布中服务列表）
const handleViewLog = (service: DeployingService) => {
  currentLogData.value = service
  logDetailVisible.value = true
}

// 查看日志详情（从日志查询列表）
const handleViewLogDetail = (logItem: any) => {
  // 将LogItem转换为DeployingService格式
  const deployingService: DeployingService = {
    id: logItem.task_id,
    serviceName: logItem.serviceName,
    branch: logItem.branch,
    environment: logItem.environment,
    status: logItem.status,
    progress: 100, // 已完成的任务进度为100
    startTime: logItem.deployTime,
    operator: logItem.operator,
    message: logItem.message,
    taskId: logItem.task_id,
    ciJobName: logItem.ci_job_name,
    cdJobName: logItem.cd_job_name,
    ciBuildId: logItem.ci_build_id,
    cdBuildId: logItem.cd_build_id,
    products: logItem.products,
    auto_deploy: logItem.auto_deploy
  }
  
  currentLogData.value = deployingService
  logDetailVisible.value = true
}

// 监听主标签页切换
watch(activeTab, (newTab) => {
  console.log('切换到标签页:', newTab)
})

// 组件挂载时确保标签页正确初始化
onMounted(() => {
  console.log('ServiceDeploy组件挂载，当前标签页:', activeTab.value)
})
</script>

<style scoped>
.service-deploy {
  height: 100%;
  padding: 20px;
}

.deploy-card {
  height: 100%;
  background: #fff;
  border-radius: 8px;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.1);
}

.deploy-tabs {
  height: 100%;
}

:deep(.el-card__body) {
  padding: 0;
  height: 100%;
}

/* 标签页头部样式 */
.service-deploy :deep(.el-tabs__header) {
  margin: 0;
  padding: 0 20px;
  background: #fff;
  border-bottom: 1px solid #e4e7ed;
}

.service-deploy :deep(.el-tabs__nav-wrap) {
  padding: 0;
}

.service-deploy :deep(.el-tabs__nav) {
  border: none;
}

.service-deploy :deep(.el-tabs__item) {
  font-size: 14px;
  font-weight: 500;
  color: #606266;
  height: 40px;
  line-height: 40px;
  padding: 0 20px;
}

.service-deploy :deep(.el-tabs__item.is-active) {
  color: #409eff;
  font-weight: 600;
}

.service-deploy :deep(.el-tabs__active-bar) {
  background-color: #409eff;
  height: 2px;
}

/* 标签页内容区域样式 */
.service-deploy :deep(.el-tabs__content) {
  height: calc(100% - 40px);
  overflow: hidden;
  padding: 0;
}

.service-deploy :deep(.el-tab-pane) {
  height: 100%;
  overflow-y: auto;
  padding: 0;
}
</style> 