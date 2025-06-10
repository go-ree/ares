<template>
  <div class="service-deploy">
    <el-card class="deploy-card">
      <el-tabs v-model="activeTab">
        <el-tab-pane label="工具" name="tool">
          <div class="deploy-tool">
            <!-- 环境选择区域 -->
            <div class="env-selector">
              <el-radio-group v-model="deployForm.environment" size="large" @change="handleEnvChange">
                <el-radio-button :value="'dev'">开发环境</el-radio-button>
                <el-radio-button :value="'test'">测试环境</el-radio-button>
                <el-radio-button :value="'moni'">模拟环境</el-radio-button>
              </el-radio-group>
              <div v-if="deployForm.environment === 'moni'" class="global-branch-input">
                <span class="branch-label">统一发布分支：</span>
                <div class="branch-input-wrapper">
                  <el-input
                    v-model="globalBranchSuffix"
                    placeholder="请输入分支后缀"
                    style="width: 240px"
                    @input="handleGlobalBranchChange"
                  >
                    <template #prefix>
                      <span class="branch-prefix">release_</span>
                    </template>
                  </el-input>
                </div>
              </div>
            </div>

            <!-- 服务选择表格 -->
            <div v-if="deployForm.environment" class="service-table">
              <el-table :data="selectedServices" style="width: 100%" border>
                <el-table-column label="服务名称" min-width="200">
                  <template #default="{ row, $index }">
                    <el-select
                      v-model="row.serviceName"
                      filterable
                      placeholder="请选择服务"
                      style="width: 100%"
                      @change="(val: string) => handleServiceSelect(val, $index)"
                    >
                      <el-option
                        v-for="service in availableServices"
                        :key="service.name"
                        :label="service.name"
                        :value="service.name"
                      />
                    </el-select>
                  </template>
                </el-table-column>
                <el-table-column prop="branch" label="发布分支" min-width="200">
                  <template #default="{ row }">
                    <template v-if="deployForm.environment === 'moni'">
                      <div class="branch-input-wrapper">
                        <el-input
                          v-model="row.branchSuffix"
                          placeholder="请输入分支后缀"
                          style="width: 240px"
                          @input="(val: string) => handleBranchSuffixChange(val, row)"
                        >
                          <template #prefix>
                            <span class="branch-prefix">release_</span>
                          </template>
                        </el-input>
                      </div>
                    </template>
                    <template v-else>
                      <span class="branch-text">{{ deployForm.environment }}</span>
                    </template>
                  </template>
                </el-table-column>
                <el-table-column prop="status" label="发布状态" width="120">
                  <template #default="{ row }">
                    <el-tag :type="getStatusType(row.status)">
                      {{ row.status }}
                      <el-icon v-if="row.status === '发布中'" class="is-loading"><Loading /></el-icon>
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column prop="lastUpdateTime" label="最后更新" width="160">
                  <template #default="{ row }">
                    <span v-if="row.lastUpdateTime">{{ row.lastUpdateTime }}</span>
                    <span v-else class="text-muted">-</span>
                  </template>
                </el-table-column>
                <el-table-column label="操作" width="280" fixed="right">
                  <template #default="{ row, $index }">
                    <el-button-group>
                      <el-button
                        type="primary"
                        :disabled="!row.serviceName || !row.branch || isServiceProcessing(row.status)"
                        @click="handleDeploySingle(row, $index)"
                      >
                        编译并发布
                      </el-button>
                      <el-button
                        type="warning"
                        :disabled="!row.serviceName || !row.branch || isServiceProcessing(row.status)"
                        @click="handleRedeploySingle(row, $index)"
                      >
                        仅重发
                      </el-button>
                      <el-button
                        type="danger"
                        :disabled="isServiceProcessing(row.status)"
                        @click="handleRemoveService($index)"
                      >
                        删除
                      </el-button>
                    </el-button-group>
                  </template>
                </el-table-column>
              </el-table>

              <!-- 添加服务按钮 -->
              <div class="table-actions">
                <el-button type="primary" plain @click="handleAddService">
                  <el-icon><Plus /></el-icon>
                  添加服务
                </el-button>
              </div>

              <!-- 批量操作按钮 -->
              <div class="batch-actions">
                <el-button 
                  type="primary" 
                  size="large" 
                  :disabled="!hasDeployableServices"
                  @click="handleBatchDeploy"
                >
                  <el-icon><Upload /></el-icon>
                  一键编译并发布
                </el-button>
                <el-button 
                  type="warning" 
                  size="large"
                  :disabled="!hasDeployableServices"
                  @click="handleBatchRedeploy"
                >
                  <el-icon><RefreshRight /></el-icon>
                  一键重发
                </el-button>
              </div>
            </div>

            <!-- 正在发布的服务列表 -->
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
          </div>
        </el-tab-pane>
        
        <el-tab-pane label="日志" name="log">
          <div class="log-content">
            <div class="log-filter">
              <el-form :inline="true" :model="logFilter">
                <el-form-item label="服务名称">
                  <el-select 
                    v-model="logFilter.serviceName" 
                    filterable 
                    placeholder="请选择服务名称"
                    clearable
                    style="width: 200px"
                  >
                    <el-option
                      v-for="service in availableServices"
                      :key="service.name"
                      :label="service.name"
                      :value="service.name"
                    />
                  </el-select>
                </el-form-item>
                <el-form-item label="环境">
                  <el-select v-model="logFilter.environment" placeholder="请选择环境" clearable>
                    <el-option label="全部" value="" />
                    <el-option label="开发环境" value="dev" />
                    <el-option label="测试环境" value="test" />
                    <el-option label="模拟环境" value="moni" />
                  </el-select>
                </el-form-item>
                <el-form-item label="时间范围">
                  <el-date-picker
                    v-model="logFilter.dateRange"
                    type="daterange"
                    range-separator="至"
                    start-placeholder="开始日期"
                    end-placeholder="结束日期"
                    clearable
                  />
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" @click="handleSearch">查询</el-button>
                  <el-button @click="handleResetLogFilter">重置</el-button>
                </el-form-item>
              </el-form>
            </div>
            <div class="log-table">
              <el-table :data="logList" style="width: 100%" v-loading="logLoading">
                <el-table-column prop="serviceName" label="服务名称" min-width="150" />
                <el-table-column prop="branch" label="发布分支" min-width="120" />
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
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column prop="deployTime" label="发布时间" width="160" />
                <el-table-column prop="operator" label="操作人" width="100" />
                <el-table-column prop="auto_deploy" label="自动部署" width="100">
                  <template #default="{ row }">
                    <el-tag :type="row.auto_deploy ? 'success' : 'info'" size="small">
                      {{ row.auto_deploy ? '是' : '否' }}
                    </el-tag>
                  </template>
                </el-table-column>
                <el-table-column label="操作" width="120" fixed="right">
                  <template #default="{ row }">
                    <el-button type="primary" link @click="viewLogDetail(row)">
                      查看日志
                    </el-button>
                  </template>
                </el-table-column>
              </el-table>
              
              <!-- 空数据提示 -->
              <div v-if="logList.length === 0 && !logLoading" class="empty-data">
                <el-empty description="暂无日志数据" />
              </div>
              
              <div class="pagination">
                <el-pagination
                  v-model:current-page="currentPage"
                  v-model:page-size="pageSize"
                  :total="total"
                  :page-sizes="[10, 20, 50, 100]"
                  layout="total, sizes, prev, pager, next"
                  @size-change="handleSizeChange"
                  @current-change="handleCurrentChange"
                />
              </div>
            </div>
          </div>
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <!-- 日志详情对话框 -->
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
              <div class="log-detail-content" v-loading="ciLogLoading" ref="ciLogContainer">
                <div v-if="isStreamingCi" class="streaming-indicator">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  <span>正在实时获取日志...</span>
                </div>
                <pre v-if="ciLog" class="log-text">{{ ciLog }}</pre>
                <div v-else-if="!ciLogLoading" class="empty-log">
                  <el-empty description="暂无 CI 日志" :image-size="60" />
                </div>
                <el-button v-if="ciLog" @click="manualScrollToBottom" size="small" style="position: absolute; bottom: 10px; right: 10px; z-index: 10;">
                  滚动到底部
                </el-button>
                <el-button v-if="ciLog" @click="testScroll" size="small" style="position: absolute; bottom: 10px; right: 120px; z-index: 10;">
                  测试滚动
                </el-button>
              </div>
            </el-tab-pane>
            <el-tab-pane label="CD 日志" name="cd">
              <div class="log-detail-content" v-loading="cdLogLoading" ref="cdLogContainer">
                <div v-if="isStreamingCd" class="streaming-indicator">
                  <el-icon class="is-loading"><Loading /></el-icon>
                  <span>正在实时获取日志...</span>
                </div>
                <pre v-if="cdLog" class="log-text">{{ cdLog }}</pre>
                <div v-else-if="!cdLogLoading" class="empty-log">
                  <el-empty description="暂无 CD 日志" :image-size="60" />
                </div>
                <el-button v-if="cdLog" @click="manualScrollToBottom" size="small" style="position: absolute; bottom: 10px; right: 10px; z-index: 10;">
                  滚动到底部
                </el-button>
                <el-button v-if="cdLog" @click="testScroll" size="small" style="position: absolute; bottom: 10px; right: 120px; z-index: 10;">
                  测试滚动
                </el-button>
              </div>
            </el-tab-pane>
          </el-tabs>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Upload, RefreshRight, Refresh, Loading, Plus } from '@element-plus/icons-vue'
import { batchDeploy, createDeploy, getTaskDetail, queryPublishLogs, queryTaskLogs } from '@/services/deploy'
import { useUserStore } from '@/stores/user'
import type { TaskRecord, PublishLogQueryParams, PublishLogTaskRecord } from '@/models/deploy'

// 当前激活的标签页
const activeTab = ref('tool')

// 用户store
const userStore = useUserStore()

// 定义日志数据接口
interface LogItem {
  task_id: number
  serviceName: string
  branch: string
  environment: string
  status: string
  deployTime: string
  operator: string
  message: string
  auto_deploy: number
  ci_job_name: string
  cd_job_name: string
  ci_build_id: number
  cd_build_id: number
  products: string
}

// 发布表单数据
const deployForm = reactive({
  serviceName: '',
  environment: '',
  version: ''
})

// 日志筛选条件
const logFilter = reactive({
  serviceName: '',
  environment: '',
  dateRange: []
})

// 日志列表数据
const logList = ref<LogItem[]>([])
const currentPage = ref(1)
const pageSize = ref(10)
const total = ref(0)
const logLoading = ref(false)

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

// 服务信息接口
interface ServiceInfo {
  name: string
  branches: string[]
}

// 选中的服务信息
interface SelectedService {
  serviceName: string
  branch: string
  branchSuffix: string  // 新增字段，用于存储模拟环境的分支后缀
  status: string
  taskId?: number       // 任务ID
  lastUpdateTime?: string // 最后更新时间
}

// 可用服务列表
const availableServices = ref<ServiceInfo[]>([])
// 选中的服务列表
const selectedServices = ref<SelectedService[]>([])

// 是否有可发布的服务
const hasDeployableServices = computed(() => {
  return selectedServices.value.some(
    service => service.serviceName && 
               service.branch && 
               !isServiceProcessing(service.status)
  )
})

// 判断服务是否正在处理中
const isServiceProcessing = (status: string): boolean => {
  return status === '发布中' ||
         status === '初始化' ||
         status === '打包中' ||
         status === '打包成功' ||
         status === '部署中'
}

// 获取环境标签类型
const getEnvType = (env: string) => {
  const envMap: Record<string, string> = {
    'dev': 'primary',    // 改为 primary，更柔和的蓝色
    'test': 'warning',   // 保持 warning，醒目的黄色
    'moni': 'info'       // 改为 info，更柔和的灰色
  }
  return envMap[env] || 'info'
}

// 获取环境显示名称
const getEnvLabel = (env: string) => {
  const envMap: Record<string, string> = {
    'dev': '开发环境',
    'test': '测试环境',
    'moni': '模拟环境'
  }
  return envMap[env] || env
}

// 获取状态标签类型
const getStatusType = (status: string) => {
  // 先获取显示文本，再根据显示文本获取标签类型
  const displayStatus = getDeployStatus(status)
  const statusMap: Record<string, string> = {
    '初始化': 'info',
    '打包中': 'primary',
    '打包成功': 'success',
    '打包失败': 'danger',
    '部署中': 'primary',
    '部署成功': 'success',
    '部署失败': 'danger',
    '已取消': 'warning',
    '超时': 'warning',
    '未知状态': 'info'
  }
  return statusMap[displayStatus] || 'info'
}

// 获取进度条状态
const getProgressStatus = (status: string) => {
  // 先获取显示文本，再根据显示文本获取进度条状态
  const displayStatus = getDeployStatus(status)
  const statusMap: Record<string, string> = {
    '初始化': '',
    '打包中': '',
    '打包成功': 'success',
    '打包失败': 'exception',
    '部署中': '',
    '部署成功': 'success',
    '部署失败': 'exception',
    '已取消': 'warning',
    '超时': 'warning',
    '未知状态': ''
  }
  return statusMap[displayStatus] || ''
}

// 获取部署状态显示文本
const getDeployStatus = (status: string): string => {
  const statusMap: Record<string, string> = {
    'init': '初始化',
    'packaging': '打包中',
    'packaged': '打包成功',
    'package_failed': '打包失败',
    'deploying': '部署中',
    'deployed': '部署成功',
    'deploy_failed': '部署失败',
    'cancelled': '已取消',
    'timeout': '超时',
    'unknown': '未知状态'
  }
  return statusMap[status] || status
}

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

// 定时刷新选中服务的任务状态
let taskStatusTimer: number | null = null

// 刷新所有选中服务的任务状态
const refreshSelectedServicesStatus = async () => {
  console.log('开始刷新选中服务状态，服务数量:', selectedServices.value.length)
  for (let i = 0; i < selectedServices.value.length; i++) {
    const service = selectedServices.value[i]
    console.log(`检查服务 ${i}: ${service.serviceName}`, {
      taskId: service.taskId,
      status: service.status,
      branch: service.branch
    })
    
    // 检查是否有任务ID且状态不是最终状态
    if (service.taskId && 
        service.status !== '部署成功' && 
        service.status !== '部署失败' && 
        service.status !== '未发布' &&
        (service.status === '发布中' || 
         service.status === '初始化' || 
         service.status === '打包中' || 
         service.status === '打包成功' || 
         service.status === '部署中')) {
      console.log(`更新服务 ${service.serviceName} 状态，任务ID: ${service.taskId}，当前状态: ${service.status}`)
      await updateSelectedServiceStatus(i, service.taskId)
    } else {
      console.log(`跳过服务 ${service.serviceName}:`, {
        hasTaskId: !!service.taskId,
        status: service.status,
        isFinalStatus: service.status === '部署成功' || service.status === '部署失败' || service.status === '未发布',
        isUpdatingStatus: service.status === '发布中' || service.status === '初始化' || service.status === '打包中' || service.status === '打包成功' || service.status === '部署中'
      })
    }
  }
}

// 根据状态计算进度
const calculateProgress = (status: string): number => {
  const progressMap: Record<string, number> = {
    'init': 0,
    'packaging': 25,
    'packaged': 50,
    'package_failed': 50,
    'deploying': 75,
    'deployed': 100,
    'deploy_failed': 100
  }
  return progressMap[status] || 0
}

// 格式化日期时间
const formatDateTime = (dateStr: string): string => {
  if (!dateStr) return ''
  const date = new Date(dateStr)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false
  })
}

// 查询任务状态
const queryTaskStatus = async (taskId: number): Promise<TaskRecord | null> => {
  try {
    const response = await getTaskDetail(taskId)
    if (response.data.code === 1) {
      return response.data.result
    } else {
      console.error('查询任务状态失败:', response.data.msg)
      return null
    }
  } catch (error) {
    console.error('查询任务状态失败:', error)
    return null
  }
}

// 更新选中服务的状态
const updateSelectedServiceStatus = async (serviceIndex: number, taskId: number) => {
  try {
    const taskDetail = await queryTaskStatus(taskId)
    if (taskDetail) {
      const service = selectedServices.value[serviceIndex]
      const oldStatus = service.status
      service.taskId = taskId
      service.lastUpdateTime = formatDateTime(taskDetail.updated_at)
      
      // 根据任务状态更新显示状态
      switch (taskDetail.status) {
        case 'init':
          service.status = '初始化'
          break
        case 'packaging':
          service.status = '打包中'
          break
        case 'packaged':
          service.status = '打包成功'
          break
        case 'package_failed':
          service.status = '打包失败'
          break
        case 'deploying':
          service.status = '部署中'
          break
        case 'deployed':
          service.status = '部署成功'
          break
        case 'deploy_failed':
          service.status = '部署失败'
          break
        default:
          service.status = getDeployStatus(taskDetail.status)
      }
      
      console.log(`服务 ${service.serviceName} 状态更新: ${oldStatus} -> ${service.status} (任务状态: ${taskDetail.status})`)
    }
  } catch (error) {
    console.error('更新服务状态失败:', error)
  }
}

// 统一编辑分支相关
const globalBranchSuffix = ref('')

// 处理全局分支变更
const handleGlobalBranchChange = (value: string) => {
  if (!value) return
  
  // 更新所有服务的分支后缀
  selectedServices.value.forEach(service => {
    service.branchSuffix = value
    service.branch = `release_${value}`
  })
}

// 处理环境变更
const handleEnvChange = async (env: string) => {
  // 清空已选服务
  selectedServices.value = []
  // 清空全局分支后缀
  globalBranchSuffix.value = ''
  // 获取可用服务列表
  await fetchAvailableServices()
  // 更新所有已选服务的分支
  selectedServices.value.forEach(service => {
    if (env === 'moni') {
      service.branch = 'release_'
      service.branchSuffix = ''
    } else {
      service.branch = env
      service.branchSuffix = ''
    }
  })
}

// 获取可用服务列表
const fetchAvailableServices = async () => {
  try {
    // 使用正确的API获取应用名称列表
    const response = await fetch('/api/v1/apps/query/appname', {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      }
    })
    
    if (!response.ok) {
      throw new Error('获取服务列表失败')
    }
    
    const data = await response.json()
    if (data.code !== 1) {
      throw new Error(data.msg || '获取服务列表失败')
    }
    
    // 将 API 返回的数据转换为组件需要的格式
    availableServices.value = (data.result || []).map((appname: string) => ({
      name: appname,
      branches: [] // 分支列表不再需要
    }))
  } catch (error) {
    console.error('获取服务列表失败:', error)
    ElMessage.error(error instanceof Error ? error.message : '获取服务列表失败')
  }
}

// 处理分支后缀变更
const handleBranchSuffixChange = (suffix: string, row: SelectedService) => {
  row.branch = `release_${suffix}`
}

// 处理服务选择
const handleServiceSelect = async (_serviceName: string, index: number) => {
  // 根据环境设置分支
  if (deployForm.environment === 'moni') {
    selectedServices.value[index].branch = 'release_'
    selectedServices.value[index].branchSuffix = ''
  } else {
    selectedServices.value[index].branch = deployForm.environment
    selectedServices.value[index].branchSuffix = ''
  }
}

// 添加服务
const handleAddService = () => {
  const newService = {
    serviceName: '',
    branch: deployForm.environment === 'moni' ? 'release_' : deployForm.environment,
    branchSuffix: deployForm.environment === 'moni' ? globalBranchSuffix.value : '',
    status: '未发布'
  }
  
  // 如果是模拟环境且有全局分支后缀，则应用它
  if (deployForm.environment === 'moni' && globalBranchSuffix.value) {
    newService.branch = `release_${globalBranchSuffix.value}`
  }
  
  selectedServices.value.push(newService)
}

// 删除服务
const handleRemoveService = (index: number) => {
  selectedServices.value.splice(index, 1)
}

// 单个服务发布前的验证
const validateServiceDeploy = (row: SelectedService) => {
  if (!row.serviceName) {
    ElMessage.warning('请选择服务')
    return false
  }
  if (deployForm.environment === 'moni' && !row.branchSuffix) {
    ElMessage.warning('请输入分支后缀')
    return false
  }
  return true
}

// 单个服务发布
const handleDeploySingle = async (row: SelectedService, index: number) => {
  if (!validateServiceDeploy(row)) return
  
  try {
    row.status = '发布中'
    
    // 检查用户是否登录
    if (!userStore.userInfo) {
      throw new Error('用户未登录')
    }
    
    // 构建发布请求
    const deployRequest = {
      app_name: row.serviceName,
      env: deployForm.environment,
      branch: row.branch
    }
    
    // 调用发布接口
    const response = await createDeploy(deployRequest, {
      username: userStore.userInfo.username,
      nameCn: userStore.userInfo.nameCn
    })
    
    if (response.data.code === 1) {
      const result = response.data.result
      if (result && result.success) {
        const taskId = result.task_record.task_id
        ElMessage.success(`${row.serviceName} 发布任务已提交，任务ID: ${taskId}`)
        
        // 先设置taskId，确保定时器能找到这个服务
        row.taskId = taskId
        
        // 立即更新选中服务的状态
        await updateSelectedServiceStatus(index, taskId)
        
        // 刷新发布中服务列表
        await refreshDeployingList()
      } else {
        throw new Error(result?.error || '发布失败')
      }
    } else {
      throw new Error(response.data.msg || '发布失败')
    }
  } catch (error) {
    row.status = '部署失败'
    console.error('发布失败:', error)
    ElMessage.error(error instanceof Error ? error.message : '发布失败')
  }
}

// 单个服务重发
const handleRedeploySingle = async (row: SelectedService, index: number) => {
  if (!validateServiceDeploy(row)) return
  
  try {
    row.status = '发布中'
    
    // 检查用户是否登录
    if (!userStore.userInfo) {
      throw new Error('用户未登录')
    }
    
    // 构建重发请求
    const deployRequest = {
      app_name: row.serviceName,
      env: deployForm.environment,
      branch: row.branch
    }
    
    // 调用发布接口（重发使用相同的接口）
    const response = await createDeploy(deployRequest, {
      username: userStore.userInfo.username,
      nameCn: userStore.userInfo.nameCn
    })
    
    if (response.data.code === 1) {
      const result = response.data.result
      if (result && result.success) {
        const taskId = result.task_record.task_id
        ElMessage.success(`${row.serviceName} 重发任务已提交，任务ID: ${taskId}`)
        
        // 先设置taskId，确保定时器能找到这个服务
        row.taskId = taskId
        
        // 立即更新选中服务的状态
        await updateSelectedServiceStatus(index, taskId)
        
        // 刷新发布中服务列表
        await refreshDeployingList()
      } else {
        throw new Error(result?.error || '重发失败')
      }
    } else {
      throw new Error(response.data.msg || '重发失败')
    }
  } catch (error) {
    row.status = '部署失败'
    console.error('重发失败:', error)
    ElMessage.error(error instanceof Error ? error.message : '重发失败')
  }
}

// 批量发布前的验证
const validateBatchDeploy = () => {
  const invalidServices = selectedServices.value.filter(service => {
    if (!service.serviceName) return true
    if (deployForm.environment === 'moni' && !service.branchSuffix) return true
    return false
  })

  if (invalidServices.length > 0) {
    ElMessage.warning('请完善服务信息后再发布')
    return false
  }
  return true
}

// 批量发布
const handleBatchDeploy = async () => {
  if (!validateBatchDeploy()) return
  
  const deployableServices = selectedServices.value.filter(
    service => service.serviceName && !isServiceProcessing(service.status)
  )
  
  if (deployableServices.length === 0) {
    ElMessage.warning('没有可发布的服务')
    return
  }

  try {
    // 检查用户是否登录
    if (!userStore.userInfo) {
      throw new Error('用户未登录')
    }
    
    // 设置所有服务状态为发布中
    deployableServices.forEach(service => {
      service.status = '发布中'
    })
    
    // 构建批量发布请求
    const deployRequests = deployableServices.map(service => ({
      app_name: service.serviceName,
      env: deployForm.environment,
      branch: service.branch
    }))
    
    // 调用批量发布接口
    const response = await batchDeploy(deployRequests, {
      username: userStore.userInfo.username,
      nameCn: userStore.userInfo.nameCn
    })
    
    if (response.data.code === 1) {
      const result = response.data.result
      const successCount = result.success_count
      const failureCount = result.failure_count
      const totalCount = result.total_count
      
      if (successCount > 0) {
        ElMessage.success(`批量发布任务已提交，成功: ${successCount}，失败: ${failureCount}，总计: ${totalCount}`)
        
        // 更新所有成功任务对应的选中服务状态
        if (result.task_records && Array.isArray(result.task_records)) {
          for (const taskRecord of result.task_records) {
            if (taskRecord.success && taskRecord.task_record) {
              const taskDetail = taskRecord.task_record
              // 找到对应的选中服务索引
              const serviceIndex = selectedServices.value.findIndex(service => 
                service.serviceName === taskDetail.app_name && service.branch === taskDetail.branch
              )
              console.log(`批量发布 - 查找服务索引:`, {
                taskDetail: {
                  app_name: taskDetail.app_name,
                  branch: taskDetail.branch,
                  task_id: taskDetail.task_id
                },
                foundIndex: serviceIndex,
                selectedServices: selectedServices.value.map(s => ({
                  serviceName: s.serviceName,
                  branch: s.branch,
                  status: s.status,
                  taskId: s.taskId
                }))
              })
              if (serviceIndex >= 0) {
                // 先设置taskId，确保定时器能找到这个服务
                selectedServices.value[serviceIndex].taskId = taskDetail.task_id
                await updateSelectedServiceStatus(serviceIndex, taskDetail.task_id)
              } else {
                console.error(`未找到匹配的服务: ${taskDetail.app_name} - ${taskDetail.branch}`)
              }
            }
          }
        }
        
        // 刷新发布中服务列表
        await refreshDeployingList()
      } else {
        throw new Error('所有服务发布都失败了')
      }
    } else {
      throw new Error(response.data.msg || '批量发布失败')
    }
  } catch (error) {
    // 发布失败，恢复状态
    deployableServices.forEach(service => {
      service.status = '部署失败'
    })
    console.error('批量发布失败:', error)
    ElMessage.error(error instanceof Error ? error.message : '批量发布失败')
  }
}

// 批量重发
const handleBatchRedeploy = async () => {
  if (!validateBatchDeploy()) return
  
  const deployableServices = selectedServices.value.filter(
    service => service.serviceName && !isServiceProcessing(service.status)
  )
  
  if (deployableServices.length === 0) {
    ElMessage.warning('没有可重发的服务')
    return
  }

  try {
    // 检查用户是否登录
    if (!userStore.userInfo) {
      throw new Error('用户未登录')
    }
    
    // 设置所有服务状态为发布中
    deployableServices.forEach(service => {
      service.status = '发布中'
    })
    
    // 构建批量重发请求
    const deployRequests = deployableServices.map(service => ({
      app_name: service.serviceName,
      env: deployForm.environment,
      branch: service.branch
    }))
    
    // 调用批量发布接口（重发使用相同的接口）
    const response = await batchDeploy(deployRequests, {
      username: userStore.userInfo.username,
      nameCn: userStore.userInfo.nameCn
    })
    
    if (response.data.code === 1) {
      const result = response.data.result
      const successCount = result.success_count
      const failureCount = result.failure_count
      const totalCount = result.total_count
      
      if (successCount > 0) {
        ElMessage.success(`批量重发任务已提交，成功: ${successCount}，失败: ${failureCount}，总计: ${totalCount}`)
        
        // 更新所有成功任务对应的选中服务状态
        if (result.task_records && Array.isArray(result.task_records)) {
          for (const taskRecord of result.task_records) {
            if (taskRecord.success && taskRecord.task_record) {
              const taskDetail = taskRecord.task_record
              // 找到对应的选中服务索引
              const serviceIndex = selectedServices.value.findIndex(service => 
                service.serviceName === taskDetail.app_name && service.branch === taskDetail.branch
              )
              console.log(`批量重发 - 查找服务索引:`, {
                taskDetail: {
                  app_name: taskDetail.app_name,
                  branch: taskDetail.branch,
                  task_id: taskDetail.task_id
                },
                foundIndex: serviceIndex,
                selectedServices: selectedServices.value.map(s => ({
                  serviceName: s.serviceName,
                  branch: s.branch,
                  status: s.status,
                  taskId: s.taskId
                }))
              })
              if (serviceIndex >= 0) {
                // 先设置taskId，确保定时器能找到这个服务
                selectedServices.value[serviceIndex].taskId = taskDetail.task_id
                await updateSelectedServiceStatus(serviceIndex, taskDetail.task_id)
              } else {
                console.error(`未找到匹配的服务: ${taskDetail.app_name} - ${taskDetail.branch}`)
              }
            }
          }
        }
        
        // 刷新发布中服务列表
        await refreshDeployingList()
      } else {
        throw new Error('所有服务重发都失败了')
      }
    } else {
      throw new Error(response.data.msg || '批量重发失败')
    }
  } catch (error) {
    // 重发失败，恢复状态
    deployableServices.forEach(service => {
      service.status = '部署失败'
    })
    console.error('批量重发失败:', error)
    ElMessage.error(error instanceof Error ? error.message : '批量重发失败')
  }
}

// 处理日志查询
const handleSearch = async () => {
  // 基本参数验证
  if (currentPage.value < 1) {
    currentPage.value = 1
  }
  if (pageSize.value < 1 || pageSize.value > 100) {
    pageSize.value = 10
  }
  
  logLoading.value = true
  try {
    // 构建查询参数
    const params: PublishLogQueryParams = {
      page_num: currentPage.value,
      page_size: pageSize.value,
      app_name: logFilter.serviceName || undefined,
      env: logFilter.environment || undefined,
      publisher: undefined
    }

    // 处理时间范围
    if (logFilter.dateRange && logFilter.dateRange.length === 2) {
      params.start_time = logFilter.dateRange[0]
      params.end_time = logFilter.dateRange[1]
    }

    // 调用日志查询API
    const response = await queryPublishLogs(params)
    
    if (response.data.code === 1) {
      const result = response.data.result
      // 转换数据格式，添加空值检查
      if (result && result.task_record && Array.isArray(result.task_record)) {
        logList.value = result.task_record.map((item: PublishLogTaskRecord) => ({
          task_id: item.task_id,
          serviceName: item.app_name,
          branch: item.branch,
          environment: item.env,
          status: getDeployStatus(item.status),
          deployTime: formatDateTime(item.created_at),
          operator: item.publisher,
          message: item.message === 'NULL' ? '' : item.message,
          auto_deploy: item.auto_deploy,
          ci_job_name: item.ci_job_name === 'NULL' ? '' : item.ci_job_name,
          cd_job_name: item.cd_job_name === 'NULL' ? '' : item.cd_job_name,
          ci_build_id: item.ci_build_id || 0,
          cd_build_id: item.cd_build_id || 0,
          products: item.products === 'NULL' ? '' : item.products
        }))
        total.value = result.total || 0
      } else {
        // 处理空结果的情况
        logList.value = []
        total.value = 0
      }
      
      // 显示查询结果提示
      if (logList.value.length > 0) {
        ElMessage.success(`查询成功，共找到 ${total.value} 条记录`)
      } else {
        ElMessage.info('查询完成，未找到相关记录')
      }
    } else {
      throw new Error(response.data.msg || '查询失败')
    }
  } catch (error) {
    console.error('查询日志失败:', error)
    const errorMessage = error instanceof Error ? error.message : '查询失败'
    ElMessage.error(errorMessage)
    
    // 清空数据
    logList.value = []
    total.value = 0
  } finally {
    logLoading.value = false
  }
}

// 重置日志筛选条件
const handleResetLogFilter = () => {
  logFilter.serviceName = ''
  logFilter.environment = ''
  logFilter.dateRange = []
  currentPage.value = 1
  pageSize.value = 10
  // 重置后立即查询
  handleSearch()
}

// 查看日志详情
const viewLogDetail = (row: LogItem) => {
  // 将LogItem转换为DeployingService格式
  const deployingService: DeployingService = {
    id: row.task_id,
    serviceName: row.serviceName,
    branch: row.branch,
    environment: row.environment,
    status: row.status,
    progress: 100, // 已完成的任务进度为100
    startTime: row.deployTime,
    operator: row.operator,
    message: row.message,
    taskId: row.task_id,
    ciJobName: row.ci_job_name,
    cdJobName: row.cd_job_name,
    ciBuildId: row.ci_build_id,
    cdBuildId: row.cd_build_id,
    products: row.products,
    auto_deploy: row.auto_deploy
  }
  
  currentLog.value = deployingService
  logDialogVisible.value = true
  activeLogTab.value = 'ci'
  fetchLogs(deployingService)
}

// 分页处理
const handleSizeChange = (val: number) => {
  pageSize.value = val
  handleSearch()
}

const handleCurrentChange = (val: number) => {
  currentPage.value = val
  handleSearch()
}

// 日志对话框相关
const logDialogVisible = ref(false)
const currentLog = ref<DeployingService>({} as DeployingService)
const activeLogTab = ref('ci')
const ciLog = ref('')
const cdLog = ref('')
const ciLogLoading = ref(false)
const cdLogLoading = ref(false)

// 日志容器引用
const ciLogContainer = ref<HTMLElement>()
const cdLogContainer = ref<HTMLElement>()

// SSE连接状态
const ciEventSource = ref<EventSource | null>(null)
const cdEventSource = ref<EventSource | null>(null)
const isStreamingCi = ref(false)
const isStreamingCd = ref(false)

// 自动滚动到底部
const scrollToBottom = (container: HTMLElement | undefined) => {
  if (container) {
    console.log('滚动到底部，容器高度:', container.scrollHeight, '当前滚动位置:', container.scrollTop)
    
    // 只滚动日志内容容器，不影响标签页
    const logContent = container.querySelector('.log-text') as HTMLElement
    if (logContent) {
      // 方法1: 直接设置scrollTop
      container.scrollTop = container.scrollHeight
      
      // 方法2: 使用scrollTo
      setTimeout(() => {
        container.scrollTo({
          top: container.scrollHeight,
          behavior: 'smooth'
        })
      }, 50)
      
      console.log('滚动完成，新位置:', container.scrollTop)
    } else {
      console.log('未找到日志内容元素')
    }
  } else {
    console.log('容器未找到，无法滚动')
  }
}

// 手动滚动到底部（用于测试）
const manualScrollToBottom = () => {
  if (activeLogTab.value === 'ci') {
    scrollToBottom(ciLogContainer.value)
  } else if (activeLogTab.value === 'cd') {
    scrollToBottom(cdLogContainer.value)
  }
}

// 测试滚动功能
const testScroll = () => {
  const container = activeLogTab.value === 'ci' ? ciLogContainer.value : cdLogContainer.value
  if (container) {
    console.log('容器信息:', {
      scrollHeight: container.scrollHeight,
      clientHeight: container.clientHeight,
      scrollTop: container.scrollTop,
      offsetHeight: container.offsetHeight,
      style: {
        overflow: container.style.overflow,
        height: container.style.height,
        maxHeight: container.style.maxHeight
      }
    })
    
    // 测试滚动
    container.scrollTop = container.scrollHeight / 2
    setTimeout(() => {
      container.scrollTop = container.scrollHeight
    }, 1000)
  }
}

// 获取日志
const fetchLogs = async (row: DeployingService) => {
  // 先清理之前的SSE连接
  cleanupEventSources()
  
  if (activeLogTab.value === 'ci') {
    ciLogLoading.value = true
    ciLog.value = ''
    
    try {
      // 检查是否有CI job信息和build_id
      if (row.ciJobName && row.ciBuildId) {
        // 使用SSE流式获取CI日志
        await fetchCiLogsStream(row.ciJobName, row.ciBuildId)
      } else {
        // 回退到原来的API
        const response = await queryTaskLogs(row.taskId, 'ci')
        if (response.data.code === 1) {
          ciLog.value = response.data.result || ''
        } else {
          throw new Error(response.data.msg || '获取 CI 日志失败')
        }
      }
    } catch (error) {
      console.error('获取 CI 日志失败:', error)
      ElMessage.error(error instanceof Error ? error.message : '获取 CI 日志失败')
      ciLog.value = ''
    } finally {
      ciLogLoading.value = false
    }
  } else if (activeLogTab.value === 'cd') {
    cdLogLoading.value = true
    cdLog.value = ''
    
    try {
      // 检查是否有CD job信息和build_id
      if (row.cdJobName && row.cdBuildId) {
        // 使用SSE流式获取CD日志
        await fetchCdLogsStream(row.cdJobName, row.cdBuildId)
      } else {
        // 回退到原来的API
        const response = await queryTaskLogs(row.taskId, 'cd')
        if (response.data.code === 1) {
          cdLog.value = response.data.result || ''
        } else {
          throw new Error(response.data.msg || '获取 CD 日志失败')
        }
      }
    } catch (error) {
      console.error('获取 CD 日志失败:', error)
      ElMessage.error(error instanceof Error ? error.message : '获取 CD 日志失败')
      cdLog.value = ''
    } finally {
      cdLogLoading.value = false
    }
  }
}

// 使用SSE获取CI日志
const fetchCiLogsStream = async (jobName: string, buildId: number) => {
  return new Promise<void>((resolve, reject) => {
    const url = `/api/v1/job/stream/log?job_name=${encodeURIComponent(jobName)}&build_id=${buildId}`
    
    ciEventSource.value = new EventSource(url)
    isStreamingCi.value = true
    
    ciEventSource.value.onmessage = (event: MessageEvent) => {
      try {
        // 解析JSON数据
        const data = JSON.parse(event.data)
        if (data.code === 1 && data.result && Array.isArray(data.result)) {
          // 将每一行日志添加到内容中
          ciLog.value += data.result.join('\n') + '\n'
          // 自动滚动到底部
          scrollToBottom(ciLogContainer.value)
        } else if (data.code === 0) {
          // 处理错误
          reject(new Error(data.msg || data.error || '获取 CI 日志失败'))
          cleanupEventSources()
        }
      } catch (error) {
        console.error('解析CI日志SSE数据失败:', error)
        reject(error)
        cleanupEventSources()
      }
    }
    
    // 监听错误事件
    ciEventSource.value.addEventListener('error', (event: MessageEvent) => {
      try {
        const data = JSON.parse(event.data)
        reject(new Error(data.msg || data.error || '获取 CI 日志失败'))
      } catch (error) {
        reject(new Error('获取 CI 日志失败'))
      }
      cleanupEventSources()
    })
    
    // 监听结束事件
    ciEventSource.value.addEventListener('end', () => {
      console.log('CI日志SSE流结束')
      cleanupEventSources()
      resolve()
    })
    
    ciEventSource.value.onerror = (error) => {
      console.error('CI日志SSE连接错误:', error)
      cleanupEventSources()
      reject(new Error('CI日志SSE连接失败'))
    }
    
    ciEventSource.value.onopen = () => {
      console.log('CI日志SSE连接已建立')
    }
    
    // 设置超时处理
    const timeout = setTimeout(() => {
      cleanupEventSources()
      resolve() // 超时后正常结束
    }, 30000) // 30秒超时
    
    // 监听连接关闭
    ciEventSource.value.addEventListener('close', () => {
      clearTimeout(timeout)
      isStreamingCi.value = false
      resolve()
    })
  })
}

// 使用SSE获取CD日志
const fetchCdLogsStream = async (jobName: string, buildId: number) => {
  return new Promise<void>((resolve, reject) => {
    const url = `/api/v1/job/stream/log?job_name=${encodeURIComponent(jobName)}&build_id=${buildId}`
    
    cdEventSource.value = new EventSource(url)
    isStreamingCd.value = true
    
    cdEventSource.value.onmessage = (event: MessageEvent) => {
      try {
        // 解析JSON数据
        const data = JSON.parse(event.data)
        if (data.code === 1 && data.result && Array.isArray(data.result)) {
          // 将每一行日志添加到内容中
          cdLog.value += data.result.join('\n') + '\n'
          // 自动滚动到底部
          scrollToBottom(cdLogContainer.value)
        } else if (data.code === 0) {
          // 处理错误
          reject(new Error(data.msg || data.error || '获取 CD 日志失败'))
          cleanupEventSources()
        }
      } catch (error) {
        console.error('解析CD日志SSE数据失败:', error)
        reject(error)
        cleanupEventSources()
      }
    }
    
    // 监听错误事件
    cdEventSource.value.addEventListener('error', (event: MessageEvent) => {
      try {
        const data = JSON.parse(event.data)
        reject(new Error(data.msg || data.error || '获取 CD 日志失败'))
      } catch (error) {
        reject(new Error('获取 CD 日志失败'))
      }
      cleanupEventSources()
    })
    
    // 监听结束事件
    cdEventSource.value.addEventListener('end', () => {
      console.log('CD日志SSE流结束')
      cleanupEventSources()
      resolve()
    })
    
    cdEventSource.value.onerror = (error) => {
      console.error('CD日志SSE连接错误:', error)
      cleanupEventSources()
      reject(new Error('CD日志SSE连接失败'))
    }
    
    cdEventSource.value.onopen = () => {
      console.log('CD日志SSE连接已建立')
    }
    
    // 设置超时处理
    const timeout = setTimeout(() => {
      cleanupEventSources()
      resolve() // 超时后正常结束
    }, 30000) // 30秒超时
    
    // 监听连接关闭
    cdEventSource.value.addEventListener('close', () => {
      clearTimeout(timeout)
      isStreamingCd.value = false
      resolve()
    })
  })
}

// 清理SSE连接
const cleanupEventSources = () => {
  if (ciEventSource.value) {
    ciEventSource.value.close()
    ciEventSource.value = null
    isStreamingCi.value = false
  }
  if (cdEventSource.value) {
    cdEventSource.value.close()
    cdEventSource.value = null
    isStreamingCd.value = false
  }
}

// 监听日志标签页切换
watch(activeLogTab, async (_newTab) => {
  if (logDialogVisible.value && currentLog.value) {
    await fetchLogs(currentLog.value)
  }
})

// 监听主标签页切换
watch(activeTab, async (newTab) => {
  if (newTab === 'log') {
    // 只有在切换到日志页时才触发查询
    handleSearch()
  }
})

// 监听CI日志内容变化，自动滚动到底部
watch(ciLog, () => {
  if (ciLog.value && activeLogTab.value === 'ci') {
    nextTick(() => {
      scrollToBottom(ciLogContainer.value)
    })
  }
})

// 监听CD日志内容变化，自动滚动到底部
watch(cdLog, () => {
  if (cdLog.value && activeLogTab.value === 'cd') {
    nextTick(() => {
      scrollToBottom(cdLogContainer.value)
    })
  }
})

// 组件挂载时获取发布中服务列表和可用服务列表
onMounted(() => {
  refreshDeployingList()
  // 无论是否选择了环境，都获取可用服务列表，供日志查询使用
  fetchAvailableServices()
  // 移除自动加载日志数据，改为在切换到日志页时加载
  // handleSearch()
  // 每10秒刷新一次发布中服务列表
  refreshTimer = window.setInterval(refreshDeployingList, 10000)
  // 每5秒刷新一次选中服务的任务状态
  taskStatusTimer = window.setInterval(refreshSelectedServicesStatus, 5000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
  }
  if (taskStatusTimer) {
    clearInterval(taskStatusTimer)
  }
  // 清理SSE连接
  cleanupEventSources()
})

// 查看日志
const handleViewLog = async (row: DeployingService) => {
  currentLog.value = row
  logDialogVisible.value = true
  activeLogTab.value = 'ci'
  await fetchLogs(row)
}

// 处理日志对话框关闭
const handleLogDialogClose = () => {
  // 清理SSE连接
  cleanupEventSources()
  
  // 清理日志相关数据
  currentLog.value = {} as DeployingService
  logDialogVisible.value = false
  activeLogTab.value = 'ci'
  ciLog.value = ''
  cdLog.value = ''
  ciLogLoading.value = false
  cdLogLoading.value = false
}
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

.deploy-tool {
  padding: 0 20px 20px 20px;
}

.env-selector {
  margin-bottom: 24px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
}

.global-branch-input {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.branch-label {
  color: #606266;
  font-size: 14px;
}

.branch-input-wrapper {
  display: inline-block;
}

.branch-prefix {
  color: #606266;
  font-size: 14px;
  font-family: monospace;
  user-select: none;
  padding-right: 4px;
}

:deep(.el-input__prefix) {
  color: #606266;
  font-family: monospace;
  border-right: 1px solid #dcdfe6;
  padding-right: 8px;
  margin-right: 8px;
}

:deep(.el-input__prefix-inner) {
  display: flex;
  align-items: center;
}

.service-table {
  margin-top: 20px;
  background: #fff;
  border-radius: 4px;
  padding: 20px;
}

.table-actions {
  margin-top: 16px;
  display: flex;
  justify-content: center;
}

.batch-actions {
  margin-top: 24px;
  display: flex;
  justify-content: center;
  gap: 16px;
}

.service-desc {
  display: none;
}

.branch-text {
  color: #606266;
  font-size: 14px;
  font-family: monospace;
}

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

.log-content {
  padding: 20px 0;
}

.log-filter {
  margin-bottom: 20px;
  padding: 20px;
  background: #f8f9fa;
  border-radius: 4px;
  border: 1px solid #e9ecef;
}

.log-filter .el-form {
  margin-bottom: 0;
}

.log-filter .el-form-item {
  margin-bottom: 0;
  margin-right: 20px;
}

.log-filter .el-form-item:last-child {
  margin-right: 0;
}

.log-table {
  margin-top: 20px;
}

.log-table :deep(.el-table) {
  box-shadow: none !important;
  border: 1px solid #ebeef5 !important;
  border-radius: 4px !important;
  background-color: #fff !important;
  color: #213547 !important;
}

.log-table :deep(.el-table__header-wrapper) {
  background-color: #fafafa !important;
}

.log-table :deep(.el-table__body-wrapper) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__header) {
  background-color: #fafafa !important;
}

.log-table :deep(.el-table__body) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__row) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__row:hover) {
  background-color: #f5f7fa !important;
}

.log-table :deep(.el-table__cell) {
  background-color: transparent !important;
  border-bottom: 1px solid #ebeef5 !important;
  color: #213547 !important;
}

.log-table :deep(.el-table__header .el-table__cell) {
  background-color: #fafafa !important;
  border-bottom: 1px solid #ebeef5 !important;
  color: #213547 !important;
}

.log-table :deep(.el-table__empty-block) {
  background-color: #fff !important;
}

.log-table :deep(.el-table__empty-text) {
  color: #909399 !important;
}

.pagination {
  margin-top: 20px;
  display: flex;
  justify-content: flex-end;
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

:deep(.el-radio-button__inner) {
  padding: 12px 20px;
}

:deep(.el-button--large) {
  padding: 12px 24px;
  font-size: 14px;
}

:deep(.el-button--large .el-icon) {
  margin-right: 4px;
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

:deep(.el-table .el-input__prefix) {
  height: 32px;
  line-height: 32px;
}

:deep(.el-tag--primary) {
  --el-tag-bg-color: var(--el-color-primary-light-9);
  --el-tag-border-color: var(--el-color-primary-light-8);
  --el-tag-hover-color: var(--el-color-primary);
}

:deep(.el-tag--info) {
  --el-tag-bg-color: var(--el-color-info-light-9);
  --el-tag-border-color: var(--el-color-info-light-8);
  --el-tag-hover-color: var(--el-color-info);
}

:deep(.el-tag--warning) {
  --el-tag-bg-color: var(--el-color-warning-light-9);
  --el-tag-border-color: var(--el-color-warning-light-8);
  --el-tag-hover-color: var(--el-color-warning);
}

:deep(.el-tag--success) {
  --el-tag-bg-color: var(--el-color-success-light-9);
  --el-tag-border-color: var(--el-color-success-light-8);
  --el-tag-hover-color: var(--el-color-success);
}

:deep(.el-tag--danger) {
  --el-tag-bg-color: var(--el-color-danger-light-9);
  --el-tag-border-color: var(--el-color-danger-light-8);
  --el-tag-hover-color: var(--el-color-danger);
  font-weight: 600;
  box-shadow: 0 2px 4px rgba(245, 108, 108, 0.2);
  animation: pulse 2s infinite;
}

@keyframes pulse {
  0% {
    box-shadow: 0 2px 4px rgba(245, 108, 108, 0.2);
  }
  50% {
    box-shadow: 0 2px 8px rgba(245, 108, 108, 0.4);
  }
  100% {
    box-shadow: 0 2px 4px rgba(245, 108, 108, 0.2);
  }
}

.log-dialog {
  :deep(.el-dialog__body) {
    padding: 0;
  }
  
  :deep(.el-dialog) {
    max-height: 90vh;
  }
}

.log-dialog-content {
  display: flex;
  flex-direction: column;
  height: 70vh;
  max-height: 70vh;
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
  overflow: hidden;
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
    overflow: hidden;
    padding: 0;
    height: 100%;
    display: flex;
    flex-direction: column;
  }
  
  :deep(.el-tab-pane) {
    height: 100%;
    overflow: hidden;
    display: flex;
    flex-direction: column;
  }
}

.log-detail-content {
  height: 100% !important;
  max-height: 100% !important;
  padding: 20px;
  overflow-y: scroll !important;
  overflow-x: auto !important;
  background-color: #1e1e1e !important;
  display: flex;
  flex-direction: column;
  position: relative;
  flex: 1;
  min-height: 0;
  box-sizing: border-box;
  
  /* 强制滚动样式 */
  scroll-behavior: smooth;
  -webkit-overflow-scrolling: touch;
  
  /* 确保内容可以滚动 */
  max-height: calc(100vh - 200px) !important;
  
  /* 强制覆盖可能的全局overflow限制 */
  overflow: auto !important;
  overflow-y: scroll !important;
  overflow-x: auto !important;
  
  /* 确保有足够的高度来滚动 */
  min-height: 200px;
  
  /* 防止内容溢出 */
  word-wrap: break-word;
  word-break: break-all;
  
  /* 自定义滚动条样式 */
  &::-webkit-scrollbar {
    width: 12px;
    height: 12px;
  }
  
  &::-webkit-scrollbar-track {
    background: #2d2d2d;
    border-radius: 6px;
  }
  
  &::-webkit-scrollbar-thumb {
    background: #666;
    border-radius: 6px;
    
    &:hover {
      background: #888;
    }
  }
  
  &::-webkit-scrollbar-corner {
    background: #2d2d2d;
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
  flex: 1;
  min-height: 0;
  overflow: visible;
  background-color: transparent !important;
  display: block;
  width: 100%;
  height: auto;
  max-width: 100%;
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

.empty-data {
  padding: 40px 0;
  text-align: center;
}

.streaming-indicator {
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 16px;
  padding: 8px 16px;
  background-color: rgba(64, 158, 255, 0.1);
  border: 1px solid rgba(64, 158, 255, 0.3);
  border-radius: 4px;
  color: #409eff;
  font-size: 14px;
}

.streaming-indicator .el-icon {
  margin-right: 8px;
  font-size: 16px;
}

/* 确保日志容器可以正常滚动 */
.log-detail-content {
  /* 强制启用滚动 */
  overflow: auto !important;
  overflow-y: scroll !important;
  overflow-x: auto !important;
  
  /* 确保有足够的高度来滚动 */
  min-height: 200px;
  
  /* 防止内容溢出 */
  word-wrap: break-word;
  word-break: break-all;
}
</style> 