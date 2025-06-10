<template>
  <div class="service-deploy">
    <el-card class="deploy-card">
      <el-tabs v-model="activeTab">
        <el-tab-pane label="工具" name="tool">
          <!-- 服务发布工具组件 -->
          <DeployTool />
          
          <!-- 发布中服务列表组件 -->
          <DeployingList @view-log="handleViewLog" />
        </el-tab-pane>
        
        <el-tab-pane label="日志" name="log">
          <!-- 日志查询组件 -->
          <LogQuery ref="logQueryRef" @view-log-detail="handleViewLogDetail" />
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- 日志详情对话框组件 -->
    <LogDetailDialog
      v-model:visible="logDialogVisible"
      :current-log="currentLog"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import DeployTool from './DeployTool.vue'
import DeployingList from './DeployingList.vue'
import LogQuery from './LogQuery.vue'
import LogDetailDialog from './LogDetailDialog.vue'

// 当前激活的标签页
const activeTab = ref('tool')

// 日志对话框相关
const logDialogVisible = ref(false)
const currentLog = ref<any>({})

// LogQuery组件引用
const logQueryRef = ref()

// 查看日志（从发布中服务列表）
const handleViewLog = (service: any) => {
  currentLog.value = service
  logDialogVisible.value = true
}

// 查看日志详情（从日志查询列表）
const handleViewLogDetail = (logItem: any) => {
  // 将LogItem转换为DeployingService格式
  const deployingService = {
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
  
  currentLog.value = deployingService
  logDialogVisible.value = true
}

// 监听主标签页切换
watch(activeTab, (newTab) => {
  if (newTab === 'log') {
    // 切换到日志页时触发查询
    console.log('切换到日志页，触发查询')
    // 使用nextTick确保组件已渲染
    nextTick(() => {
      if (logQueryRef.value && logQueryRef.value.handleSearch) {
        logQueryRef.value.handleSearch()
      }
    })
  }
})
</script>

<style scoped>
.service-deploy {
  min-height: 100%;
  background-color: #f5f7fa;
  color: #213547;
}

/* 强制使用浅色主题 */
.service-deploy :deep(*) {
  color-scheme: light !important;
}

.service-deploy :deep(.el-table) {
  color-scheme: light !important;
  background-color: #fff !important;
}

.service-deploy :deep(.el-table__header-wrapper) {
  background-color: #fafafa !important;
}

.service-deploy :deep(.el-table__body-wrapper) {
  background-color: #fff !important;
}

.deploy-card {
  background: #fff;
  border-radius: 4px;
}

:deep(.el-tabs__header) {
  margin-bottom: 0;
}

:deep(.el-tabs__nav-wrap::after) {
  height: 1px;
}

:deep(.el-tabs__item) {
  height: 40px;
  line-height: 40px;
  font-size: 14px;
}

:deep(.el-tabs__item.is-active) {
  font-weight: 500;
}
</style> 