<template>
  <el-dialog
    v-model="logDialogVisible"
    :title="`${currentLog.serviceName || '未知服务'} - 发布日志详情`"
    width="90%"
    :fullscreen="false"
    destroy-on-close
    class="log-dialog"
    @close="handleLogDialogClose"
  >
    <div class="log-dialog-content">
      <div class="log-header">
        <div class="log-info">
          <el-descriptions :column="3" border>
            <el-descriptions-item label="服务名称">{{ currentLog.serviceName }}</el-descriptions-item>
            <el-descriptions-item label="发布分支">{{ currentLog.branch }}</el-descriptions-item>
            <el-descriptions-item label="环境">{{ getEnvLabel(currentLog.environment) }}</el-descriptions-item>
            <el-descriptions-item label="发布状态">
              <el-tag :type="getStatusType(currentLog.status)">{{ currentLog.status }}</el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="开始时间">{{ currentLog.startTime }}</el-descriptions-item>
            <el-descriptions-item label="操作人">{{ currentLog.operator }}</el-descriptions-item>
            <el-descriptions-item label="自动部署">
              <el-tag :type="currentLog.auto_deploy ? 'success' : 'info'" size="small">
                {{ currentLog.auto_deploy ? '是' : '否' }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="CI Job">{{ currentLog.ciJobName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="CD Job">{{ currentLog.cdJobName || '-' }}</el-descriptions-item>
            <el-descriptions-item label="镜像地址" v-if="currentLog.products">
              <el-tooltip :content="currentLog.products" placement="top">
                <span class="truncate-text">{{ currentLog.products }}</span>
              </el-tooltip>
            </el-descriptions-item>
            <el-descriptions-item label="错误信息" v-if="currentLog.message">
              <el-tooltip :content="currentLog.message" placement="top">
                <span class="error-message">{{ currentLog.message }}</span>
              </el-tooltip>
            </el-descriptions-item>
          </el-descriptions>
        </div>
        
        <!-- 连接状态指示器 -->
        <div v-if="activeConnections.length > 0" class="connection-status">
          <el-tag type="info" size="small">
            <el-icon><Loading /></el-icon>
            活跃连接: {{ activeConnections.length }}
          </el-tag>
          <div class="connection-list">
            <div v-for="conn in activeConnections" :key="conn.id" class="connection-item">
              <el-tag :type="conn.type === 'ci' ? 'primary' : 'success'" size="small">
                {{ conn.type.toUpperCase() }}: {{ conn.jobName }}
              </el-tag>
            </div>
          </div>
        </div>
      </div>
      
      <div class="log-tabs">
        <el-tabs v-model="activeLogTab">
          <el-tab-pane label="CI 日志" name="ci">
            <template #label>
              <span>CI 日志</span>
              <el-tag v-if="isStreamingCi" type="success" size="small" style="margin-left: 8px;">实时</el-tag>
            </template>
            <div class="log-container">
              <div class="log-detail-content" v-loading="ciLogLoading" ref="ciLogContainer">
                <div v-if="ciLogLoading" class="loading-indicator">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  <span>正在获取CI日志...</span>
                </div>
                <div v-else-if="isStreamingCi && !ciLog" class="streaming-indicator">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  <span>正在实时获取日志...</span>
                </div>
                <div v-else-if="ciLog && ciLog.includes('获取CI日志失败')" class="error-log">
                  <div class="error-message">
                    <el-icon><Warning /></el-icon>
                    <span>{{ ciLog }}</span>
                  </div>
                  <el-button @click="retryFetchLogs" type="primary" size="small">
                    重试
                  </el-button>
                </div>
                <pre v-else-if="ciLog" class="log-text">{{ getDisplayLog(ciLog) }}</pre>
                <div v-else class="empty-log">
                  <el-empty description="暂无 CI 日志" :image-size="60" />
                </div>
              </div>
              <div v-if="ciLog && !ciLog.includes('获取CI日志失败')" class="log-controls">
                <el-button @click="() => scrollToBottomPrecise('ci')" size="small" type="primary">
                  滚动到底部
                </el-button>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane label="CD 日志" name="cd">
            <template #label>
              <span>CD 日志</span>
              <el-tag v-if="isStreamingCd" type="success" size="small" style="margin-left: 8px;">实时</el-tag>
            </template>
            <div class="log-container">
              <div class="log-detail-content" v-loading="cdLogLoading" ref="cdLogContainer">
                <div v-if="cdLogLoading" class="loading-indicator">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  <span>正在获取CD日志...</span>
                </div>
                <div v-else-if="isStreamingCd && !cdLog" class="streaming-indicator">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  <span>正在实时获取日志...</span>
                </div>
                <div v-else-if="cdLog && cdLog.includes('获取CD日志失败')" class="error-log">
                  <div class="error-message">
                    <el-icon><Warning /></el-icon>
                    <span>{{ cdLog }}</span>
                  </div>
                  <el-button @click="retryFetchLogs" type="primary" size="small">
                    重试
                  </el-button>
                </div>
                <pre v-else-if="cdLog" class="log-text">{{ getDisplayLog(cdLog) }}</pre>
                <div v-else class="empty-log">
                  <el-empty description="暂无 CD 日志" :image-size="60" />
                </div>
              </div>
              <div v-if="cdLog && !cdLog.includes('获取CD日志失败')" class="log-controls">
                <el-button @click="() => scrollToBottomPrecise('cd')" size="small" type="primary">
                  滚动到底部
                </el-button>
              </div>
            </div>
          </el-tab-pane>
        </el-tabs>
      </div>
    </div>
  </el-dialog>
</template>

<script setup lang="ts">
import { watch, computed, ref, onUnmounted } from 'vue'
import { Loading, Warning } from '@element-plus/icons-vue'
import { useLog } from '@/composables/useLog'
import type { DeployingService } from '@/types/deploy'

const {
  // 响应式数据
  logDialogVisible,
  currentLog,
  activeLogTab,
  ciLog,
  cdLog,
  ciLogLoading,
  cdLogLoading,
  ciLogContainer,
  cdLogContainer,
  
  // 工具函数
  getStatusType,
  getEnvLabel,
  manualScrollToBottom,
  scrollToBottomPrecise,
  getCurrentStreamingStatus,
  getDisplayLog,
  
  // 事件处理函数
  fetchLogs,
  handleLogDialogClose,
  handleLogDialogOpen,
  retryFetchLogs,
  
  // 连接管理
  getActiveConnections,
  
  // 清理函数
  cleanupLogsAndConnections,
  
  // 连接检查相关
  stopConnectionCheck
} = useLog()

// 计算属性：获取当前连接状态
const isStreamingCi = computed(() => getCurrentStreamingStatus('ci'))
const isStreamingCd = computed(() => getCurrentStreamingStatus('cd'))

// 计算属性：获取活跃连接列表
const activeConnections = computed(() => getActiveConnections())

// 记录上一次的服务信息
const lastServiceInfo = ref<string>('')

// 检查是否是同一个服务
const isSameService = (newService: DeployingService) => {
  const newServiceInfo = `${newService.serviceName}_${newService.ciJobName}_${newService.ciBuildId}_${newService.cdJobName}_${newService.cdBuildId}`
  return newServiceInfo === lastServiceInfo.value
}

// 定义props
interface Props {
  visible: boolean
  logData?: DeployingService
}

const props = withDefaults(defineProps<Props>(), {
  visible: false,
  logData: undefined
})

// 定义事件
const emit = defineEmits<{
  'update:visible': [value: boolean]
  'close': []
}>()

// 监听visible变化
watch(() => props.visible, (newVal) => {
  if (newVal) {
    logDialogVisible.value = true
    if (props.logData) {
      // 检查是否是同一个服务
      if (!isSameService(props.logData)) {
        // 如果是不同的服务，先清理之前的日志和连接
        console.log('切换到不同服务，清理之前的日志和连接')
        cleanupLogsAndConnections()
      }
      
      currentLog.value = props.logData
      // 更新服务信息
      lastServiceInfo.value = `${props.logData.serviceName}_${props.logData.ciJobName}_${props.logData.ciBuildId}_${props.logData.cdJobName}_${props.logData.cdBuildId}`
      
      fetchLogs(props.logData)
    }
  } else {
    logDialogVisible.value = false
  }
})

// 监听logDialogVisible变化
watch(logDialogVisible, (newVal) => {
  emit('update:visible', newVal)
  if (!newVal) {
    // 对话框关闭时调用清理函数并触发close事件
    handleLogDialogClose()
    emit('close')
  } else {
    // 对话框打开时调用恢复函数
    handleLogDialogOpen()
  }
})

// 监听日志标签页切换
watch(activeLogTab, async (newTab) => {
  if (logDialogVisible.value && currentLog.value) {
    console.log(`切换到${newTab}标签页`)
    
    // 检查当前标签页是否已经有日志内容
    const hasLogContent = newTab === 'ci' ? !!ciLog.value : !!cdLog.value
    const isConnectionActive = newTab === 'ci' ? 
      getCurrentStreamingStatus('ci') : 
      getCurrentStreamingStatus('cd')
    
    // 如果已经有日志内容或连接活跃，跳过重新获取
    if (hasLogContent || isConnectionActive) {
      console.log(`${newTab}标签页已有日志内容或连接活跃，跳过重新获取`)
      return
    }
    
    // 只有在没有日志内容且没有活跃连接时才获取
    console.log(`${newTab}标签页需要获取日志`)
    await fetchLogs(currentLog.value)
  }
})

// 组件卸载时清理资源
onUnmounted(() => {
  console.log('LogDetail组件卸载，清理资源')
  stopConnectionCheck()
})
</script>

<style scoped>
.log-dialog-content {
  max-height: 80vh;
  overflow-y: auto;
}

.log-header {
  margin-bottom: 20px;
}

.log-info {
  margin-bottom: 16px;
}

.log-tabs {
  margin-top: 20px;
}

/* 确保LogDetail中的标签页样式正确 */
.log-tabs :deep(.el-tabs__header) {
  margin: 0 !important;
  padding: 0 20px !important;
  background: #fff !important;
  border-bottom: 1px solid #e4e7ed !important;
  position: relative !important;
  z-index: 1 !important;
}

.log-tabs :deep(.el-tabs__nav-wrap) {
  padding: 0 !important;
}

.log-tabs :deep(.el-tabs__nav) {
  border: none !important;
}

.log-tabs :deep(.el-tabs__item) {
  font-size: 14px !important;
  font-weight: 500 !important;
  color: #606266 !important;
  height: 40px !important;
  line-height: 40px !important;
  padding: 0 20px !important;
}

.log-tabs :deep(.el-tabs__item.is-active) {
  color: #409eff !important;
  font-weight: 600 !important;
}

.log-tabs :deep(.el-tabs__active-bar) {
  background-color: #409eff !important;
  height: 2px !important;
}

.log-tabs :deep(.el-tabs__content) {
  padding: 20px 0 0 0 !important;
}

.log-container {
  display: flex;
  flex-direction: column;
  height: 600px;
}

.log-detail-content {
  flex: 1;
  overflow-y: auto;
  background: #1e1e1e;
  border-radius: 4px;
  padding: 16px;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: 12px;
  line-height: 1.5;
  position: relative;
  min-height: 500px;
}

.log-text {
  color: #d4d4d4;
  margin: 0;
  white-space: pre-wrap;
  word-wrap: break-word;
  font-size: 13px;
  line-height: 1.6;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Consolas', monospace;
}

.loading-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #909399;
  font-style: italic;
  gap: 8px;
}

.streaming-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #909399;
  font-style: italic;
  gap: 8px;
}

.empty-log {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
}

.log-controls {
  margin-top: 12px;
  display: flex;
  justify-content: flex-end;
}

.truncate-text {
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: inline-block;
}

.error-message {
  color: #f56c6c;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: inline-block;
}

.error-log {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 16px;
  color: #f56c6c;
}

.error-message {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
}

:deep(.el-dialog__body) {
  padding: 20px;
}

:deep(.el-descriptions__label) {
  font-weight: 500;
}

/* 自定义对话框样式 */
:deep(.el-dialog) {
  margin: 5vh auto !important;
  max-height: 90vh;
}

:deep(.el-dialog__header) {
  padding: 20px 20px 10px 20px;
  border-bottom: 1px solid #e4e7ed;
}

:deep(.el-dialog__title) {
  font-size: 16px;
  font-weight: 600;
}

:deep(.el-dialog__body) {
  padding: 20px;
  max-height: calc(90vh - 120px);
  overflow-y: auto;
}

.connection-status {
  margin-top: 12px;
  padding: 8px 12px;
  background: #f5f7fa;
  border-radius: 4px;
  border: 1px solid #e4e7ed;
}

.connection-list {
  margin-top: 8px;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.connection-item {
  display: flex;
  align-items: center;
}
</style> 