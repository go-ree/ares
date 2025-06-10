<template>
  <el-dialog
    v-model="dialogVisible"
    :title="`${currentLog.serviceName || '未知服务'} - 发布日志详情`"
    width="80%"
    destroy-on-close
    class="log-dialog"
    @close="handleDialogClose"
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
import { ref, watch, nextTick, onUnmounted } from 'vue'
import { useDeployStatus } from '@/composables/useDeployStatus'
import { useLogStreaming } from '@/composables/useLogStreaming'

// 使用组合式函数
const { getEnvLabel, getStatusType } = useDeployStatus()
const { 
  ciLog, 
  cdLog, 
  ciLogLoading, 
  cdLogLoading, 
  isStreamingCi, 
  isStreamingCd,
  ciLogContainer,
  cdLogContainer,
  fetchLogs,
  fetchCiLogs,
  fetchCdLogs,
  cleanupEventSources,
  clearLogs,
  manualScrollToBottom
} = useLogStreaming()

// 发布中服务列表数据
interface DeployingService {
  id: number
  serviceName: string
  branch: string
  environment: string
  status: string
  progress: number
  startTime: string
  operator: string
  message?: string
  taskId: number
  ciJobName?: string
  cdJobName?: string
  ciBuildId?: number
  cdBuildId?: number
  products?: string
  auto_deploy?: number
  pipelineParam?: {
    env: string
    image: string
    branch: string
    git_url: string
    app_name: string
    [key: string]: any
  }
}

// Props
const props = defineProps<{
  visible: boolean
  currentLog: DeployingService
}>()

// Emits
const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
}>()

// 对话框可见性
const dialogVisible = ref(false)
watch(() => props.visible, (val) => {
  dialogVisible.value = val
  // 对话框打开时清理并获取日志
  if (val && props.currentLog) {
    console.log('对话框打开，清理并获取日志:', props.currentLog)
    clearLogs()
    fetchLogs(props.currentLog, activeLogTab.value)
  }
})

watch(dialogVisible, (val) => {
  emit('update:visible', val)
})

// 当前激活的日志标签页
const activeLogTab = ref('ci')

// 监听日志标签页切换
watch(activeLogTab, async (newTab) => {
  if (dialogVisible.value && props.currentLog) {
    console.log('切换日志标签页:', newTab)
    if (newTab === 'ci') {
      await fetchCiLogs(props.currentLog)
    } else if (newTab === 'cd') {
      await fetchCdLogs(props.currentLog)
    }
  }
})

// 监听currentLog变化
watch(() => props.currentLog, (newLog) => {
  if (dialogVisible.value && newLog) {
    console.log('日志记录变化，重新获取日志:', newLog)
    clearLogs()
    fetchLogs(newLog, activeLogTab.value)
  }
}, { deep: true })

// 监听CI日志内容变化，自动滚动到底部
watch(ciLog, () => {
  if (ciLog.value && activeLogTab.value === 'ci') {
    nextTick(() => {
      manualScrollToBottom()
    })
  }
})

// 监听CD日志内容变化，自动滚动到底部
watch(cdLog, () => {
  if (cdLog.value && activeLogTab.value === 'cd') {
    nextTick(() => {
      manualScrollToBottom()
    })
  }
})

// 处理对话框关闭
const handleDialogClose = () => {
  // 清理SSE连接
  cleanupEventSources()
  
  // 清理日志相关数据
  clearLogs()
  
  emit('update:visible', false)
  activeLogTab.value = 'ci'
}

// 组件卸载时清理资源
onUnmounted(() => {
  cleanupEventSources()
})
</script>

<style scoped>
.log-dialog {
  :deep(.el-dialog__body) {
    padding: 0;
  }
  
  :deep(.el-dialog) {
    max-height: 70vh;
    height: 70vh;
    min-height: 500px;
  }
}

.log-dialog-content {
  display: flex;
  flex-direction: column;
  height: 100%;
  max-height: 100%;
}

.log-header {
  padding: 20px;
  border-bottom: 1px solid var(--el-border-color-light);
  flex-shrink: 0;
}

.log-info {
  :deep(.el-descriptions__label) {
    width: 100px;
    justify-content: flex-end;
  }
}

.log-tabs {
  flex: 1;
  display: flex;
  flex-direction: column;
  overflow: visible !important;
  min-height: 0;
  
  :deep(.el-tabs__header) {
    flex-shrink: 0;
    margin-bottom: 0;
    position: sticky;
    top: 0;
    z-index: 10;
    background: #fff;
    border-bottom: 1px solid var(--el-border-color-light);
  }
  
  :deep(.el-tabs__content) {
    flex: 1;
    overflow: visible !important;
    padding: 0;
    height: 100%;
    display: flex;
    flex-direction: column;
  }
  
  :deep(.el-tab-pane) {
    height: 100%;
    overflow: visible !important;
    display: flex;
    flex-direction: column;
  }
}

.log-container {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.log-detail-content {
  flex: 1;
  padding: 20px;
  background-color: #1e1e1e !important;
  position: relative;
  overflow: auto !important;
  overflow-y: scroll !important;
  overflow-x: auto !important;
  min-height: 0;
  max-height: 50vh;
  
  /* 强制滚动样式 */
  scroll-behavior: smooth;
  -webkit-overflow-scrolling: touch;
  
  /* 确保背景色始终正确 */
  color: #d4d4d4 !important;
  
  /* 防止内容闪烁 */
  will-change: auto;
  
  /* 自定义滚动条样式 */
  &::-webkit-scrollbar {
    width: 12px;
    height: 12px;
    background-color: transparent;
  }
  
  &::-webkit-scrollbar-track {
    background: #1e1e1e;
    border-radius: 6px;
    border: 1px solid #1e1e1e;
  }
  
  &::-webkit-scrollbar-thumb {
    background: #666;
    border-radius: 6px;
    border: 1px solid #1e1e1e;
    
    &:hover {
      background: #888;
    }
  }
  
  &::-webkit-scrollbar-corner {
    background: #1e1e1e;
  }
  
  /* 处理滚动条可能遮挡的问题 */
  &::-webkit-scrollbar-track-piece {
    background: #1e1e1e;
  }
  
  /* 确保滚动条不会影响背景色 */
  &::-webkit-scrollbar-button {
    background: #1e1e1e;
  }
  
  /* 确保所有子元素都继承正确的背景色 */
  & * {
    background-color: transparent !important;
  }
  
  /* 特别处理pre元素 */
  & pre {
    background-color: transparent !important;
    color: #d4d4d4 !important;
  }
}

.log-text {
  margin: 0;
  color: #d4d4d4 !important;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Consolas', monospace;
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-wrap: break-word;
  background-color: transparent !important;
  display: block;
  width: 100%;
  height: auto;
  max-width: 100%;
  
  /* 防止内容闪烁 */
  will-change: auto;
  
  /* 确保文本颜色始终正确 */
  color: #d4d4d4 !important;
  
  /* 防止继承错误的背景色 */
  background: transparent !important;
  background-color: transparent !important;
}

.empty-log {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--el-text-color-secondary);
  font-size: 14px;
}

.truncate-text {
  display: inline-block;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}

.error-message {
  color: var(--el-color-danger);
  font-weight: 500;
  max-width: 200px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  display: inline-block;
}

.streaming-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 16px;
  padding: 8px 16px;
  background-color: rgba(30, 30, 30, 0.9) !important;
  border: 1px solid rgba(64, 158, 255, 0.3);
  border-radius: 4px;
  color: #409eff;
  font-size: 14px;
  position: relative;
  z-index: 1;
  
  /* 确保不会影响背景色 */
  backdrop-filter: none;
  -webkit-backdrop-filter: none;
}

.streaming-indicator .el-icon {
  margin-right: 8px;
  font-size: 16px;
}

.log-controls {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 10px 20px;
  background-color: #f5f5f5;
  border-top: 1px solid #e0e0e0;
  flex-shrink: 0;
}

/* 处理流式传输时的状态 */
.log-detail-content:empty,
.log-detail-content:not(:has(.log-text)) {
  background-color: #1e1e1e !important;
  color: #d4d4d4 !important;
}

/* 确保在加载状态下也保持正确的背景色 */
.log-detail-content:has(.el-loading-mask) {
  background-color: #1e1e1e !important;
}

/* 防止Element Plus的loading遮罩影响背景色 */
.log-detail-content :deep(.el-loading-mask) {
  background-color: rgba(30, 30, 30, 0.8) !important;
}

/* 处理流式传输过程中的状态 */
.log-detail-content:has(.streaming-indicator) {
  background-color: #1e1e1e !important;
}

/* 确保streaming-indicator不会影响背景色 */
.log-detail-content .streaming-indicator {
  background-color: rgba(30, 30, 30, 0.9) !important;
  color: #409eff !important;
}

/* 确保在内容更新时背景色不变 */
.log-detail-content:has(pre) {
  background-color: #1e1e1e !important;
}

/* 防止任何可能的泛白 */
.log-detail-content,
.log-detail-content * {
  background-color: transparent !important;
}

.log-detail-content {
  background-color: #1e1e1e !important;
}

/* 特别处理streaming-indicator，确保不会导致泛白 */
.log-detail-content .streaming-indicator,
.log-detail-content .streaming-indicator * {
  background-color: rgba(30, 30, 30, 0.9) !important;
  color: #409eff !important;
}

/* 确保streaming-indicator的父容器背景色正确 */
.log-detail-content:has(.streaming-indicator) {
  background-color: #1e1e1e !important;
}
</style> 