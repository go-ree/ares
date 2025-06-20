<template>
  <el-dialog
    v-model="logDialogVisible"
    :title="`${currentLog.serviceName || '未知服务'} - 发布日志详情`"
    width="80%"
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
      </div>
      
      <div class="log-tabs">
        <el-tabs v-model="activeLogTab">
          <el-tab-pane label="CI 日志" name="ci">
            <div class="log-container">
              <div class="log-detail-content" v-loading="ciLogLoading" ref="ciLogContainer">
                <div v-if="isStreamingCi && !ciLog" class="streaming-indicator">
                  <span>正在实时获取日志...</span>
                </div>
                <pre v-if="ciLog" class="log-text">{{ ciLog }}</pre>
                <div v-else-if="!ciLogLoading && !isStreamingCi" class="empty-log">
                  <el-empty description="暂无 CI 日志" :image-size="60" />
                </div>
              </div>
              <div v-if="ciLog" class="log-controls">
                <el-button @click="manualScrollToBottom" size="small" type="primary">
                  滚动到底部
                </el-button>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane label="CD 日志" name="cd">
            <div class="log-container">
              <div class="log-detail-content" v-loading="cdLogLoading" ref="cdLogContainer">
                <div v-if="isStreamingCd && !cdLog" class="streaming-indicator">
                  <span>正在实时获取日志...</span>
                </div>
                <pre v-if="cdLog" class="log-text">{{ cdLog }}</pre>
                <div v-else-if="!cdLogLoading && !isStreamingCd" class="empty-log">
                  <el-empty description="暂无 CD 日志" :image-size="60" />
                </div>
              </div>
              <div v-if="cdLog" class="log-controls">
                <el-button @click="manualScrollToBottom" size="small" type="primary">
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
import { watch } from 'vue'
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
  isStreamingCi,
  isStreamingCd,
  
  // 工具函数
  getStatusType,
  getEnvLabel,
  manualScrollToBottom,
  
  // 事件处理函数
  fetchLogs,
  handleLogDialogClose
} = useLog()

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
}>()

// 监听visible变化
watch(() => props.visible, (newVal) => {
  if (newVal) {
    logDialogVisible.value = true
    if (props.logData) {
      currentLog.value = props.logData
      fetchLogs(props.logData)
    }
  } else {
    logDialogVisible.value = false
  }
})

// 监听logDialogVisible变化
watch(logDialogVisible, (newVal) => {
  emit('update:visible', newVal)
})

// 监听日志标签页切换
watch(activeLogTab, async (_newTab) => {
  if (logDialogVisible.value && currentLog.value) {
    await fetchLogs(currentLog.value)
  }
})
</script>

<style scoped>
.log-dialog-content {
  max-height: 70vh;
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
  height: 400px;
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
}

.log-text {
  color: #d4d4d4;
  margin: 0;
  white-space: pre-wrap;
  word-wrap: break-word;
}

.streaming-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #909399;
  font-style: italic;
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

:deep(.el-dialog__body) {
  padding: 20px;
}

:deep(.el-descriptions__label) {
  font-weight: 500;
}
</style> 