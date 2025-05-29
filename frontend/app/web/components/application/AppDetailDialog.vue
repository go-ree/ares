<!-- 应用详情对话框组件 -->
<template>
  <el-dialog
    v-model="dialogVisible"
    title="应用详情"
    width="60%"
    :close-on-click-modal="false"
    destroy-on-close
  >
    <el-descriptions
      v-loading="loading"
      :column="2"
      border
    >
      <el-descriptions-item label="应用ID" :span="2">
        {{ appDetail?.app_id }}
      </el-descriptions-item>
      <el-descriptions-item label="应用名称">
        {{ appDetail?.app_name }}
      </el-descriptions-item>
      <el-descriptions-item label="应用中文名">
        {{ appDetail?.app_name_cn }}
      </el-descriptions-item>
      <el-descriptions-item label="开发语言">
        {{ formatDevLanguage(appDetail?.dev_language) }}
      </el-descriptions-item>
      <el-descriptions-item label="应用状态">
        <el-tag :type="getStatusType(appDetail?.status)">
          {{ getStatusText(appDetail?.status) }}
        </el-tag>
      </el-descriptions-item>
      <el-descriptions-item label="负责人">
        {{ appDetail?.owner_cn }}
      </el-descriptions-item>
      <el-descriptions-item label="Git仓库">
        <el-link
          v-if="appDetail?.git_url"
          type="primary"
          :href="appDetail.git_url"
          target="_blank"
        >
          {{ appDetail.git_url }}
        </el-link>
        <span v-else>-</span>
      </el-descriptions-item>
      <el-descriptions-item label="创建时间">
        {{ formatDate(appDetail?.created_at) }}
      </el-descriptions-item>
      <el-descriptions-item label="更新时间">
        {{ formatDate(appDetail?.updated_at) }}
      </el-descriptions-item>
      <el-descriptions-item label="中文描述" :span="2">
        {{ appDetail?.description_cn || '-' }}
      </el-descriptions-item>
    </el-descriptions>

    <template #footer>
      <span class="dialog-footer">
        <el-button @click="dialogVisible = false">关闭</el-button>
      </span>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { getAppDetail } from '@/services/application'
import type { AppInfo } from '@/models/application'
import { AppStatus } from '@/models/application'

const props = defineProps<{
  visible: boolean
  appId?: number
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
}>()

// 对话框可见性
const dialogVisible = ref(false)
watch(() => props.visible, (val) => {
  dialogVisible.value = val
})
watch(dialogVisible, (val) => {
  emit('update:visible', val)
})

// 加载状态
const loading = ref(false)

// 应用详情数据
const appDetail = ref<AppInfo>()

// 获取应用详情
const fetchAppDetail = async () => {
  if (!props.appId) return
  
  loading.value = true
  try {
    const response = await getAppDetail(props.appId)
    if (response.data.code === 1) {
      appDetail.value = response.data.result
    } else {
      ElMessage.error(response.data.msg || '获取应用详情失败')
    }
  } catch (error) {
    console.error('获取应用详情失败:', error)
    ElMessage.error('获取应用详情失败')
  } finally {
    loading.value = false
  }
}

// 监听对话框显示状态，显示时获取详情
watch(() => props.visible, (val) => {
  if (val && props.appId) {
    fetchAppDetail()
  }
})

// 格式化开发语言显示
const formatDevLanguage = (language?: string) => {
  if (!language) return '-'
  return language.charAt(0).toUpperCase() + language.slice(1).toLowerCase()
}

// 格式化日期显示
const formatDate = (date?: string) => {
  if (!date) return '-'
  return new Date(date).toLocaleString()
}

// 获取状态标签类型
const getStatusType = (status?: AppStatus) => {
  switch (status) {
    case AppStatus.DEPLOYED:
      return 'success'
    case AppStatus.REJECTED:
      return 'danger'
    case AppStatus.PENDING:
      return 'warning'
    case AppStatus.APPROVED:
      return 'info'
    default:
      return 'info'
  }
}

// 获取状态文本
const getStatusText = (status?: AppStatus) => {
  switch (status) {
    case AppStatus.DEPLOYED:
      return '已部署'
    case AppStatus.REJECTED:
      return '已拒绝'
    case AppStatus.PENDING:
      return '待审核'
    case AppStatus.APPROVED:
      return '已通过'
    default:
      return '未知'
  }
}
</script>

<style scoped>
.dialog-footer {
  display: flex;
  justify-content: flex-end;
  gap: 12px;
}

:deep(.el-descriptions) {
  padding: 20px;
}

:deep(.el-descriptions__label) {
  width: 120px;
  font-weight: bold;
}

:deep(.el-descriptions__content) {
  word-break: break-all;
}
</style> 