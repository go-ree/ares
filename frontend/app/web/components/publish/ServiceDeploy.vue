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
                <el-radio-button :value="'sim'">模拟环境</el-radio-button>
              </el-radio-group>
              <el-button
                v-if="deployForm.environment === 'sim'"
                type="primary"
                plain
                class="edit-branch-btn"
                @click="handleEditAllBranches"
              >
                <el-icon><Edit /></el-icon>
                统一编辑发布分支
              </el-button>
            </div>

            <!-- 统一编辑分支对话框 -->
            <el-dialog
              v-model="branchDialogVisible"
              title="统一编辑发布分支"
              width="500px"
              :close-on-click-modal="false"
            >
              <div class="branch-dialog-content">
                <div class="branch-prefix-wrapper">
                  <span class="branch-prefix">release_</span>
                  <el-input
                    v-model="globalBranchSuffix"
                    placeholder="请输入分支后缀"
                    style="width: 300px"
                  />
                </div>
                <div class="branch-preview">
                  <div class="preview-title">预览：</div>
                  <div class="preview-list">
                    <div v-for="service in selectedServices" :key="service.serviceName" class="preview-item">
                      <span class="service-name">{{ service.serviceName }}</span>
                      <span class="branch-name">release_{{ globalBranchSuffix }}</span>
                    </div>
                  </div>
                </div>
              </div>
              <template #footer>
                <span class="dialog-footer">
                  <el-button @click="branchDialogVisible = false">取消</el-button>
                  <el-button type="primary" @click="handleConfirmBranchEdit">
                    确认
                  </el-button>
                </span>
              </template>
            </el-dialog>

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
                      >
                        <span>{{ service.name }}</span>
                        <span class="service-desc">{{ service.description }}</span>
                      </el-option>
                    </el-select>
                  </template>
                </el-table-column>
                <el-table-column prop="branch" label="发布分支" min-width="200">
                  <template #default="{ row }">
                    <template v-if="deployForm.environment === 'sim'">
                      <div class="branch-input-wrapper">
                        <span class="branch-prefix">release_</span>
                        <el-input
                          v-model="row.branchSuffix"
                          placeholder="请输入分支后缀"
                          style="width: 200px"
                          @input="(val: string) => handleBranchSuffixChange(val, row)"
                        />
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
                <el-table-column label="操作" width="120" fixed="right">
                  <template #default="{ row }">
                    <el-button
                      type="primary"
                      link
                      :disabled="row.status !== '发布中'"
                      @click="handleCancelDeploy(row)"
                    >
                      取消发布
                    </el-button>
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
                    <el-option label="生产环境" value="prod" />
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
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
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
  id: string
  serviceName: string
  branch: string
  environment: string
  status: string
  progress: number
  startTime: string
  operator: string
}

const deployingList = ref<DeployingService[]>([])
const deployingLoading = ref(false)

// 服务信息接口
interface ServiceInfo {
  name: string
  description: string
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
    'dev': 'info',
    'test': 'warning',
    'prod': 'danger'
  }
  return envMap[env] || 'info'
}

// 获取环境显示名称
const getEnvLabel = (env: string) => {
  const envMap: Record<string, string> = {
    'dev': '开发环境',
    'test': '测试环境',
    'prod': '生产环境'
  }
  return envMap[env] || env
}

// 获取进度条状态
const getProgressStatus = (status: string) => {
  const statusMap: Record<string, string> = {
    '成功': 'success',
    '失败': 'exception',
    '进行中': ''
  }
  return statusMap[status] || ''
}

// 获取状态标签类型
const getStatusType = (status: string) => {
  const statusMap: Record<string, string> = {
    '成功': 'success',
    '失败': 'danger',
    '进行中': 'warning'
  }
  return statusMap[status] || 'info'
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
    // TODO: 调用获取发布中服务列表 API
    // 模拟数据
    deployingList.value = [
      {
        id: '1',
        serviceName: 'service-a',
        branch: 'dev',
        environment: 'dev',
        status: '发布中',
        progress: 45,
        startTime: '2024-03-20 10:00:00',
        operator: '张三'
      },
      {
        id: '2',
        serviceName: 'service-b',
        branch: 'release_20240320',
        environment: 'sim',
        status: '发布中',
        progress: 80,
        startTime: '2024-03-20 09:30:00',
        operator: '李四'
      }
    ]
  } catch (error) {
    ElMessage.error('获取发布中服务列表失败')
  } finally {
    deployingLoading.value = false
  }
}

// 定时刷新发布中服务列表
let refreshTimer: number | null = null

// 处理环境变更
const handleEnvChange = async (env: string) => {
  // 清空已选服务
  selectedServices.value = []
  // 获取可用服务列表
  await fetchAvailableServices()
  // 更新所有已选服务的分支
  selectedServices.value.forEach(service => {
    if (env === 'sim') {
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
    // TODO: 调用获取服务列表 API
    // 模拟数据
    availableServices.value = [
      {
        name: 'service-a',
        description: '服务A描述',
        branches: [] // 不再需要分支列表，因为分支是固定的
      },
      {
        name: 'service-b',
        description: '服务B描述',
        branches: []
      }
    ]
  } catch (error) {
    ElMessage.error('获取服务列表失败')
  }
}

// 处理分支后缀变更
const handleBranchSuffixChange = (suffix: string, row: SelectedService) => {
  row.branch = `release_${suffix}`
}

// 处理服务选择
const handleServiceSelect = async (serviceName: string, index: number) => {
  // 根据环境设置分支
  if (deployForm.environment === 'sim') {
    selectedServices.value[index].branch = 'release_'
    selectedServices.value[index].branchSuffix = ''
  } else {
    selectedServices.value[index].branch = deployForm.environment
    selectedServices.value[index].branchSuffix = ''
  }
}

// 添加服务
const handleAddService = () => {
  selectedServices.value.push({
    serviceName: '',
    branch: deployForm.environment === 'sim' ? 'release_' : deployForm.environment,
    branchSuffix: '',
    status: '未发布'
  })
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
  if (deployForm.environment === 'sim' && !row.branchSuffix) {
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
    if (deployForm.environment === 'sim' && !service.branchSuffix) return true
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

// 统一编辑分支相关
const branchDialogVisible = ref(false)
const globalBranchSuffix = ref('')

// 处理统一编辑分支
const handleEditAllBranches = () => {
  if (selectedServices.value.length === 0) {
    ElMessage.warning('请先添加需要发布的服务')
    return
  }
  // 如果所有服务的分支后缀都相同，则使用该后缀作为初始值
  const suffixes = selectedServices.value.map(service => service.branchSuffix)
  const allSame = suffixes.every(suffix => suffix === suffixes[0])
  globalBranchSuffix.value = allSame ? suffixes[0] : ''
  branchDialogVisible.value = true
}

// 确认统一编辑分支
const handleConfirmBranchEdit = () => {
  if (!globalBranchSuffix.value) {
    ElMessage.warning('请输入分支后缀')
    return
  }
  
  // 更新所有服务的分支后缀
  selectedServices.value.forEach(service => {
    service.branchSuffix = globalBranchSuffix.value
    service.branch = `release_${globalBranchSuffix.value}`
  })
  
  branchDialogVisible.value = false
  ElMessage.success('已统一更新发布分支')
}
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
  justify-content: center;
  align-items: center;
  gap: 16px;
}

.edit-branch-btn {
  margin-left: 16px;
}

.branch-dialog-content {
  padding: 0 20px;
}

.branch-prefix-wrapper {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 24px;
}

.branch-preview {
  background: #f8f9fa;
  border-radius: 4px;
  padding: 16px;
}

.preview-title {
  font-size: 14px;
  color: #606266;
  margin-bottom: 12px;
}

.preview-list {
  max-height: 300px;
  overflow-y: auto;
}

.preview-item {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 8px 0;
  border-bottom: 1px solid #ebeef5;
}

.preview-item:last-child {
  border-bottom: none;
}

.service-name {
  color: #303133;
  font-size: 14px;
}

.branch-name {
  color: #606266;
  font-family: monospace;
  font-size: 14px;
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
  color: #909399;
  font-size: 12px;
  margin-left: 8px;
}

.branch-input-wrapper {
  display: flex;
  align-items: center;
  gap: 4px;
}

.branch-prefix {
  color: #606266;
  font-size: 14px;
  font-family: monospace;
}

.branch-text {
  color: #606266;
  font-size: 14px;
  font-family: monospace;
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
</style> 