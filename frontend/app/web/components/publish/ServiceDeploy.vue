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
                <el-table-column label="操作" width="280" fixed="right">
                  <template #default="{ row, $index }">
                    <el-button-group>
                      <el-button
                        type="primary"
                        :disabled="!row.serviceName || !row.branch || row.status === '发布中'"
                        @click="handleDeploySingle(row)"
                      >
                        编译并发布
                      </el-button>
                      <el-button
                        type="warning"
                        :disabled="!row.serviceName || !row.branch || row.status === '发布中'"
                        @click="handleRedeploySingle(row)"
                      >
                        仅重发
                      </el-button>
                      <el-button
                        type="danger"
                        :disabled="row.status === '发布中'"
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
                  <el-input v-model="logFilter.serviceName" placeholder="请输入服务名称" />
                </el-form-item>
                <el-form-item label="环境">
                  <el-select v-model="logFilter.environment" placeholder="请选择环境">
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
                  />
                </el-form-item>
                <el-form-item>
                  <el-button type="primary" @click="handleSearch">查询</el-button>
                </el-form-item>
              </el-form>
            </div>
            <div class="log-table">
              <el-table :data="logList" style="width: 100%">
                <el-table-column prop="serviceName" label="服务名称" />
                <el-table-column prop="environment" label="环境" />
                <el-table-column prop="version" label="版本号" />
                <el-table-column prop="status" label="状态">
                  <template #default="{ row }">
                    <el-tag :type="getStatusType(row.status)">{{ row.status }}</el-tag>
                  </template>
                </el-table-column>
                <el-table-column prop="deployTime" label="发布时间" />
                <el-table-column prop="operator" label="操作人" />
                <el-table-column label="操作">
                  <template #default="{ row }">
                    <el-button type="text" @click="viewLogDetail(row)">查看详情</el-button>
                  </template>
                </el-table-column>
              </el-table>
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
      :title="`${currentLog.serviceName} - 发布日志`"
      width="80%"
      destroy-on-close
      class="log-dialog"
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
            </el-descriptions>
          </div>
        </div>
        
        <div class="log-tabs">
          <el-tabs v-model="activeLogTab">
            <el-tab-pane label="CI 日志" name="ci">
              <div class="log-content" v-loading="ciLogLoading">
                <pre v-if="ciLog" class="log-text">{{ ciLog }}</pre>
                <div v-else-if="!ciLogLoading" class="empty-log">暂无 CI 日志</div>
              </div>
            </el-tab-pane>
            <el-tab-pane label="CD 日志" name="cd">
              <div class="log-content" v-loading="cdLogLoading">
                <pre v-if="cdLog" class="log-text">{{ cdLog }}</pre>
                <div v-else-if="!cdLogLoading" class="empty-log">暂无 CD 日志</div>
              </div>
            </el-tab-pane>
          </el-tabs>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Upload, RefreshRight, Refresh, Loading, Plus, Edit } from '@element-plus/icons-vue'

// 当前激活的标签页
const activeTab = ref('tool')

// 定义日志数据接口
interface LogItem {
  serviceName: string
  environment: string
  version: string
  status: string
  deployTime: string
  operator: string
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
  products?: string
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
}

// 可用服务列表
const availableServices = ref<ServiceInfo[]>([])
// 选中的服务列表
const selectedServices = ref<SelectedService[]>([])

// 是否有可发布的服务
const hasDeployableServices = computed(() => {
  return selectedServices.value.some(
    service => service.serviceName && service.branch && service.status !== '发布中'
  )
})

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
    '编译打包中': 'primary',
    '编译打包成功': 'success',
    '编译打包失败': 'danger',
    '部署中': 'primary',
    '部署成功': 'success',
    '部署失败': 'danger'
  }
  return statusMap[displayStatus] || 'info'
}

// 获取进度条状态
const getProgressStatus = (status: string) => {
  // 先获取显示文本，再根据显示文本获取进度条状态
  const displayStatus = getDeployStatus(status)
  const statusMap: Record<string, string> = {
    '初始化': '',
    '编译打包中': '',
    '编译打包成功': 'success',
    '编译打包失败': 'exception',
    '部署中': '',
    '部署成功': 'success',
    '部署失败': 'exception'
  }
  return statusMap[displayStatus] || ''
}

// 获取部署状态显示文本
const getDeployStatus = (status: string): string => {
  const statusMap: Record<string, string> = {
    'init': '初始化',
    'packaging': '编译打包中',
    'packaged': '编译打包成功',
    'package_failed': '编译打包失败',
    'deploying': '部署中',
    'deployed': '部署成功',
    'deploy_failed': '部署失败'
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
    // 使用 vite.config.ts 中配置的代理
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
const handleServiceSelect = async (serviceName: string, index: number) => {
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
const handleDeploySingle = async (row: SelectedService) => {
  if (!validateServiceDeploy(row)) return
  
  try {
    // TODO: 调用单个服务发布 API
    row.status = '发布中'
    ElMessage.success(`${row.serviceName} 发布任务已提交`)
    // 模拟发布完成
    setTimeout(() => {
      row.status = '发布成功'
    }, 2000)
  } catch (error) {
    row.status = '发布失败'
    ElMessage.error('发布失败')
  }
}

// 单个服务重发
const handleRedeploySingle = async (row: SelectedService) => {
  if (!validateServiceDeploy(row)) return
  
  try {
    // TODO: 调用单个服务重发 API
    row.status = '发布中'
    ElMessage.success(`${row.serviceName} 重发任务已提交`)
    // 模拟重发完成
    setTimeout(() => {
      row.status = '发布成功'
    }, 2000)
  } catch (error) {
    row.status = '发布失败'
    ElMessage.error('重发失败')
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
    service => service.serviceName && service.status !== '发布中'
  )
  
  if (deployableServices.length === 0) {
    ElMessage.warning('没有可发布的服务')
    return
  }

  try {
    // TODO: 调用批量发布 API
    deployableServices.forEach(service => {
      service.status = '发布中'
    })
    ElMessage.success('批量发布任务已提交')
    // 模拟发布完成
    setTimeout(() => {
      deployableServices.forEach(service => {
        service.status = '发布成功'
      })
    }, 2000)
  } catch (error) {
    deployableServices.forEach(service => {
      service.status = '发布失败'
    })
    ElMessage.error('批量发布失败')
  }
}

// 批量重发
const handleBatchRedeploy = async () => {
  if (!validateBatchDeploy()) return
  
  const deployableServices = selectedServices.value.filter(
    service => service.serviceName && service.status !== '发布中'
  )
  
  if (deployableServices.length === 0) {
    ElMessage.warning('没有可重发的服务')
    return
  }

  try {
    // TODO: 调用批量重发 API
    deployableServices.forEach(service => {
      service.status = '发布中'
    })
    ElMessage.success('批量重发任务已提交')
    // 模拟重发完成
    setTimeout(() => {
      deployableServices.forEach(service => {
        service.status = '发布成功'
      })
    }, 2000)
  } catch (error) {
    deployableServices.forEach(service => {
      service.status = '发布失败'
    })
    ElMessage.error('批量重发失败')
  }
}

// 处理日志查询
const handleSearch = async () => {
  try {
    // TODO: 调用日志查询 API
    // 模拟数据
    logList.value = [
      {
        serviceName: '示例服务',
        environment: 'dev',
        version: '1.0.0',
        status: '成功',
        deployTime: '2024-03-20 10:00:00',
        operator: '张三'
      }
    ]
    total.value = 1
  } catch (error) {
    ElMessage.error('查询失败')
  }
}

// 查看日志详情
const viewLogDetail = (row: LogItem) => {
  // TODO: 实现查看日志详情的逻辑
  console.log('查看日志详情', row)
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

// 查看日志
const handleViewLog = async (row: DeployingService) => {
  currentLog.value = row
  logDialogVisible.value = true
  activeLogTab.value = 'ci'
  await fetchLogs(row)
}

// 获取日志
const fetchLogs = async (row: DeployingService) => {
  if (activeLogTab.value === 'ci' && row.ciJobName) {
    ciLogLoading.value = true
    try {
      // TODO: 调用获取 CI 日志的 API
      const response = await fetch(`/api/v1/deploy/log/ci?task_id=${row.taskId}`, {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json'
        }
      })
      
      if (!response.ok) {
        throw new Error('获取 CI 日志失败')
      }
      
      const data = await response.json()
      if (data.code !== 1) {
        throw new Error(data.msg || '获取 CI 日志失败')
      }
      
      ciLog.value = data.result || ''
    } catch (error) {
      console.error('获取 CI 日志失败:', error)
      ElMessage.error(error instanceof Error ? error.message : '获取 CI 日志失败')
      ciLog.value = ''
    } finally {
      ciLogLoading.value = false
    }
  } else if (activeLogTab.value === 'cd' && row.cdJobName) {
    cdLogLoading.value = true
    try {
      // TODO: 调用获取 CD 日志的 API
      const response = await fetch(`/api/v1/deploy/log/cd?task_id=${row.taskId}`, {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json'
        }
      })
      
      if (!response.ok) {
        throw new Error('获取 CD 日志失败')
      }
      
      const data = await response.json()
      if (data.code !== 1) {
        throw new Error(data.msg || '获取 CD 日志失败')
      }
      
      cdLog.value = data.result || ''
    } catch (error) {
      console.error('获取 CD 日志失败:', error)
      ElMessage.error(error instanceof Error ? error.message : '获取 CD 日志失败')
      cdLog.value = ''
    } finally {
      cdLogLoading.value = false
    }
  }
}

// 监听日志标签页切换
watch(activeLogTab, async (newTab) => {
  if (logDialogVisible.value && currentLog.value) {
    await fetchLogs(currentLog.value)
  }
})

// 组件挂载时获取发布中服务列表和可用服务列表
onMounted(() => {
  refreshDeployingList()
  if (deployForm.environment) {
    fetchAvailableServices()
  }
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
.service-deploy {
  padding: 20px;
}

.deploy-card {
  background: #fff;
  border-radius: 4px;
}

.deploy-tool {
  padding: 20px 0;
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
}

.log-table {
  margin-top: 20px;
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
}

.log-dialog {
  :deep(.el-dialog__body) {
    padding: 0;
  }
}

.log-dialog-content {
  display: flex;
  flex-direction: column;
  height: 70vh;
}

.log-header {
  padding: 20px;
  border-bottom: 1px solid var(--el-border-color-light);
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
  
  :deep(.el-tabs__content) {
    flex: 1;
    overflow: hidden;
    padding: 0;
  }
  
  :deep(.el-tab-pane) {
    height: 100%;
  }
}

.log-content {
  height: 100%;
  padding: 20px;
  overflow: auto;
  background-color: #1e1e1e;
}

.log-text {
  margin: 0;
  color: #d4d4d4;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Consolas', monospace;
  font-size: 13px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-wrap: break-word;
}

.empty-log {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--el-text-color-secondary);
  font-size: 14px;
}
</style> 