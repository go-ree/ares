<template>
  <div class="deploying-list">
    <div class="section-title">
      <el-icon><Loading /></el-icon>
      <span>正在发布的服务</span>
      <el-button
        type="primary"
        link
        :loading="deployingLoading"
        @click="refreshDeployingList"
      >
        <el-icon><Refresh /></el-icon>
        刷新
      </el-button>
    </div>
    <el-table 
      :data="deployingList" 
      style="width: 100%" 
      border
      v-loading="deployingLoading"
    >
      <el-table-column prop="serviceName" label="服务名称" min-width="150" />
      <el-table-column prop="branch" label="发布分支" min-width="150" />
      <el-table-column prop="environment" label="环境" width="100">
        <template #default="{ row }">
          <el-tag :type="getEnvType(row.environment)">
            {{ getEnvLabel(row.environment) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="status" label="状态" width="120">
        <template #default="{ row }">
          <el-tag :type="getStatusType(row.status)">
            {{ row.status }}
            <el-icon v-if="row.status === '发布中'" class="is-loading"><Loading /></el-icon>
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="progress" label="进度" width="200">
        <template #default="{ row }">
          <el-progress 
            :percentage="row.progress" 
            :status="getProgressStatus(row.status)"
            :stroke-width="15"
          />
        </template>
      </el-table-column>
      <el-table-column prop="startTime" label="开始时间" width="160" />
      <el-table-column prop="operator" label="操作人" width="100" />
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button-group>
            <el-button
              type="primary"
              link
              :disabled="row.status !== '发布中'"
              @click="handleCancelDeploy(row)"
            >
              取消发布
            </el-button>
            <el-button
              type="primary"
              link
              @click="handleViewLog(row)"
            >
              查询日志
            </el-button>
          </el-button-group>
        </template>
      </el-table-column>
    </el-table>
    <div v-if="deployingList.length === 0 && !deployingLoading" class="empty-tip">
      暂无正在发布的服务
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Loading, Refresh } from '@element-plus/icons-vue'
import { useDeployStatus } from '@/composables/useDeployStatus'

// 使用组合式函数
const { getEnvType, getEnvLabel, getStatusType, getProgressStatus, getDeployStatus, calculateProgress, formatDateTime } = useDeployStatus()

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

const deployingList = ref<DeployingService[]>([])
const deployingLoading = ref(false)

// 定义事件
const emit = defineEmits<{
  (e: 'view-log', service: DeployingService): void
}>()

// 取消发布
const handleCancelDeploy = async (row: DeployingService) => {
  try {
    await ElMessageBox.confirm(
      `确定要取消 ${row.serviceName} 的发布吗？`,
      '取消发布',
      {
        confirmButtonText: '确定',
        cancelButtonText: '取消',
        type: 'warning'
      }
    )
    
    // TODO: 调用取消发布 API
    row.status = '已取消'
    ElMessage.success('已取消发布')
  } catch (error) {
    if (error !== 'cancel') {
      ElMessage.error('取消发布失败')
    }
  }
}

// 查看日志
const handleViewLog = (row: DeployingService) => {
  emit('view-log', row)
}

// 刷新发布中服务列表
const refreshDeployingList = async () => {
  deployingLoading.value = true
  try {
    const response = await fetch('/api/v1/deploy/publish/status', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      }
    })
    
    if (!response.ok) {
      throw new Error('获取发布中服务列表失败')
    }
    
    const data = await response.json()
    if (data.code !== 1) {
      throw new Error(data.msg || '获取发布中服务列表失败')
    }
    
    // 将 API 返回的数据转换为组件需要的格式
    deployingList.value = (data.result || []).map((item: any) => ({
      id: item.task_id,
      serviceName: item.app_name,
      branch: item.branch,
      environment: item.env,
      status: getDeployStatus(item.status),
      progress: calculateProgress(item.status),
      startTime: formatDateTime(item.created_at),
      operator: item.publisher,
      message: item.message === 'NULL' ? '' : item.message,
      taskId: item.task_id,
      ciJobName: item.ci_job_name === 'NULL' ? '' : item.ci_job_name,
      cdJobName: item.cd_job_name === 'NULL' ? '' : item.cd_job_name,
      ciBuildId: item.ci_build_id || null,
      cdBuildId: item.cd_build_id || null,
      products: item.products === 'NULL' ? '' : item.products,
      pipelineParam: item.pipeline_param
    }))
  } catch (error) {
    console.error('获取发布中服务列表失败:', error)
    ElMessage.error(error instanceof Error ? error.message : '获取发布中服务列表失败')
  } finally {
    deployingLoading.value = false
  }
}

// 定时刷新发布中服务列表
let refreshTimer: number | null = null

// 组件挂载时获取发布中服务列表
onMounted(() => {
  refreshDeployingList()
  // 每10秒刷新一次发布中服务列表
  refreshTimer = window.setInterval(refreshDeployingList, 10000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
  }
})
</script>

<style scoped>
.deploying-list {
  margin-top: 32px;
  padding: 20px;
  background: #f8f9fa;
  border-radius: 4px;
}

.section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 16px;
  font-size: 16px;
  font-weight: 500;
  color: #303133;
}

.section-title .el-icon {
  font-size: 18px;
  color: #409eff;
}

.section-title .el-button {
  margin-left: auto;
}

.empty-tip {
  text-align: center;
  color: #909399;
  font-size: 14px;
  padding: 32px 0;
}

:deep(.el-progress-bar__inner) {
  transition: width 0.6s ease;
}

:deep(.el-tag .el-icon) {
  margin-left: 4px;
  animation: rotating 2s linear infinite;
}

@keyframes rotating {
  from {
    transform: rotate(0deg);
  }
  to {
    transform: rotate(360deg);
  }
}
</style> 