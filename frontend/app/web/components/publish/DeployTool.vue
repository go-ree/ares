<template>
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
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Upload, RefreshRight, Loading, Plus } from '@element-plus/icons-vue'
import { batchDeploy, createDeploy } from '@/services/deploy'
import { useUserStore } from '@/stores/user'
import { useDeployStatus } from '@/composables/useDeployStatus'
import { useServiceManagement } from '@/composables/useServiceManagement'

// 用户store
const userStore = useUserStore()

// 使用组合式函数
const { getStatusType, isServiceProcessing } = useDeployStatus()
const { 
  availableServices, 
  selectedServices, 
  fetchAvailableServices,
  updateSelectedServiceStatus 
} = useServiceManagement()

// 发布表单数据
const deployForm = reactive({
  serviceName: '',
  environment: '',
  version: ''
})

// 统一编辑分支相关
const globalBranchSuffix = ref('')

// 是否有可发布的服务
const hasDeployableServices = computed(() => {
  return selectedServices.value.some(
    service => service.serviceName && 
               service.branch && 
               !isServiceProcessing(service.status)
  )
})

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

// 处理分支后缀变更
const handleBranchSuffixChange = (suffix: string, row: any) => {
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
const validateServiceDeploy = (row: any) => {
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
const handleDeploySingle = async (row: any, index: number) => {
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
const handleRedeploySingle = async (row: any, index: number) => {
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
              if (serviceIndex >= 0) {
                // 先设置taskId，确保定时器能找到这个服务
                selectedServices.value[serviceIndex].taskId = taskDetail.task_id
                await updateSelectedServiceStatus(serviceIndex, taskDetail.task_id)
              }
            }
          }
        }
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
              if (serviceIndex >= 0) {
                // 先设置taskId，确保定时器能找到这个服务
                selectedServices.value[serviceIndex].taskId = taskDetail.task_id
                await updateSelectedServiceStatus(serviceIndex, taskDetail.task_id)
              }
            }
          }
        }
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

// 组件挂载时获取可用服务列表
onMounted(() => {
  fetchAvailableServices()
})
</script>

<style scoped>
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

.branch-text {
  color: #606266;
  font-size: 14px;
  font-family: monospace;
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